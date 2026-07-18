package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
)

type ScheduleHandler struct{}

func NewScheduleHandler() *ScheduleHandler {
	return &ScheduleHandler{}
}

// GetSchedule returns the active weekly schedule for a class (including daily overrides/exceptions)
func (h *ScheduleHandler) GetSchedule(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class ID"})
		return
	}

	dateParam := c.Query("date")
	if dateParam == "" {
		dateParam = time.Now().Format("2006-01-02")
	}

	parsedQueryDate, err := time.Parse("2006-01-02", dateParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format. Use YYYY-MM-DD"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	// Check if this date is a holiday
	var isHoliday bool
	err = dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM school_holidays WHERE holiday_date = $1 AND is_deleted = false)", parsedQueryDate).Scan(&isHoliday)
	if err == nil && isHoliday {
		c.JSON(http.StatusOK, []models.ClassScheduleResponse{})
		return
	}

	// 1. Fetch recurring weekly schedule
	query := `
		SELECT cs.id, cs.class_id, cs.day_of_week, cs.lesson_number, cs.subject_id, s.name as subject_name, cs.start_date, cs.end_date
		FROM class_schedules cs
		JOIN subjects s ON cs.subject_id = s.id
		WHERE cs.class_id = $1 AND cs.is_deleted = false AND s.is_deleted = false
		  AND $2::date BETWEEN cs.start_date AND cs.end_date
		ORDER BY cs.day_of_week, cs.lesson_number`

	rows, err := dbConn.Query(query, classID, parsedQueryDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query class schedule", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []models.ClassScheduleResponse{}
	for rows.Next() {
		var item models.ClassScheduleResponse
		var startDate, endDate time.Time
		err := rows.Scan(&item.ID, &item.ClassID, &item.DayOfWeek, &item.LessonNumber, &item.SubjectID, &item.SubjectName, &startDate, &endDate)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse schedule row", "details": err.Error()})
			return
		}
		item.StartDate = startDate.Format("2006-01-02")
		item.EndDate = endDate.Format("2006-01-02")
		list = append(list, item)
	}

	// 2. Fetch exceptions for this specific date
	excRows, err := dbConn.Query(`
		SELECT ce.id, ce.lesson_number, ce.subject_id, s.name as subject_name
		FROM class_schedule_exceptions ce
		LEFT JOIN subjects s ON ce.subject_id = s.id
		WHERE ce.class_id = $1 AND ce.date = $2 AND ce.is_deleted = false
	`, classID, parsedQueryDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query schedule exceptions", "details": err.Error()})
		return
	}
	defer excRows.Close()

	// Map of exception by lesson number
	type excData struct {
		ID          int
		SubjectID   *int
		SubjectName *string
	}
	exceptions := make(map[int]excData)
	for excRows.Next() {
		var id, lessonNum int
		var subID *int
		var subName *string
		if errScan := excRows.Scan(&id, &lessonNum, &subID, &subName); errScan == nil {
			exceptions[lessonNum] = excData{ID: id, SubjectID: subID, SubjectName: subName}
		}
	}

	targetDayOfWeek := int(parsedQueryDate.Weekday())
	if targetDayOfWeek == 0 {
		targetDayOfWeek = 7
	}

	// 3. Merge exceptions into the schedule list
	// Replace or cancel existing slots in the list
	for i, item := range list {
		if item.DayOfWeek == targetDayOfWeek {
			if exc, found := exceptions[item.LessonNumber]; found {
				if exc.SubjectID == nil {
					list[i].SubjectID = 0
					list[i].SubjectName = "Bekor qilingan"
				} else {
					list[i].SubjectID = *exc.SubjectID
					if exc.SubjectName != nil {
						list[i].SubjectName = *exc.SubjectName
					}
				}
				delete(exceptions, item.LessonNumber)
			}
		}
	}

	// Add any extra lessons that were not in the recurring template
	for lessonNum, exc := range exceptions {
		if exc.SubjectID != nil {
			weekday := int(parsedQueryDate.Weekday())
			if weekday == 0 {
				weekday = 7 // Map Sunday to 7
			}
			var subName string
			if exc.SubjectName != nil {
				subName = *exc.SubjectName
			}
			list = append(list, models.ClassScheduleResponse{
				ID:           exc.ID,
				ClassID:      classID,
				DayOfWeek:    weekday,
				LessonNumber: lessonNum,
				SubjectID:    *exc.SubjectID,
				SubjectName:  subName,
				StartDate:    dateParam,
				EndDate:      dateParam,
			})
		}
	}

	c.JSON(http.StatusOK, list)
}

