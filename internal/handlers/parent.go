package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type ParentHandler struct{}

func NewParentHandler() *ParentHandler {
	return &ParentHandler{}
}

type CreateParentRequest struct {
	FirstName  string  `json:"first_name" binding:"required"`
	LastName   string  `json:"last_name" binding:"required"`
	MiddleName *string `json:"middle_name"`
	Passport   *string `json:"passport"`
	Phone      string  `json:"phone" binding:"required"`
	Email      *string `json:"email"`
	Password   string  `json:"password" binding:"required"`
}

// CreateAndLinkParent registers a parent user profile if not exists, and links it to a student
func (h *ParentHandler) CreateAndLinkParent(c *gin.Context) {
	studentIDStr := c.Param("id")
	studentID, err := strconv.Atoi(studentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student ID"})
		return
	}

	var req CreateParentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// 1. Get student class ID and actual students.id profile
	var actualStudentID, classID int
	err = dbConn.QueryRow("SELECT id, class_id FROM students WHERE (id = $1 OR user_id = $1) AND is_deleted = false", studentID).Scan(&actualStudentID, &classID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi yoki o'chirilgan"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check student details", "details": err.Error()})
		}
		return
	}

	// 2. Authorization check: Admin or assigned main teacher of student's class
	if userRole != "ADMIN" {
		if userRole != "MAIN_TEACHER" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va ushbu sinf rahbari ota-onani bog'lay oladi"})
			return
		}
		var isMain bool
		err = dbConn.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM class_teachers 
				WHERE class_id = $1 AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false
			)
		`, classID, currentUserID).Scan(&isMain)
		if err != nil || !isMain {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: siz ushbu sinf rahbari emassiz"})
			return
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// 3. Check if parent user already exists by phone
	var parentID int
	var existingRoleName string
	err = tx.QueryRow(`
		SELECT u.id, r.name FROM users u
		JOIN roles r ON u.role_id = r.id
		WHERE u.phone = $1 AND u.is_deleted = false
	`, req.Phone).Scan(&parentID, &existingRoleName)

	if err != nil && err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing parent user", "details": err.Error()})
		return
	}

	isNewUser := false
	if err == sql.ErrNoRows {
		// Create new parent user
		isNewUser = true

		// Resolve role ID for PARENT
		var parentRoleID int
		err = tx.QueryRow("SELECT id FROM roles WHERE name = 'PARENT'").Scan(&parentRoleID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Role 'PARENT' not initialized in database"})
			return
		}

		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt password"})
			return
		}

		insertUserQuery := `
			INSERT INTO users (first_name, last_name, middle_name, passport, phone, email, password_hash, role_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id`
		err = tx.QueryRow(insertUserQuery, req.FirstName, req.LastName, req.MiddleName, req.Passport, req.Phone, req.Email, string(hashedPassword), parentRoleID).Scan(&parentID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create parent user profile", "details": err.Error()})
			return
		}

		// Audit Log for user creation
		audit.LogChange(c, tx, audit.LogData{
			Action:    "CREATE",
			TableName: "users",
			RecordID:  strconv.Itoa(parentID),
			NewValues: models.User{
				ID:         parentID,
				FirstName:  req.FirstName,
				LastName:   req.LastName,
				MiddleName: req.MiddleName,
				Passport:   req.Passport,
				Phone:      &req.Phone,
				Email:      req.Email,
				RoleID:     parentRoleID,
				IsDeleted:  false,
			},
		})
	} else {
		// Existing user found. Verify that they have the PARENT role.
		if existingRoleName != "PARENT" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Telefon raqamli foydalanuvchi tizimda mavjud, lekin roli '%s'. Faqat PARENT roldagi foydalanuvchini bog'lash mumkin.", existingRoleName)})
			return
		}
	}

	// 4. Check if student parent link already exists
	var linkExists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM student_parents WHERE student_id = $1 AND parent_id = $2)", actualStudentID, parentID).Scan(&linkExists)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing link"})
		return
	}

	if linkExists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ushbu ota-ona allaqachon o'quvchiga bog'langan"})
		return
	}

	// 5. Create student-parent link
	_, err = tx.Exec("INSERT INTO student_parents (student_id, parent_id) VALUES ($1, $2)", actualStudentID, parentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to map parent to student", "details": err.Error()})
		return
	}

	// Audit Log for link creation
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "student_parents",
		RecordID:  fmt.Sprintf("%d-%d", actualStudentID, parentID),
		NewValues: map[string]int{
			"student_id": actualStudentID,
			"parent_id":  parentID,
		},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save link transaction"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":     "Ota-ona muvaffaqiyatli bog'landi",
		"parent_id":   parentID,
		"is_new_user": isNewUser,
	})
}

// ListStudentParents fetches all parents linked to a specific student
func (h *ParentHandler) ListStudentParents(c *gin.Context) {
	studentIDStr := c.Param("id")
	studentID, err := strconv.Atoi(studentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// 1. Get student class ID and actual students.id profile
	var actualStudentID, classID int
	err = dbConn.QueryRow("SELECT id, class_id FROM students WHERE (id = $1 OR user_id = $1) AND is_deleted = false", studentID).Scan(&actualStudentID, &classID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi yoki o'chirilgan"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query student info", "details": err.Error()})
		}
		return
	}

	// 2. Authorization check: Admin or assigned main teacher of student's class
	if userRole != "ADMIN" {
		if userRole != "MAIN_TEACHER" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va ushbu sinf rahbari ota-onalarni ko'ra oladi"})
			return
		}
		var isMain bool
		err = dbConn.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM class_teachers 
				WHERE class_id = $1 AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false
			)
		`, classID, currentUserID).Scan(&isMain)
		if err != nil || !isMain {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: siz ushbu sinf rahbari emassiz"})
			return
		}
	}

	// 3. Retrieve parents
	query := `
		SELECT u.id, u.email, u.phone, u.first_name, u.last_name, u.middle_name, u.created_at
		FROM student_parents sp
		JOIN users u ON sp.parent_id = u.id
		WHERE sp.student_id = $1 AND u.is_deleted = false
		ORDER BY u.first_name, u.last_name`

	rows, err := dbConn.Query(query, actualStudentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query linked parents", "details": err.Error()})
		return
	}
	defer rows.Close()

	type ParentResponse struct {
		ID         int       `json:"id"`
		FirstName  string    `json:"first_name"`
		LastName   string    `json:"last_name"`
		MiddleName *string   `json:"middle_name,omitempty"`
		Phone      string    `json:"phone"`
		Email      *string   `json:"email,omitempty"`
		CreatedAt  time.Time `json:"created_at"`
	}

	list := []ParentResponse{}
	for rows.Next() {
		var p ParentResponse
		var emailNull, middleNameNull sql.NullString
		err := rows.Scan(&p.ID, &emailNull, &p.Phone, &p.FirstName, &p.LastName, &middleNameNull, &p.CreatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan parent record", "details": err.Error()})
			return
		}
		if emailNull.Valid {
			p.Email = &emailNull.String
		}
		if middleNameNull.Valid {
			p.MiddleName = &middleNameNull.String
		}
		list = append(list, p)
	}

	c.JSON(http.StatusOK, list)
}

