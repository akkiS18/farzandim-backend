package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
)

type ImportHandler struct{}

func NewImportHandler() *ImportHandler {
	return &ImportHandler{}
}

type RowError struct {
	Row   int    `json:"row"`
	Error string `json:"error"`
}

type ImportResult struct {
	Success       bool       `json:"success"`
	ImportedCount int        `json:"imported_count"`
	FailedCount   int        `json:"failed_count"`
	Errors        []RowError `json:"errors"`
}

type TenantUserResponse struct {
	ID             int        `json:"id"`
	Email          *string    `json:"email"`
	Phone          *string    `json:"phone"`
	FirstName      string     `json:"first_name"`
	LastName       string     `json:"last_name"`
	MiddleName     *string    `json:"middle_name"`
	Passport       *string    `json:"passport"`
	RoleID         int        `json:"role_id"`
	RoleName       string     `json:"role_name"`
	ClassID        *int       `json:"class_id,omitempty"`
	ClassName      *string    `json:"class_name,omitempty"`
	StudentID      *int       `json:"student_id,omitempty"`
	StudentName    *string    `json:"student_name,omitempty"`
	Address        *string    `json:"address,omitempty"`
	BirthDate      *time.Time `json:"birthdate,omitempty"`
	EnrollmentDate *time.Time `json:"enrollment_date,omitempty"`
	INA            *string    `json:"ina,omitempty"`
	Balance        *float64   `json:"balance,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// ListUsers lists all active users in the school tenant database with filters
func (h *ImportHandler) ListUsers(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	roleFilter := c.Query("role")
	classFilter := c.Query("class_id")
	searchFilter := c.Query("search")

	roleVal, exists := c.Get("role")
	currentRole := ""
	if exists {
		currentRole = roleVal.(string)
	}

	userIDVal, exists := c.Get("userID")
	userIDStr := ""
	if exists {
		userIDStr = userIDVal.(string)
	}

	dateFilter := c.Query("date")
	sDeletedCond := "s.is_deleted = false"
	uDeletedCond := "u.is_deleted = false"
	suDeletedCond := "su.is_deleted = false"

	if dateFilter != "" {
		if _, err := time.Parse("2006-01-02", dateFilter); err == nil {
			sDeletedCond = fmt.Sprintf("(s.is_deleted = false OR s.deleted_at::date >= '%s'::date) AND (s.enrollment_date IS NULL OR s.enrollment_date <= '%s'::date)", dateFilter, dateFilter)
			uDeletedCond = fmt.Sprintf("(u.is_deleted = false OR u.deleted_at::date >= '%s'::date)", dateFilter)
			suDeletedCond = fmt.Sprintf("(su.is_deleted = false OR su.deleted_at::date >= '%s'::date) AND (s.enrollment_date IS NULL OR s.enrollment_date <= '%s'::date)", dateFilter, dateFilter)
		}
	}

	var query string
	var args []interface{}
	argCount := 1

	if currentRole == "PARENT" {
		parentID, err := strconv.Atoi(userIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parent user ID"})
			return
		}
		query = fmt.Sprintf(`
			SELECT u.id, u.email, u.phone, u.first_name, u.last_name, u.middle_name, u.role_id, r.name as role_name, u.created_at,
			       s.class_id, cl.name as class_name,
			       s.id as student_id, NULL::text as student_name,
			       u.passport, s.address, s.birthdate, s.ina, s.balance, s.enrollment_date
			FROM users u
			JOIN roles r ON u.role_id = r.id
			JOIN students s ON u.id = s.user_id AND %s
			JOIN classes cl ON s.class_id = cl.id AND cl.is_deleted = false
			JOIN student_parents sp ON sp.student_id = s.id
			WHERE %s AND sp.parent_id = $1`, sDeletedCond, uDeletedCond)
		args = append(args, parentID)
		argCount++
	} else if currentRole == "STUDENT" {
		studentUserID, err := strconv.Atoi(userIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student user ID"})
			return
		}
		query = fmt.Sprintf(`
			SELECT u.id, u.email, u.phone, u.first_name, u.last_name, u.middle_name, u.role_id, r.name as role_name, u.created_at,
			       s.class_id, cl.name as class_name,
			       s.id as student_id, NULL::text as student_name,
			       u.passport, s.address, s.birthdate, s.ina, s.balance, s.enrollment_date
			FROM users u
			JOIN roles r ON u.role_id = r.id
			JOIN students s ON u.id = s.user_id AND %s
			JOIN classes cl ON s.class_id = cl.id AND cl.is_deleted = false
			WHERE %s AND u.id = $1`, sDeletedCond, uDeletedCond)
		args = append(args, studentUserID)
		argCount++
	} else if roleFilter == "PARENT" {
		query = fmt.Sprintf(`
			SELECT u.id, u.email, u.phone, u.first_name, u.last_name, u.middle_name, u.role_id, r.name as role_name, u.created_at,
			       s.class_id, cl.name as class_name,
			       s.id as student_id, (su.first_name || ' ' || su.last_name || COALESCE(' ' || su.middle_name, '')) as student_name,
			       u.passport, NULL::text as address, NULL::date as birthdate, NULL::text as ina, NULL::numeric as balance, NULL::date as enrollment_date
			FROM users u
			JOIN roles r ON u.role_id = r.id
			LEFT JOIN student_parents sp ON sp.parent_id = u.id
			LEFT JOIN students s ON sp.student_id = s.id AND %s
			LEFT JOIN users su ON s.user_id = su.id AND %s
			LEFT JOIN classes cl ON s.class_id = cl.id AND cl.is_deleted = false
			WHERE %s AND r.name = 'PARENT'`, sDeletedCond, suDeletedCond, uDeletedCond)

		if classFilter != "" {
			classID, err := strconv.Atoi(classFilter)
			if err == nil {
				query += fmt.Sprintf(" AND s.class_id = $%d", argCount)
				args = append(args, classID)
				argCount++
			}
		}
	} else {
		query = fmt.Sprintf(`
			SELECT u.id, u.email, u.phone, u.first_name, u.last_name, u.middle_name, u.role_id, r.name as role_name, u.created_at,
			       s.class_id, cl.name as class_name,
			       s.id as student_id, NULL::text as student_name,
			       u.passport, s.address, s.birthdate, s.ina, s.balance, s.enrollment_date
			FROM users u
			JOIN roles r ON u.role_id = r.id
			LEFT JOIN students s ON u.id = s.user_id AND %s
			LEFT JOIN classes cl ON s.class_id = cl.id AND cl.is_deleted = false
			WHERE %s`, sDeletedCond, uDeletedCond)
	}

	if roleFilter != "" && roleFilter != "PARENT" {
		query += fmt.Sprintf(" AND r.name = $%d", argCount)
		args = append(args, roleFilter)
		argCount++
	}

	if classFilter != "" && roleFilter != "PARENT" {
		classID, err := strconv.Atoi(classFilter)
		if err == nil {
			query += fmt.Sprintf(" AND s.class_id = $%d", argCount)
			args = append(args, classID)
			argCount++
		}
	}

	if searchFilter != "" {
		query += fmt.Sprintf(" AND (u.first_name ILIKE $%d OR u.last_name ILIKE $%d OR u.phone ILIKE $%d)", argCount, argCount, argCount)
		args = append(args, "%"+searchFilter+"%")
		argCount++
	}

	query += " ORDER BY u.created_at DESC"

	rows, err := dbConn.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query school users", "details": err.Error()})
		return
	}
	defer rows.Close()

	usersList := []TenantUserResponse{}
	for rows.Next() {
		var u TenantUserResponse
		var emailNull, middleNameNull, phoneNull sql.NullString
		var classIDNull sql.NullInt64
		var classNameNull sql.NullString
		var studentIDNull sql.NullInt64
		var studentNameNull sql.NullString
		var passportNull sql.NullString
		var addressNull sql.NullString
		var birthdateNull sql.NullTime
		var enrollmentDateNull sql.NullTime
		var inaNull sql.NullString
		var balanceNull sql.NullFloat64

		err := rows.Scan(
			&u.ID, &emailNull, &phoneNull, &u.FirstName, &u.LastName, &middleNameNull, &u.RoleID, &u.RoleName, &u.CreatedAt,
			&classIDNull, &classNameNull, &studentIDNull, &studentNameNull,
			&passportNull, &addressNull, &birthdateNull, &inaNull, &balanceNull, &enrollmentDateNull,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse user records", "details": err.Error()})
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
		if passportNull.Valid {
			u.Passport = &passportNull.String
		}
		if classIDNull.Valid {
			cid := int(classIDNull.Int64)
			u.ClassID = &cid
		}
		if classNameNull.Valid {
			u.ClassName = &classNameNull.String
		}
		if studentIDNull.Valid {
			sid := int(studentIDNull.Int64)
			u.StudentID = &sid
		}
		if studentNameNull.Valid {
			u.StudentName = &studentNameNull.String
		}
		if addressNull.Valid {
			u.Address = &addressNull.String
		}
		if birthdateNull.Valid {
			u.BirthDate = &birthdateNull.Time
		}
		if enrollmentDateNull.Valid {
			u.EnrollmentDate = &enrollmentDateNull.Time
		}
		if inaNull.Valid {
			u.INA = &inaNull.String
		}
		if balanceNull.Valid {
			u.Balance = &balanceNull.Float64
		}

		usersList = append(usersList, u)
	}

	c.JSON(http.StatusOK, usersList)
}

// ImportStudents imports student user profiles and assigns classes from Excel
func (h *ImportHandler) ImportStudents(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	tenantDB := tenantDBVal.(*sql.DB)

	classIDQuery := c.Query("class_id")
	var targetClassID int
	var hasTargetClass bool
	if classIDQuery != "" {
		cid, err := strconv.Atoi(classIDQuery)
		if err == nil {
			// Verify class exists
			var dummy int
			dbErr := tenantDB.QueryRow("SELECT id FROM classes WHERE id = $1 AND is_deleted = false", cid).Scan(&dummy)
			if dbErr == nil {
				targetClassID = cid
				hasTargetClass = true
			} else {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Specified class_id does not exist or has been deleted"})
				return
			}
		}
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}

	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer src.Close()

	f, err := excelize.OpenReader(src)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read Excel file format"})
		return
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel file contains no sheets"})
		return
	}
	sheetName := sheets[0]

	rows, err := f.GetRows(sheetName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve sheet data"})
		return
	}

	if len(rows) <= 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel sheet is empty (only header or no rows found)"})
		return
	}

	// 1. Resolve role ID for STUDENT
	var studentRoleID int
	err = tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'STUDENT'").Scan(&studentRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Role 'STUDENT' is not initialized in tenant DB"})
		return
	}

	// 2. Parse headers
	headers := rows[0]
	colIndices := map[string]int{
		"ism":              -1,
		"familiya":         -1,
		"sharif":           -1,
		"sinf":             -1,
		"manzil":           -1,
		"tug'ilgan sana":   -1,
		"guvohnoma raqami": -1,
		"balans":           -1,
	}

	for i, hCell := range headers {
		cleanHeader := strings.ToLower(strings.TrimSpace(hCell))
		if cleanHeader == "ism" {
			colIndices["ism"] = i
		} else if cleanHeader == "familiya" {
			colIndices["familiya"] = i
		} else if cleanHeader == "sharif" {
			colIndices["sharif"] = i
		} else if cleanHeader == "sinf" {
			colIndices["sinf"] = i
		} else if cleanHeader == "manzil" || cleanHeader == "address" || cleanHeader == "uy manzili" {
			colIndices["manzil"] = i
		} else if cleanHeader == "tug'ilgan sana" || cleanHeader == "tugilgan sana" || cleanHeader == "birthdate" {
			colIndices["tug'ilgan sana"] = i
		} else if cleanHeader == "guvohnoma raqami" || cleanHeader == "guvohnoma" || cleanHeader == "ina" || cleanHeader == "birth certificate" {
			colIndices["guvohnoma raqami"] = i
		} else if cleanHeader == "balans" || cleanHeader == "balance" {
			colIndices["balans"] = i
		}
	}

	// Check required headers
	requiredHeaders := []string{"ism", "familiya"}
	if !hasTargetClass {
		requiredHeaders = append(requiredHeaders, "sinf")
	}
	for _, reqH := range requiredHeaders {
		if colIndices[reqH] == -1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Required header '%s' is missing in spreadsheet", reqH)})
			return
		}
	}

	var rowErrors []RowError
	successCount := 0
	failedCount := 0

	// Helper function to safely read cell
	getCell := func(row []string, key string) string {
		idx := colIndices[key]
		if idx >= 0 && idx < len(row) {
			return strings.TrimSpace(row[idx])
		}
		return ""
	}

	// Process each student row in its own database transaction for isolation
	for rIdx := 1; rIdx < len(rows); rIdx++ {
		row := rows[rIdx]
		if len(row) == 0 {
			continue // skip empty lines
		}

		ism := getCell(row, "ism")
		familiya := getCell(row, "familiya")
		sharif := getCell(row, "sharif")
		sinfName := ""
		if !hasTargetClass {
			sinfName = getCell(row, "sinf")
		}

		rowNum := rIdx + 1

		// Validation
		if ism == "" || familiya == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Ism va Familiya maydonlari to'ldirilishi shart"})
			failedCount++
			continue
		}
		if !hasTargetClass && sinfName == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Sinf maydoni to'ldirilishi shart"})
			failedCount++
			continue
		}

		// Generate random password
		parol := "STUDENT_NO_LOGIN_ACCESS_RANDOM_PASS_" + time.Now().Format("20060102150405.000")

		// Begin transaction for current row
		tx, err := tenantDB.Begin()
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Baza tranzaksiyasini boshlab bo'lmadi: %v", err)})
			failedCount++
			continue
		}

		// 1. Resolve or create Class
		var classID int
		if hasTargetClass {
			classID = targetClassID
		} else {
			err = tx.QueryRow("SELECT id FROM classes WHERE name = $1 AND is_deleted = false", sinfName).Scan(&classID)
			if err != nil {
				if err == sql.ErrNoRows {
					// Create class dynamically
					err = tx.QueryRow("INSERT INTO classes (name) VALUES ($1) RETURNING id", sinfName).Scan(&classID)
					if err != nil {
						tx.Rollback()
						rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Sinfni yaratib bo'lmadi: %v", err)})
						failedCount++
						continue
					}
					// Log audit event for class creation
					audit.LogChange(c, tx, audit.LogData{
						Action:    "CREATE",
						TableName: "classes",
						RecordID:  strconv.Itoa(classID),
						NewValues: models.Class{ID: classID, Name: sinfName, IsDeleted: false},
					})
				} else {
					tx.Rollback()
					rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Sinfni bazadan qidirishda xatolik: %v", err)})
					failedCount++
					continue
				}
			}
		}

		// 2. Hash Password
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(parol), bcrypt.DefaultCost)
		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Parolni shifrlashda xatolik"})
			failedCount++
			continue
		}

		// 3. Create User record
		var userID int
		var middleNamePtr *string
		if sharif != "" {
			middleNamePtr = &sharif
		}

		insertUserQuery := `
			INSERT INTO users (first_name, last_name, middle_name, phone, password_hash, role_id)
			VALUES ($1, $2, $3, NULL, $4, $5)
			RETURNING id`
		err = tx.QueryRow(insertUserQuery, ism, familiya, middleNamePtr, string(hashedPassword), studentRoleID).Scan(&userID)
		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Foydalanuvchini yaratib bo'lmadi: %v", err)})
			failedCount++
			continue
		}

		// 4. Create Student record
		manzil := getCell(row, "manzil")
		tugilganSanaStr := getCell(row, "tug'ilgan sana")
		ina := getCell(row, "guvohnoma raqami")
		var addressPtr *string
		if manzil != "" {
			addressPtr = &manzil
		}
		var birthdate *time.Time
		if tugilganSanaStr != "" {
			parsedDate, err := time.Parse("2006-01-02", tugilganSanaStr)
			if err != nil {
				parsedDate, err = time.Parse("02.01.2006", tugilganSanaStr)
			}
			if err == nil {
				birthdate = &parsedDate
			}
		}
		var inaPtr *string
		if ina != "" {
			inaPtr = &ina
		}

		var studentID int
		insertStudentQuery := `
			INSERT INTO students (user_id, class_id, address, birthdate, ina, enrollment_date)
			VALUES ($1, $2, $3, $4, $5, NOW()::date)
			RETURNING id`
		err = tx.QueryRow(insertStudentQuery, userID, classID, addressPtr, birthdate, inaPtr).Scan(&studentID)
		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("O'quvchi profilini yaratib bo'lmadi: %v", err)})
			failedCount++
			continue
		}

		// 5. Audit Log
		newUser := models.User{
			ID:         userID,
			FirstName:  ism,
			LastName:   familiya,
			MiddleName: middleNamePtr,
			Phone:      nil,
			RoleID:     studentRoleID,
			IsDeleted:  false,
		}

		audit.LogChange(c, tx, audit.LogData{
			Action:    "CREATE",
			TableName: "users",
			RecordID:  strconv.Itoa(userID),
			NewValues: newUser,
		})

		// Commit row changes
		if err := tx.Commit(); err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Ma'lumotlarni saqlashda xatolik (Commit): %v", err)})
			failedCount++
		} else {
			successCount++
		}
	}

	c.JSON(http.StatusOK, ImportResult{
		Success:       len(rowErrors) == 0,
		ImportedCount: successCount,
		FailedCount:   failedCount,
		Errors:        rowErrors,
	})
}

// ImportTeachers imports teacher user profiles (MAIN_TEACHER / SUBJECT_TEACHER) from Excel
func (h *ImportHandler) ImportTeachers(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	tenantDB := tenantDBVal.(*sql.DB)

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}

	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer src.Close()

	f, err := excelize.OpenReader(src)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read Excel file format"})
		return
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel file contains no sheets"})
		return
	}
	sheetName := sheets[0]

	rows, err := f.GetRows(sheetName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve sheet data"})
		return
	}

	if len(rows) <= 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel sheet is empty (only header or no rows found)"})
		return
	}

	// 1. Fetch role mappings from tenant DB
	roleMap := make(map[string]int)
	roleRows, err := tenantDB.Query("SELECT id, name FROM roles WHERE name IN ('MAIN_TEACHER', 'SUBJECT_TEACHER')")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve teacher roles configurations"})
		return
	}
	defer roleRows.Close()

	for roleRows.Next() {
		var id int
		var name string
		if err := roleRows.Scan(&id, &name); err == nil {
			roleMap[name] = id
		}
	}

	// 2. Parse headers
	headers := rows[0]
	colIndices := map[string]int{
		"ism":      -1,
		"familiya": -1,
		"sharif":   -1,
		"telefon":  -1,
		"rol":      -1,
		"parol":    -1,
	}

	for i, hCell := range headers {
		cleanHeader := strings.ToLower(strings.TrimSpace(hCell))
		if _, exists := colIndices[cleanHeader]; exists {
			colIndices[cleanHeader] = i
		}
	}

	// Check required headers
	requiredHeaders := []string{"ism", "familiya", "telefon", "rol"}
	for _, reqH := range requiredHeaders {
		if colIndices[reqH] == -1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Required header '%s' is missing in spreadsheet", reqH)})
			return
		}
	}

	var rowErrors []RowError
	successCount := 0
	failedCount := 0

	getCell := func(row []string, key string) string {
		idx := colIndices[key]
		if idx >= 0 && idx < len(row) {
			return strings.TrimSpace(row[idx])
		}
		return ""
	}

	// Process each teacher row in its own database transaction for isolation
	for rIdx := 1; rIdx < len(rows); rIdx++ {
		row := rows[rIdx]
		if len(row) == 0 {
			continue // skip empty lines
		}

		ism := getCell(row, "ism")
		familiya := getCell(row, "familiya")
		sharif := getCell(row, "sharif")
		telefon := getCell(row, "telefon")
		rolName := strings.ToUpper(getCell(row, "rol"))
		parol := getCell(row, "parol")

		rowNum := rIdx + 1

		// Validation
		if ism == "" || familiya == "" || telefon == "" || rolName == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Ism, Familiya, Telefon va Rol maydonlari to'ldirilishi shart"})
			failedCount++
			continue
		}

		// Standardize role name
		if rolName == "TEACHER" || rolName == "O'QITUVCHI" || rolName == "OQITUVCHI" {
			rolName = "SUBJECT_TEACHER"
		}
		if rolName == "MAIN" || rolName == "ASOSIY O'QITUVCHI" || rolName == "SINF RAHBARI" {
			rolName = "MAIN_TEACHER"
		}

		roleID, ok := roleMap[rolName]
		if !ok {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Noma'lum rol '%s'. Faqat 'MAIN_TEACHER' yoki 'SUBJECT_TEACHER' kiritilishi mumkin", rolName)})
			failedCount++
			continue
		}

		if parol == "" {
			parol = "password123" // default password
		}

		// Begin transaction for current row
		tx, err := tenantDB.Begin()
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Baza tranzaksiyasini boshlab bo'lmadi: %v", err)})
			failedCount++
			continue
		}

		// Hash Password
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(parol), bcrypt.DefaultCost)
		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Parolni shifrlashda xatolik"})
			failedCount++
			continue
		}

		// Create User record
		var userID int
		var middleNamePtr *string
		if sharif != "" {
			middleNamePtr = &sharif
		}

		insertUserQuery := `
			INSERT INTO users (first_name, last_name, middle_name, phone, password_hash, role_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id`
		err = tx.QueryRow(insertUserQuery, ism, familiya, middleNamePtr, telefon, string(hashedPassword), roleID).Scan(&userID)
		if err != nil {
			tx.Rollback()
			if strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "users_phone_key") {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Telefon raqam '%s' allaqachon ro'yxatdan o'tgan", telefon)})
			} else {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Foydalanuvchini yaratib bo'lmadi: %v", err)})
			}
			failedCount++
			continue
		}

		// Audit Log
		newUser := models.User{
			ID:         userID,
			FirstName:  ism,
			LastName:   familiya,
			MiddleName: middleNamePtr,
			Phone:      &telefon,
			RoleID:     roleID,
			IsDeleted:  false,
		}

		audit.LogChange(c, tx, audit.LogData{
			Action:    "CREATE",
			TableName: "users",
			RecordID:  strconv.Itoa(userID),
			NewValues: newUser,
		})

		// Commit row changes
		if err := tx.Commit(); err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Ma'lumotlarni saqlashda xatolik (Commit): %v", err)})
			failedCount++
		} else {
			successCount++
		}
	}

	c.JSON(http.StatusOK, ImportResult{
		Success:       len(rowErrors) == 0,
		ImportedCount: successCount,
		FailedCount:   failedCount,
		Errors:        rowErrors,
	})
}

// ExportStudentTemplate generates a sample student import Excel template
func (h *ImportHandler) ExportStudentTemplate(c *gin.Context) {
	f := excelize.NewFile()
	defer f.Close()

	classIDStr := c.Query("class_id")
	var headers []string
	var sampleRow []string

	if classIDStr != "" {
		headers = []string{"ism", "familiya", "sharif", "manzil", "tug'ilgan sana", "guvohnoma raqami", "balans"}
		sampleRow = []string{"Ali", "Valiyev", "Karimovich", "Toshkent sh., Chilonzor d.", "2015-05-12", "I-TN № 123456", "0.00"}
	} else {
		headers = []string{"ism", "familiya", "sharif", "sinf", "manzil", "tug'ilgan sana", "guvohnoma raqami", "balans"}
		sampleRow = []string{"Ali", "Valiyev", "Karimovich", "9-A", "Toshkent sh., Chilonzor d.", "2015-05-12", "I-TN № 123456", "0.00"}
	}

	// Write headers
	for i, name := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Sheet1", cell, name)
	}

	// Add sample data row
	for i, val := range sampleRow {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		f.SetCellValue("Sheet1", cell, val)
	}

	c.Header("Content-Disposition", "attachment; filename=oquvchi_template.xlsx")
	c.Header("Content-Type", "application/octet-stream")
	
	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate template file"})
	}
}

// ExportTeacherTemplate generates a sample teacher import Excel template
func (h *ImportHandler) ExportTeacherTemplate(c *gin.Context) {
	f := excelize.NewFile()
	defer f.Close()

	// Write headers
	headers := []string{"ism", "familiya", "sharif", "telefon", "rol", "parol"}
	for i, name := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Sheet1", cell, name)
	}

	// Add sample data row
	sampleRow := []string{"Olim", "Sodiqov", "Valiyevich", "+998907654321", "MAIN_TEACHER", "parol123"}
	for i, val := range sampleRow {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		f.SetCellValue("Sheet1", cell, val)
	}

	c.Header("Content-Disposition", "attachment; filename=oqituvchilar_shablon.xlsx")
	c.Header("Content-Type", "application/octet-stream")
	
	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate template file"})
	}
}

// ExportParentTemplate generates a sample parent import Excel template populated with active students
func (h *ImportHandler) ExportParentTemplate(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	classIDFilter := c.Query("class_id")

	f := excelize.NewFile()
	defer f.Close()

	// Write headers
	headers := []string{"o'quvchi uid", "ism", "familiya", "sharif", "parent ism", "parent familiya", "parent sharif", "parent turi", "nomer", "parol", "passport"}
	for i, name := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Sheet1", cell, name)
	}

	// Fetch active students
	var query string
	var rows *sql.Rows
	var err error

	if classIDFilter != "" {
		classID, err := strconv.Atoi(classIDFilter)
		if err == nil {
			query = `
				SELECT s.id, u.first_name, u.last_name, COALESCE(u.middle_name, '')
				FROM students s
				JOIN users u ON s.user_id = u.id
				WHERE s.is_deleted = false AND u.is_deleted = false AND s.class_id = $1
				ORDER BY u.first_name, u.last_name`
			rows, err = dbConn.Query(query, classID)
		}
	}

	if rows == nil {
		query = `
			SELECT s.id, u.first_name, u.last_name, COALESCE(u.middle_name, '')
			FROM students s
			JOIN users u ON s.user_id = u.id
			WHERE s.is_deleted = false AND u.is_deleted = false
			ORDER BY u.first_name, u.last_name`
		rows, err = dbConn.Query(query)
	}

	hasStudents := false
	if err == nil && rows != nil {
		defer rows.Close()
		rowNum := 2
		for rows.Next() {
			var studentID int
			var firstName, lastName, middleName string
			if err := rows.Scan(&studentID, &firstName, &lastName, &middleName); err == nil {
				hasStudents = true
				f.SetCellValue("Sheet1", fmt.Sprintf("A%d", rowNum), studentID)
				f.SetCellValue("Sheet1", fmt.Sprintf("B%d", rowNum), firstName)
				f.SetCellValue("Sheet1", fmt.Sprintf("C%d", rowNum), lastName)
				f.SetCellValue("Sheet1", fmt.Sprintf("D%d", rowNum), middleName)
				rowNum++
			}
		}
	}

	if !hasStudents {
		// Add sample data row
		sampleRow := []string{"1", "Olim", "Sodiqov", "Valiyevich", "Karim", "Sodiqov", "Eshmatovich", "ota", "+998909998877", "parol123", "AA1234567"}
		for i, val := range sampleRow {
			cell, _ := excelize.CoordinatesToCellName(i+1, 2)
			f.SetCellValue("Sheet1", cell, val)
		}
	}

	c.Header("Content-Disposition", "attachment; filename=ota_ona_template.xlsx")
	c.Header("Content-Type", "application/octet-stream")
	
	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate template file"})
	}
}

// ImportParents imports parent user profiles and links them to students from Excel
func (h *ImportHandler) ImportParents(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	tenantDB := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}

	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer src.Close()

	f, err := excelize.OpenReader(src)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read Excel file format"})
		return
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel file contains no sheets"})
		return
	}
	sheetName := sheets[0]

	rows, err := f.GetRows(sheetName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve sheet data"})
		return
	}

	if len(rows) <= 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel sheet is empty"})
		return
	}

	// Resolve role ID for PARENT
	var parentRoleID int
	err = tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'PARENT'").Scan(&parentRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Role 'PARENT' is not initialized in tenant DB"})
		return
	}

	// Parse headers
	headers := rows[0]
	colIndices := map[string]int{
		"o'quvchi uid":    -1,
		"ism":             -1,
		"familiya":        -1,
		"sharif":          -1,
		"parent ism":      -1,
		"parent familiya": -1,
		"parent sharif":   -1,
		"parent turi":     -1,
		"nomer":           -1,
		"parol":           -1,
		"passport":        -1,
	}

	for i, hCell := range headers {
		cleanHeader := strings.ToLower(strings.TrimSpace(hCell))
		if cleanHeader == "o'quvchi uid" || cleanHeader == "student id" || cleanHeader == "oquvchi uid" {
			colIndices["o'quvchi uid"] = i
		} else if cleanHeader == "parent ism" || cleanHeader == "parent_first_name" {
			colIndices["parent ism"] = i
		} else if cleanHeader == "parent familiya" || cleanHeader == "parent_last_name" {
			colIndices["parent familiya"] = i
		} else if cleanHeader == "parent sharif" || cleanHeader == "parent_middle_name" {
			colIndices["parent sharif"] = i
		} else if cleanHeader == "parent turi" || cleanHeader == "relation_type" || cleanHeader == "turi" {
			colIndices["parent turi"] = i
		} else if cleanHeader == "nomer" || cleanHeader == "telefon" || cleanHeader == "phone" {
			colIndices["nomer"] = i
		} else if cleanHeader == "parol" || cleanHeader == "password" {
			colIndices["parol"] = i
		} else if cleanHeader == "passport" || cleanHeader == "pasport" || cleanHeader == "passport info" {
			colIndices["passport"] = i
		}
	}

	// Check required headers
	requiredHeaders := []string{"o'quvchi uid", "parent ism", "parent familiya", "nomer", "parol"}
	for _, reqH := range requiredHeaders {
		if colIndices[reqH] == -1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Required header '%s' is missing in spreadsheet", reqH)})
			return
		}
	}

	var rowErrors []RowError
	successCount := 0
	failedCount := 0

	getCell := func(row []string, key string) string {
		idx := colIndices[key]
		if idx >= 0 && idx < len(row) {
			return strings.TrimSpace(row[idx])
		}
		return ""
	}

	for rIdx := 1; rIdx < len(rows); rIdx++ {
		row := rows[rIdx]
		if len(row) == 0 {
			continue
		}

		studentUID := getCell(row, "o'quvchi uid")
		parentIsm := getCell(row, "parent ism")
		parentFamiliya := getCell(row, "parent familiya")
		parentSharif := getCell(row, "parent sharif")
		parentTuri := getCell(row, "parent turi")
		nomer := getCell(row, "nomer")
		parol := getCell(row, "parol")

		rowNum := rIdx + 1

		// If parent phone number is empty, skip this row silently
		if nomer == "" {
			continue
		}

		if studentUID == "" || parentIsm == "" || parentFamiliya == "" || parol == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Parent kiritilganda O'quvchi UID, Parent Ism, Parent Familiya va Parol maydonlari to'ldirilishi shart"})
			failedCount++
			continue
		}

		if parentTuri == "" {
			parentTuri = "ota"
		}

		// 1. Resolve Student
		var studentID, classID int
		isNumeric := true
		for _, char := range studentUID {
			if char < '0' || char > '9' {
				isNumeric = false
				break
			}
		}

		var studentErr error
		if isNumeric && studentUID != "" {
			uidInt, _ := strconv.Atoi(studentUID)
			studentErr = tenantDB.QueryRow("SELECT id, class_id FROM students WHERE (id = $1 OR user_id = $1) AND is_deleted = false", uidInt).Scan(&studentID, &classID)
		} else {
			studentErr = tenantDB.QueryRow(`
				SELECT s.id, s.class_id FROM students s
				JOIN users u ON s.user_id = u.id
				WHERE u.phone = $1 AND s.is_deleted = false AND u.is_deleted = false
			`, studentUID).Scan(&studentID, &classID)
		}

		if studentErr != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("O'quvchi topilmadi: %s", studentUID)})
			failedCount++
			continue
		}

		// 2. Validate Permission
		if userRole != "ADMIN" {
			if userRole != "MAIN_TEACHER" {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Ruxsat etilmagan: faqat admin va sinf rahbari ota-ona yuklay oladi"})
				failedCount++
				continue
			}
			var isMain bool
			err = tenantDB.QueryRow(`
				SELECT EXISTS(
					SELECT 1 FROM class_teachers 
					WHERE class_id = $1 AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false
				)
			`, classID, currentUserID).Scan(&isMain)
			if err != nil || !isMain {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Ruxsat etilmagan: siz ushbu o'quvchining sinf rahbari emassiz"})
				failedCount++
				continue
			}
		}

		// Begin transaction for row
		tx, err := tenantDB.Begin()
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Baza tranzaksiyasini boshlab bo'lmadi: %v", err)})
			failedCount++
			continue
		}

		// 3. Check if parent user already exists by phone
		var parentID int
		var existingRoleName string
		err = tx.QueryRow(`
			SELECT u.id, r.name FROM users u
			JOIN roles r ON u.role_id = r.id
			WHERE u.phone = $1 AND u.is_deleted = false
		`, nomer).Scan(&parentID, &existingRoleName)

		if err != nil && err != sql.ErrNoRows {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Ota-onani tekshirishda xatolik: %v", err)})
			failedCount++
			continue
		}

		if err == sql.ErrNoRows {
			// Create new parent user
			hashedPassword, err := bcrypt.GenerateFromPassword([]byte(parol), bcrypt.DefaultCost)
			if err != nil {
				tx.Rollback()
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Parolni shifrlashda xatolik"})
				failedCount++
				continue
			}

			var middleNamePtr *string
			if parentSharif != "" {
				middleNamePtr = &parentSharif
			}

			passport := getCell(row, "passport")
			var passportPtr *string
			if passport != "" {
				passportPtr = &passport
			}

			insertUserQuery := `
				INSERT INTO users (first_name, last_name, middle_name, passport, phone, password_hash, role_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				RETURNING id`
			err = tx.QueryRow(insertUserQuery, parentIsm, parentFamiliya, middleNamePtr, passportPtr, nomer, string(hashedPassword), parentRoleID).Scan(&parentID)
			if err != nil {
				tx.Rollback()
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Ota-ona profilini yaratib bo'lmadi: %v", err)})
				failedCount++
				continue
			}

			// Audit log user creation
			audit.LogChange(c, tx, audit.LogData{
				Action:    "CREATE",
				TableName: "users",
				RecordID:  strconv.Itoa(parentID),
				NewValues: models.User{
					ID:         parentID,
					FirstName:  parentIsm,
					LastName:   parentFamiliya,
					MiddleName: middleNamePtr,
					Passport:   passportPtr,
					Phone:      &nomer,
					RoleID:     parentRoleID,
					IsDeleted:  false,
				},
			})
		} else {
			// Existing user found. Verify that they have the PARENT role.
			if existingRoleName != "PARENT" {
				tx.Rollback()
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Foydalanuvchi tizimda mavjud, lekin uning roli '%s' (faqat PARENT roldagi profillarni bog'lash mumkin)", existingRoleName)})
				failedCount++
				continue
			}
			// Password remains unchanged ("parol eskiligicha qolib ketishi kerak")
		}

		// 4. Create or update link in student_parents
		upsertLinkQuery := `
			INSERT INTO student_parents (student_id, parent_id, relation_type)
			VALUES ($1, $2, $3)
			ON CONFLICT (student_id, parent_id) DO UPDATE SET relation_type = EXCLUDED.relation_type`
		_, err = tx.Exec(upsertLinkQuery, studentID, parentID, parentTuri)
		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Ota-ona bog'liqligini saqlashda xatolik: %v", err)})
			failedCount++
			continue
		}

		// Audit Log link creation
		audit.LogChange(c, tx, audit.LogData{
			Action:    "CREATE",
			TableName: "student_parents",
			RecordID:  fmt.Sprintf("%d-%d", studentID, parentID),
			NewValues: map[string]interface{}{
				"student_id":    studentID,
				"parent_id":     parentID,
				"relation_type": parentTuri,
			},
		})

		if err := tx.Commit(); err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Ma'lumotlarni saqlashda xatolik (Commit): %v", err)})
			failedCount++
		} else {
			successCount++
		}
	}

	c.JSON(http.StatusOK, ImportResult{
		Success:       len(rowErrors) == 0,
		ImportedCount: successCount,
		FailedCount:   failedCount,
		Errors:        rowErrors,
	})
}

// ExportGradeTemplate generates a sample grade import Excel template populated with active students of selected class
func (h *ImportHandler) ExportGradeTemplate(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	classIDStr := c.Query("class_id")
	subjectIDStr := c.Query("subject_id")

	if classIDStr == "" || subjectIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "class_id and subject_id are required parameters"})
		return
	}

	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid class_id"})
		return
	}

	subjectID, err := strconv.Atoi(subjectIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subject_id"})
		return
	}

	// Fetch Subject Name
	var subjectName string
	err = dbConn.QueryRow("SELECT name FROM subjects WHERE id = $1 AND is_deleted = false", subjectID).Scan(&subjectName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "subject not found or inactive"})
		return
	}

	f := excelize.NewFile()
	defer f.Close()

	// Write headers
	headers := []string{"o'quvchi uid", "ism", "familiya", "subject_id", "fan", "baho"}
	for i, name := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Sheet1", cell, name)
	}

	// Fetch active students of class
	query := `
		SELECT s.id, u.first_name, u.last_name
		FROM students s
		JOIN users u ON s.user_id = u.id
		WHERE s.is_deleted = false AND u.is_deleted = false AND s.class_id = $1
		ORDER BY u.first_name, u.last_name`

	rows, err := dbConn.Query(query, classID)
	hasStudents := false
	if err == nil && rows != nil {
		defer rows.Close()
		rowNum := 2
		for rows.Next() {
			var studentID int
			var firstName, lastName string
			if err := rows.Scan(&studentID, &firstName, &lastName); err == nil {
				hasStudents = true
				f.SetCellValue("Sheet1", fmt.Sprintf("A%d", rowNum), studentID)
				f.SetCellValue("Sheet1", fmt.Sprintf("B%d", rowNum), firstName)
				f.SetCellValue("Sheet1", fmt.Sprintf("C%d", rowNum), lastName)
				f.SetCellValue("Sheet1", fmt.Sprintf("D%d", rowNum), subjectID)
				f.SetCellValue("Sheet1", fmt.Sprintf("E%d", rowNum), subjectName)
				f.SetCellValue("Sheet1", fmt.Sprintf("F%d", rowNum), "") // Empty for teacher to fill
				rowNum++
			}
		}
	}

	if !hasStudents {
		// Sample row
		f.SetCellValue("Sheet1", "A2", "1")
		f.SetCellValue("Sheet1", "B2", "Ali")
		f.SetCellValue("Sheet1", "C2", "Valiyev")
		f.SetCellValue("Sheet1", "D2", subjectID)
		f.SetCellValue("Sheet1", "E2", subjectName)
		f.SetCellValue("Sheet1", "F2", "5")
	}

	c.Header("Content-Disposition", "attachment; filename=baholash_template.xlsx")
	c.Header("Content-Type", "application/octet-stream")
	
	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate template file"})
	}
}

// ImportGrades imports grades from uploaded Excel file
func (h *ImportHandler) ImportGrades(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File is required"})
		return
	}

	openedFile, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer openedFile.Close()

	f, err := excelize.OpenReader(openedFile)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read excel format"})
		return
	}
	defer f.Close()

	rows, err := f.GetRows("Sheet1")
	if err != nil || len(rows) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sheet1 is missing or contains no data rows"})
		return
	}

	// Resolve teacher user
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	teacherID, err := strconv.Atoi(userIDStr)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid user context"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	// Begin database transaction
	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Retrieve active grading system details (once)
	var gsID int
	var gsName, gsType string
	var minVal, maxVal sql.NullFloat64
	var optionsBytes []byte

	err = tx.QueryRow("SELECT id, name, type, min_value, max_value, options FROM grading_systems WHERE is_active = true").
		Scan(&gsID, &gsName, &gsType, &minVal, &maxVal, &optionsBytes)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No active grading system configured for the school"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve active grading system details"})
		return
	}

	var allowedLabels []string
	var opts []GradingSystemOption
	if gsType == "LETTER" {
		if optionsBytes == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Active letter grading system options are missing in DB"})
			return
		}
		if err := json.Unmarshal(optionsBytes, &opts); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse grading system options"})
			return
		}
		allowedLabels = make([]string, len(opts))
		for i, opt := range opts {
			allowedLabels[i] = opt.Label
		}
	}

	var rowErrors []RowError
	importedCount := 0

	for idx, row := range rows {
		if idx == 0 {
			continue // Skip headers
		}
		rowNum := idx + 1

		if len(row) < 6 || strings.TrimSpace(row[5]) == "" {
			continue // Skip empty grade cells
		}

		studentIDStr := strings.TrimSpace(row[0])
		subjectIDStr := strings.TrimSpace(row[3])
		gradeValue := strings.TrimSpace(row[5])

		studentID, err := strconv.Atoi(studentIDStr)
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Invalid student UID format"})
			continue
		}

		subjectID, err := strconv.Atoi(subjectIDStr)
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Invalid subject ID format"})
			continue
		}

		// Validate Student
		var studentExists bool
		err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM students WHERE id = $1 AND is_deleted = false)", studentID).Scan(&studentExists)
		if err != nil || !studentExists {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Student ID %d not found or inactive", studentID)})
			continue
		}

		// Validate Subject
		var subjectExists bool
		err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM subjects WHERE id = $1 AND is_deleted = false)", subjectID).Scan(&subjectExists)
		if err != nil || !subjectExists {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Subject ID %d not found or inactive", subjectID)})
			continue
		}

		// Validate value
		var numericValue *float64

		switch gsType {
		case "NUMERIC":
			val, err := strconv.ParseFloat(gradeValue, 64)
			if err != nil {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Grade value must be a valid number"})
				continue
			}
			if minVal.Valid && val < minVal.Float64 {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Grade %.2f is below minimum %.2f", val, minVal.Float64)})
				continue
			}
			if maxVal.Valid && val > maxVal.Float64 {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Grade %.2f is above maximum %.2f", val, maxVal.Float64)})
				continue
			}
			numericValue = &val

		case "PERCENTAGE":
			val, err := strconv.ParseFloat(gradeValue, 64)
			if err != nil {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Grade value must be a valid percentage"})
				continue
			}
			minLimit := 0.0
			maxLimit := 100.0
			if minVal.Valid {
				minLimit = minVal.Float64
			}
			if maxVal.Valid {
				maxLimit = maxVal.Float64
			}
			if val < minLimit || val > maxLimit {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Grade %.2f must be between %.2f and %.2f", val, minLimit, maxLimit)})
				continue
			}
			numericValue = &val

		case "LETTER":
			found := false
			for _, opt := range opts {
				if opt.Label == gradeValue {
					found = true
					numericValue = opt.NumericValue
					break
				}
			}
			if !found {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Invalid grade value '%s'. Allowed: %s", gradeValue, strings.Join(allowedLabels, ", "))})
				continue
			}
		}

		// Insert
		var gradeID int
		insertQuery := `
			INSERT INTO grades (student_id, subject_id, teacher_id, value, numeric_value, grade_date)
			VALUES ($1, $2, $3, $4, $5, NOW())
			RETURNING id`

		err = tx.QueryRow(insertQuery, studentID, subjectID, teacherID, gradeValue, numericValue).Scan(&gradeID)
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Database insert failed: %s", err.Error())})
			continue
		}

		newGrade := models.Grade{
			ID:           gradeID,
			StudentID:    studentID,
			SubjectID:    subjectID,
			TeacherID:    teacherID,
			Value:        gradeValue,
			NumericValue: numericValue,
			GradeDate:    time.Now(),
			IsDeleted:    false,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}

		// Log audit
		audit.LogChange(c, tx, audit.LogData{
			Action:    "CREATE",
			TableName: "grades",
			RecordID:  strconv.Itoa(gradeID),
			NewValues: newGrade,
		})

		importedCount++
	}

	if len(rowErrors) > 0 {
		tx.Rollback()
		c.JSON(http.StatusBadRequest, gin.H{
			"success":        false,
			"imported_count": 0,
			"failed_count":   len(rowErrors),
			"errors":         rowErrors,
		})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ImportResult{
		Success:       true,
		ImportedCount: importedCount,
		FailedCount:   0,
		Errors:        []RowError{},
	})
}

// ExportHolidayTemplate generates an Excel template for holiday import
func (h *ImportHandler) ExportHolidayTemplate(c *gin.Context) {
	f := excelize.NewFile()
	sheet := "Bayramlar"
	f.NewSheet(sheet)
	f.DeleteSheet("Sheet1")

	headers := []string{"Sana (YYYY-MM-DD)", "Bayram Nomi"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}

	// Example row
	f.SetCellValue(sheet, "A2", "2026-09-01")
	f.SetCellValue(sheet, "B2", "O'qituvchilar kuni")

	style, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#D4F562"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	f.SetCellStyle(sheet, "A1", "B1", style)
	f.SetColWidth(sheet, "A", "A", 20)
	f.SetColWidth(sheet, "B", "B", 30)

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", `attachment; filename="bayramlar_template.xlsx"`)
	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write template", "details": err.Error()})
	}
}

// ImportHolidays bulk-imports holidays from an Excel file
func (h *ImportHandler) ImportHolidays(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel fayl yuklanmadi", "details": err.Error()})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Faylni ochib bo'lmadi"})
		return
	}
	defer file.Close()

	f, err := excelize.OpenReader(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel formatini o'qib bo'lmadi", "details": err.Error()})
		return
	}

	sheetName := f.GetSheetName(0)
	rows, err := f.GetRows(sheetName)
	if err != nil || len(rows) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel fayl bo'sh yoki noto'g'ri formatda"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	ensureHolidayColumns(dbConn)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	var rowErrors []RowError
	importedCount := 0

	for idx, row := range rows[1:] { // skip header
		rowNum := idx + 2
		if len(row) < 2 {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Sana va bayram nomi kerak"})
			continue
		}

		dateStr := strings.TrimSpace(row[0])
		name := strings.TrimSpace(row[1])

		if dateStr == "" || name == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Sana yoki bayram nomi bo'sh"})
			continue
		}

		holidayDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			// also try Excel numeric date
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Sana formati noto'g'ri: '%s', YYYY-MM-DD bo'lishi kerak", dateStr)})
			continue
		}

		// Upsert: insert or update if already exists
		var holidayID int
		var isDeleted bool
		err = tx.QueryRow("SELECT id, is_deleted FROM school_holidays WHERE holiday_date = $1", holidayDate).Scan(&holidayID, &isDeleted)
		if err != nil && err != sql.ErrNoRows {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Bazada tekshirishda xatolik: " + err.Error()})
			continue
		}

		if err == sql.ErrNoRows {
			err = tx.QueryRow(
				`INSERT INTO school_holidays (holiday_date, name, target_levels, target_classes) VALUES ($1, $2, '{}', '{}') RETURNING id`,
				holidayDate, name,
			).Scan(&holidayID)
			if err != nil {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Qo'shishda xatolik: " + err.Error()})
				continue
			}
		} else {
			_, err = tx.Exec(
				`UPDATE school_holidays SET name=$1, is_deleted=false, deleted_at=NULL, updated_at=NOW() WHERE id=$2`,
				name, holidayID,
			)
			if err != nil {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Yangilashda xatolik: " + err.Error()})
				continue
			}
		}

		importedCount++
	}

	if len(rowErrors) > 0 && importedCount == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":        false,
			"imported_count": 0,
			"failed_count":   len(rowErrors),
			"errors":         rowErrors,
		})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ImportResult{
		Success:       true,
		ImportedCount: importedCount,
		FailedCount:   len(rowErrors),
		Errors:        rowErrors,
	})
}

// SmartStudentRow represents the parsed data coming from the frontend smart parser
type SmartStudentRow struct {
	FirstName      string `json:"firstName"`
	LastName       string `json:"lastName"`
	MiddleName     string `json:"middleName"`
	ClassName      string `json:"className"`
	BirthDate      string `json:"birthDate"`
	EnrollmentDate string `json:"enrollmentDate"`
	INA            string `json:"ina"`
	ParentName     string `json:"parentName"`
	ParentPhone    string `json:"parentPhone"`
	Address        string `json:"address"`
}

type ImportStudentsSmartRequest struct {
	Students []SmartStudentRow `json:"students"`
}

// ImportStudentsSmart processes pre-parsed and validated JSON batch data for students
func (h *ImportHandler) ImportStudentsSmart(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	tenantDB := tenantDBVal.(*sql.DB)

	var req ImportStudentsSmartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	if len(req.Students) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No students to import"})
		return
	}

	// Fetch roles
	var studentRoleID, parentRoleID int
	err := tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'STUDENT'").Scan(&studentRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "STUDENT role not found"})
		return
	}
	err = tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'PARENT'").Scan(&parentRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "PARENT role not found"})
		return
	}

	var rowErrors []RowError
	successCount := 0
	failedCount := 0

	for i, sRow := range req.Students {
		rowNum := i + 1
		
		if sRow.FirstName == "" || sRow.LastName == "" || sRow.ClassName == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Missing required fields"})
			failedCount++
			continue
		}

		tx, err := tenantDB.Begin()
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Transaction error"})
			failedCount++
			continue
		}

		// 1. Resolve or create Class
		var classID int
		err = tx.QueryRow("SELECT id FROM classes WHERE name = $1 AND is_deleted = false", sRow.ClassName).Scan(&classID)
		if err != nil {
			if err == sql.ErrNoRows {
				err = tx.QueryRow("INSERT INTO classes (name) VALUES ($1) RETURNING id", sRow.ClassName).Scan(&classID)
				if err != nil {
					tx.Rollback()
					rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Failed to create class"})
					failedCount++
					continue
				}
				audit.LogChange(c, tx, audit.LogData{Action: "CREATE", TableName: "classes", RecordID: strconv.Itoa(classID), NewValues: models.Class{ID: classID, Name: sRow.ClassName, IsDeleted: false}})
			} else {
				tx.Rollback()
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Database error resolving class"})
				failedCount++
				continue
			}
		}

		// 2. Create Student User
		studentPass := "SMART_" + time.Now().Format("20060102150405.000") + strconv.Itoa(i)
		hashedPass, _ := bcrypt.GenerateFromPassword([]byte(studentPass), bcrypt.DefaultCost)

		var studentUserID int
		var midNamePtr *string
		if sRow.MiddleName != "" { midNamePtr = &sRow.MiddleName }
		
		err = tx.QueryRow("INSERT INTO users (first_name, last_name, middle_name, password_hash, role_id) VALUES ($1, $2, $3, $4, $5) RETURNING id",
			sRow.FirstName, sRow.LastName, midNamePtr, string(hashedPass), studentRoleID).Scan(&studentUserID)
		
		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Failed to create student user"})
			failedCount++
			continue
		}

		// 3. Create Student record
		var birthdate *time.Time
		if sRow.BirthDate != "" {
			if parsed, e := time.Parse("2006-01-02", sRow.BirthDate); e == nil {
				birthdate = &parsed
			} else if parsed, e := time.Parse("02.01.2006", sRow.BirthDate); e == nil {
				birthdate = &parsed
			}
		}

		var enrollmentDate *time.Time
		if sRow.EnrollmentDate != "" {
			if parsed, e := time.Parse("2006-01-02", sRow.EnrollmentDate); e == nil {
				enrollmentDate = &parsed
			} else if parsed, e := time.Parse("02.01.2006", sRow.EnrollmentDate); e == nil {
				enrollmentDate = &parsed
			}
		}
		if enrollmentDate == nil {
			now := time.Now()
			enrollmentDate = &now
		}

		var addressPtr, inaPtr *string
		if sRow.Address != "" { addressPtr = &sRow.Address }
		if sRow.INA != "" { inaPtr = &sRow.INA }

		var studentID int
		err = tx.QueryRow("INSERT INTO students (user_id, class_id, address, birthdate, ina, enrollment_date) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id",
			studentUserID, classID, addressPtr, birthdate, inaPtr, enrollmentDate).Scan(&studentID)
		
		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Failed to create student profile"})
			failedCount++
			continue
		}

		// 4. Handle Parent (if provided)
		if sRow.ParentName != "" && sRow.ParentPhone != "" {
			var parentUserID int
			// Try to find existing parent by phone
			err = tx.QueryRow("SELECT id FROM users WHERE phone = $1 AND role_id = $2 AND is_deleted = false", sRow.ParentPhone, parentRoleID).Scan(&parentUserID)
			if err != nil && err != sql.ErrNoRows {
				tx.Rollback()
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Database error resolving parent"})
				failedCount++
				continue
			}

			if err == sql.ErrNoRows {
				// Create new parent user
				parts := strings.SplitN(strings.TrimSpace(sRow.ParentName), " ", 2)
				pLast := parts[0]
				pFirst := ""
				if len(parts) > 1 {
					pFirst = parts[1]
				}

				pPass := "123"
				pHashed, _ := bcrypt.GenerateFromPassword([]byte(pPass), bcrypt.DefaultCost)
				
				err = tx.QueryRow("INSERT INTO users (first_name, last_name, phone, password_hash, role_id) VALUES ($1, $2, $3, $4, $5) RETURNING id",
					pFirst, pLast, sRow.ParentPhone, string(pHashed), parentRoleID).Scan(&parentUserID)
				
				if err != nil {
					tx.Rollback()
					rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Failed to create parent user"})
					failedCount++
					continue
				}
			}

			// Link parent and student
			_, err = tx.Exec("INSERT INTO student_parents (student_id, parent_id) VALUES ($1, $2) ON CONFLICT DO NOTHING", studentID, parentUserID)
			if err != nil {
				tx.Rollback()
				rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Failed to link parent to student"})
				failedCount++
				continue
			}
		}

		if err := tx.Commit(); err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Commit failed"})
			failedCount++
		} else {
			successCount++
		}
	}

	c.JSON(http.StatusOK, ImportResult{
		Success:       failedCount == 0,
		ImportedCount: successCount,
		FailedCount:   failedCount,
		Errors:        rowErrors,
	})
}

type SmartStudentRowInput struct {
	ClassName         string `json:"class_name"`
	StudentFirstName  string `json:"student_first_name"`
	StudentLastName   string `json:"student_last_name"`
	StudentMiddleName string `json:"student_middle_name"`
	StudentBirthdate  string `json:"student_birthdate"`
	StudentDocumentNo string `json:"student_document_no"`
	EnrollmentDate    string `json:"enrollment_date"`
	Address           string `json:"address"`
	FatherFullName    string `json:"father_full_name"`
	FatherDocumentNo  string `json:"father_document_no"`
	FatherPhone       string `json:"father_phone"`
	MotherFullName    string `json:"mother_full_name"`
	MotherDocumentNo  string `json:"mother_document_no"`
	MotherPhone       string `json:"mother_phone"`
}

type BatchSmartStudentImportRequest struct {
	Students []SmartStudentRowInput `json:"students"`
}

// BatchImportStudentsSmart handles importing students with student, father, and mother data
func (h *ImportHandler) BatchImportStudentsSmart(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	tenantDB := tenantDBVal.(*sql.DB)

	var req BatchSmartStudentImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri so'rov formati (JSON format error)"})
		return
	}

	if len(req.Students) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Saqlash uchun o'quvchilar ro'yxati bo'sh"})
		return
	}

	var studentRoleID int
	err := tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'STUDENT'").Scan(&studentRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "STUDENT roli topilmadi"})
		return
	}

	var parentRoleID int
	err = tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'PARENT'").Scan(&parentRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "PARENT roli topilmadi"})
		return
	}

	var rowErrors []RowError
	successCount := 0
	failedCount := 0

	splitFIO := func(fullName string) (string, string, string) {
		trimmed := strings.TrimSpace(fullName)
		if trimmed == "" {
			return "", "", ""
		}
		parts := strings.Fields(trimmed)
		last := parts[0]
		first := ""
		middle := ""
		if len(parts) > 1 {
			first = parts[1]
		}
		if len(parts) > 2 {
			middle = strings.Join(parts[2:], " ")
		}
		return last, first, middle
	}

	for idx, st := range req.Students {
		rowNum := idx + 1
		sinfName := strings.TrimSpace(st.ClassName)
		firstName := strings.TrimSpace(st.StudentFirstName)
		lastName := strings.TrimSpace(st.StudentLastName)
		middleName := strings.TrimSpace(st.StudentMiddleName)

		if firstName == "" && lastName != "" {
			l, f, m := splitFIO(lastName)
			if f != "" {
				lastName = l
				firstName = f
				if middleName == "" {
					middleName = m
				}
			} else {
				firstName = "O'quvchi"
			}
		} else if lastName == "" && firstName != "" {
			l, f, m := splitFIO(firstName)
			if l != "" {
				lastName = l
				firstName = f
				if middleName == "" {
					middleName = m
				}
			} else {
				lastName = "O'quvchi"
			}
		}

		if firstName == "" {
			firstName = "O'quvchi"
		}
		if lastName == "" {
			lastName = fmt.Sprintf("Nomalum_%d", rowNum)
		}
		if sinfName == "" {
			sinfName = "1-A"
		}

		tx, err := tenantDB.Begin()
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Baza tranzaksiyasi boshlanmadi"})
			failedCount++
			continue
		}

		// 1. Resolve Class — Return error if class does not exist
		var classID int
		classQuery := `
			SELECT id FROM classes 
			WHERE (
				LOWER(name) = LOWER($1) 
				OR REGEXP_REPLACE(UPPER(TRIM(name)), '\s*-\s*', '-', 'g') = REGEXP_REPLACE(UPPER(TRIM($1)), '\s*-\s*', '-', 'g')
			) AND is_deleted = false`
		err = tx.QueryRow(classQuery, sinfName).Scan(&classID)
		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Sinf '%s' topilmadi", sinfName)})
			failedCount++
			continue
		}

		// 2. Create Student User
		studentPass := "STUDENT_NO_LOGIN_" + fmt.Sprintf("%d_%d", time.Now().UnixNano(), rowNum)
		hashedStudentPass, _ := bcrypt.GenerateFromPassword([]byte(studentPass), bcrypt.DefaultCost)

		var middleNamePtr *string
		if middleName != "" {
			middleNamePtr = &middleName
		}

		var studentUserID int
		err = tx.QueryRow(`
			INSERT INTO users (first_name, last_name, middle_name, phone, password_hash, role_id)
			VALUES ($1, $2, $3, NULL, $4, $5)
			RETURNING id`, firstName, lastName, middleNamePtr, string(hashedStudentPass), studentRoleID).Scan(&studentUserID)

		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("O'quvchi accountini yaratishda xatolik: %v", err)})
			failedCount++
			continue
		}

		// 3. Create Student profile
		var addressPtr *string
		if strings.TrimSpace(st.Address) != "" && st.Address != "-" {
			a := strings.TrimSpace(st.Address)
			addressPtr = &a
		}

		var birthdatePtr *time.Time
		if strings.TrimSpace(st.StudentBirthdate) != "" && st.StudentBirthdate != "-" {
			bdStr := strings.TrimSpace(st.StudentBirthdate)
			formats := []string{
				"2006-01-02",
				"02.01.2006",
				"01.02.2006",
				"02/01/2006",
				"01/02/2006",
				"2006/01/02",
				"02,01,2006",
			}
			for _, fmtStr := range formats {
				if parsed, err := time.Parse(fmtStr, bdStr); err == nil {
					birthdatePtr = &parsed
					break
				}
			}
		}

		var inaPtr *string
		if strings.TrimSpace(st.StudentDocumentNo) != "" && st.StudentDocumentNo != "-" {
			d := NormalizeDocumentNo(st.StudentDocumentNo)
			inaPtr = &d
		}

		var enrollmentDatePtr *time.Time
		if strings.TrimSpace(st.EnrollmentDate) != "" && st.EnrollmentDate != "-" {
			edStr := strings.TrimSpace(st.EnrollmentDate)
			formats := []string{
				"2006-01-02",
				"02.01.2006",
				"01.02.2006",
				"02/01/2006",
				"01/02/2006",
				"2006/01/02",
				"02,01,2006",
			}
			for _, fmtStr := range formats {
				if parsed, err := time.Parse(fmtStr, edStr); err == nil {
					enrollmentDatePtr = &parsed
					break
				}
			}
		}
		if enrollmentDatePtr == nil {
			now := time.Now()
			enrollmentDatePtr = &now
		}

		var studentID int
		err = tx.QueryRow(`
			INSERT INTO students (user_id, class_id, address, birthdate, ina, enrollment_date)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id`, studentUserID, classID, addressPtr, birthdatePtr, inaPtr, enrollmentDatePtr).Scan(&studentID)

		if err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("O'quvchi profilida xatolik: %v", err)})
			failedCount++
			continue
		}

		// Helper to add parent
		createOrGetParent := func(fullName, passport, phone, relType string) error {
			cleanName := strings.TrimSpace(fullName)
			cleanPhone := strings.TrimSpace(phone)
			cleanDoc := NormalizeDocumentNo(passport)

			if cleanName == "" || cleanName == "-" {
				return nil // Skip if parent not provided
			}

			pLast, pFirst, pMiddle := splitFIO(cleanName)
			if pFirst == "" && pLast != "" {
				pFirst = "Vasiy"
			}

			var pPhonePtr *string
			found := false
			var parentUserID int

			if cleanPhone != "" && cleanPhone != "-" && !strings.Contains(strings.ToLower(cleanPhone), "yo'q") {
				// 1. Check if exact parent profile (same phone AND same name) already exists
				var existingID int
				err := tx.QueryRow(`
					SELECT id FROM users 
					WHERE phone = $1 AND LOWER(first_name) = LOWER($2) AND LOWER(last_name) = LOWER($3) AND role_id = $4 AND is_deleted = false`, 
					cleanPhone, pFirst, pLast, parentRoleID).Scan(&existingID)
				if err == nil {
					parentUserID = existingID
					found = true
				} else {
					// 2. Check if phone is already taken by another user
					var phoneCount int
					_ = tx.QueryRow("SELECT COUNT(1) FROM users WHERE phone = $1 AND is_deleted = false", cleanPhone).Scan(&phoneCount)
					if phoneCount == 0 {
						pPhonePtr = &cleanPhone
					} else {
						// Phone taken by spouse/relative: pass NULL to avoid unique constraint conflict
						pPhonePtr = nil
					}
				}
			}

			var pDocPtr *string
			if cleanDoc != "" && cleanDoc != "-" && !strings.Contains(strings.ToLower(cleanDoc), "vafot") {
				pDocPtr = &cleanDoc
			}

			if !found && pDocPtr != nil {
				var existingPassID int
				err := tx.QueryRow(`
					SELECT u.id FROM users u
					JOIN roles r ON u.role_id = r.id
					WHERE r.name = 'PARENT' AND (LOWER(TRIM(u.passport)) = LOWER($1) OR REGEXP_REPLACE(LOWER(u.passport), '[^a-z0-9]', '', 'g') = REGEXP_REPLACE(LOWER($1), '[^a-z0-9]', '', 'g'))
					  AND u.is_deleted = false
					LIMIT 1
				`, *pDocPtr).Scan(&existingPassID)
				if err == nil {
					parentUserID = existingPassID
					found = true
				}
			}

			var pMiddlePtr *string
			if pMiddle != "" {
				pMiddlePtr = &pMiddle
			}

			if !found {
				pPass := "123"
				pHashed, _ := bcrypt.GenerateFromPassword([]byte(pPass), bcrypt.DefaultCost)

				err := tx.QueryRow(`
					INSERT INTO users (first_name, last_name, middle_name, phone, passport, document_no, password_hash, role_id)
					VALUES ($1, $2, $3, $4, $5, $5, $6, $7)
					RETURNING id`, pFirst, pLast, pMiddlePtr, pPhonePtr, pDocPtr, string(pHashed), parentRoleID).Scan(&parentUserID)

				if err != nil {
					return fmt.Errorf("Vasiy accountida xatolik: %v", err)
				}
			} else if pDocPtr != nil {
				_, _ = tx.Exec("UPDATE users SET passport = $1, document_no = $1 WHERE id = $2 AND (document_no IS NULL OR document_no = '')", *pDocPtr, parentUserID)
			}

			_, err = tx.Exec(`
				INSERT INTO student_parents (student_id, parent_id, relation_type)
				VALUES ($1, $2, $3)
				ON CONFLICT (student_id, parent_id) DO NOTHING`, studentID, parentUserID, relType)
			if err != nil {
				return fmt.Errorf("Vasiyni o'quvchiga bog'lashda xatolik: %v", err)
			}
			return nil
		}

		// 4. Father
		if err := createOrGetParent(st.FatherFullName, st.FatherDocumentNo, st.FatherPhone, "ota"); err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: err.Error()})
			failedCount++
			continue
		}

		// 5. Mother
		if err := createOrGetParent(st.MotherFullName, st.MotherDocumentNo, st.MotherPhone, "ona"); err != nil {
			tx.Rollback()
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: err.Error()})
			failedCount++
			continue
		}

		if err := tx.Commit(); err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Commit xatosi"})
			failedCount++
		} else {
			successCount++
		}
	}

	c.JSON(http.StatusOK, ImportResult{
		Success:       failedCount == 0,
		ImportedCount: successCount,
		FailedCount:   failedCount,
		Errors:        rowErrors,
	})
}

func fixRomanPrefix(prefix string) string {
	upper := strings.ToUpper(strings.TrimSpace(prefix))
	upper = strings.ReplaceAll(upper, "L", "I")
	upper = strings.ReplaceAll(upper, "1", "I")
	upper = strings.ReplaceAll(upper, "|", "I")
	return upper
}

func NormalizeDocumentNo(doc string) string {
	clean := strings.TrimSpace(doc)
	if clean == "" || clean == "-" || strings.EqualFold(clean, "yo'q") {
		return clean
	}

	// 1. Birth certificate with separator (e.g. I-NA. 086354, l-NA 086354, l-na.086354, 1-NA 086354, II-NA 086354, etc.)
	reBirth1 := regexp.MustCompile(`(?i)^([ivxl1\|]+)[\s\.-]+([a-z]{2})[\s\.-]*(\d+)$`)
	if matches := reBirth1.FindStringSubmatch(clean); len(matches) == 4 {
		roman := fixRomanPrefix(matches[1])
		series := strings.ToUpper(matches[2])
		num := matches[3]
		return fmt.Sprintf("%s-%s %s", roman, series, num)
	}

	// 2. Birth certificate without separator (e.g. ina086354, lna086354, iina 086354)
	reBirth2 := regexp.MustCompile(`(?i)^([ivxl1\|]{1,4})([a-z]{2})[\s\.-]*(\d+)$`)
	if matches := reBirth2.FindStringSubmatch(clean); len(matches) == 4 {
		roman := fixRomanPrefix(matches[1])
		series := strings.ToUpper(matches[2])
		num := matches[3]
		return fmt.Sprintf("%s-%s %s", roman, series, num)
	}

	// 3. Passport: 2 letters + numbers (e.g. AB 1234567 or AB. 1234567 -> AB1234567)
	rePass := regexp.MustCompile(`(?i)^([a-z]{2})[\s\.-]*(\d{6,8})$`)
	if matches := rePass.FindStringSubmatch(clean); len(matches) == 3 {
		return fmt.Sprintf("%s%s", strings.ToUpper(matches[1]), matches[2])
	}

	return clean
}

