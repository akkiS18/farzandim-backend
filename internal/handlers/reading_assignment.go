package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

type ReadingAssignmentHandler struct{}

func NewReadingAssignmentHandler() *ReadingAssignmentHandler {
	return &ReadingAssignmentHandler{}
}

// CreateAssignment creates a reading assignment, attaches books & students (or class)
func (h *ReadingAssignmentHandler) CreateAssignment(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	userIDVal, _ := c.Get("userID")
	currentUserID, _ := strconv.Atoi(userIDVal.(string))

	var input models.CreateReadingAssignmentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request fields", "details": err.Error()})
		return
	}

	if len(input.BookIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Kamida bitta kitob tanlanishi shart"})
		return
	}

	var targetStudentIDs []int

	// Resolve direct student IDs or user_ids from database
	if len(input.StudentIDs) > 0 {
		rows, err := dbConn.Query(`
			SELECT s.id FROM students s
			WHERE (s.id = ANY($1) OR s.user_id = ANY($1)) AND s.is_deleted = false`, pq.Array(input.StudentIDs))
		if err == nil {
			for rows.Next() {
				var sid int
				if err := rows.Scan(&sid); err == nil {
					targetStudentIDs = append(targetStudentIDs, sid)
				}
			}
			rows.Close()
		}
	}

	// Deduplicate student IDs into a clean Unique Set
	sMap := make(map[int]bool)
	var finalStudentIDs []int
	for _, sid := range targetStudentIDs {
		if sid > 0 && !sMap[sid] {
			sMap[sid] = true
			finalStudentIDs = append(finalStudentIDs, sid)
		}
	}

	if len(finalStudentIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Topshiriq biriktirish uchun kamida bitta o'quvchi kiritilishi shart"})
		return
	}

	tx, err := dbConn.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// 1. Create reading assignment
	var assignmentID int
	err = tx.QueryRow(`
		INSERT INTO reading_assignments (title, teacher_id, start_date, end_date, description, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		RETURNING id`,
		input.Title, currentUserID, input.StartDate, input.EndDate, input.Description,
	).Scan(&assignmentID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create reading assignment", "details": err.Error()})
		return
	}

	// 2. Attach Books (reading_assignment_books)
	for _, bookID := range input.BookIDs {
		_, err := tx.Exec(`
			INSERT INTO reading_assignment_books (assignment_id, book_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			assignmentID, bookID,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to attach book to assignment", "details": err.Error()})
			return
		}
	}

	// 3. Create student reading progress records (student_reading_progress)
	for _, bookID := range input.BookIDs {
		for _, studentID := range finalStudentIDs {
			_, err := tx.Exec(`
				INSERT INTO student_reading_progress (assignment_id, book_id, student_id, status, created_at, updated_at)
				VALUES ($1, $2, $3, 'assigned', NOW(), NOW())
				ON CONFLICT (assignment_id, book_id, student_id) DO NOTHING`,
				assignmentID, bookID, studentID,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create student reading progress", "details": err.Error()})
				return
			}
		}
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE_READING_ASSIGNMENT",
		TableName: "reading_assignments",
		RecordID:  strconv.Itoa(assignmentID),
		NewValues: map[string]interface{}{
			"title":         input.Title,
			"book_count":    len(input.BookIDs),
			"student_count": len(finalStudentIDs),
		},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction commit failed", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":          "Mutolaa topshirig'i muvaffaqiyatli yaratildi va o'quvchilarga biriktirildi",
		"assignment_id":    assignmentID,
		"assigned_students": len(finalStudentIDs),
		"assigned_books":    len(input.BookIDs),
	})
}

// ListAssignments returns reading assignments created by teacher or all for admin
func (h *ReadingAssignmentHandler) ListAssignments(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	currentUserID, _ := strconv.Atoi(userIDVal.(string))

	query := `
		SELECT ra.id, ra.title, ra.teacher_id, u.first_name || ' ' || u.last_name as teacher_name,
		       ra.start_date, ra.end_date, ra.description, ra.created_at
		FROM reading_assignments ra
		JOIN users u ON ra.teacher_id = u.id
		WHERE ra.is_deleted = false`

	var args []interface{}
	if userRole != "ADMIN" {
		query += " AND ra.teacher_id = $1"
		args = append(args, currentUserID)
	}
	query += " ORDER BY ra.created_at DESC"

	rows, err := dbConn.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch reading assignments", "details": err.Error()})
		return
	}
	defer rows.Close()

	var assignments []models.ReadingAssignment
	for rows.Next() {
		var ra models.ReadingAssignment
		var sDate, eDate time.Time

		err := rows.Scan(
			&ra.ID, &ra.Title, &ra.TeacherID, &ra.TeacherName,
			&sDate, &eDate, &ra.Description, &ra.CreatedAt,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan assignment", "details": err.Error()})
			return
		}
		ra.StartDate = sDate.Format("2006-01-02")
		ra.EndDate = eDate.Format("2006-01-02")

		// Fetch attached books for this assignment
		bRows, err := dbConn.Query(`
			SELECT b.id, b.title, b.author, b.cover_url, COALESCE(b.download_link, ''), COALESCE(b.location_in_school, '')
			FROM books b
			JOIN reading_assignment_books rab ON rab.book_id = b.id
			WHERE rab.assignment_id = $1 AND b.is_deleted = false`, ra.ID)
		if err == nil {
			var books []models.Book
			for bRows.Next() {
				var bk models.Book
				bRows.Scan(&bk.ID, &bk.Title, &bk.Author, &bk.CoverURL, &bk.DownloadLink, &bk.LocationInSchool)
				books = append(books, bk)
			}
			bRows.Close()
			ra.Books = books
		}

		assignments = append(assignments, ra)
	}

	c.JSON(http.StatusOK, assignments)
}

// GetAssignmentDetails returns assignment details with student progress matrix
func (h *ReadingAssignmentHandler) GetAssignmentDetails(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	assignmentID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid assignment ID"})
		return
	}

	var ra models.ReadingAssignment
	var sDate, eDate time.Time
	err = dbConn.QueryRow(`
		SELECT ra.id, ra.title, ra.teacher_id, u.first_name || ' ' || u.last_name as teacher_name,
		       ra.start_date, ra.end_date, ra.description, ra.created_at
		FROM reading_assignments ra
		JOIN users u ON ra.teacher_id = u.id
		WHERE ra.id = $1 AND ra.is_deleted = false`, assignmentID).Scan(
		&ra.ID, &ra.Title, &ra.TeacherID, &ra.TeacherName, &sDate, &eDate, &ra.Description, &ra.CreatedAt,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Reading assignment not found"})
		return
	}
	ra.StartDate = sDate.Format("2006-01-02")
	ra.EndDate = eDate.Format("2006-01-02")

	// Fetch books
	bRows, _ := dbConn.Query(`
		SELECT b.id, b.title, b.author, b.cover_url, COALESCE(b.download_link, ''), COALESCE(b.location_in_school, '')
		FROM books b
		JOIN reading_assignment_books rab ON rab.book_id = b.id
		WHERE rab.assignment_id = $1 AND b.is_deleted = false`, assignmentID)
	var books []models.Book
	for bRows.Next() {
		var bk models.Book
		bRows.Scan(&bk.ID, &bk.Title, &bk.Author, &bk.CoverURL, &bk.DownloadLink, &bk.LocationInSchool)
		books = append(books, bk)
	}
	bRows.Close()
	ra.Books = books

	// Fetch student progress
	pRows, err := dbConn.Query(`
		SELECT srp.id, srp.assignment_id, srp.book_id, srp.student_id,
		       u.first_name || ' ' || u.last_name as student_name, COALESCE(cl.name, '') as class_name,
		       srp.status, COALESCE(srp.grade_value, ''), srp.numeric_value, srp.grading_system_id,
		       COALESCE(srp.teacher_feedback, ''), srp.graded_by, srp.graded_at, srp.created_at, srp.updated_at
		FROM student_reading_progress srp
		JOIN students s ON srp.student_id = s.id
		JOIN users u ON s.user_id = u.id
		LEFT JOIN classes cl ON s.class_id = cl.id
		WHERE srp.assignment_id = $1
		ORDER BY cl.name ASC, u.last_name ASC`, assignmentID)

	var progressList []models.StudentReadingProgress
	if err == nil {
		for pRows.Next() {
			var srp models.StudentReadingProgress
			var numVal sql.NullFloat64
			var gsID, gradedBy sql.NullInt64
			var gradedAt sql.NullTime

			err := pRows.Scan(
				&srp.ID, &srp.AssignmentID, &srp.BookID, &srp.StudentID,
				&srp.StudentName, &srp.ClassName, &srp.Status, &srp.GradeValue,
				&numVal, &gsID, &srp.TeacherFeedback, &gradedBy, &gradedAt,
				&srp.CreatedAt, &srp.UpdatedAt,
			)
			if err == nil {
				if numVal.Valid {
					nv := numVal.Float64
					srp.NumericValue = &nv
				}
				if gsID.Valid {
					gid := int(gsID.Int64)
					srp.GradingSystemID = &gid
				}
				if gradedBy.Valid {
					gb := int(gradedBy.Int64)
					srp.GradedBy = &gb
				}
				if gradedAt.Valid {
					gt := gradedAt.Time
					srp.GradedAt = &gt
				}
				progressList = append(progressList, srp)
			}
		}
		pRows.Close()
	}
	ra.Students = progressList

	c.JSON(http.StatusOK, ra)
}

// GradeStudentBook grades or updates progress of a student for a specific book in an assignment
func (h *ReadingAssignmentHandler) GradeStudentBook(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	currentUserID, _ := strconv.Atoi(userIDVal.(string))

	var input models.GradeStudentBookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request fields", "details": err.Error()})
		return
	}

	status := input.Status
	if status == "" {
		if input.GradeValue != "" {
			status = "graded"
		} else {
			status = "completed"
		}
	}

	tx, err := dbConn.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback()

	query := `
		INSERT INTO student_reading_progress 
			(assignment_id, book_id, student_id, status, grade_value, numeric_value, grading_system_id, teacher_feedback, graded_by, graded_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW(), NOW())
		ON CONFLICT (assignment_id, book_id, student_id) DO UPDATE SET
			status = EXCLUDED.status,
			grade_value = EXCLUDED.grade_value,
			numeric_value = EXCLUDED.numeric_value,
			grading_system_id = EXCLUDED.grading_system_id,
			teacher_feedback = EXCLUDED.teacher_feedback,
			graded_by = EXCLUDED.graded_by,
			graded_at = NOW(),
			updated_at = NOW()
		RETURNING id`

	var srpID int
	err = tx.QueryRow(query,
		input.AssignmentID, input.BookID, input.StudentID, status,
		input.GradeValue, input.NumericValue, input.GradingSystemID, input.TeacherFeedback, currentUserID,
	).Scan(&srpID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to grade student book progress", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "GRADE_READING_PROGRESS",
		TableName: "student_reading_progress",
		RecordID:  strconv.Itoa(srpID),
		NewValues: map[string]interface{}{
			"grade_value": input.GradeValue,
			"status":      status,
		},
	})

	tx.Commit()
	c.JSON(http.StatusOK, gin.H{"message": "O'quvchining mutolaa bahosi muvaffaqiyatli saqlandi", "id": srpID})
}

// GetStudentAssignments returns reading assignments for logged in Student or Parent
func (h *ReadingAssignmentHandler) GetStudentAssignments(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	currentUserID, _ := strconv.Atoi(userIDVal.(string))

	var studentIDs []int

	if userRole == "STUDENT" {
		var sid int
		err := dbConn.QueryRow("SELECT id FROM students WHERE user_id = $1 AND is_deleted = false", currentUserID).Scan(&sid)
		if err == nil {
			studentIDs = append(studentIDs, sid)
		}
	} else if userRole == "PARENT" {
		rows, err := dbConn.Query(`
			SELECT sp.student_id FROM student_parents sp
			JOIN students s ON sp.student_id = s.id
			WHERE sp.parent_id = $1 AND s.is_deleted = false`, currentUserID)
		if err == nil {
			for rows.Next() {
				var sid int
				if err := rows.Scan(&sid); err == nil {
					studentIDs = append(studentIDs, sid)
				}
			}
			rows.Close()
		}
	}

	if len(studentIDs) == 0 {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}

	pRows, err := dbConn.Query(`
		SELECT srp.id, srp.assignment_id, ra.title as assignment_title, ra.start_date, ra.end_date,
		       srp.book_id, b.title, b.author, b.description, b.cover_url, COALESCE(b.download_link, ''), COALESCE(b.location_in_school, ''),
		       bc.name as category_name, srp.student_id, u.first_name || ' ' || u.last_name as student_name,
		       srp.status, COALESCE(srp.grade_value, ''), srp.numeric_value, COALESCE(srp.teacher_feedback, ''),
		       srp.graded_at, srp.created_at
		FROM student_reading_progress srp
		JOIN reading_assignments ra ON srp.assignment_id = ra.id
		JOIN books b ON srp.book_id = b.id
		LEFT JOIN book_categories bc ON b.category_id = bc.id AND bc.is_deleted = false
		JOIN students s ON srp.student_id = s.id
		JOIN users u ON s.user_id = u.id
		WHERE srp.student_id = ANY($1) AND ra.is_deleted = false AND b.is_deleted = false
		ORDER BY ra.end_date DESC, ra.created_at DESC`, pq.Array(studentIDs))

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch student reading assignments", "details": err.Error()})
		return
	}
	defer pRows.Close()

	type StudentAssignmentItem struct {
		ID               int      `json:"id"`
		AssignmentID     int      `json:"assignment_id"`
		AssignmentTitle  string   `json:"assignment_title"`
		StartDate        string   `json:"start_date"`
		EndDate          string   `json:"end_date"`
		BookID           int      `json:"book_id"`
		BookTitle        string   `json:"book_title"`
		BookAuthor       string   `json:"book_author"`
		BookDescription  string   `json:"book_description"`
		CoverURL         string   `json:"cover_url"`
		DownloadLink     string   `json:"download_link"`
		LocationInSchool string   `json:"location_in_school"`
		CategoryName     string   `json:"category_name"`
		StudentID        int      `json:"student_id"`
		StudentName      string   `json:"student_name"`
		Status           string   `json:"status"`
		GradeValue       string   `json:"grade_value"`
		NumericValue     *float64 `json:"numeric_value,omitempty"`
		TeacherFeedback  string   `json:"teacher_feedback"`
		GradedAt         *string  `json:"graded_at,omitempty"`
	}

	var results []StudentAssignmentItem
	for pRows.Next() {
		var item StudentAssignmentItem
		var sDate, eDate time.Time
		var numVal sql.NullFloat64
		var catName sql.NullString
		var gradedAt sql.NullTime

		err := pRows.Scan(
			&item.ID, &item.AssignmentID, &item.AssignmentTitle, &sDate, &eDate,
			&item.BookID, &item.BookTitle, &item.BookAuthor, &item.BookDescription, &item.CoverURL, &item.DownloadLink, &item.LocationInSchool,
			&catName, &item.StudentID, &item.StudentName,
			&item.Status, &item.GradeValue, &numVal, &item.TeacherFeedback, &gradedAt, &sDate,
		)
		if err == nil {
			item.StartDate = sDate.Format("2006-01-02")
			item.EndDate = eDate.Format("2006-01-02")
			if catName.Valid {
				item.CategoryName = catName.String
			}
			if numVal.Valid {
				nv := numVal.Float64
				item.NumericValue = &nv
			}
			if gradedAt.Valid {
				gt := gradedAt.Time.Format("2006-01-02 15:04")
				item.GradedAt = &gt
			}
			results = append(results, item)
		}
	}

	c.JSON(http.StatusOK, results)
}

// DeleteAssignment soft deletes a reading assignment
func (h *ReadingAssignmentHandler) DeleteAssignment(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	assignmentID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid assignment ID"})
		return
	}

	tx, err := dbConn.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec("UPDATE reading_assignments SET is_deleted = true, deleted_at = NOW() WHERE id = $1 AND is_deleted = false", assignmentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete assignment"})
		return
	}

	rowsAff, _ := res.RowsAffected()
	if rowsAff == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Assignment not found"})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE_READING_ASSIGNMENT",
		TableName: "reading_assignments",
		RecordID:  strconv.Itoa(assignmentID),
	})

	tx.Commit()
	c.JSON(http.StatusOK, gin.H{"message": "Reading assignment deleted successfully"})
}
