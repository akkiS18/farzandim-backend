package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type TenantUserHandler struct{}

func NewTenantUserHandler() *TenantUserHandler {
	return &TenantUserHandler{}
}

type CreateStudentRequest struct {
	FirstName      string  `json:"first_name" binding:"required"`
	LastName       string  `json:"last_name" binding:"required"`
	MiddleName     *string `json:"middle_name"`
	Email          *string `json:"email"`
	Address        *string `json:"address"`
	BirthDate      *string `json:"birthdate"`
	EnrollmentDate *string `json:"enrollment_date"`
	INA            *string `json:"ina"`
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

type UpdateTeacherRequest struct {
	FirstName  string  `json:"first_name" binding:"required"`
	LastName   string  `json:"last_name" binding:"required"`
	MiddleName *string `json:"middle_name"`
	Phone      string  `json:"phone"`
	RoleName   string  `json:"role"`
	Password   *string `json:"password"`
}

type AssignTeacherRequest struct {
	TeacherID     int   `json:"teacher_id" binding:"required"`
	SubjectID     *int  `json:"subject_id"`
	IsMainTeacher bool  `json:"is_main_teacher"`
}

type SubjectRequest struct {
	Name         string  `json:"name" binding:"required"`
	TargetLevels []int64 `json:"target_levels"`
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

	// Check INA uniqueness if provided
	if req.INA != nil {
		cleanINA := strings.TrimSpace(*req.INA)
		if cleanINA != "" && cleanINA != "-" && !strings.EqualFold(cleanINA, "yo'q") {
			normINA := NormalizeDocumentNo(cleanINA)
			reg, _ := regexp.Compile("[^a-z0-9]")
			rawNorm := reg.ReplaceAllString(strings.ToLower(normINA), "")

			var existingStudentName, existingClassName string
			err = dbConn.QueryRow(`
				SELECT COALESCE(u.first_name || ' ' || u.last_name, ''), COALESCE(c.name, 'Sinfatsiz')
				FROM students s
				JOIN users u ON s.user_id = u.id
				LEFT JOIN classes c ON s.class_id = c.id
				WHERE (
					LOWER(TRIM(s.ina)) = LOWER($1)
					OR LOWER(TRIM(s.ina)) = LOWER($2)
					OR REGEXP_REPLACE(LOWER(s.ina), '[^a-z0-9]', '', 'g') = $3
					OR REGEXP_REPLACE(REGEXP_REPLACE(LOWER(s.ina), '^[l1]-', 'i-'), '[^a-z0-9]', '', 'g') = $3
				)
				  AND s.is_deleted = false AND u.is_deleted = false AND (c.id IS NULL OR c.is_deleted = false)
				LIMIT 1
			`, cleanINA, normINA, rawNorm).Scan(&existingStudentName, &existingClassName)
			if err == nil {
				c.JSON(http.StatusConflict, gin.H{
					"error": fmt.Sprintf("Ushbu Guvohnoma (INA) raqami ('%s') bilan '%s' ismli o'quvchi '%s' sinfida allaqachon mavjud!", cleanINA, existingStudentName, existingClassName),
				})
				return
			}
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
	var birthdateVal interface{}
	if req.BirthDate != nil && *req.BirthDate != "" {
		birthdateVal = *req.BirthDate
	} else {
		birthdateVal = nil
	}

	var enrollmentDateVal interface{}
	if req.EnrollmentDate != nil && *req.EnrollmentDate != "" {
		if parsedDate, err := time.Parse("2006-01-02", *req.EnrollmentDate); err == nil {
			enrollmentDateVal = parsedDate
		} else {
			enrollmentDateVal = time.Now()
		}
	} else {
		enrollmentDateVal = time.Now()
	}

	insertStudentQuery := `
		INSERT INTO students (user_id, class_id, address, birthdate, ina, enrollment_date)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`
	err = tx.QueryRow(insertStudentQuery, userID, classID, req.Address, birthdateVal, req.INA, enrollmentDateVal).Scan(&studentID)
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

// UpdateTeacher updates a teacher user's profile details
func (h *TenantUserHandler) UpdateTeacher(c *gin.Context) {
	teacherIDStr := c.Param("id")
	teacherID, err := strconv.Atoi(teacherIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid teacher ID"})
		return
	}

	callerRole, _ := c.Get("role")
	callerUserID, _ := c.Get("userID")
	if callerRole != "ADMIN" && fmt.Sprintf("%v", callerUserID) != teacherIDStr {
		c.JSON(http.StatusForbidden, gin.H{"error": "Siz faqat shaxsiy profilingizni o'zgartirishingiz mumkin"})
		return
	}

	var req UpdateTeacherRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
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

	// Get old user details for audit
	var oldUser models.User
	var oldPhoneNull, oldMiddleNameNull sql.NullString
	err = tx.QueryRow(`
		SELECT u.id, u.first_name, u.last_name, u.middle_name, u.phone, u.role_id 
		FROM users u 
		WHERE u.id = $1 AND u.is_deleted = false
	`, teacherID).Scan(&oldUser.ID, &oldUser.FirstName, &oldUser.LastName, &oldMiddleNameNull, &oldPhoneNull, &oldUser.RoleID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Foydalanuvchi topilmadi"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query teacher details", "details": err.Error()})
		}
		return
	}
	if oldMiddleNameNull.Valid {
		oldUser.MiddleName = &oldMiddleNameNull.String
	}
	if oldPhoneNull.Valid {
		oldUser.Phone = &oldPhoneNull.String
	}

	roleID := oldUser.RoleID
	if req.RoleName != "" {
		req.RoleName = strings.ToUpper(req.RoleName)
		if req.RoleName != "MAIN_TEACHER" && req.RoleName != "SUBJECT_TEACHER" && req.RoleName != "ADMIN" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Rol faqat MAIN_TEACHER, SUBJECT_TEACHER yoki ADMIN bo'lishi mumkin"})
			return
		}
		err = dbConn.QueryRow("SELECT id FROM roles WHERE name = $1", req.RoleName).Scan(&roleID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Role '%s' is not initialized", req.RoleName)})
			return
		}
	}

	var phonePtr *string
	if strings.TrimSpace(req.Phone) != "" {
		p := strings.TrimSpace(req.Phone)
		phonePtr = &p
	} else {
		phonePtr = oldUser.Phone
	}

	var middleNamePtr *string
	if req.MiddleName != nil {
		middleNamePtr = req.MiddleName
	} else {
		middleNamePtr = oldUser.MiddleName
	}

	setClauses := []string{
		"first_name = $1",
		"last_name = $2",
		"middle_name = $3",
		"phone = $4",
		"role_id = $5",
		"updated_at = NOW()",
	}
	args := []interface{}{req.FirstName, req.LastName, middleNamePtr, phonePtr, roleID}

	if req.Password != nil && *req.Password != "" {
		hashed, hashErr := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt password"})
			return
		}
		setClauses = append(setClauses, fmt.Sprintf("password_hash = $%d", len(args)+1))
		args = append(args, string(hashed))
	}

	args = append(args, teacherID)
	updateQuery := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d", strings.Join(setClauses, ", "), len(args))

	_, err = tx.Exec(updateQuery, args...)
	if err != nil {
		if strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "users_phone_key") {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Telefon raqam '%s' allaqachon ro'yxatdan o'tgan", req.Phone)})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update teacher profile", "details": err.Error()})
		}
		return
	}

	newUser := models.User{
		ID:         teacherID,
		FirstName:  req.FirstName,
		LastName:   req.LastName,
		MiddleName: middleNamePtr,
		Phone:      phonePtr,
		RoleID:     roleID,
		IsDeleted:  false,
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "users",
		RecordID:  strconv.Itoa(teacherID),
		OldValues: oldUser,
		NewValues: newUser,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit profile update", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, newUser)
}

