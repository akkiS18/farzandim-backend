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

type TenantUserHandler struct{}

func NewTenantUserHandler() *TenantUserHandler {
	return &TenantUserHandler{}
}

type CreateStudentRequest struct {
	FirstName  string  `json:"first_name" binding:"required"`
	LastName   string  `json:"last_name" binding:"required"`
	MiddleName *string `json:"middle_name"`
	Email      *string `json:"email"`
}

type CreateTeacherRequest struct {
	FirstName  string  `json:"first_name" binding:"required"`
	LastName   string  `json:"last_name" binding:"required"`
	MiddleName *string `json:"middle_name"`
	Phone      string  `json:"phone" binding:"required"`
	RoleName   string  `json:"role" binding:"required"` // MAIN_TEACHER or SUBJECT_TEACHER
	Password   string  `json:"password" binding:"required"`
	Email      *string `json:"email"`
}

type AssignTeacherRequest struct {
	TeacherID     int  `json:"teacher_id" binding:"required"`
	SubjectID     int  `json:"subject_id" binding:"required"`
	IsMainTeacher bool `json:"is_main_teacher"`
}

type SubjectRequest struct {
	Name string `json:"name" binding:"required"`
}

type ClassTeacherResponse struct {
	ID            int     `json:"id"`
	ClassID       int     `json:"class_id"`
	SubjectID     int     `json:"subject_id"`
	SubjectName   string  `json:"subject_name"`
	TeacherID     int     `json:"teacher_id"`
	FirstName     string  `json:"first_name"`
	LastName      string  `json:"last_name"`
	MiddleName    *string `json:"middle_name,omitempty"`
	Phone         string  `json:"phone"`
	IsMainTeacher bool    `json:"is_main_teacher"`
	RoleName      string  `json:"role_name"`
}

// CreateClassStudent creates a student under a specific class manually
func (h *TenantUserHandler) CreateClassStudent(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class ID"})
		return
	}

	var req CreateStudentRequest
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

	// Authorization check: Admin or assigned main teacher of this class
	if userRole != "ADMIN" {
		if userRole != "MAIN_TEACHER" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va ushbu sinf rahbari o'quvchi qo'sha oladi"})
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

	// Get role ID for STUDENT
	var studentRoleID int
	err = dbConn.QueryRow("SELECT id FROM roles WHERE name = 'STUDENT'").Scan(&studentRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Role 'STUDENT' is not initialized"})
		return
	}

	// Hash password
	passText := "STUDENT_NO_LOGIN_ACCESS_RANDOM_PASS_" + time.Now().Format("20060102150405.000")
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(passText), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt password"})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Insert User
	var userID int
	insertUserQuery := `
		INSERT INTO users (first_name, last_name, middle_name, phone, email, password_hash, role_id)
		VALUES ($1, $2, $3, NULL, $4, $5, $6)
		RETURNING id`
	err = tx.QueryRow(insertUserQuery, req.FirstName, req.LastName, req.MiddleName, req.Email, string(hashedPassword), studentRoleID).Scan(&userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write user profile", "details": err.Error()})
		return
	}

	// Insert Student link
	var studentID int
	insertStudentQuery := `
		INSERT INTO students (user_id, class_id)
		VALUES ($1, $2)
		RETURNING id`
	err = tx.QueryRow(insertStudentQuery, userID, classID).Scan(&studentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to map student profile", "details": err.Error()})
		return
	}

	newUser := models.User{
		ID:         userID,
		FirstName:  req.FirstName,
		LastName:   req.LastName,
		MiddleName: req.MiddleName,
		Phone:      nil,
		Email:      req.Email,
		RoleID:     studentRoleID,
		IsDeleted:  false,
	}


	// Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "users",
		RecordID:  strconv.Itoa(userID),
		NewValues: newUser,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save data (Commit)", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newUser)
}

