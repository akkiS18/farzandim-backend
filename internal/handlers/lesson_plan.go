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
	"github.com/xuri/excelize/v2"
)

type LessonPlanHandler struct{}

func NewLessonPlanHandler() *LessonPlanHandler {
	return &LessonPlanHandler{}
}

// List returns lesson plans with optional filters: class_id, subject_id, teacher_id, start_date range
func (h *LessonPlanHandler) List(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	userRoleVal, _ := c.Get("role")
	userRole, _ := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr, _ := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	classIDStr := c.Query("class_id")
	classIDsStr := c.Query("class_ids")
	subjectIDStr := c.Query("subject_id")
	startDateFrom := c.Query("start_date_from")
	startDateTo := c.Query("start_date_to")
	searchQuery := strings.TrimSpace(c.Query("search"))

	whereClauses := []string{"lp.is_deleted = false"}
	args := []interface{}{}
	argIdx := 1

	// Teachers can ONLY view their own lesson plans even if they are MAIN_TEACHER (sinf rahbari)
	if userRole != "ADMIN" {
		whereClauses = append(whereClauses, fmt.Sprintf("lp.teacher_id = $%d", argIdx))
		args = append(args, currentUserID)
		argIdx++
	} else if teacherIDStr := c.Query("teacher_id"); teacherIDStr != "" {
		if tID, err := strconv.Atoi(teacherIDStr); err == nil && tID > 0 {
			whereClauses = append(whereClauses, fmt.Sprintf("lp.teacher_id = $%d", argIdx))
			args = append(args, tID)
			argIdx++
		}
	}

	if classIDsStr != "" {
		parts := strings.Split(classIDsStr, ",")
		var cIDs []int
		for _, p := range parts {
			if id, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && id > 0 {
				cIDs = append(cIDs, id)
			}
		}
		if len(cIDs) > 0 {
			placeholders := make([]string, len(cIDs))
			for i, id := range cIDs {
				placeholders[i] = fmt.Sprintf("$%d", argIdx)
				args = append(args, id)
				argIdx++
			}
			whereClauses = append(whereClauses, fmt.Sprintf("lp.class_id IN (%s)", strings.Join(placeholders, ", ")))
		}
	} else if classIDStr != "" {
		if cID, err := strconv.Atoi(classIDStr); err == nil && cID > 0 {
			whereClauses = append(whereClauses, fmt.Sprintf("lp.class_id = $%d", argIdx))
			args = append(args, cID)
			argIdx++
		}
	}

	if subjectIDStr != "" {
		if sID, err := strconv.Atoi(subjectIDStr); err == nil && sID > 0 {
			whereClauses = append(whereClauses, fmt.Sprintf("lp.subject_id = $%d", argIdx))
			args = append(args, sID)
			argIdx++
		}
	}

	if startDateFrom != "" {
		if tFrom, err := parseFlexibleDate(startDateFrom); err == nil {
			whereClauses = append(whereClauses, fmt.Sprintf("lp.start_date >= $%d", argIdx))
			args = append(args, tFrom.Format("2006-01-02"))
			argIdx++
		}
	}

	if startDateTo != "" {
		if tTo, err := parseFlexibleDate(startDateTo); err == nil {
			whereClauses = append(whereClauses, fmt.Sprintf("lp.start_date <= $%d", argIdx))
			args = append(args, tTo.Format("2006-01-02"))
			argIdx++
		}
	}

	if searchQuery != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("lp.topic_name ILIKE $%d", argIdx))
		args = append(args, "%"+searchQuery+"%")
		argIdx++
	}

	query := fmt.Sprintf(`
		SELECT 
			lp.id, lp.teacher_id, 
			COALESCE(u.first_name || ' ' || u.last_name, '') as teacher_name,
			lp.class_id, COALESCE(c.name, '') as class_name,
			lp.subject_id, COALESCE(s.name, '') as subject_name,
			lp.day_of_week, lp.lesson_number, lp.start_date, lp.topic_name, lp.notes,
			lp.created_at
		FROM lesson_plans lp
		LEFT JOIN users u ON lp.teacher_id = u.id
		LEFT JOIN classes c ON lp.class_id = c.id
		LEFT JOIN subjects s ON lp.subject_id = s.id
		WHERE %s
		ORDER BY lp.start_date ASC, lp.day_of_week ASC, lp.lesson_number ASC, lp.id ASC
	`, strings.Join(whereClauses, " AND "))

	rows, err := dbConn.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch lesson plans", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []models.LessonPlanResponse{}
	for rows.Next() {
		var item models.LessonPlanResponse
		var startDate, createdAt time.Time

		err := rows.Scan(
			&item.ID, &item.TeacherID, &item.TeacherName,
			&item.ClassID, &item.ClassName,
			&item.SubjectID, &item.SubjectName,
			&item.DayOfWeek, &item.LessonNumber, &startDate, &item.TopicName, &item.Notes,
			&createdAt,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan lesson plan row", "details": err.Error()})
			return
		}
		item.StartDate = startDate.Format("2006-01-02")
		item.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
		list = append(list, item)
	}

	c.JSON(http.StatusOK, list)
}