type BatchTransferStudentsRequest struct {
	UserIDs    []int `json:"user_ids"`
	StudentIDs []int `json:"student_ids"`
}

// TransferStudentsClass handles batch transfer of students to a target class
func (h *TenantUserHandler) TransferStudentsClass(c *gin.Context) {
	classIDStr := c.Param("id")
	targetClassID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid target class ID"})
		return
	}

	var req BatchTransferStudentsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request fields", "details": err.Error()})
		return
	}

	// Merge UserIDs and StudentIDs to support both input formats
	idMap := make(map[int]bool)
	for _, id := range req.UserIDs {
		if id > 0 {
			idMap[id] = true
		}
	}
	for _, id := range req.StudentIDs {
		if id > 0 {
			idMap[id] = true
		}
	}

	if len(idMap) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "O'tkazish uchun kamida bitta user_id yoki student_id kiritilishi shart"})
		return
	}

	var targetIDs []int
	for id := range idMap {
		targetIDs = append(targetIDs, id)
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// 1. Verify target class exists and is not deleted
	var targetClassExists bool
	err = dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM classes WHERE id = $1 AND is_deleted = false)", targetClassID).Scan(&targetClassExists)
	if err != nil || !targetClassExists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Manzil sinf topilmadi yoki o'chirilgan"})
		return
	}

	// 2. Fetch and validate all requested users/students
	queryUsers := `
		SELECT u.id as user_id, u.first_name, u.last_name, r.name as role_name, s.id as student_id, COALESCE(s.class_id, 0) as source_class_id
		FROM users u
		JOIN roles r ON u.role_id = r.id
		LEFT JOIN students s ON s.user_id = u.id AND s.is_deleted = false
		WHERE (u.id = ANY($1) OR s.id = ANY($1)) AND u.is_deleted = false`

	rows, err := dbConn.Query(queryUsers, pq.Array(targetIDs))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query students for validation", "details": err.Error()})
		return
	}
	defer rows.Close()

	type StudentTransferMeta struct {
		StudentID     int
		UserID        int
		FirstName     string
		LastName      string
		SourceClassID int
	}

	var validStudents []StudentTransferMeta
	sourceClassIDsMap := make(map[int]bool)
	sourceClassIDsMap[targetClassID] = true

	for rows.Next() {
		var uID, sID, scID int
		var fname, lname, rname string
		var sIDNull, scIDNull sql.NullInt64

		err := rows.Scan(&uID, &fname, &lname, &rname, &sIDNull, &scIDNull)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse student data", "details": err.Error()})
			return
		}

		if rname != "STUDENT" || !sIDNull.Valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Foydalanuvchi '%s %s' (ID %d) o'quvchi rolida emas", fname, lname, uID)})
			return
		}

		sID = int(sIDNull.Int64)
		if scIDNull.Valid {
			scID = int(scIDNull.Int64)
		}

		validStudents = append(validStudents, StudentTransferMeta{
			StudentID:     sID,
			UserID:        uID,
			FirstName:     fname,
			LastName:      lname,
			SourceClassID: scID,
		})

		if scID > 0 {
			sourceClassIDsMap[scID] = true
		}
	}

	if len(validStudents) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Birorta ham mos keladigan faol o'quvchi topilmadi"})
		return
	}

	// 3. Authorization Check: Admin OR Sinb Rahbari of target class / source classes
	if userRole != "ADMIN" {
		if userRole != "MAIN_TEACHER" && userRole != "SUBJECT_TEACHER" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va tegishli sinf rahbari o'quvchilarni o'tkaza oladi"})
			return
		}

		var checkClasses []int
		for cid := range sourceClassIDsMap {
			checkClasses = append(checkClasses, cid)
		}

		var isMainTeacher bool
		err = dbConn.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM class_teachers 
				WHERE class_id = ANY($1) AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false
			)
		`, pq.Array(checkClasses), currentUserID).Scan(&isMainTeacher)

		if err != nil || !isMainTeacher {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: siz o'quvchilarning manba yoki manzil sinf rahbari emassiz"})
			return
		}
	}

	// 4. Perform atomic bulk transfer in database transaction
	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	var updatedStudentIDs []int
	for _, st := range validStudents {
		updatedStudentIDs = append(updatedStudentIDs, st.StudentID)
	}

	_, err = tx.Exec("UPDATE students SET class_id = $1 WHERE id = ANY($2) AND is_deleted = false", targetClassID, pq.Array(updatedStudentIDs))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to transfer students in database", "details": err.Error()})
		return
	}

	// 5. Audit Log per transferred student
	for _, st := range validStudents {
		audit.LogChange(c, tx, audit.LogData{
			Action:    "CLASS_TRANSFER",
			TableName: "students",
			RecordID:  strconv.Itoa(st.StudentID),
			OldValues: map[string]interface{}{"class_id": st.SourceClassID},
			NewValues: map[string]interface{}{"class_id": targetClassID, "student_name": fmt.Sprintf("%s %s", st.FirstName, st.LastName)},
		})
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":          fmt.Sprintf("%d ta o'quvchi yangi sinfga muvaffaqiyatli o'tkazildi", len(validStudents)),
		"transferred_count": len(validStudents),
		"target_class_id":  targetClassID,
	})
}


// DeleteTeacher soft-deletes a teacher user and unassigns from class subjects
func (h *TenantUserHandler) DeleteTeacher(c *gin.Context) {
	teacherIDStr := c.Param("id")
	teacherID, err := strconv.Atoi(teacherIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid teacher ID"})
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

	var oldUser models.User
	err = tx.QueryRow(`
		SELECT u.id, u.first_name, u.last_name, u.role_id 
		FROM users u
		JOIN roles r ON u.role_id = r.id
		WHERE u.id = $1 AND r.name IN ('MAIN_TEACHER', 'SUBJECT_TEACHER') AND u.is_deleted = false
	`, teacherID).Scan(&oldUser.ID, &oldUser.FirstName, &oldUser.LastName, &oldUser.RoleID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "O'qituvchi topilmadi"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query teacher", "details": err.Error()})
		}
		return
	}

	now := time.Now()
	_, err = tx.Exec("UPDATE users SET is_deleted = true, deleted_at = $1 WHERE id = $2", now, teacherID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to soft delete user", "details": err.Error()})
		return
	}

	_, err = tx.Exec("UPDATE class_teachers SET is_deleted = true, deleted_at = $1 WHERE teacher_id = $2", now, teacherID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unassign class teacher links", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "users",
		RecordID:  strconv.Itoa(teacherID),
		OldValues: oldUser,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit deletion", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "O'qituvchi muvaffaqiyatli o'chirildi"})
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
		SELECT ct.id, ct.class_id, COALESCE(ct.subject_id, 0) as subject_id, COALESCE(s.name, 'Tanlanmagan / Kirmaydi') as subject_name, ct.teacher_id,
		       u.first_name, u.last_name, u.middle_name, u.phone, ct.is_main_teacher, r.name as role_name
		FROM class_teachers ct
		JOIN users u ON ct.teacher_id = u.id
		JOIN roles r ON u.role_id = r.id
		LEFT JOIN subjects s ON ct.subject_id = s.id AND s.is_deleted = false
		WHERE ct.class_id = $1 AND ct.is_deleted = false AND u.is_deleted = false
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

	// Verify that the subject exists if provided
	var subjID sql.NullInt64
	if req.SubjectID != nil && *req.SubjectID > 0 {
		subjID = sql.NullInt64{Int64: int64(*req.SubjectID), Valid: true}
		var subjectExists bool
		err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM subjects WHERE id = $1 AND is_deleted = false)", *req.SubjectID).Scan(&subjectExists)
		if err != nil || !subjectExists {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Tanlangan fan topilmadi yoki o'chirilgan"})
			return
		}
	} else {
		subjID = sql.NullInt64{Valid: false}
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
	err = tx.QueryRow("SELECT id, is_deleted FROM class_teachers WHERE class_id = $1 AND (($2::integer IS NULL AND subject_id IS NULL) OR subject_id = $2::integer) AND teacher_id = $3", classID, subjID, req.TeacherID).Scan(&mappingID, &isDeleted)

	if err != nil {
		if err == sql.ErrNoRows {
			// Insert new active link
			insertQuery := `
				INSERT INTO class_teachers (class_id, subject_id, teacher_id, is_main_teacher)
				VALUES ($1, $2, $3, $4)
				RETURNING id`
			err = tx.QueryRow(insertQuery, classID, subjID, req.TeacherID, req.IsMainTeacher).Scan(&mappingID)
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
		SELECT ct.id, ct.class_id, COALESCE(ct.subject_id, 0) as subject_id, COALESCE(s.name, 'Tanlanmagan / Kirmaydi') as subject_name, ct.teacher_id,
		       u.first_name, u.last_name, u.phone, ct.is_main_teacher, r.name as role_name
		FROM class_teachers ct
		JOIN users u ON ct.teacher_id = u.id
		JOIN roles r ON u.role_id = r.id
		LEFT JOIN subjects s ON ct.subject_id = s.id AND s.is_deleted = false
		WHERE ct.id = $1`
	err = tx.QueryRow(query, mappingID).Scan(&res.ID, &res.ClassID, &res.SubjectID, &res.SubjectName, &res.TeacherID, &res.FirstName, &res.LastName, &res.Phone, &res.IsMainTeacher, &res.RoleName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve map details", "details": err.Error()})
		return
	}

	auditSubjectID := 0
	if req.SubjectID != nil {
		auditSubjectID = *req.SubjectID
	}
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "class_teachers",
		RecordID:  strconv.Itoa(mappingID),
		NewValues: models.ClassTeacher{
			ID:            mappingID,
			ClassID:       classID,
			SubjectID:     auditSubjectID,
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

type UpdateClassTeacherRequest struct {
	SubjectID     *int  `json:"subject_id"`
	TeacherID     *int  `json:"teacher_id"`
	IsMainTeacher *bool `json:"is_main_teacher"`
}

// UpdateClassTeacher updates an existing class teacher subject assignment
func (h *TenantUserHandler) UpdateClassTeacher(c *gin.Context) {
	classTeacherIDStr := c.Param("class_teacher_id")
	classTeacherID, err := strconv.Atoi(classTeacherIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid mapping ID"})
		return
	}

	var req UpdateClassTeacherRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
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

	var oldMapping models.ClassTeacher
	var oldSubjNull sql.NullInt64
	err = tx.QueryRow("SELECT id, class_id, subject_id, teacher_id, is_main_teacher, is_deleted FROM class_teachers WHERE id = $1 AND is_deleted = false", classTeacherID).
		Scan(&oldMapping.ID, &oldMapping.ClassID, &oldSubjNull, &oldMapping.TeacherID, &oldMapping.IsMainTeacher, &oldMapping.IsDeleted)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "O'qituvchi biriktiruvi topilmadi"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query assignment", "details": err.Error()})
		}
		return
	}

	var finalSubjectID sql.NullInt64
	if req.SubjectID != nil && *req.SubjectID > 0 {
		finalSubjectID = sql.NullInt64{Int64: int64(*req.SubjectID), Valid: true}
	} else {
		finalSubjectID = sql.NullInt64{Valid: false}
	}

	teacherID := oldMapping.TeacherID
	if req.TeacherID != nil && *req.TeacherID > 0 {
		teacherID = *req.TeacherID
	}

	isMainTeacher := oldMapping.IsMainTeacher
	if req.IsMainTeacher != nil {
		isMainTeacher = *req.IsMainTeacher
	}

	if isMainTeacher {
		_, _ = tx.Exec("UPDATE class_teachers SET is_main_teacher = false WHERE class_id = $1 AND id != $2", oldMapping.ClassID, classTeacherID)
	}

	_, err = tx.Exec("UPDATE class_teachers SET subject_id = $1, teacher_id = $2, is_main_teacher = $3 WHERE id = $4", finalSubjectID, teacherID, isMainTeacher, classTeacherID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update assignment", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "class_teachers",
		RecordID:  strconv.Itoa(classTeacherID),
		OldValues: oldMapping,
		NewValues: map[string]interface{}{
			"subject_id":      finalSubjectID,
			"teacher_id":      teacherID,
			"is_main_teacher": isMainTeacher,
		},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction commit failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "O'qituvchi biriktiruvi muvaffaqiyatli yangilandi"})
}