// CreateTeacher creates a global teacher user manually
func (h *TenantUserHandler) CreateTeacher(c *gin.Context) {
	var req CreateTeacherRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
		return
	}

	req.RoleName = strings.ToUpper(req.RoleName)
	if req.RoleName != "MAIN_TEACHER" && req.RoleName != "SUBJECT_TEACHER" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Rol faqat MAIN_TEACHER yoki SUBJECT_TEACHER bo'lishi mumkin"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	var roleID int
	err := dbConn.QueryRow("SELECT id FROM roles WHERE name = $1", req.RoleName).Scan(&roleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Role '%s' is not initialized in tenant DB", req.RoleName)})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt password"})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	var userID int
	insertUserQuery := `
		INSERT INTO users (first_name, last_name, middle_name, phone, email, password_hash, role_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`
	err = tx.QueryRow(insertUserQuery, req.FirstName, req.LastName, req.MiddleName, req.Phone, req.Email, string(hashedPassword), roleID).Scan(&userID)
	if err != nil {
		if strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "users_phone_key") {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Telefon raqam '%s' allaqachon ro'yxatdan o'tgan", req.Phone)})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write user profile", "details": err.Error()})
		}
		return
	}

	newUser := models.User{
		ID:         userID,
		FirstName:  req.FirstName,
		LastName:   req.LastName,
		MiddleName: req.MiddleName,
		Phone:      &req.Phone,
		Email:      req.Email,
		RoleID:     roleID,
		IsDeleted:  false,
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "users",
		RecordID:  strconv.Itoa(userID),
		NewValues: newUser,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save data (Commit)", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newUser)
}

// ListTeachers fetches all registered teachers in the tenant database
func (h *TenantUserHandler) ListTeachers(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	query := `
		SELECT u.id, u.email, u.phone, u.first_name, u.last_name, u.middle_name, u.role_id, r.name as role_name, u.created_at
		FROM users u
		JOIN roles r ON u.role_id = r.id
		WHERE r.name IN ('MAIN_TEACHER', 'SUBJECT_TEACHER') AND u.is_deleted = false
		ORDER BY u.first_name, u.last_name`

	rows, err := dbConn.Query(query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query teachers", "details": err.Error()})
		return
	}
	defer rows.Close()

	teachers := []TenantUserResponse{}
	for rows.Next() {
		var u TenantUserResponse
		var emailNull, middleNameNull, phoneNull sql.NullString

		err := rows.Scan(&u.ID, &emailNull, &phoneNull, &u.FirstName, &u.LastName, &middleNameNull, &u.RoleID, &u.RoleName, &u.CreatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse teacher record", "details": err.Error()})
			return
		}

		if emailNull.Valid {
			u.Email = &emailNull.String
		}
		if phoneNull.Valid {
			u.Phone = &phoneNull.String
		}
		if middleNameNull.Valid {
			u.MiddleName = &middleNameNull.String
		}

		teachers = append(teachers, u)
	}

	c.JSON(http.StatusOK, teachers)
}