// GetMeta returns only the classes and subjects taught by the current teacher (or all if admin)
func (h *LessonPlanHandler) GetMeta(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	userRoleVal, _ := c.Get("role")
	userRole, _ := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr, _ := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	type SimpleItem struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	var classesList []SimpleItem
	var subjectsList []SimpleItem

	if userRole == "ADMIN" {
		cRows, err := dbConn.Query("SELECT id, name FROM classes WHERE is_deleted = false ORDER BY name ASC")
		if err == nil {
			defer cRows.Close()
			for cRows.Next() {
				var it SimpleItem
				if err := cRows.Scan(&it.ID, &it.Name); err == nil {
					classesList = append(classesList, it)
				}
			}
		}

		sRows, err := dbConn.Query("SELECT id, name FROM subjects WHERE is_deleted = false ORDER BY name ASC")
		if err == nil {
			defer sRows.Close()
			for sRows.Next() {
				var it SimpleItem
				if err := sRows.Scan(&it.ID, &it.Name); err == nil {
					subjectsList = append(subjectsList, it)
				}
			}
		}
	} else {
		cRows, err := dbConn.Query(`
			SELECT DISTINCT c.id, c.name
			FROM class_teachers ct
			JOIN classes c ON ct.class_id = c.id
			WHERE ct.teacher_id = $1 AND ct.is_deleted = false AND c.is_deleted = false
			ORDER BY c.name ASC
		`, currentUserID)
		if err == nil {
			defer cRows.Close()
			for cRows.Next() {
				var it SimpleItem
				if err := cRows.Scan(&it.ID, &it.Name); err == nil {
					classesList = append(classesList, it)
				}
			}
		}

		sRows, err := dbConn.Query(`
			SELECT DISTINCT s.id, s.name
			FROM class_teachers ct
			JOIN subjects s ON ct.subject_id = s.id
			WHERE ct.teacher_id = $1 AND ct.is_deleted = false AND s.is_deleted = false
			ORDER BY s.name ASC
		`, currentUserID)
		if err == nil {
			defer sRows.Close()
			for sRows.Next() {
				var it SimpleItem
				if err := sRows.Scan(&it.ID, &it.Name); err == nil {
					subjectsList = append(subjectsList, it)
				}
			}
		}
	}

	if classesList == nil {
		classesList = []SimpleItem{}
	}
	if subjectsList == nil {
		subjectsList = []SimpleItem{}
	}

	c.JSON(http.StatusOK, gin.H{
		"classes":  classesList,
		"subjects": subjectsList,
	})
}