type ClassTeacherHistoryItem struct {
	ID            int        `json:"id"`
	ClassID       int        `json:"class_id"`
	SubjectID     int        `json:"subject_id"`
	SubjectName   string     `json:"subject_name"`
	TeacherID     int        `json:"teacher_id"`
	FirstName     string     `json:"first_name"`
	LastName      string     `json:"last_name"`
	MiddleName    string     `json:"middle_name"`
	Phone         string     `json:"phone"`
	IsMainTeacher bool       `json:"is_main_teacher"`
	IsDeleted     bool       `json:"is_deleted"`
	CreatedAt     time.Time  `json:"created_at"`
	DeletedAt     *time.Time `json:"deleted_at"`
}

// GetClassTeacherHistory retrieves history of active and past teacher assignments for a class
func (h *TenantUserHandler) GetClassTeacherHistory(c *gin.Context) {
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
		       u.first_name, u.last_name, COALESCE(u.middle_name, ''), COALESCE(u.phone, ''),
		       ct.is_main_teacher, ct.is_deleted, ct.created_at, ct.deleted_at
		FROM class_teachers ct
		JOIN users u ON ct.teacher_id = u.id
		JOIN subjects s ON ct.subject_id = s.id
		WHERE ct.class_id = $1
		ORDER BY ct.created_at DESC, ct.id DESC`

	rows, err := dbConn.Query(query, classID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch teacher assignment history", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []ClassTeacherHistoryItem{}
	for rows.Next() {
		var item ClassTeacherHistoryItem
		var deletedAtNull sql.NullTime
		err := rows.Scan(
			&item.ID, &item.ClassID, &item.SubjectID, &item.SubjectName, &item.TeacherID,
			&item.FirstName, &item.LastName, &item.MiddleName, &item.Phone,
			&item.IsMainTeacher, &item.IsDeleted, &item.CreatedAt, &deletedAtNull,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan history item", "details": err.Error()})
			return
		}
		if deletedAtNull.Valid {
			item.DeletedAt = &deletedAtNull.Time
		}
		list = append(list, item)
	}

	c.JSON(http.StatusOK, list)
}

func ensureSubjectColumns(db *sql.DB) {
	_, _ = db.Exec(`ALTER TABLE subjects ADD COLUMN IF NOT EXISTS target_levels INT[];`)
}

// ListSubjects fetches all active subjects
func (h *TenantUserHandler) ListSubjects(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	ensureSubjectColumns(dbConn)

	rows, err := dbConn.Query("SELECT id, name, COALESCE(target_levels, '{}') FROM subjects WHERE is_deleted = false ORDER BY name ASC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query subjects", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []models.Subject{}
	for rows.Next() {
		var s models.Subject
		var targetLevels pq.Int64Array
		if err := rows.Scan(&s.ID, &s.Name, &targetLevels); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan subject", "details": err.Error()})
			return
		}
		s.TargetLevels = targetLevels
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

	ensureSubjectColumns(dbConn)

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
			err = tx.QueryRow(`
				INSERT INTO subjects (name, target_levels) 
				VALUES ($1, $2) 
				RETURNING id`, strings.TrimSpace(req.Name), pq.Int64Array(req.TargetLevels)).Scan(&subjectID)
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
			// Reactivate & Update levels
			_, err = tx.Exec("UPDATE subjects SET is_deleted = false, deleted_at = NULL, target_levels = $2 WHERE id = $1", subjectID, pq.Int64Array(req.TargetLevels))
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
		ID:           subjectID,
		Name:         strings.TrimSpace(req.Name),
		TargetLevels: req.TargetLevels,
		IsDeleted:    false,
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

// DeleteSubject soft-deletes a subject
func (h *TenantUserHandler) DeleteSubject(c *gin.Context) {
	idStr := c.Param("id")
	subjectID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid subject ID"})
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

	var exists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM subjects WHERE id = $1 AND is_deleted = false)", subjectID).Scan(&exists)
	if err != nil || !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Fan topilmadi"})
		return
	}

	_, err = tx.Exec("UPDATE subjects SET is_deleted = true, deleted_at = NOW() WHERE id = $1", subjectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete subject", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "subjects",
		RecordID:  strconv.Itoa(subjectID),
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit delete subject", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Fan muvaffaqiyatli o'chirildi"})
}

// UpdateStudentRequest holds fields that can be updated for a student
type UpdateStudentRequest struct {
	FirstName      string  `json:"first_name" binding:"required"`
	LastName       string  `json:"last_name" binding:"required"`
	MiddleName     *string `json:"middle_name"`
	Phone          *string `json:"phone"`
	Password       *string `json:"password"`
	Address        *string `json:"address"`
	BirthDate      *string `json:"birthdate"` // Format: YYYY-MM-DD
	EnrollmentDate *string `json:"enrollment_date"` // Format: YYYY-MM-DD
	INA            *string `json:"ina"`
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

	authorized := false
	if userRole == "ADMIN" {
		authorized = true
	} else if userRole == "MAIN_TEACHER" {
		var isMain bool
		dbConn.QueryRow(`SELECT EXISTS(SELECT 1 FROM class_teachers WHERE class_id = $1 AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false)`, classID, currentUserID).Scan(&isMain)
		if isMain {
			authorized = true
		}
	} else if userRole == "PARENT" {
		var isLinked bool
		dbConn.QueryRow(`SELECT EXISTS(SELECT 1 FROM student_parents WHERE student_id = $1 AND parent_id = $2)`, studentID, currentUserID).Scan(&isLinked)
		if isLinked {
			authorized = true
		}
	}

	if !authorized {
		c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: ushbu o'quvchi ma'lumotlarini o'zgartirishga huquqingiz yo'q"})
		return
	}

	// Check INA uniqueness if updating INA
	if req.INA != nil {
		cleanINA := strings.TrimSpace(*req.INA)
		if cleanINA != "" && cleanINA != "-" && !strings.EqualFold(cleanINA, "yo'q") {
			normINA := NormalizeDocumentNo(cleanINA)
			reg, _ := regexp.Compile("[^a-z0-9]")
			rawNorm := reg.ReplaceAllString(strings.ToLower(normINA), "")

			var existingStudentName, existingClassName string
			err = dbConn.QueryRow(`
				SELECT COALESCE(u.first_name || ' ' || u.last_name, ''), COALESCE(c.name, 'Sinfatsiz')
				FROM students s
				JOIN users u ON s.user_id = u.id
				LEFT JOIN classes c ON s.class_id = c.id
				WHERE s.id != $1 AND (
					LOWER(TRIM(s.ina)) = LOWER($2)
					OR LOWER(TRIM(s.ina)) = LOWER($3)
					OR REGEXP_REPLACE(LOWER(s.ina), '[^a-z0-9]', '', 'g') = $4
					OR REGEXP_REPLACE(REGEXP_REPLACE(LOWER(s.ina), '^[l1]-', 'i-'), '[^a-z0-9]', '', 'g') = $4
				)
				  AND s.is_deleted = false AND u.is_deleted = false AND (c.id IS NULL OR c.is_deleted = false)
				LIMIT 1
			`, studentID, cleanINA, normINA, rawNorm).Scan(&existingStudentName, &existingClassName)
			if err == nil {
				c.JSON(http.StatusConflict, gin.H{
					"error": fmt.Sprintf("Ushbu Guvohnoma (INA) raqami ('%s') bilan boshqa o'quvchi ('%s', %s sinfi) allaqachon ro'yxatdan o'tgan!", cleanINA, existingStudentName, existingClassName),
				})
				return
			}
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
	var setClauses []string
	var args []interface{}
	argIdx := 1

	if req.FirstName != "" {
		setClauses = append(setClauses, fmt.Sprintf("first_name = $%d", argIdx))
		args = append(args, req.FirstName)
		argIdx++
	}
	if req.LastName != "" {
		setClauses = append(setClauses, fmt.Sprintf("last_name = $%d", argIdx))
		args = append(args, req.LastName)
		argIdx++
	}
	if req.MiddleName != nil {
		setClauses = append(setClauses, fmt.Sprintf("middle_name = $%d", argIdx))
		args = append(args, req.MiddleName)
		argIdx++
	}
	if req.Phone != nil {
		setClauses = append(setClauses, fmt.Sprintf("phone = $%d", argIdx))
		args = append(args, req.Phone)
		argIdx++
	}

	if req.Password != nil && *req.Password != "" {
		hashed, hashErr := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Parolni shifrlashda xatolik"})
			return
		}
		setClauses = append(setClauses, fmt.Sprintf("password_hash = $%d", argIdx))
		args = append(args, string(hashed))
		argIdx++
	}

	if len(setClauses) > 0 {
		setClauses = append(setClauses, fmt.Sprintf("updated_at = NOW()"))
		args = append(args, targetUserID)
		updateQuery := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d", strings.Join(setClauses, ", "), argIdx)
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
	}

	var birthdate *time.Time
	if req.BirthDate != nil && *req.BirthDate != "" {
		parsedDate, err := time.Parse("2006-01-02", *req.BirthDate)
		if err == nil {
			birthdate = &parsedDate
		}
	}

	var enrollmentDate *time.Time
	if req.EnrollmentDate != nil && *req.EnrollmentDate != "" {
		parsedDate, err := time.Parse("2006-01-02", *req.EnrollmentDate)
		if err == nil {
			enrollmentDate = &parsedDate
		}
	}

	if enrollmentDate != nil {
		_, err = tx.Exec(`
			UPDATE students 
			SET address = $1, birthdate = $2, ina = $3, enrollment_date = $4 
			WHERE id = $5`, 
			req.Address, birthdate, req.INA, enrollmentDate, studentID)
	} else {
		_, err = tx.Exec(`
			UPDATE students 
			SET address = $1, birthdate = $2, ina = $3 
			WHERE id = $4`, 
			req.Address, birthdate, req.INA, studentID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "O'quvchi qo'shimcha ma'lumotlarini yangilashda xatolik", "details": err.Error()})
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

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "students",
		RecordID:  strconv.Itoa(studentID),
		OldValues: map[string]interface{}{"student_id": studentID},
		NewValues: map[string]interface{}{
			"address":         req.Address,
			"birthdate":       birthdate,
			"enrollment_date": enrollmentDate,
			"ina":             req.INA,
		},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit update", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":              targetUserID,
		"first_name":      req.FirstName,
		"last_name":       req.LastName,
		"middle_name":     req.MiddleName,
		"phone":           req.Phone,
		"role_id":         oldUser.RoleID,
		"address":         req.Address,
		"birthdate":       birthdate,
		"enrollment_date": enrollmentDate,
		"ina":             req.INA,
	})
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

type CheckStudentDocumentsRequest struct {
	Documents []string `json:"documents"`
}

type ExistingStudentDocInfo struct {
	StudentID  int    `json:"student_id"`
	UserID     int    `json:"user_id"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	MiddleName string `json:"middle_name"`
	ClassID    int    `json:"class_id"`
	ClassName  string `json:"class_name"`
	INA        string `json:"ina"`
}