// ListClassTeachers lists teachers assigned to a specific class
func (h *TenantUserHandler) ListClassTeachers(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	query := `
		SELECT ct.id, ct.class_id, ct.subject_id, s.name as subject_name, ct.teacher_id,
		       u.first_name, u.last_name, u.middle_name, u.phone, ct.is_main_teacher, r.name as role_name
		FROM class_teachers ct
		JOIN users u ON ct.teacher_id = u.id
		JOIN roles r ON u.role_id = r.id
		JOIN subjects s ON ct.subject_id = s.id
		WHERE ct.class_id = $1 AND ct.is_deleted = false AND u.is_deleted = false AND s.is_deleted = false
		ORDER BY ct.is_main_teacher DESC, u.first_name, u.last_name`

	rows, err := dbConn.Query(query, classID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query class teachers", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []ClassTeacherResponse{}
	for rows.Next() {
		var item ClassTeacherResponse
		var middleNameNull sql.NullString

		err := rows.Scan(
			&item.ID, &item.ClassID, &item.SubjectID, &item.SubjectName, &item.TeacherID,
			&item.FirstName, &item.LastName, &middleNameNull, &item.Phone, &item.IsMainTeacher, &item.RoleName,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan class teacher", "details": err.Error()})
			return
		}

		if middleNameNull.Valid {
			item.MiddleName = &middleNameNull.String
		}

		list = append(list, item)
	}

	c.JSON(http.StatusOK, list)
}

// AssignClassTeacher links a teacher to a class with a subject and toggle
func (h *TenantUserHandler) AssignClassTeacher(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class ID"})
		return
	}

	var req AssignTeacherRequest
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

	// Authorization check: Admin or assigned main teacher of this class
	if userRole != "ADMIN" {
		if userRole != "MAIN_TEACHER" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va ushbu sinf rahbari o'qituvchi biriktira oladi"})
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

		// A non-admin cannot assign someone as the main teacher of a class
		if req.IsMainTeacher {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin sinf rahbarini tayinlay oladi"})
			return
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Verify that the teacher exists and has a teacher role
	var teacherRoleName string
	err = tx.QueryRow(`
		SELECT r.name FROM users u
		JOIN roles r ON u.role_id = r.id
		WHERE u.id = $1 AND u.is_deleted = false
	`, req.TeacherID).Scan(&teacherRoleName)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Tanlangan o'qituvchi topilmadi yoki o'chirilgan"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify teacher role", "details": err.Error()})
		}
		return
	}
	if teacherRoleName != "MAIN_TEACHER" && teacherRoleName != "SUBJECT_TEACHER" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Faqat o'qituvchi rolidagi foydalanuvchini biriktirish mumkin"})
		return
	}

	// Verify that the subject exists
	var subjectExists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM subjects WHERE id = $1 AND is_deleted = false)", req.SubjectID).Scan(&subjectExists)
	if err != nil || !subjectExists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Tanlangan fan topilmadi yoki o'chirilgan"})
		return
	}

	// If this is set as main teacher, turn off is_main_teacher flag for any other teacher in this class
	if req.IsMainTeacher {
		_, err = tx.Exec("UPDATE class_teachers SET is_main_teacher = false WHERE class_id = $1", classID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reset previous main teacher flag", "details": err.Error()})
			return
		}
	}

	// Check if this mapping already exists (even if soft-deleted)
	var mappingID int
	var isDeleted bool
	err = tx.QueryRow("SELECT id, is_deleted FROM class_teachers WHERE class_id = $1 AND subject_id = $2 AND teacher_id = $3", classID, req.SubjectID, req.TeacherID).Scan(&mappingID, &isDeleted)

	if err != nil {
		if err == sql.ErrNoRows {
			// Insert new active link
			insertQuery := `
				INSERT INTO class_teachers (class_id, subject_id, teacher_id, is_main_teacher)
				VALUES ($1, $2, $3, $4)
				RETURNING id`
			err = tx.QueryRow(insertQuery, classID, req.SubjectID, req.TeacherID, req.IsMainTeacher).Scan(&mappingID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to map class teacher link", "details": err.Error()})
				return
			}
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify class teacher mapping", "details": err.Error()})
			return
		}
	} else {
		// Mapping exists. If soft-deleted, reactivate it, otherwise update it
		updateQuery := `
			UPDATE class_teachers 
			SET is_deleted = false, deleted_at = NULL, is_main_teacher = $1 
			WHERE id = $2`
		_, err = tx.Exec(updateQuery, req.IsMainTeacher, mappingID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update class teacher link", "details": err.Error()})
			return
		}
	}

	// Fetch mapping details to return
	var res ClassTeacherResponse
	query := `
		SELECT ct.id, ct.class_id, ct.subject_id, s.name as subject_name, ct.teacher_id,
		       u.first_name, u.last_name, u.phone, ct.is_main_teacher, r.name as role_name
		FROM class_teachers ct
		JOIN users u ON ct.teacher_id = u.id
		JOIN roles r ON u.role_id = r.id
		JOIN subjects s ON ct.subject_id = s.id
		WHERE ct.id = $1`
	err = tx.QueryRow(query, mappingID).Scan(&res.ID, &res.ClassID, &res.SubjectID, &res.SubjectName, &res.TeacherID, &res.FirstName, &res.LastName, &res.Phone, &res.IsMainTeacher, &res.RoleName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve map details", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "class_teachers",
		RecordID:  strconv.Itoa(mappingID),
		NewValues: models.ClassTeacher{
			ID:            mappingID,
			ClassID:       classID,
			SubjectID:     req.SubjectID,
			TeacherID:     req.TeacherID,
			IsMainTeacher: req.IsMainTeacher,
			IsDeleted:     false,
		},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit assignment", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// UnassignClassTeacher removes teacher class subject linking (soft delete)
func (h *TenantUserHandler) UnassignClassTeacher(c *gin.Context) {
	classTeacherIDStr := c.Param("class_teacher_id")
	classTeacherID, err := strconv.Atoi(classTeacherIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid mapping ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Query old value
	var oldMapping models.ClassTeacher
	err = tx.QueryRow("SELECT id, class_id, subject_id, teacher_id, is_main_teacher, is_deleted FROM class_teachers WHERE id = $1 AND is_deleted = false", classTeacherID).
		Scan(&oldMapping.ID, &oldMapping.ClassID, &oldMapping.SubjectID, &oldMapping.TeacherID, &oldMapping.IsMainTeacher, &oldMapping.IsDeleted)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "O'qituvchi biriktiruvi topilmadi yoki allaqachon o'chirilgan"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query assignment info", "details": err.Error()})
		}
		return
	}

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// Authorization check: Admin or assigned main teacher of this class
	if userRole != "ADMIN" {
		if userRole != "MAIN_TEACHER" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va ushbu sinf rahbari o'qituvchi biriktiruvi o'chira oladi"})
			return
		}
		var isMain bool
		err = tx.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM class_teachers 
				WHERE class_id = $1 AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false
			)
		`, oldMapping.ClassID, currentUserID).Scan(&isMain)
		if err != nil || !isMain {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: siz ushbu sinf rahbari emassiz"})
			return
		}

		// A non-admin cannot unassign a main teacher
		if oldMapping.IsMainTeacher {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin sinf rahbarini o'chira oladi"})
			return
		}
	}

	// Update soft delete
	now := time.Now()
	_, err = tx.Exec("UPDATE class_teachers SET is_deleted = true, deleted_at = $1 WHERE id = $2", now, classTeacherID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to soft delete assignment", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "SOFT_DELETE",
		TableName: "class_teachers",
		RecordID:  strconv.Itoa(classTeacherID),
		OldValues: oldMapping,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit unassignment", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "O'qituvchi sinfdan muvaffaqiyatli o'chirildi"})
}

// ListSubjects fetches all active subjects
func (h *TenantUserHandler) ListSubjects(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	rows, err := dbConn.Query("SELECT id, name FROM subjects WHERE is_deleted = false ORDER BY name ASC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query subjects", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []models.Subject{}
	for rows.Next() {
		var s models.Subject
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan subject", "details": err.Error()})
			return
		}
		list = append(list, s)
	}

	c.JSON(http.StatusOK, list)
}

// CreateSubject inserts a new school subject
func (h *TenantUserHandler) CreateSubject(c *gin.Context) {
	var req SubjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Subject name is required", "details": err.Error()})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Check if subject exists (even soft-deleted)
	var subjectID int
	var isDeleted bool
	err = tx.QueryRow("SELECT id, is_deleted FROM subjects WHERE LOWER(name) = LOWER($1)", strings.TrimSpace(req.Name)).Scan(&subjectID, &isDeleted)

	if err != nil {
		if err == sql.ErrNoRows {
			// Insert new
			err = tx.QueryRow("INSERT INTO subjects (name) VALUES ($1) RETURNING id", strings.TrimSpace(req.Name)).Scan(&subjectID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write subject record", "details": err.Error()})
				return
			}
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify subject details", "details": err.Error()})
			return
		}
	} else {
		if isDeleted {
			// Reactivate
			_, err = tx.Exec("UPDATE subjects SET is_deleted = false, deleted_at = NULL WHERE id = $1", subjectID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reactivate subject", "details": err.Error()})
				return
			}
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Fan '%s' allaqachon mavjud", req.Name)})
			return
		}
	}

	newSubject := models.Subject{
		ID:        subjectID,
		Name:      strings.TrimSpace(req.Name),
		IsDeleted: false,
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "subjects",
		RecordID:  strconv.Itoa(subjectID),
		NewValues: newSubject,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit subject creation", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newSubject)
}

// UpdateStudentRequest holds fields that can be updated for a student
type UpdateStudentRequest struct {
	FirstName  string  `json:"first_name" binding:"required"`
	LastName   string  `json:"last_name" binding:"required"`
	MiddleName *string `json:"middle_name"`
	Phone      *string `json:"phone"`
	Password   *string `json:"password"`
}

// UpdateStudent updates a student user's profile (name, phone, password)
func (h *TenantUserHandler) UpdateStudent(c *gin.Context) {
	studentIDStr := c.Param("id")
	studentID, err := strconv.Atoi(studentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student ID"})
		return
	}

	var req UpdateStudentRequest
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

	// Resolve student → user_id, and validate authorization
	var targetUserID int
	var classID int
	err = dbConn.QueryRow(`SELECT s.user_id, s.class_id FROM students s WHERE s.id = $1 AND s.is_deleted = false`, studentID).Scan(&targetUserID, &classID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "O'quvchi ma'lumotlarini olishda xatolik", "details": err.Error()})
		return
	}

	if userRole != "ADMIN" {
		var isMain bool
		dbConn.QueryRow(`SELECT EXISTS(SELECT 1 FROM class_teachers WHERE class_id = $1 AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false)`, classID, currentUserID).Scan(&isMain)
		if !isMain {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: siz ushbu sinf rahbari emassiz"})
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
	var oldUser models.User
	var oldPhoneNull sql.NullString
	var oldMiddleNameNull sql.NullString
	tx.QueryRow(`SELECT id, first_name, last_name, middle_name, phone, role_id FROM users WHERE id = $1`, targetUserID).
		Scan(&oldUser.ID, &oldUser.FirstName, &oldUser.LastName, &oldMiddleNameNull, &oldPhoneNull, &oldUser.RoleID)
	if oldMiddleNameNull.Valid {
		oldUser.MiddleName = &oldMiddleNameNull.String
	}
	if oldPhoneNull.Valid {
		oldUser.Phone = &oldPhoneNull.String
	}

	// Build update query dynamically
	setClauses := []string{"first_name = $1", "last_name = $2", "middle_name = $3", "phone = $4", "updated_at = NOW()"}
	args := []interface{}{req.FirstName, req.LastName, req.MiddleName, req.Phone}

	if req.Password != nil && *req.Password != "" {
		hashed, hashErr := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Parolni shifrlashda xatolik"})
			return
		}
		setClauses = append(setClauses, fmt.Sprintf("password_hash = $%d", len(args)+1))
		args = append(args, string(hashed))
	}

	args = append(args, targetUserID)
	updateQuery := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d", strings.Join(setClauses, ", "), len(args))
	_, err = tx.Exec(updateQuery, args...)
	if err != nil {
		if strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "users_phone_key") {
			phone := ""
			if req.Phone != nil {
				phone = *req.Phone
			}
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Telefon raqam '%s' allaqachon ro'yxatdan o'tgan", phone)})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "O'quvchi ma'lumotlarini yangilashda xatolik", "details": err.Error()})
		}
		return
	}

	newUser := models.User{
		ID:         targetUserID,
		FirstName:  req.FirstName,
		LastName:   req.LastName,
		MiddleName: req.MiddleName,
		Phone:      req.Phone,
		RoleID:     oldUser.RoleID,
		IsDeleted:  false,
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "users",
		RecordID:  strconv.Itoa(targetUserID),
		OldValues: oldUser,
		NewValues: newUser,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit update", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, newUser)
}

// DeleteStudent soft-deletes a student and their user record
func (h *TenantUserHandler) DeleteStudent(c *gin.Context) {
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

	// Resolve student → user_id, class_id
	var targetUserID int
	var classID int
	err = dbConn.QueryRow(`SELECT s.user_id, s.class_id FROM students s WHERE s.id = $1 AND s.is_deleted = false`, studentID).Scan(&targetUserID, &classID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "O'quvchi ma'lumotlarini olishda xatolik", "details": err.Error()})
		return
	}

	if userRole != "ADMIN" {
		var isMain bool
		dbConn.QueryRow(`SELECT EXISTS(SELECT 1 FROM class_teachers WHERE class_id = $1 AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false)`, classID, currentUserID).Scan(&isMain)
		if !isMain {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: siz ushbu sinf rahbari emassiz"})
			return
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure"})
		return
	}
	defer tx.Rollback()

	now := "NOW()"
	_, err = tx.Exec(`UPDATE students SET is_deleted = true, deleted_at = `+now+` WHERE id = $1`, studentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "O'quvchi profilini o'chirishda xatolik", "details": err.Error()})
		return
	}
	_, err = tx.Exec(`UPDATE users SET is_deleted = true, deleted_at = NOW() WHERE id = $1`, targetUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Foydalanuvchi profilini o'chirishda xatolik", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "students",
		RecordID:  strconv.Itoa(studentID),
		OldValues: map[string]interface{}{"student_id": studentID, "user_id": targetUserID},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit deletion", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "O'quvchi muvaffaqiyatli o'chirildi"})
}