// Create adds a single lesson plan
func (h *LessonPlanHandler) Create(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	userRoleVal, _ := c.Get("role")
	userRole, _ := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr, _ := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	var req models.CreateLessonPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri parametrlar kiritildi", "details": err.Error()})
		return
	}

	parsedDate, err := parseFlexibleDate(req.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Teacher can only add plans for subjects they teach
	if userRole != "ADMIN" {
		var isAllowed bool
		_ = dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM class_teachers WHERE teacher_id = $1 AND subject_id = $2 AND is_deleted = false)", currentUserID, req.SubjectID).Scan(&isAllowed)
		if !isAllowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Siz ushbu fandan dars bermaysiz. Faqat o'zingizning fanlaringiz rejasini kiritishingiz mumkin."})
			return
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction creation failed"})
		return
	}
	defer tx.Rollback()

	var newID int
	err = tx.QueryRow(`
		INSERT INTO lesson_plans (
			teacher_id, class_id, subject_id, day_of_week, lesson_number,
			start_date, topic_name, notes, is_deleted, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, false, NOW(), NOW())
		RETURNING id
	`, currentUserID, req.ClassID, req.SubjectID, req.DayOfWeek, req.LessonNumber,
		parsedDate.Format("2006-01-02"), strings.TrimSpace(req.TopicName), strings.TrimSpace(req.Notes),
	).Scan(&newID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Dars rejasini saqlab bo'lmadi", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "lesson_plans",
		RecordID:  strconv.Itoa(newID),
		NewValues: req,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Dars rejasi muvaffaqiyatli saqlandi", "id": newID})
}