// SaveSchedule overwrites/sets the weekly schedule for a class
func (h *ScheduleHandler) SaveSchedule(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class ID"})
		return
	}

	var req models.SaveScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request fields", "details": err.Error()})
		return
	}

	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "start_date must be in YYYY-MM-DD format"})
		return
	}

	endDate, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end_date must be in YYYY-MM-DD format"})
		return
	}

	if startDate.After(endDate) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "start_date end_date'dan keyin bo'lishi mumkin emas"})
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
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va ushbu sinf rahbari dars jadvalini o'zgartira oladi"})
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

	// Check if the new schedule date range overlaps with another active schedule period
	var hasOverlap bool
	err = dbConn.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM class_schedules 
			WHERE class_id = $1 
			  AND start_date <> $2 
			  AND is_deleted = false 
			  AND start_date <= $3::date 
			  AND end_date >= $4::date
		)
	`, classID, req.StartDate, req.EndDate, req.StartDate).Scan(&hasOverlap)
	if err == nil && hasOverlap {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Yangi dars jadvali sanalari mavjud faol jadval davri bilan ustma-ust tushib qolyapti"})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start database transaction"})
		return
	}
	defer tx.Rollback()

	// Get old active schedules to log audit properly
	var oldSchedules []models.ClassSchedule
	oldRows, err := tx.Query(`SELECT id, class_id, day_of_week, lesson_number, subject_id, start_date, end_date FROM class_schedules WHERE class_id = $1 AND start_date = $2 AND is_deleted = false`, classID, startDate)
	if err == nil {
		for oldRows.Next() {
			var old models.ClassSchedule
			if errScan := oldRows.Scan(&old.ID, &old.ClassID, &old.DayOfWeek, &old.LessonNumber, &old.SubjectID, &old.StartDate, &old.EndDate); errScan == nil {
				oldSchedules = append(oldSchedules, old)
			}
		}
		oldRows.Close()
	}

	// Overwrite strategy: soft-delete all existing schedule records for this class with the same start_date
	_, err = tx.Exec(`UPDATE class_schedules SET is_deleted = true, deleted_at = NOW() WHERE class_id = $1 AND start_date = $2 AND is_deleted = false`, classID, startDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear previous schedule records", "details": err.Error()})
		return
	}

	// Insert new schedule records
	var newSchedules []models.ClassSchedule
	for _, lesson := range req.Lessons {
		var newCS models.ClassSchedule
		err = tx.QueryRow(`
			INSERT INTO class_schedules (class_id, day_of_week, lesson_number, subject_id, start_date, end_date)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (class_id, day_of_week, lesson_number, start_date) 
			DO UPDATE SET subject_id = EXCLUDED.subject_id, end_date = EXCLUDED.end_date, is_deleted = false, deleted_at = NULL, updated_at = NOW()
			RETURNING id, class_id, day_of_week, lesson_number, subject_id, start_date, end_date
		`, classID, lesson.DayOfWeek, lesson.LessonNumber, lesson.SubjectID, startDate, endDate).Scan(
			&newCS.ID, &newCS.ClassID, &newCS.DayOfWeek, &newCS.LessonNumber, &newCS.SubjectID, &newCS.StartDate, &newCS.EndDate,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write schedule lesson", "details": err.Error()})
			return
		}
		newSchedules = append(newSchedules, newCS)
	}

	// Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "class_schedules",
		RecordID:  strconv.Itoa(classID),
		OldValues: oldSchedules,
		NewValues: newSchedules,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit schedule changes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Dars jadvali muvaffaqiyatli saqlandi", "schedules": newSchedules})
}

// ListScheduleExceptions returns the history of all schedule exceptions for a class
func (h *ScheduleHandler) ListScheduleExceptions(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	query := `
		SELECT ce.id, ce.class_id, ce.date, ce.lesson_number, ce.subject_id, s.name as subject_name, ce.is_deleted, ce.created_at
		FROM class_schedule_exceptions ce
		LEFT JOIN subjects s ON ce.subject_id = s.id
		WHERE ce.class_id = $1
		ORDER BY ce.date DESC, ce.lesson_number ASC`

	rows, err := dbConn.Query(query, classID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query schedule exceptions history", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []models.ScheduleExceptionResponse{}
	for rows.Next() {
		var item models.ScheduleExceptionResponse
		var overrideDate time.Time
		err := rows.Scan(&item.ID, &item.ClassID, &overrideDate, &item.LessonNumber, &item.SubjectID, &item.SubjectName, &item.IsDeleted, &item.CreatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse exception history row", "details": err.Error()})
			return
		}
		item.Date = overrideDate.Format("2006-01-02")
		if item.SubjectID == nil {
			item.SubjectName = "Bekor qilingan"
		}
		list = append(list, item)
	}

	c.JSON(http.StatusOK, list)
}

// SaveScheduleException creates a new daily schedule override
func (h *ScheduleHandler) SaveScheduleException(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class ID"})
		return
	}

	var req models.SaveExceptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request fields", "details": err.Error()})
		return
	}

	overrideDate, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Date must be in YYYY-MM-DD format"})
		return
	}

	// Rule: only if date of exception is after or exactly today
	todayStr := time.Now().Format("2006-01-02")
	today, _ := time.Parse("2006-01-02", todayStr)
	if overrideDate.Before(today) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dars o'zgarishini faqat bugun yoki kelajakdagi kunlar uchun kiritish mumkin"})
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
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va ushbu sinf rahbari dars o'zgarishi kirita oladi"})
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

	// Validate subject if specified
	if req.SubjectID != nil {
		var exists bool
		err = dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM subjects WHERE id = $1 AND is_deleted = false)", *req.SubjectID).Scan(&exists)
		if err != nil || !exists {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Tanlangan fan topilmadi yoki o'chirilgan"})
			return
		}
	}

	// Rule: check if an active exception already exists for this slot
	var exists bool
	err = dbConn.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM class_schedule_exceptions 
			WHERE class_id = $1 AND date = $2 AND lesson_number = $3 AND is_deleted = false
		)
	`, classID, overrideDate, req.LessonNumber).Scan(&exists)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing exceptions", "details": err.Error()})
		return
	}
	if exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ushbu dars soati uchun allaqachon dars o'zgarishi kiritilgan. Avvalgisini o'chirib, keyin yangisini yarating."})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start database transaction"})
		return
	}
	defer tx.Rollback()

	// Insert exception override record
	var newException models.ScheduleException
	err = tx.QueryRow(`
		INSERT INTO class_schedule_exceptions (class_id, date, lesson_number, subject_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, class_id, date, lesson_number, subject_id, is_deleted, created_at, updated_at
	`, classID, overrideDate, req.LessonNumber, req.SubjectID).Scan(
		&newException.ID, &newException.ClassID, &newException.Date, &newException.LessonNumber, &newException.SubjectID, &newException.IsDeleted, &newException.CreatedAt, &newException.UpdatedAt,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write schedule exception override", "details": err.Error()})
		return
	}

	// Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "class_schedule_exceptions",
		RecordID:  strconv.Itoa(newException.ID),
		OldValues: nil,
		NewValues: newException,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit changes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Dars o'zgarishi muvaffaqiyatli saqlandi", "exception": newException})
}