// UnlinkParent removes the connection between parent and student
func (h *ParentHandler) UnlinkParent(c *gin.Context) {
	studentIDStr := c.Param("id")
	studentID, err := strconv.Atoi(studentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student ID"})
		return
	}

	parentIDStr := c.Param("parent_id")
	parentID, err := strconv.Atoi(parentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parent ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// 1. Get student class ID and actual students.id profile
	var actualStudentID, classID int
	err = dbConn.QueryRow("SELECT id, class_id FROM students WHERE (id = $1 OR user_id = $1) AND is_deleted = false", studentID).Scan(&actualStudentID, &classID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi yoki o'chirilgan"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query student details", "details": err.Error()})
		}
		return
	}

	// 2. Authorization check: Admin or assigned main teacher of student's class
	if userRole != "ADMIN" {
		if userRole != "MAIN_TEACHER" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va ushbu sinf rahbari ota-onani o'chira oladi"})
			return
		}
		var isMain bool
		err = dbConn.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM class_teachers 
				WHERE class_id = $1 AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false
			)
		`, classID, currentUserID).Scan(&isMain)
		if err != nil || !isMain {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: siz ushbu sinf rahbari emassiz"})
			return
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// 3. Verify link exists before deleting
	var linkExists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM student_parents WHERE student_id = $1 AND parent_id = $2)", actualStudentID, parentID).Scan(&linkExists)
	if err != nil || !linkExists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bog'liqlik topilmadi"})
		return
	}

	// 4. Delete the link record
	_, err = tx.Exec("DELETE FROM student_parents WHERE student_id = $1 AND parent_id = $2", actualStudentID, parentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete student parent link", "details": err.Error()})
		return
	}

	// Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "student_parents",
		RecordID:  fmt.Sprintf("%d-%d", actualStudentID, parentID),
		OldValues: map[string]int{
			"student_id": actualStudentID,
			"parent_id":  parentID,
		},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit unlink action"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Ota-ona o'quvchidan muvaffaqiyatli ajratildi"})
}

// UpdateParentRequest holds editable fields for a parent user
type UpdateParentRequest struct {
	FirstName  *string `json:"first_name"`
	LastName   *string `json:"last_name"`
	MiddleName *string `json:"middle_name"`
	Passport   *string `json:"passport"`
	Phone      *string `json:"phone"`
	Password   *string `json:"password"`
}

// UpdateParent updates a parent user's profile
func (h *ParentHandler) UpdateParent(c *gin.Context) {
	parentIDStr := c.Param("parent_id")
	parentID, err := strconv.Atoi(parentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parent ID"})
		return
	}

	var req UpdateParentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// Verify parent exists and has PARENT role
	var parentRoleID int
	var parentRoleName string
	err = dbConn.QueryRow(`SELECT u.id, r.name FROM users u JOIN roles r ON u.role_id = r.id WHERE u.id = $1 AND u.is_deleted = false`, parentID).Scan(&parentRoleID, &parentRoleName)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Ota-ona topilmadi"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ota-ona ma'lumotlarini olishda xatolik"})
		return
	}
	if parentRoleName != "PARENT" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Foydalanuvchi PARENT emas"})
		return
	}

	// Authorization: admin, parent themselves, or main teacher of any class this parent's student is in
	if userRole != "ADMIN" && currentUserID != parentID {
		var isMain bool
		dbConn.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM class_teachers ct
				JOIN student_parents sp ON sp.student_id IN (
					SELECT id FROM students WHERE is_deleted = false
				)
				JOIN students s ON sp.student_id = s.id AND s.is_deleted = false
				WHERE sp.parent_id = $1 AND ct.teacher_id = $2 AND ct.class_id = s.class_id AND ct.is_main_teacher = true AND ct.is_deleted = false
			)`, parentID, currentUserID).Scan(&isMain)
		if !isMain {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan"})
			return
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure"})
		return
	}
	defer tx.Rollback()

	// Read old values for audit
	var oldUser struct {
		FirstName  string
		LastName   string
		MiddleName *string
		Passport   *string
		Phone      *string
		RoleID     int
	}
	var oldPhoneNull, oldMiddleNull, oldPassportNull sql.NullString
	tx.QueryRow(`SELECT first_name, last_name, middle_name, passport, phone, role_id FROM users WHERE id = $1`, parentID).
		Scan(&oldUser.FirstName, &oldUser.LastName, &oldMiddleNull, &oldPassportNull, &oldPhoneNull, &oldUser.RoleID)
	if oldMiddleNull.Valid {
		oldUser.MiddleName = &oldMiddleNull.String
	}
	if oldPassportNull.Valid {
		oldUser.Passport = &oldPassportNull.String
	}
	if oldPhoneNull.Valid {
		oldUser.Phone = &oldPhoneNull.String
	}

	// Build dynamic update
	firstName := oldUser.FirstName
	if req.FirstName != nil && *req.FirstName != "" {
		firstName = *req.FirstName
	}

	lastName := oldUser.LastName
	if req.LastName != nil && *req.LastName != "" {
		lastName = *req.LastName
	}

	middleName := oldUser.MiddleName
	if req.MiddleName != nil {
		middleName = req.MiddleName
	}

	passport := oldUser.Passport
	if req.Passport != nil {
		passport = req.Passport
	}

	phone := ""
	if oldUser.Phone != nil {
		phone = *oldUser.Phone
	}
	if req.Phone != nil && *req.Phone != "" {
		phone = *req.Phone
	}

	setClauses := []string{"first_name = $1", "last_name = $2", "middle_name = $3", "passport = $4", "phone = $5", "updated_at = NOW()"}
	args := []interface{}{firstName, lastName, middleName, passport, phone}

	if req.Password != nil && *req.Password != "" {
		hashed, hashErr := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Parolni shifrlashda xatolik"})
			return
		}
		setClauses = append(setClauses, fmt.Sprintf("password_hash = $%d", len(args)+1))
		args = append(args, string(hashed))
	}
	args = append(args, parentID)

	updateQuery := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d", strings.Join(setClauses, ", "), len(args))
	_, err = tx.Exec(updateQuery, args...)
	if err != nil {
		if strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "users_phone_key") {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Telefon raqam '%s' allaqachon ro'yxatdan o'tgan", phone)})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Ota-ona ma'lumotlarini yangilashda xatolik", "details": err.Error()})
		}
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "users",
		RecordID:  strconv.Itoa(parentID),
		OldValues: oldUser,
		NewValues: map[string]interface{}{"first_name": firstName, "last_name": lastName, "middle_name": middleName, "passport": passport, "phone": phone},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit update"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          parentID,
		"first_name":  firstName,
		"last_name":   lastName,
		"middle_name": middleName,
		"passport":    passport,
		"phone":       phone,
	})
}

