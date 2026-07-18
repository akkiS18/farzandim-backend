package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
)

type GradeHandler struct{}

func NewGradeHandler() *GradeHandler {
	return &GradeHandler{}
}

type CreateGradeRequest struct {
	StudentID       int     `json:"student_id" binding:"required"`
	SubjectID       int     `json:"subject_id" binding:"required"`
	Value           string  `json:"value" binding:"required"`
	GradeDate       *string `json:"grade_date"` // Optional, format YYYY-MM-DD
	GradingSystemID *int    `json:"grading_system_id"`
	GradeType       *string `json:"grade_type"`
	GradeCategory   *string `json:"grade_category"`
	LessonNumber    *int    `json:"lesson_number"`
}

type BatchCreateGradesRequest struct {
	Grades []CreateGradeRequest `json:"grades" binding:"required,dive"`
}

type GradeResponse struct {
	ID               int       `json:"id"`
	StudentID        int       `json:"student_id"`
	StudentName      string    `json:"student_name"`
	ClassName        string    `json:"class_name"`
	SubjectID        int       `json:"subject_id"`
	SubjectName      string    `json:"subject_name"`
	TeacherID        int       `json:"teacher_id"`
	TeacherName      string    `json:"teacher_name"`
	Value            string    `json:"value"`
	NumericValue     *float64  `json:"numeric_value,omitempty"`
	GradeDate        time.Time `json:"grade_date"`
	Status           string    `json:"status"`
	ApprovedByParent bool      `json:"approved_by_parent"`
	GradingSystemID  *int      `json:"grading_system_id,omitempty"`
	GradeType        string    `json:"grade_type"`
	GradeCategory    string    `json:"grade_category"`
	LessonNumber     *int      `json:"lesson_number,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (h *GradeHandler) CreateGrade(c *gin.Context) {
	var req CreateGradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
		return
	}

	// 1. Resolve teacher_id from JWT token context
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

	// 2. Validate Student exists and is not deleted, and retrieve class_id
	var studentClassID int
	err = tx.QueryRow("SELECT class_id FROM students WHERE id = $1 AND is_deleted = false", req.StudentID).Scan(&studentClassID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Student profile not found or inactive"})
		return
	}

	// 2.5 Verify if teacher is assigned to this student's class and subject, or is the main teacher (unless ADMIN)
	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	if userRole != "ADMIN" {
		var isAllowed bool
		queryAllowed := `
			SELECT EXISTS(
				SELECT 1 FROM class_teachers
				WHERE class_id = $1 AND teacher_id = $2 AND is_deleted = false
				AND (subject_id = $3 OR is_main_teacher = true)
			)
		`
		err = tx.QueryRow(queryAllowed, studentClassID, teacherID, req.SubjectID).Scan(&isAllowed)
		if err != nil || !isAllowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Sizga ushbu sinfga va fanga baho qo'yishga ruxsat berilmagan"})
			return
		}
	}

	// 3. Validate Subject exists and is not deleted
	var subjectExists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM subjects WHERE id = $1 AND is_deleted = false)", req.SubjectID).Scan(&subjectExists)
	if err != nil || !subjectExists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Subject not found or inactive"})
		return
	}

	// 4. Resolve and query active grading system
	var gsID *int
	var gsName, gsType string
	var minVal, maxVal sql.NullFloat64
	var optionsBytes []byte
	var numericValue *float64

	isAttendance := req.GradeType != nil && *req.GradeType == "ATTENDANCE"
	isBehaviorWithoutGS := req.GradeType != nil && *req.GradeType == "BEHAVIOR" && req.GradingSystemID == nil

	if isAttendance {
		// Validate attendance values: +, -, k
		if req.Value != "+" && req.Value != "-" && req.Value != "k" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Davomat qiymati faqat '+', '-' yoki 'k' bo'lishi mumkin"})
			return
		}
	} else if isBehaviorWithoutGS {
		// Validate behavior values: -5 to 5
		val, err := strconv.ParseFloat(req.Value, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Xulq qiymati faqat -5 va 5 oralig'idagi son bo'lishi mumkin"})
			return
		}
		if val < -5 || val > 5 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Xulq qiymati -5 va 5 oralig'ida bo'lishi shart"})
			return
		}
		numericValue = &val
	} else {
		// Retrieve and validate using the requested or active grading system
		var dbGsID int
		if req.GradingSystemID != nil {
			err = tx.QueryRow("SELECT id, name, type, min_value, max_value, options FROM grading_systems WHERE id = $1", *req.GradingSystemID).
				Scan(&dbGsID, &gsName, &gsType, &minVal, &maxVal, &optionsBytes)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Tanlangan baholash tizimi topilmadi"})
				return
			}
		} else {
			err = tx.QueryRow("SELECT id, name, type, min_value, max_value, options FROM grading_systems WHERE is_active = true").
				Scan(&dbGsID, &gsName, &gsType, &minVal, &maxVal, &optionsBytes)
			if err != nil {
				if err == sql.ErrNoRows {
					c.JSON(http.StatusBadRequest, gin.H{"error": "No active grading system configured for the school"})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve active grading system details"})
				return
			}
		}
		gsID = &dbGsID

		// 5. Validate grade value using active grading system
		switch gsType {
		case "NUMERIC":
			val, err := strconv.ParseFloat(req.Value, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value must be a valid number for '%s' grading system", gsName)})
				return
			}
			if minVal.Valid && val < minVal.Float64 {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value %s is below minimum allowed value of %.2f", req.Value, minVal.Float64)})
				return
			}
			if maxVal.Valid && val > maxVal.Float64 {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value %s is above maximum allowed value of %.2f", req.Value, maxVal.Float64)})
				return
			}
			numericValue = &val

		case "PERCENTAGE":
			val, err := strconv.ParseFloat(req.Value, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value must be a valid percentage for '%s' grading system", gsName)})
				return
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
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value %s must be between %.2f and %.2f", req.Value, minLimit, maxLimit)})
				return
			}
			numericValue = &val

		case "LETTER":
			if optionsBytes == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Active letter grading system options are missing in DB"})
				return
			}
			var opts []GradingSystemOption
			if err := json.Unmarshal(optionsBytes, &opts); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse grading system options"})
				return
			}

			found := false
			for _, opt := range opts {
				if opt.Label == req.Value {
					found = true
					numericValue = opt.NumericValue
					break
				}
			}
			if !found {
				allowedLabels := make([]string, len(opts))
				for i, opt := range opts {
					allowedLabels[i] = opt.Label
				}
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid grade value '%s' for '%s' grading system. Allowed values: %s", req.Value, gsName, strings.Join(allowedLabels, ", ")),
				})
				return
			}
		}
	}

	// 6. Determine Grade Date
	gradeDate := time.Now()
	if req.GradeDate != nil && *req.GradeDate != "" {
		parsedDate, err := time.Parse("2006-01-02", *req.GradeDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "grade_date must be in YYYY-MM-DD format"})
			return
		}
		gradeDate = parsedDate
	}

	// Check if this date falls on a holiday
	var isHoliday bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM school_holidays WHERE holiday_date = $1::date AND is_deleted = false)", gradeDate.Format("2006-01-02")).Scan(&isHoliday)
	if err == nil && isHoliday {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dam olish kunlarida baho qo'yish taqiqlanadi"})
		return
	}

	// 7. Insert Grade record
	var gradeID int
	gType := "MASTERY"
	if req.GradeType != nil && *req.GradeType != "" {
		gType = *req.GradeType
	}
	gCat := "DAILY"
	if req.GradeCategory != nil && *req.GradeCategory != "" {
		gCat = *req.GradeCategory
	}

	insertQuery := `
		INSERT INTO grades (student_id, subject_id, teacher_id, value, numeric_value, grade_date, grading_system_id, grade_type, grade_category, lesson_number)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`

	err = tx.QueryRow(insertQuery, req.StudentID, req.SubjectID, teacherID, req.Value, numericValue, gradeDate, gsID, gType, gCat, req.LessonNumber).Scan(&gradeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to insert grade record", "details": err.Error()})
		return
	}

	newGrade := models.Grade{
		ID:               gradeID,
		StudentID:        req.StudentID,
		SubjectID:        req.SubjectID,
		TeacherID:        teacherID,
		Value:            req.Value,
		NumericValue:     numericValue,
		GradeDate:        gradeDate,
		IsDeleted:        false,
		GradeType:        gType,
		GradeCategory:    gCat,
		LessonNumber:     req.LessonNumber,
		GradingSystemID:  gsID,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	// 8. Log audit change
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "grades",
		RecordID:  strconv.Itoa(gradeID),
		NewValues: newGrade,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newGrade)
}

func (h *GradeHandler) ListGrades(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRole, _ := c.Get("role")
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// Filter query params
	studentIDParam := c.Query("student_id")
	subjectIDParam := c.Query("subject_id")
	classIDParam := c.Query("class_id")
	statusParam := c.Query("status")

	// Base SQL Query for retrieval with rich metadata
	query := `
		SELECT g.id, g.student_id, u_student.first_name || ' ' || u_student.last_name as student_name, cl.name as class_name,
		       g.subject_id, s.name as subject_name, g.teacher_id, u_teacher.first_name || ' ' || u_teacher.last_name as teacher_name,
		       g.value, g.numeric_value, g.grade_date, g.status, g.approved_by_parent, g.grading_system_id,
		       g.grade_type, g.grade_category, g.lesson_number, g.created_at, g.updated_at
		FROM grades g
		JOIN students st ON g.student_id = st.id AND st.is_deleted = false
		JOIN users u_student ON st.user_id = u_student.id AND u_student.is_deleted = false
		JOIN classes cl ON st.class_id = cl.id AND cl.is_deleted = false
		JOIN subjects s ON g.subject_id = s.id AND s.is_deleted = false
		JOIN users u_teacher ON g.teacher_id = u_teacher.id AND u_teacher.is_deleted = false
		WHERE g.is_deleted = false`

	var args []interface{}
	var argCount = 1

	// Access control:
	// If user is STUDENT, restrict view to only their own student profile
	if userRole == "STUDENT" {
		var studentProfileID int
		err := dbConn.QueryRow("SELECT id FROM students WHERE user_id = $1 AND is_deleted = false", currentUserID).Scan(&studentProfileID)
		if err != nil {
			c.JSON(http.StatusOK, []GradeResponse{})
			return
		}
		query += fmt.Sprintf(" AND g.student_id = $%d", argCount)
		args = append(args, studentProfileID)
		argCount++
	} else if userRole == "PARENT" {
		// If user is PARENT, restrict view to only their children
		rows, err := dbConn.Query("SELECT student_id FROM student_parents WHERE parent_id = $1", currentUserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify parent relationship"})
			return
		}
		defer rows.Close()

		var childrenIDs []int
		for rows.Next() {
			var cid int
			if err := rows.Scan(&cid); err == nil {
				childrenIDs = append(childrenIDs, cid)
			}
		}

		if len(childrenIDs) == 0 {
			c.JSON(http.StatusOK, []GradeResponse{})
			return
		}

		// Build a placeholders string, e.g. "$1, $2"
		var placeholders []string
		for _, childID := range childrenIDs {
			placeholders = append(placeholders, fmt.Sprintf("$%d", argCount))
			args = append(args, childID)
			argCount++
		}
		query += fmt.Sprintf(" AND g.student_id IN (%s)", strings.Join(placeholders, ", "))
	} else {
		// ADMIN or TEACHER - apply optional search filters
		if studentIDParam != "" {
			sid, err := strconv.Atoi(studentIDParam)
			if err == nil {
				query += fmt.Sprintf(" AND g.student_id = $%d", argCount)
				args = append(args, sid)
				argCount++
			}
		}

		if classIDParam != "" {
			cid, err := strconv.Atoi(classIDParam)
			if err == nil {
				query += fmt.Sprintf(" AND st.class_id = $%d", argCount)
				args = append(args, cid)
				argCount++
			}
		}
	}

	// Apply subject filter for all roles if applicable
	if subjectIDParam != "" {
		subid, err := strconv.Atoi(subjectIDParam)
		if err == nil {
			query += fmt.Sprintf(" AND g.subject_id = $%d", argCount)
			args = append(args, subid)
			argCount++
		}
	}

	if statusParam != "" {
		query += fmt.Sprintf(" AND g.status = $%d", argCount)
		args = append(args, statusParam)
		argCount++
	}

	query += " ORDER BY g.grade_date DESC, g.created_at DESC"

	rows, err := dbConn.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query grades records", "details": err.Error()})
		return
	}
	defer rows.Close()

	grades := []GradeResponse{}
	for rows.Next() {
		var r GradeResponse
		var numVal sql.NullFloat64
		var gsIDNull sql.NullInt64

		var lessonNumNull sql.NullInt64
		var updatedAtTime time.Time

		err := rows.Scan(
			&r.ID, &r.StudentID, &r.StudentName, &r.ClassName,
			&r.SubjectID, &r.SubjectName, &r.TeacherID, &r.TeacherName,
			&r.Value, &numVal, &r.GradeDate, &r.Status, &r.ApprovedByParent, &gsIDNull,
			&r.GradeType, &r.GradeCategory, &lessonNumNull, &r.CreatedAt, &updatedAtTime,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse grade records", "details": err.Error()})
			return
		}

		if numVal.Valid {
			r.NumericValue = &numVal.Float64
		}
		if gsIDNull.Valid {
			val := int(gsIDNull.Int64)
			r.GradingSystemID = &val
		}
		if lessonNumNull.Valid {
			val := int(lessonNumNull.Int64)
			r.LessonNumber = &val
		}
		r.UpdatedAt = updatedAtTime

		grades = append(grades, r)
	}

	c.JSON(http.StatusOK, grades)
}

func (h *GradeHandler) UpdateGrade(c *gin.Context) {
	gradeIDStr := c.Param("id")
	gradeID, err := strconv.Atoi(gradeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid grade ID parameter"})
		return
	}

	var req CreateGradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// 1. Fetch old grade data
	var oldGrade models.Grade
	var oldNumVal sql.NullFloat64
	var gsIDNull sql.NullInt64
	var lessonNumberNull sql.NullInt64
	queryOld := "SELECT id, student_id, subject_id, teacher_id, value, numeric_value, grade_date, status, approved_by_parent, grading_system_id, grade_type, grade_category, lesson_number, is_deleted FROM grades WHERE id = $1 AND is_deleted = false"
	err = tx.QueryRow(queryOld, gradeID).Scan(
		&oldGrade.ID, &oldGrade.StudentID, &oldGrade.SubjectID, &oldGrade.TeacherID,
		&oldGrade.Value, &oldNumVal, &oldGrade.GradeDate, &oldGrade.Status, &oldGrade.ApprovedByParent, &gsIDNull,
		&oldGrade.GradeType, &oldGrade.GradeCategory, &lessonNumberNull, &oldGrade.IsDeleted,
	)
	if oldNumVal.Valid {
		oldGrade.NumericValue = &oldNumVal.Float64
	}
	if gsIDNull.Valid {
		val := int(gsIDNull.Int64)
		oldGrade.GradingSystemID = &val
	}
	if lessonNumberNull.Valid {
		val := int(lessonNumberNull.Int64)
		oldGrade.LessonNumber = &val
	}
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Grade record not found or already deleted"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing grade record", "details": err.Error()})
		}
		return
	}
	if oldNumVal.Valid {
		oldGrade.NumericValue = &oldNumVal.Float64
	}

	// 1.5 Enforce approved lock
	if oldGrade.Status == "approved" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Tasdiqlangan (approved) baholarni o'zgartirib bo'lmaydi"})
		return
	}

	// 1.6 Resolve teacher context and check permissions
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	teacherID, err := strconv.Atoi(userIDStr)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid user context"})
		return
	}
	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)

	if userRole != "ADMIN" {
		var studentClassID int
		err = tx.QueryRow("SELECT class_id FROM students WHERE id = $1 AND is_deleted = false", req.StudentID).Scan(&studentClassID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Student profile not found or inactive"})
			return
		}

		var isAllowed bool
		queryAllowed := `
			SELECT EXISTS(
				SELECT 1 FROM class_teachers
				WHERE class_id = $1 AND teacher_id = $2 AND is_deleted = false
				AND (subject_id = $3 OR is_main_teacher = true)
			)
		`
		err = tx.QueryRow(queryAllowed, studentClassID, teacherID, req.SubjectID).Scan(&isAllowed)
		if err != nil || !isAllowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Sizga ushbu sinfga va fanga baho qo'yishga ruxsat berilmagan"})
			return
		}
	}

	// 2. Validate Student exists
	var studentExists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM students WHERE id = $1 AND is_deleted = false)", req.StudentID).Scan(&studentExists)
	if err != nil || !studentExists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Student profile not found or inactive"})
		return
	}

	// 3. Validate Subject exists
	var subjectExists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM subjects WHERE id = $1 AND is_deleted = false)", req.SubjectID).Scan(&subjectExists)
	if err != nil || !subjectExists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Subject not found or inactive"})
		return
	}

	// 4. Query active/selected grading system to validate new value
	var gsID *int
	var gsName, gsType string
	var minVal, maxVal sql.NullFloat64
	var optionsBytes []byte
	var numericValue *float64

	isAttendance := req.GradeType != nil && *req.GradeType == "ATTENDANCE"
	isBehaviorWithoutGS := req.GradeType != nil && *req.GradeType == "BEHAVIOR" && req.GradingSystemID == nil

	if isAttendance {
		if req.Value != "+" && req.Value != "-" && req.Value != "k" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Davomat qiymati faqat '+', '-' yoki 'k' bo'lishi mumkin"})
			return
		}
	} else if isBehaviorWithoutGS {
		val, err := strconv.ParseFloat(req.Value, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Xulq qiymati faqat -5 va 5 oralig'idagi son bo'lishi mumkin"})
			return
		}
		if val < -5 || val > 5 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Xulq qiymati -5 va 5 oralig'ida bo'lishi shart"})
			return
		}
		numericValue = &val
	} else {
		var dbGsID int
		if req.GradingSystemID != nil {
			err = tx.QueryRow("SELECT id, name, type, min_value, max_value, options FROM grading_systems WHERE id = $1", *req.GradingSystemID).
				Scan(&dbGsID, &gsName, &gsType, &minVal, &maxVal, &optionsBytes)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Tanlangan baholash tizimi topilmadi"})
				return
			}
		} else {
			var existingGsID sql.NullInt64
			tx.QueryRow("SELECT grading_system_id FROM grades WHERE id = $1", gradeID).Scan(&existingGsID)
			if existingGsID.Valid {
				err = tx.QueryRow("SELECT id, name, type, min_value, max_value, options FROM grading_systems WHERE id = $1", existingGsID.Int64).
					Scan(&dbGsID, &gsName, &gsType, &minVal, &maxVal, &optionsBytes)
			} else {
				err = tx.QueryRow("SELECT id, name, type, min_value, max_value, options FROM grading_systems WHERE is_active = true").
					Scan(&dbGsID, &gsName, &gsType, &minVal, &maxVal, &optionsBytes)
			}
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve grading system details"})
				return
			}
		}
		gsID = &dbGsID

		// Validate new grade value
		switch gsType {
		case "NUMERIC":
			val, err := strconv.ParseFloat(req.Value, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value must be a valid number for '%s' grading system", gsName)})
				return
			}
			if minVal.Valid && val < minVal.Float64 {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value %s is below minimum allowed value of %.2f", req.Value, minVal.Float64)})
				return
			}
			if maxVal.Valid && val > maxVal.Float64 {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value %s is above maximum allowed value of %.2f", req.Value, maxVal.Float64)})
				return
			}
			numericValue = &val

		case "PERCENTAGE":
			val, err := strconv.ParseFloat(req.Value, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value must be a valid percentage for '%s' grading system", gsName)})
				return
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
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value %s must be between %.2f and %.2f", req.Value, minLimit, maxLimit)})
				return
			}
			numericValue = &val

		case "LETTER":
			if optionsBytes == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Active letter grading system options are missing in DB"})
				return
			}
			var opts []GradingSystemOption
			if err := json.Unmarshal(optionsBytes, &opts); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse grading system options"})
				return
			}

			found := false
			for _, opt := range opts {
				if opt.Label == req.Value {
					found = true
					numericValue = opt.NumericValue
					break
				}
			}
			if !found {
				allowedLabels := make([]string, len(opts))
				for i, opt := range opts {
					allowedLabels[i] = opt.Label
				}
				c.JSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("Invalid grade value '%s' for '%s' grading system. Allowed values: %s", req.Value, gsName, strings.Join(allowedLabels, ", ")),
				})
				return
			}
		}
	}

	// Determine Grade Date
	gradeDate := time.Now()
	if req.GradeDate != nil && *req.GradeDate != "" {
		parsedDate, err := time.Parse("2006-01-02", *req.GradeDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "grade_date must be in YYYY-MM-DD format"})
			return
		}
		gradeDate = parsedDate
	} else {
		gradeDate = oldGrade.GradeDate // Preserve previous date if not provided
	}

	// Check if this date falls on a holiday
	var isHoliday bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM school_holidays WHERE holiday_date = $1::date AND is_deleted = false)", gradeDate.Format("2006-01-02")).Scan(&isHoliday)
	if err == nil && isHoliday {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dam olish kunlarida baho qo'yish taqiqlanadi"})
		return
	}

	gType := "MASTERY"
	if req.GradeType != nil && *req.GradeType != "" {
		gType = *req.GradeType
	}
	gCat := "DAILY"
	if req.GradeCategory != nil && *req.GradeCategory != "" {
		gCat = *req.GradeCategory
	}

	// Update Grade
	updateQuery := `
		UPDATE grades 
		SET student_id = $1, subject_id = $2, value = $3, numeric_value = $4, grade_date = $5, status = 'marked', approved_by_parent = false, grading_system_id = $6, grade_type = $7, grade_category = $8, lesson_number = $9, updated_at = NOW()
		WHERE id = $10`
	_, err = tx.Exec(updateQuery, req.StudentID, req.SubjectID, req.Value, numericValue, gradeDate, gsID, gType, gCat, req.LessonNumber, gradeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update grade record", "details": err.Error()})
		return
	}

	updatedGrade := models.Grade{
		ID:               gradeID,
		StudentID:        req.StudentID,
		SubjectID:        req.SubjectID,
		TeacherID:        oldGrade.TeacherID,
		Value:            req.Value,
		NumericValue:     numericValue,
		GradeDate:        gradeDate,
		Status:           "marked",
		ApprovedByParent: false,
		GradingSystemID:  gsID,
		GradeType:        gType,
		GradeCategory:    gCat,
		LessonNumber:     req.LessonNumber,
		IsDeleted:        false,
	}

	// Log audit change
	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "grades",
		RecordID:  strconv.Itoa(gradeID),
		OldValues: oldGrade,
		NewValues: updatedGrade,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, updatedGrade)
}

func (h *GradeHandler) DeleteGrade(c *gin.Context) {
	gradeIDStr := c.Param("id")
	gradeID, err := strconv.Atoi(gradeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid grade ID parameter"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// 1. Fetch old grade data
	var oldGrade models.Grade
	var oldNumVal sql.NullFloat64
	var gsIDNull sql.NullInt64
	var lessonNumberNull sql.NullInt64
	queryOld := "SELECT id, student_id, subject_id, teacher_id, value, numeric_value, grade_date, status, approved_by_parent, grading_system_id, grade_type, grade_category, lesson_number, is_deleted FROM grades WHERE id = $1 AND is_deleted = false"
	err = tx.QueryRow(queryOld, gradeID).Scan(
		&oldGrade.ID, &oldGrade.StudentID, &oldGrade.SubjectID, &oldGrade.TeacherID,
		&oldGrade.Value, &oldNumVal, &oldGrade.GradeDate, &oldGrade.Status, &oldGrade.ApprovedByParent, &gsIDNull,
		&oldGrade.GradeType, &oldGrade.GradeCategory, &lessonNumberNull, &oldGrade.IsDeleted,
	)
	if oldNumVal.Valid {
		oldGrade.NumericValue = &oldNumVal.Float64
	}
	if gsIDNull.Valid {
		val := int(gsIDNull.Int64)
		oldGrade.GradingSystemID = &val
	}
	if lessonNumberNull.Valid {
		val := int(lessonNumberNull.Int64)
		oldGrade.LessonNumber = &val
	}
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Grade record not found or already deleted"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch grade details", "details": err.Error()})
		}
		return
	}
	if oldNumVal.Valid {
		oldGrade.NumericValue = &oldNumVal.Float64
	}

	// 1.5 Enforce approved lock
	if oldGrade.Status == "approved" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Tasdiqlangan (approved) baholarni o'chirib bo'lmaydi"})
		return
	}

	// 1.6 Resolve teacher context and check permissions
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	teacherID, err := strconv.Atoi(userIDStr)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid user context"})
		return
	}
	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)

	if userRole != "ADMIN" {
		var studentClassID int
		err = tx.QueryRow("SELECT class_id FROM students WHERE id = $1 AND is_deleted = false", oldGrade.StudentID).Scan(&studentClassID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Student profile not found or inactive"})
			return
		}

		var isAllowed bool
		queryAllowed := `
			SELECT EXISTS(
				SELECT 1 FROM class_teachers
				WHERE class_id = $1 AND teacher_id = $2 AND is_deleted = false
				AND (subject_id = $3 OR is_main_teacher = true)
			)
		`
		err = tx.QueryRow(queryAllowed, studentClassID, teacherID, oldGrade.SubjectID).Scan(&isAllowed)
		if err != nil || !isAllowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Sizga ushbu sinfdan bahoni o'chirishga ruxsat berilmagan"})
			return
		}
	}

	// 2. Perform Soft Delete
	now := time.Now()
	_, err = tx.Exec("UPDATE grades SET is_deleted = true, deleted_at = $1 WHERE id = $2", now, gradeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to perform soft delete of grade record", "details": err.Error()})
		return
	}

	// 3. Log audit change
	audit.LogChange(c, tx, audit.LogData{
		Action:    "SOFT_DELETE",
		TableName: "grades",
		RecordID:  strconv.Itoa(gradeID),
		OldValues: oldGrade,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Grade deleted successfully"})
}

func (h *GradeHandler) BatchCreateGrades(c *gin.Context) {
	var req BatchCreateGradesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
		return
	}

	// 1. Resolve teacher_id from JWT token context
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

	var insertedGrades []models.Grade

	for _, gReq := range req.Grades {
		// Validate Student exists and retrieve class_id
		var studentClassID int
		err = tx.QueryRow("SELECT class_id FROM students WHERE id = $1 AND is_deleted = false", gReq.StudentID).Scan(&studentClassID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Student ID %d not found or inactive", gReq.StudentID)})
			return
		}

		// Verify if teacher is assigned to this student's class and subject, or is the main teacher (unless ADMIN)
		userRoleVal, _ := c.Get("role")
		userRole := userRoleVal.(string)
		if userRole != "ADMIN" {
			var isAllowed bool
			queryAllowed := `
				SELECT EXISTS(
					SELECT 1 FROM class_teachers
					WHERE class_id = $1 AND teacher_id = $2 AND is_deleted = false
					AND (subject_id = $3 OR is_main_teacher = true)
				)
			`
			err = tx.QueryRow(queryAllowed, studentClassID, teacherID, gReq.SubjectID).Scan(&isAllowed)
			if err != nil || !isAllowed {
				c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("O'quvchi %d ning sinfiga va ushbu fanga baho qo'yishga ruxsat berilmagan", gReq.StudentID)})
				return
			}
		}

		// Validate Subject
		var subjectExists bool
		err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM subjects WHERE id = $1 AND is_deleted = false)", gReq.SubjectID).Scan(&subjectExists)
		if err != nil || !subjectExists {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Subject ID %d not found or inactive", gReq.SubjectID)})
			return
		}

		isAttendance := gReq.GradeType != nil && *gReq.GradeType == "ATTENDANCE"
		isBehaviorWithoutGS := gReq.GradeType != nil && *gReq.GradeType == "BEHAVIOR" && gReq.GradingSystemID == nil
		var numericValue *float64

		var currentGsID *int
		var currentGsName, currentGsType string
		var currentMinVal, currentMaxVal sql.NullFloat64
		var currentOptionsBytes []byte

		if isAttendance {
			if gReq.Value != "+" && gReq.Value != "-" && gReq.Value != "k" {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Student %d: Davomat qiymati faqat '+', '-' yoki 'k' bo'lishi mumkin", gReq.StudentID)})
				return
			}
		} else if isBehaviorWithoutGS {
			val, err := strconv.ParseFloat(gReq.Value, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Student %d: Xulq qiymati faqat -5 va 5 oralig'idagi son bo'lishi mumkin", gReq.StudentID)})
				return
			}
			if val < -5 || val > 5 {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Student %d: Xulq qiymati -5 va 5 oralig'ida bo'lishi shart", gReq.StudentID)})
				return
			}
			numericValue = &val
		} else {
			var dbGsID int
			if gReq.GradingSystemID != nil {
				err = tx.QueryRow("SELECT id, name, type, min_value, max_value, options FROM grading_systems WHERE id = $1", *gReq.GradingSystemID).
					Scan(&dbGsID, &currentGsName, &currentGsType, &currentMinVal, &currentMaxVal, &currentOptionsBytes)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Student %d: Tanlangan baholash tizimi topilmadi", gReq.StudentID)})
					return
				}
			} else {
				dbGsID = gsID
				currentGsName = gsName
				currentGsType = gsType
				currentMinVal = minVal
				currentMaxVal = maxVal
				currentOptionsBytes = optionsBytes
			}
			currentGsID = &dbGsID

			var currentOpts []GradingSystemOption
			var currentAllowedLabels []string
			if currentGsType == "LETTER" {
				if currentOptionsBytes == nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Active letter grading system options are missing in DB"})
					return
				}
				if err := json.Unmarshal(currentOptionsBytes, &currentOpts); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse grading system options"})
					return
				}
				currentAllowedLabels = make([]string, len(currentOpts))
				for i, opt := range currentOpts {
					currentAllowedLabels[i] = opt.Label
				}
			}

			switch currentGsType {
			case "NUMERIC":
				val, err := strconv.ParseFloat(gReq.Value, 64)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value for student %d must be a valid number", gReq.StudentID)})
					return
				}
				if currentMinVal.Valid && val < currentMinVal.Float64 {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade %.2f for student %d is below minimum %.2f", val, gReq.StudentID, currentMinVal.Float64)})
					return
				}
				if currentMaxVal.Valid && val > currentMaxVal.Float64 {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade %.2f for student %d is above maximum %.2f", val, gReq.StudentID, currentMaxVal.Float64)})
					return
				}
				numericValue = &val

			case "PERCENTAGE":
				val, err := strconv.ParseFloat(gReq.Value, 64)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade value for student %d must be a valid percentage", gReq.StudentID)})
					return
				}
				minLimit := 0.0
				maxLimit := 100.0
				if currentMinVal.Valid {
					minLimit = currentMinVal.Float64
				}
				if currentMaxVal.Valid {
					maxLimit = currentMaxVal.Float64
				}
				if val < minLimit || val > maxLimit {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Grade %.2f for student %d must be between %.2f and %.2f", val, gReq.StudentID, minLimit, maxLimit)})
					return
				}
				numericValue = &val

			case "LETTER":
				found := false
				for _, opt := range currentOpts {
					if opt.Label == gReq.Value {
						found = true
						numericValue = opt.NumericValue
						break
					}
				}
				if !found {
					c.JSON(http.StatusBadRequest, gin.H{
						"error": fmt.Sprintf("Invalid grade value '%s' for student %d. Allowed: %s", gReq.Value, gReq.StudentID, strings.Join(currentAllowedLabels, ", ")),
					})
					return
				}
			}
		}

		// Grade date
		gradeDate := time.Now()
		if gReq.GradeDate != nil && *gReq.GradeDate != "" {
			parsedDate, err := time.Parse("2006-01-02", *gReq.GradeDate)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("grade_date for student %d must be in YYYY-MM-DD format", gReq.StudentID)})
				return
			}
			gradeDate = parsedDate
		}

		// Check if grade date falls on a holiday
		var isHoliday bool
		err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM school_holidays WHERE holiday_date = $1::date AND is_deleted = false)", gradeDate.Format("2006-01-02")).Scan(&isHoliday)
		if err == nil && isHoliday {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("O'quvchi %d uchun baho qo'yish taqiqlanadi: dam olish kuni", gReq.StudentID)})
			return
		}

		// Insert
		var gradeID int
		gType := "MASTERY"
		if gReq.GradeType != nil && *gReq.GradeType != "" {
			gType = *gReq.GradeType
		}
		gCat := "DAILY"
		if gReq.GradeCategory != nil && *gReq.GradeCategory != "" {
			gCat = *gReq.GradeCategory
		}

		insertQuery := `
			INSERT INTO grades (student_id, subject_id, teacher_id, value, numeric_value, grade_date, grading_system_id, grade_type, grade_category, lesson_number)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING id`

		err = tx.QueryRow(insertQuery, gReq.StudentID, gReq.SubjectID, teacherID, gReq.Value, numericValue, gradeDate, currentGsID, gType, gCat, gReq.LessonNumber).Scan(&gradeID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to insert grade record", "details": err.Error()})
			return
		}

		newGrade := models.Grade{
			ID:               gradeID,
			StudentID:        gReq.StudentID,
			SubjectID:        gReq.SubjectID,
			TeacherID:        teacherID,
			Value:            gReq.Value,
			NumericValue:     numericValue,
			GradeDate:        gradeDate,
			Status:           "marked",
			ApprovedByParent: false,
			GradingSystemID:  currentGsID,
			GradeType:        gType,
			GradeCategory:    gCat,
			LessonNumber:     gReq.LessonNumber,
			IsDeleted:        false,
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}

		// Log audit
		audit.LogChange(c, tx, audit.LogData{
			Action:    "CREATE",
			TableName: "grades",
			RecordID:  strconv.Itoa(gradeID),
			NewValues: newGrade,
		})

		insertedGrades = append(insertedGrades, newGrade)
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, insertedGrades)
}

type ChangeGradeStatusRequest struct {
	MarkUIDs []int  `json:"mark_uids" binding:"required"`
	Status   string `json:"status" binding:"required"`
}

func (h *GradeHandler) ChangeGradeStatus(c *gin.Context) {
	var req ChangeGradeStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
		return
	}

	if req.Status != "marked" && req.Status != "approved" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Status must be either 'marked' or 'approved'"})
		return
	}

	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)
	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Batch update and audit log each
	var updatedCount int
	for _, id := range req.MarkUIDs {
		// Fetch old grade to check existence, ownership/auth and compile audit log
		var oldGrade models.Grade
		var oldNumVal sql.NullFloat64
		err = tx.QueryRow(`
			SELECT id, student_id, subject_id, teacher_id, value, numeric_value, grade_date, status, approved_by_parent, is_deleted 
			FROM grades WHERE id = $1 AND is_deleted = false
		`, id).Scan(
			&oldGrade.ID, &oldGrade.StudentID, &oldGrade.SubjectID, &oldGrade.TeacherID,
			&oldGrade.Value, &oldNumVal, &oldGrade.GradeDate, &oldGrade.Status, &oldGrade.ApprovedByParent, &oldGrade.IsDeleted,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				continue // skip non-existent
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing grade record", "details": err.Error()})
			return
		}

		if oldNumVal.Valid {
			oldGrade.NumericValue = &oldNumVal.Float64
		}

		// Authorization Check: Teachers can only approve grades in classes/subjects they are assigned to (unless ADMIN)
		if userRole != "ADMIN" {
			// Get student's class ID
			var studentClassID int
			err = tx.QueryRow("SELECT class_id FROM students WHERE id = $1 AND is_deleted = false", oldGrade.StudentID).Scan(&studentClassID)
			if err != nil {
				continue // Skip if student not found/deleted
			}

			// Verify assignment or advisor check
			var isAllowed bool
			queryAllowed := `
				SELECT EXISTS(
					SELECT 1 FROM class_teachers
					WHERE class_id = $1 AND teacher_id = $2 AND is_deleted = false
					AND (subject_id = $3 OR is_main_teacher = true)
				)
			`
			err = tx.QueryRow(queryAllowed, studentClassID, currentUserID, oldGrade.SubjectID).Scan(&isAllowed)
			if err != nil || !isAllowed {
				c.JSON(http.StatusForbidden, gin.H{"error": "Sizga ushbu bahoning statusini o'zgartirishga ruxsat berilmagan"})
				return
			}
		}

		// Update status
		_, err = tx.Exec("UPDATE grades SET status = $1, updated_at = NOW() WHERE id = $2", req.Status, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update grade status", "details": err.Error()})
			return
		}

		// Prepare newGrade for audit
		newGrade := oldGrade
		newGrade.Status = req.Status

		audit.LogChange(c, tx, audit.LogData{
			Action:    "STATUS_CHANGE",
			TableName: "grades",
			RecordID:  strconv.Itoa(id),
			OldValues: oldGrade,
			NewValues: newGrade,
		})
		updatedCount++
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Statuses updated successfully", "updated_count": updatedCount})
}

func (h *GradeHandler) ParentApproveGrade(c *gin.Context) {
	gradeIDStr := c.Param("id")
	gradeID, err := strconv.Atoi(gradeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid grade ID parameter"})
		return
	}

	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)
	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// 1. Fetch grade details
	var grade models.Grade
	var numVal sql.NullFloat64
	err = tx.QueryRow(`
		SELECT id, student_id, subject_id, teacher_id, value, numeric_value, grade_date, status, approved_by_parent, is_deleted 
		FROM grades WHERE id = $1 AND is_deleted = false
	`, gradeID).Scan(
		&grade.ID, &grade.StudentID, &grade.SubjectID, &grade.TeacherID,
		&grade.Value, &numVal, &grade.GradeDate, &grade.Status, &grade.ApprovedByParent, &grade.IsDeleted,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Grade record not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query grade details", "details": err.Error()})
		}
		return
	}
	if numVal.Valid {
		grade.NumericValue = &numVal.Float64
	}

	// 2. Authorization check: verify parent is linked to this student (unless ADMIN)
	if userRole != "ADMIN" {
		var isLinked bool
		err = tx.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM student_parents 
				WHERE student_id = $1 AND parent_id = $2
			)
		`, grade.StudentID, currentUserID).Scan(&isLinked)
		if err != nil || !isLinked {
			c.JSON(http.StatusForbidden, gin.H{"error": "Sizga ushbu o'quvchining baholarini tasdiqlashga ruxsat berilmagan"})
			return
		}
	}

	// 3. Perform parent approval update
	_, err = tx.Exec("UPDATE grades SET approved_by_parent = true, updated_at = NOW() WHERE id = $1", gradeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update parent approval status", "details": err.Error()})
		return
	}

	newGrade := grade
	newGrade.ApprovedByParent = true

	// 4. Log audit change
	audit.LogChange(c, tx, audit.LogData{
		Action:    "PARENT_APPROVE",
		TableName: "grades",
		RecordID:  strconv.Itoa(gradeID),
		OldValues: grade,
		NewValues: newGrade,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, newGrade)
}