// CheckStudentDocuments checks if any document numbers (INA) already exist in tenant database
func (h *TenantUserHandler) CheckStudentDocuments(c *gin.Context) {
	var req CheckStudentDocumentsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	cleanDocs := []string{}
	reg, _ := regexp.Compile("[^a-z0-9]")
	for _, d := range req.Documents {
		trimmed := strings.TrimSpace(d)
		if trimmed != "" && trimmed != "-" && !strings.EqualFold(trimmed, "yo'q") {
			normDoc := NormalizeDocumentNo(trimmed)
			norm := reg.ReplaceAllString(strings.ToLower(normDoc), "")
			if norm != "" {
				cleanDocs = append(cleanDocs, norm)
				cleanDocs = append(cleanDocs, strings.ToLower(normDoc))
				cleanDocs = append(cleanDocs, strings.ToLower(trimmed))
				if strings.HasPrefix(norm, "i") {
					cleanDocs = append(cleanDocs, "l"+norm[1:])
				}
				if strings.HasPrefix(norm, "l") {
					cleanDocs = append(cleanDocs, "i"+norm[1:])
				}
			}
		}
	}

	if len(cleanDocs) == 0 {
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	// Clean up legacy dirty 'l-' prefix in database asynchronously
	go func(db *sql.DB) {
		_, _ = db.Exec(`
			UPDATE students 
			SET ina = REGEXP_REPLACE(ina, '^[lL1]-', 'I-') 
			WHERE ina ~* '^[lL1]-[a-zA-Z]{2}';
			UPDATE users 
			SET passport = REGEXP_REPLACE(passport, '^[lL1]-', 'I-') 
			WHERE passport ~* '^[lL1]-[a-zA-Z]{2}';
		`)
	}(dbConn)

	rows, err := dbConn.Query(`
		SELECT s.id, s.user_id, u.first_name, u.last_name, COALESCE(u.middle_name, ''), COALESCE(s.class_id, 0), COALESCE(c.name, 'Sinfatsiz'), COALESCE(s.ina, '')
		FROM students s
		JOIN users u ON s.user_id = u.id
		LEFT JOIN classes c ON s.class_id = c.id
		WHERE (
			LOWER(TRIM(s.ina)) = ANY($1) 
			OR REGEXP_REPLACE(LOWER(s.ina), '[^a-z0-9]', '', 'g') = ANY($1)
			OR REGEXP_REPLACE(REGEXP_REPLACE(LOWER(s.ina), '^[l1]-', 'i-'), '[^a-z0-9]', '', 'g') = ANY($1)
		) 
		  AND s.is_deleted = false AND u.is_deleted = false AND (c.id IS NULL OR c.is_deleted = false)
	`, pq.Array(cleanDocs))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check student documents", "details": err.Error()})
		return
	}
	defer rows.Close()

	result := make(map[string]ExistingStudentDocInfo)
	for rows.Next() {
		var info ExistingStudentDocInfo
		if err := rows.Scan(&info.StudentID, &info.UserID, &info.FirstName, &info.LastName, &info.MiddleName, &info.ClassID, &info.ClassName, &info.INA); err == nil {
			info.INA = NormalizeDocumentNo(info.INA)
			normINA := reg.ReplaceAllString(strings.ToLower(info.INA), "")
			result[normINA] = info
			result[strings.ToLower(strings.TrimSpace(info.INA))] = info
			if strings.HasPrefix(normINA, "i") {
				result["l"+normINA[1:]] = info
			}
			if strings.HasPrefix(normINA, "l") {
				result["i"+normINA[1:]] = info
			}
		}
	}

	c.JSON(http.StatusOK, result)
}