// Update modifies an existing lesson plan
func (h *LessonPlanHandler) Update(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid lesson plan ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole, _ := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr, _ := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	var req models.UpdateLessonPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request fields", "details": err.Error()})
		return
	}

	parsedDate, err := parseFlexibleDate(req.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Permission check
	if userRole != "ADMIN" {
		var ownerID int
		err := dbConn.QueryRow("SELECT teacher_id FROM lesson_plans WHERE id = $1 AND is_deleted = false", id).Scan(&ownerID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Dars rejasi topilmadi"})
			return
		}
		if ownerID != currentUserID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: siz bu rejani o'zgartira olmaysiz"})
			return
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure"})
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE lesson_plans
		SET class_id = $1, subject_id = $2, day_of_week = $3, lesson_number = $4,
		    start_date = $5, topic_name = $6, notes = $7, updated_at = NOW()
		WHERE id = $8 AND is_deleted = false
	`, req.ClassID, req.SubjectID, req.DayOfWeek, req.LessonNumber,
		parsedDate.Format("2006-01-02"), strings.TrimSpace(req.TopicName), strings.TrimSpace(req.Notes), id,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update lesson plan", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "lesson_plans",
		RecordID:  strconv.Itoa(id),
		NewValues: req,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Dars rejasi muvaffaqiyatli yangilandi"})
}

// Delete soft-deletes a lesson plan
func (h *LessonPlanHandler) Delete(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid lesson plan ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole, _ := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr, _ := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// Permission check
	if userRole != "ADMIN" {
		var ownerID int
		err := dbConn.QueryRow("SELECT teacher_id FROM lesson_plans WHERE id = $1 AND is_deleted = false", id).Scan(&ownerID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Dars rejasi topilmadi"})
			return
		}
		if ownerID != currentUserID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: siz bu rejani o'chira olmaysiz"})
			return
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure"})
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec("UPDATE lesson_plans SET is_deleted = true, deleted_at = NOW() WHERE id = $1 AND is_deleted = false", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete lesson plan", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "lesson_plans",
		RecordID:  strconv.Itoa(id),
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Dars rejasi muvaffaqiyatli o'chirildi"})
}

// ExportLessonPlanTemplate generates an Excel template matching the user's required schema
func (h *LessonPlanHandler) ExportLessonPlanTemplate(c *gin.Context) {
	f := excelize.NewFile()
	sheet := "Ish_rejasi"
	f.NewSheet(sheet)
	f.DeleteSheet("Sheet1")

	// Columns matching user's exact specification:
	// hafta kuni | dars nome | sinf | fan | start_date | mavzu nomi
	headers := []string{
		"hafta kuni",
		"dars nome",
		"sinf",
		"fan",
		"start_date",
		"mavzu nomi",
	}

	for i, hdr := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, hdr)
	}

	// Sample rows matching user's screenshot
	f.SetCellValue(sheet, "A2", "1")
	f.SetCellValue(sheet, "B2", "1")
	f.SetCellValue(sheet, "C2", "1-A")
	f.SetCellValue(sheet, "D2", "Matematika")
	f.SetCellValue(sheet, "E2", "2026-09-01")
	f.SetCellValue(sheet, "F2", "1 raqamini o'rganish")

	f.SetCellValue(sheet, "A3", "1")
	f.SetCellValue(sheet, "B3", "4")
	f.SetCellValue(sheet, "C3", "1-A")
	f.SetCellValue(sheet, "D3", "Matematika")
	f.SetCellValue(sheet, "E3", "2026-09-01")
	f.SetCellValue(sheet, "F3", "1 raqamini takrorlash")

	f.SetCellValue(sheet, "A4", "5")
	f.SetCellValue(sheet, "B4", "4")
	f.SetCellValue(sheet, "C4", "1-A")
	f.SetCellValue(sheet, "D4", "Matematika")
	f.SetCellValue(sheet, "E4", "2026-10-25")
	f.SetCellValue(sheet, "F4", "10 gacha sanash")

	style, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "1D1E26"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#D4F562"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	f.SetCellStyle(sheet, "A1", "F1", style)
	f.SetRowHeight(sheet, 1, 26)

	f.SetColWidth(sheet, "A", "A", 16)
	f.SetColWidth(sheet, "B", "B", 16)
	f.SetColWidth(sheet, "C", "C", 16)
	f.SetColWidth(sheet, "D", "D", 24)
	f.SetColWidth(sheet, "E", "E", 20)
	f.SetColWidth(sheet, "F", "F", 45)

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", `attachment; filename="ish_rejasi_shablon.xlsx"`)
	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Shablon faylni yaratib bo'lmadi", "details": err.Error()})
	}
}

// ImportLessonPlans bulk-imports lesson plans from an Excel file
func (h *LessonPlanHandler) ImportLessonPlans(c *gin.Context) {
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

	userRoleVal, _ := c.Get("role")
	userRole, _ := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr, _ := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// Pre-load allowed subject IDs for non-admin teachers
	allowedSubjectIDs := make(map[int]bool)
	if userRole != "ADMIN" {
		tRows, err := dbConn.Query("SELECT DISTINCT subject_id FROM class_teachers WHERE teacher_id = $1 AND is_deleted = false AND subject_id IS NOT NULL", currentUserID)
		if err == nil {
			defer tRows.Close()
			for tRows.Next() {
				var sID int
				if err := tRows.Scan(&sID); err == nil {
					allowedSubjectIDs[sID] = true
				}
			}
		}
	}

	// Pre-load classes map (name -> id)
	classRows, err := dbConn.Query("SELECT id, name FROM classes WHERE is_deleted = false")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load classes", "details": err.Error()})
		return
	}
	defer classRows.Close()

	classMap := make(map[string]int)
	for classRows.Next() {
		var cID int
		var cName string
		if err := classRows.Scan(&cID, &cName); err == nil {
			classMap[strings.ToLower(strings.TrimSpace(cName))] = cID
		}
	}

	// Pre-load subjects map (name -> id)
	subjectRows, err := dbConn.Query("SELECT id, name FROM subjects WHERE is_deleted = false")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load subjects", "details": err.Error()})
		return
	}
	defer subjectRows.Close()

	subjectMap := make(map[string]int)
	for subjectRows.Next() {
		var sID int
		var sName string
		if err := subjectRows.Scan(&sID, &sName); err == nil {
			subjectMap[strings.ToLower(strings.TrimSpace(sName))] = sID
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	type RowError struct {
		Row   int    `json:"row"`
		Error string `json:"error"`
	}

	var rowErrors []RowError
	importedCount := 0

	for idx, row := range rows[1:] { // skip header row
		rowNum := idx + 2

		// Extract fields safely
		dayOfWeekStr := ""
		lessonNumStr := ""
		className := ""
		subjectName := ""
		startDateStr := ""
		topicName := ""

		if len(row) > 0 {
			dayOfWeekStr = strings.TrimSpace(row[0])
		}
		if len(row) > 1 {
			lessonNumStr = strings.TrimSpace(row[1])
		}
		if len(row) > 2 {
			className = strings.TrimSpace(row[2])
		}
		if len(row) > 3 {
			subjectName = strings.TrimSpace(row[3])
		}
		if len(row) > 4 {
			startDateStr = strings.TrimSpace(row[4])
		}
		if len(row) > 5 {
			topicName = strings.TrimSpace(row[5])
		}

		// Skip completely empty rows
		if dayOfWeekStr == "" && lessonNumStr == "" && className == "" && subjectName == "" && startDateStr == "" && topicName == "" {
			continue
		}

		// Validate mandatory fields
		if className == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Sinf nomi ko'rsatilmagan"})
			continue
		}
		if subjectName == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Fan nomi ko'rsatilmagan"})
			continue
		}
		if topicName == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Mavzu nomi (majburiy maydon) to'ldirilmagan"})
			continue
		}
		if startDateStr == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: "Boshlanish sanasi (start_date) to'ldirilmagan"})
			continue
		}

		parsedDate, errDate := parseFlexibleDate(startDateStr)
		if errDate != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: errDate.Error()})
			continue
		}

		// Resolve Class ID
		classID, classExists := classMap[strings.ToLower(className)]
		if !classExists {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("'%s' nomli sinf tizimda topilmadi", className)})
			continue
		}

		// Resolve Subject ID
		subjectID, subjectExists := subjectMap[strings.ToLower(subjectName)]
		if !subjectExists {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("'%s' nomli fan tizimda topilmadi", subjectName)})
			continue
		}

		// Strictly enforce that teacher can only import plans for subjects they teach
		if userRole != "ADMIN" && !allowedSubjectIDs[subjectID] {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Siz '%s' fanidan dars bermaysiz. Faqat o'zingiz dars beradigan fanlar rejasini yuklashingiz mumkin.", subjectName)})
			continue
		}

		// Parse Day of Week (default to weekday of date if empty or invalid)
		dayOfWeek := 1
		if dayOfWeekStr != "" {
			if d, err := strconv.Atoi(dayOfWeekStr); err == nil && d >= 1 && d <= 7 {
				dayOfWeek = d
			}
		} else {
			dayOfWeek = int(parsedDate.Weekday())
			if dayOfWeek == 0 {
				dayOfWeek = 7
			}
		}

		// Parse Lesson Number (default to 1 if empty)
		lessonNumber := 1
		if lessonNumStr != "" {
			if l, err := strconv.Atoi(lessonNumStr); err == nil && l >= 1 {
				lessonNumber = l
			}
		}

		var newPlanID int
		err = tx.QueryRow(`
			INSERT INTO lesson_plans (
				teacher_id, class_id, subject_id, day_of_week, lesson_number,
				start_date, topic_name, notes, is_deleted, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, '', false, NOW(), NOW())
			RETURNING id
		`, currentUserID, classID, subjectID, dayOfWeek, lessonNumber, parsedDate.Format("2006-01-02"), topicName).Scan(&newPlanID)

		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Error: fmt.Sprintf("Bazaga yozishda xatolik: %s", err.Error())})
			continue
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"imported_count": importedCount,
		"failed_count":   len(rowErrors),
		"errors":         rowErrors,
	})
}