// DeleteScheduleException soft-deletes a schedule exception
func (h *ScheduleHandler) DeleteScheduleException(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class ID"})
		return
	}

	exceptionIDStr := c.Param("exception_id")
	exceptionID, err := strconv.Atoi(exceptionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid exception ID"})
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
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: faqat admin va ushbu sinf rahbari dars o'zgarishi o'chira oladi"})
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

	// Query exception and check constraints
	var classIDOfException int
	var exceptionDate time.Time
	var isDeleted bool
	var oldException models.ScheduleException
	err = dbConn.QueryRow(`
		SELECT id, class_id, date, lesson_number, subject_id, is_deleted 
		FROM class_schedule_exceptions 
		WHERE id = $1
	`, exceptionID).Scan(&oldException.ID, &classIDOfException, &exceptionDate, &oldException.LessonNumber, &oldException.SubjectID, &isDeleted)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Dars o'zgarishi topilmadi"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query exception", "details": err.Error()})
		return
	}
	oldException.ClassID = classIDOfException
	oldException.Date = exceptionDate
	oldException.IsDeleted = isDeleted

	if classIDOfException != classID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dars o'zgarishi berilgan sinfga tegishli emas"})
		return
	}
	if isDeleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ushbu dars o'zgarishi allaqachon o'chirilgan"})
		return
	}

	// Rule: only if date of exception is after or exactly today
	todayStr := time.Now().Format("2006-01-02")
	today, _ := time.Parse("2006-01-02", todayStr)
	if exceptionDate.Before(today) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "O'tmishdagi dars o'zgarishlarini o'chirib bo'lmaydi"})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start database transaction"})
		return
	}
	defer tx.Rollback()

	// Perform soft delete
	_, err = tx.Exec(`
		UPDATE class_schedule_exceptions 
		SET is_deleted = true, deleted_at = NOW(), updated_at = NOW() 
		WHERE id = $1
	`, exceptionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete schedule exception override", "details": err.Error()})
		return
	}

	// Prepare audited value
	newException := oldException
	newException.IsDeleted = true
	nowTime := time.Now()
	newException.DeletedAt = &nowTime

	// Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "class_schedule_exceptions",
		RecordID:  strconv.Itoa(exceptionID),
		OldValues: oldException,
		NewValues: newException,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit changes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Dars o'zgarishi muvaffaqiyatli o'chirildi"})
}

// GetSchedulePeriods returns a list of distinct schedule date periods (start_date to end_date) configured for a class
func (h *ScheduleHandler) GetSchedulePeriods(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	rows, err := dbConn.Query(`
		SELECT DISTINCT start_date, end_date 
		FROM class_schedules 
		WHERE class_id = $1 AND is_deleted = false 
		ORDER BY start_date DESC
	`, classID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query schedule periods", "details": err.Error()})
		return
	}
	defer rows.Close()

	type Period struct {
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	}

	list := []Period{}
	for rows.Next() {
		var startT, endT time.Time
		if err := rows.Scan(&startT, &endT); err == nil {
			list = append(list, Period{
				StartDate: startT.Format("2006-01-02"),
				EndDate:   endT.Format("2006-01-02"),
			})
		}
	}

	c.JSON(http.StatusOK, list)
}