// GetParent retrieves a parent user's profile details from the database
func (h *ParentHandler) GetParent(c *gin.Context) {
	parentIDStr := c.Param("parent_id")
	parentID, err := strconv.Atoi(parentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parent ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	var user struct {
		ID         int     `json:"id"`
		FirstName  string  `json:"first_name"`
		LastName   string  `json:"last_name"`
		MiddleName *string `json:"middle_name"`
		Passport   *string `json:"passport"`
		Phone      *string `json:"phone"`
		Email      *string `json:"email"`
		Role       string  `json:"role"`
		TelegramID *string `json:"telegram_id"`
	}

	query := `
		SELECT u.id, u.first_name, u.last_name, u.middle_name, u.passport, u.phone, u.email, r.name, u.telegram_id
		FROM users u
		JOIN roles r ON u.role_id = r.id
		WHERE u.id = $1 AND u.is_deleted = false`

	var middleName, passport, phone, email, telegramID sql.NullString
	err = dbConn.QueryRow(query, parentID).Scan(
		&user.ID, &user.FirstName, &user.LastName, &middleName, &passport, &phone, &email, &user.Role, &telegramID,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Ota-ona topilmadi"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database query failed", "details": err.Error()})
		}
		return
	}

	if middleName.Valid {
		user.MiddleName = &middleName.String
	}
	if passport.Valid {
		user.Passport = &passport.String
	}
	if phone.Valid {
		user.Phone = &phone.String
	}
	if email.Valid {
		user.Email = &email.String
	}
	if telegramID.Valid {
		user.TelegramID = &telegramID.String
	}

	c.JSON(http.StatusOK, user)
}