type TransferByDocumentRequest struct {
	INA             string `json:"ina"`
	TargetClassName string `json:"target_class_name"`
}

// TransferStudentByDocument transfers an existing student identified by INA to a target class
func (h *TenantUserHandler) TransferStudentByDocument(c *gin.Context) {
	var req TransferByDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request parameters", "details": err.Error()})
		return
	}

	cleanINA := strings.TrimSpace(req.INA)
	targetClassName := strings.TrimSpace(req.TargetClassName)

	if cleanINA == "" || targetClassName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ina va target_class_name bo'sh bo'lmasligi kerak"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure"})
		return
	}
	defer tx.Rollback()

	// 1. Resolve Target Class ID
	var targetClassID int
	classQuery := `
		SELECT id FROM classes 
		WHERE (
			LOWER(name) = LOWER($1) 
			OR REGEXP_REPLACE(UPPER(TRIM(name)), '\s*-\s*', '-', 'g') = REGEXP_REPLACE(UPPER(TRIM($1)), '\s*-\s*', '-', 'g')
		) AND is_deleted = false`
	err = tx.QueryRow(classQuery, targetClassName).Scan(&targetClassID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Sinf '%s' topilmadi. Avval sinf yaratilishi kerak.", targetClassName)})
		return
	}

	// 2. Find existing student by normalized INA
	normINA := NormalizeDocumentNo(cleanINA)
	var studentID, userID, oldClassID int
	var firstName, lastName, oldClassName string
	err = tx.QueryRow(`
		SELECT s.id, s.user_id, COALESCE(s.class_id, 0), u.first_name, u.last_name, COALESCE(c.name, 'Sinfatsiz')
		FROM students s
		JOIN users u ON s.user_id = u.id
		LEFT JOIN classes c ON s.class_id = c.id
		WHERE (
			LOWER(TRIM(s.ina)) = LOWER($1) 
			OR LOWER(TRIM(s.ina)) = LOWER($2)
			OR REGEXP_REPLACE(LOWER(s.ina), '[^a-z0-9]', '', 'g') = REGEXP_REPLACE(LOWER($1), '[^a-z0-9]', '', 'g')
			OR REGEXP_REPLACE(LOWER(s.ina), '[^a-z0-9]', '', 'g') = REGEXP_REPLACE(LOWER($2), '[^a-z0-9]', '', 'g')
			OR REGEXP_REPLACE(REGEXP_REPLACE(LOWER(s.ina), '^[l1]-', 'i-'), '[^a-z0-9]', '', 'g') = REGEXP_REPLACE(REGEXP_REPLACE(LOWER($2), '^[l1]-', 'i-'), '[^a-z0-9]', '', 'g')
		)
		  AND s.is_deleted = false AND u.is_deleted = false AND (c.id IS NULL OR c.is_deleted = false)
		LIMIT 1
	`, cleanINA, normINA).Scan(&studentID, &userID, &oldClassID, &firstName, &lastName, &oldClassName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Hujjat raqami '%s' bo'lgan o'quvchi topilmadi", cleanINA)})
		return
	}

	// 3. Update student class_id and normalize INA
	_, err = tx.Exec("UPDATE students SET class_id = $1, ina = $2 WHERE id = $3", targetClassID, normINA, studentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Sinfni yangilashda xatolik", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "students",
		RecordID:  strconv.Itoa(studentID),
		OldValues: map[string]interface{}{"student_id": studentID, "class_id": oldClassID, "class_name": oldClassName},
		NewValues: map[string]interface{}{"student_id": studentID, "class_id": targetClassID, "class_name": targetClassName},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transfer"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("%s %s muvaffaqiyatli %s sinfiga o'tkazildi!", firstName, lastName, targetClassName),
		"student_id": studentID,
		"user_id": userID,
		"new_class_name": targetClassName,
	})
}
