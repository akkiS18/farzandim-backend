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
	"github.com/farzandim/backend/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
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
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	var list []models.ClassScheduleResponse

	if dateParam != "" {
		parsedQueryDate, err := time.Parse("2006-01-02", dateParam)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format. Use YYYY-MM-DD"})
			return
		}

		// Check if this date is a holiday for this class
		var classLevel int
		_ = dbConn.QueryRow("SELECT level FROM classes WHERE id = $1 AND is_deleted = false", classID).Scan(&classLevel)

		var isHoliday bool
		err = dbConn.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM school_holidays 
				WHERE holiday_date = $1 AND is_deleted = false
				  AND (
					(cardinality(target_levels) IS NULL OR cardinality(target_levels) = 0)
					AND (cardinality(target_classes) IS NULL OR cardinality(target_classes) = 0)
					OR $2 = ANY(target_levels)
					OR $3 = ANY(target_classes)
				  )
			)`, parsedQueryDate, classLevel, classID).Scan(&isHoliday)
		if err == nil && isHoliday {
			c.JSON(http.StatusOK, []models.ClassScheduleResponse{})
			return
		}

		query := `
			SELECT cs.id, cs.class_id, cs.day_of_week, cs.lesson_number, cs.subject_id, s.name as subject_name, cs.start_date, cs.end_date
			FROM class_schedules cs
			JOIN subjects s ON cs.subject_id = s.id
			WHERE cs.class_id = $1 AND cs.is_deleted = false AND s.is_deleted = false
			  AND $2::date BETWEEN cs.start_date AND cs.end_date
			  AND cs.start_date = (
				SELECT MAX(cs2.start_date)
				FROM class_schedules cs2
				WHERE cs2.class_id = $1 AND cs2.is_deleted = false
				  AND $2::date BETWEEN cs2.start_date AND cs2.end_date
			  )
			ORDER BY cs.day_of_week, cs.lesson_number`
		rows, err := dbConn.Query(query, classID, parsedQueryDate)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query class schedule", "details": err.Error()})
			return
		}
		defer rows.Close()

		for rows.Next() {
			var item models.ClassScheduleResponse
			var startDate, endDate time.Time
			err := rows.Scan(&item.ID, &item.ClassID, &item.DayOfWeek, &item.LessonNumber, &item.SubjectID, &item.SubjectName, &startDate, &endDate)
			if err != nil {
				rows.Close()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse schedule row", "details": err.Error()})
				return
			}
			item.StartDate = startDate.Format("2006-01-02")
			item.EndDate = endDate.Format("2006-01-02")
			list = append(list, item)
		}
		rows.Close()

		// Fetch exceptions for this specific date
		excRows, err := dbConn.Query(`
			SELECT ce.id, ce.lesson_number, ce.subject_id, s.name as subject_name
			FROM class_schedule_exceptions ce
			LEFT JOIN subjects s ON ce.subject_id = s.id
			WHERE ce.class_id = $1 AND ce.date = $2 AND ce.is_deleted = false
		`, classID, parsedQueryDate)
		if err == nil {
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
			excRows.Close()

			targetDayOfWeek := int(parsedQueryDate.Weekday())
			if targetDayOfWeek == 0 {
				targetDayOfWeek = 7
			}

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

			for lessonNum, exc := range exceptions {
				if exc.SubjectID != nil {
					weekday := int(parsedQueryDate.Weekday())
					if weekday == 0 {
						weekday = 7
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
		}
	} else {
		// When no date parameter is passed, return ALL non-deleted schedule records for this class across all periods
		query := `
			SELECT cs.id, cs.class_id, cs.day_of_week, cs.lesson_number, cs.subject_id, s.name as subject_name, cs.start_date, cs.end_date
			FROM class_schedules cs
			JOIN subjects s ON cs.subject_id = s.id
			WHERE cs.class_id = $1 AND cs.is_deleted = false AND s.is_deleted = false
			ORDER BY cs.start_date ASC, cs.day_of_week, cs.lesson_number`
		rows, err := dbConn.Query(query, classID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query class schedule", "details": err.Error()})
			return
		}
		defer rows.Close()

		for rows.Next() {
			var item models.ClassScheduleResponse
			var startDate, endDate time.Time
			err := rows.Scan(&item.ID, &item.ClassID, &item.DayOfWeek, &item.LessonNumber, &item.SubjectID, &item.SubjectName, &startDate, &endDate)
			if err != nil {
				rows.Close()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse schedule row", "details": err.Error()})
				return
			}
			item.StartDate = startDate.Format("2006-01-02")
			item.EndDate = endDate.Format("2006-01-02")
			list = append(list, item)
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
	var conflictStart, conflictEnd string
	var overlapQuery string
	var overlapArgs []interface{}

	if req.OriginalStartDate != "" {
		// Editing existing period: check overlap with ANY OTHER active period
		overlapQuery = `
			SELECT to_char(start_date, 'YYYY-MM-DD'), to_char(end_date, 'YYYY-MM-DD')
			FROM class_schedules 
			WHERE class_id = $1 
			  AND start_date <> $2::date
			  AND is_deleted = false 
			  AND start_date <= $3::date 
			  AND end_date >= $4::date
			LIMIT 1
		`
		overlapArgs = []interface{}{classID, req.OriginalStartDate, req.EndDate, req.StartDate}
	} else {
		// Adding new period: check overlap with ALL active periods
		overlapQuery = `
			SELECT to_char(start_date, 'YYYY-MM-DD'), to_char(end_date, 'YYYY-MM-DD')
			FROM class_schedules 
			WHERE class_id = $1 
			  AND is_deleted = false 
			  AND start_date <= $2::date 
			  AND end_date >= $3::date
			LIMIT 1
		`
		overlapArgs = []interface{}{classID, req.EndDate, req.StartDate}
	}

	err = dbConn.QueryRow(overlapQuery, overlapArgs...).Scan(&conflictStart, &conflictEnd)
	if err == nil && conflictStart != "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Siz kiritgan sana oralig'i (%s — %s) davridagi mavjud dars jadvali bilan ustma-ust tushib qolyapti!", conflictStart, conflictEnd),
		})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start database transaction"})
		return
	}
	defer tx.Rollback()

	targetOldStartDate := startDate
	if req.OriginalStartDate != "" {
		if parsedOrig, errOrig := time.Parse("2006-01-02", req.OriginalStartDate); errOrig == nil {
			targetOldStartDate = parsedOrig
		}
	}

	// Get old active schedules to log audit properly
	var oldSchedules []models.ClassSchedule
	oldRows, err := tx.Query(`SELECT id, class_id, day_of_week, lesson_number, subject_id, start_date, end_date FROM class_schedules WHERE class_id = $1 AND start_date = $2 AND is_deleted = false`, classID, targetOldStartDate)
	if err == nil {
		for oldRows.Next() {
			var old models.ClassSchedule
			if errScan := oldRows.Scan(&old.ID, &old.ClassID, &old.DayOfWeek, &old.LessonNumber, &old.SubjectID, &old.StartDate, &old.EndDate); errScan == nil {
				oldSchedules = append(oldSchedules, old)
			}
		}
		oldRows.Close()
	}

	// If editing an existing period (or updating by start_date), soft-delete the previous period records
	if req.OriginalStartDate != "" || len(oldSchedules) > 0 {
		_, err = tx.Exec(`UPDATE class_schedules SET is_deleted = true, deleted_at = NOW() WHERE class_id = $1 AND start_date = $2 AND is_deleted = false`, classID, targetOldStartDate)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear previous schedule records", "details": err.Error()})
			return
		}
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
		ORDER BY start_date ASC
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

// ExportScheduleTemplate generates an Excel template matching the exact format:
// hafta kuni | dars nome | sinf | fan | start_date | end_date
func (h *ScheduleHandler) ExportScheduleTemplate(c *gin.Context) {
	f := excelize.NewFile()
	defer f.Close()

	sheetName := "Dars Jadvali"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create worksheet"})
		return
	}
	f.SetActiveSheet(index)
	_ = f.DeleteSheet("Sheet1")

	// Headers
	headers := []string{"hafta kuni", "dars nome", "sinf", "fan", "start_date", "end_date"}
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheetName, cell, header)
	}

	// Sample data matching user screenshot
	sampleRows := [][]interface{}{
		{1, 1, "1-A", "Matematika", "2026-09-01", "2026-10-30"},
		{1, 2, "1-B", "Ingliz tili", "2026-09-01", "2026-10-30"},
		{1, 3, "1-A", "Ona tili", "2026-09-01", "2026-10-30"},
		{1, 4, "1-B", "Rus tili", "2026-09-01", "2026-10-30"},
	}

	for rowIdx, rowData := range sampleRows {
		for colIdx, val := range rowData {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+2)
			_ = f.SetCellValue(sheetName, cell, val)
		}
	}

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=dars_jadvali_shablon.xlsx")
	c.Header("File-Name", "dars_jadvali_shablon.xlsx")

	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate excel file"})
	}
}

type SmartScheduleImportRow struct {
	DayOfWeek    int    `json:"day_of_week"`
	LessonNumber int    `json:"lesson_number"`
	ClassName    string `json:"class_name"`
	SubjectName  string `json:"subject_name"`
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
}

type BatchImportSchedulesRequest struct {
	Schedules []SmartScheduleImportRow `json:"schedules"`
}

// BatchImportSchedulesSmart imports schedules for multiple classes at once,
// creating intervals and strictly validating teacher/time conflicts across classes.
func (h *ScheduleHandler) BatchImportSchedulesSmart(c *gin.Context) {
	var req BatchImportSchedulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if len(req.Schedules) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Import qilish uchun dars jadvali kiritilmadi"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback()

	// 1. Cache classes map (normalized class name -> id)
	classMap := make(map[string]int)
	cRows, err := tx.Query("SELECT id, name FROM classes WHERE is_deleted = false")
	if err == nil {
		for cRows.Next() {
			var cid int
			var cname string
			if err := cRows.Scan(&cid, &cname); err == nil {
				classMap[utils.NormalizeClassName(cname)] = cid
			}
		}
		cRows.Close()
	}

	// 2. Cache subjects map (lowercased subject name -> id)
	subjectMap := make(map[string]int)
	sRows, err := tx.Query("SELECT id, name FROM subjects WHERE is_deleted = false")
	if err == nil {
		for sRows.Next() {
			var sid int
			var sname string
			if err := sRows.Scan(&sid, &sname); err == nil {
				subjectMap[strings.ToLower(strings.TrimSpace(sname))] = sid
			}
		}
		sRows.Close()
	}

	// Helper to resolve or insert subject
	getOrCreateSubject := func(name string) (int, error) {
		cleanName := strings.TrimSpace(name)
		lowName := strings.ToLower(cleanName)
		if sid, exists := subjectMap[lowName]; exists {
			return sid, nil
		}
		var newID int
		err := tx.QueryRow("INSERT INTO subjects (name) VALUES ($1) RETURNING id", cleanName).Scan(&newID)
		if err != nil {
			return 0, err
		}
		subjectMap[lowName] = newID
		return newID, nil
	}

	dayNames := map[int]string{1: "Dushanba", 2: "Seshanba", 3: "Chorshanba", 4: "Payshanba", 5: "Juma", 6: "Shanba"}

	type ProcessedItem struct {
		ClassID      int
		ClassName    string
		DayOfWeek    int
		LessonNumber int
		SubjectID    int
		SubjectName  string
		StartDate    string
		EndDate      string
	}

	processedList := make([]ProcessedItem, 0, len(req.Schedules))

	for idx, item := range req.Schedules {
		cleanClass := strings.TrimSpace(item.ClassName)
		if cleanClass == "" {
			continue
		}

		classID, exists := classMap[utils.NormalizeClassName(cleanClass)]
		if !exists {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("%d-qatorda kiritilgan '%s' sinfi bazada topilmadi!", idx+1, cleanClass),
			})
			return
		}

		if item.DayOfWeek < 1 || item.DayOfWeek > 6 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("%d-qatorda hafta kuni 1 va 6 orasida bo'lishi kerak (Kiritilgan: %d)", idx+1, item.DayOfWeek),
			})
			return
		}

		if item.LessonNumber < 1 || item.LessonNumber > 10 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("%d-qatorda dars soati 1 va 10 orasida bo'lishi kerak (Kiritilgan: %d)", idx+1, item.LessonNumber),
			})
			return
		}

		cleanSubject := strings.TrimSpace(item.SubjectName)
		if cleanSubject == "" {
			continue
		}

		subjectID, err := getOrCreateSubject(cleanSubject)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("%d-qatorda '%s' fanini yaratishda xatolik: %v", idx+1, cleanSubject, err),
			})
			return
		}

		startD := strings.TrimSpace(item.StartDate)
		if startD == "" || strings.Contains(startD, "sentabr") {
			startD = "2026-09-01"
		}
		endD := strings.TrimSpace(item.EndDate)
		if endD == "" || strings.Contains(endD, "oktabr") || strings.Contains(endD, "may") {
			endD = "2026-10-30"
		}

		if _, pErr := time.Parse("2006-01-02", startD); pErr != nil {
			startD = "2026-09-01"
		}
		if _, pErr := time.Parse("2006-01-02", endD); pErr != nil {
			endD = "2027-05-31"
		}

		processedList = append(processedList, ProcessedItem{
			ClassID:      classID,
			ClassName:    cleanClass,
			DayOfWeek:    item.DayOfWeek,
			LessonNumber: item.LessonNumber,
			SubjectID:    subjectID,
			SubjectName:  cleanSubject,
			StartDate:    startD,
			EndDate:      endD,
		})
	}

	// 4. Internal Overlap Conflict Check (within payload)
	for i := 0; i < len(processedList); i++ {
		for j := i + 1; j < len(processedList); j++ {
			a := processedList[i]
			b := processedList[j]

			if a.ClassID != b.ClassID && a.DayOfWeek == b.DayOfWeek && a.LessonNumber == b.LessonNumber {
				if (a.StartDate <= b.EndDate) && (a.EndDate >= b.StartDate) {
					var teacherA, teacherB int
					_ = tx.QueryRow("SELECT teacher_id FROM class_teachers WHERE class_id = $1 AND subject_id = $2 AND is_deleted = false LIMIT 1", a.ClassID, a.SubjectID).Scan(&teacherA)
					_ = tx.QueryRow("SELECT teacher_id FROM class_teachers WHERE class_id = $1 AND subject_id = $2 AND is_deleted = false LIMIT 1", b.ClassID, b.SubjectID).Scan(&teacherB)

					if teacherA > 0 && teacherB > 0 && teacherA == teacherB {
						var tName string
						_ = tx.QueryRow("SELECT first_name || ' ' || last_name FROM users WHERE id = $1", teacherA).Scan(&tName)
						c.JSON(http.StatusConflict, gin.H{
							"error": fmt.Sprintf("DARS JADVALI ZIDDIYATI! O'qituvchi '%s' %s kuni %d-dars soatida bir vaqtning o'zida ham '%s', ham '%s' sinflariga dars o'tishi kiritilgan!",
								tName, dayNames[a.DayOfWeek], a.LessonNumber, a.ClassName, b.ClassName),
						})
						return
					}
				}
			}
		}
	}

	// 5. Database Existing Schedule Overlap Conflict Check
	for _, item := range processedList {
		var existingClassID int
		var existingClassName, existingTeacherName string

		err := tx.QueryRow(`
			SELECT cs.class_id, c.name, COALESCE(u.first_name || ' ' || u.last_name, '')
			FROM class_schedules cs
			JOIN classes c ON cs.class_id = c.id
			LEFT JOIN class_teachers ct ON cs.class_id = ct.class_id AND cs.subject_id = ct.subject_id AND ct.is_deleted = false
			LEFT JOIN users u ON ct.teacher_id = u.id
			WHERE cs.class_id != $1 AND cs.day_of_week = $2 AND cs.lesson_number = $3 AND cs.is_deleted = false
			  AND ($4::date <= cs.end_date AND $5::date >= cs.start_date)
			  AND ct.teacher_id IN (
				  SELECT teacher_id FROM class_teachers WHERE class_id = $1 AND subject_id = $6 AND is_deleted = false
			  )
			LIMIT 1
		`, item.ClassID, item.DayOfWeek, item.LessonNumber, item.StartDate, item.EndDate, item.SubjectID).Scan(&existingClassID, &existingClassName, &existingTeacherName)

		if err == nil && existingClassID > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"error": fmt.Sprintf("BAZADAGI JADVAL BILAN ZIDDIYAT! O'qituvchi '%s' %s kuni %d-dars soatida allaqachon '%s' sinfiga darsga biriktirilgan!",
					existingTeacherName, dayNames[item.DayOfWeek], item.LessonNumber, existingClassName),
			})
			return
		}
	}

	// 6. UPSERT into class_schedules
	insertedCount := 0
	for _, item := range processedList {
		_, err := tx.Exec(`
			INSERT INTO class_schedules (class_id, day_of_week, lesson_number, subject_id, start_date, end_date, is_deleted)
			VALUES ($1, $2, $3, $4, $5, $6, false)
			ON CONFLICT (class_id, day_of_week, lesson_number, start_date)
			DO UPDATE SET subject_id = EXCLUDED.subject_id, end_date = EXCLUDED.end_date, is_deleted = false, updated_at = NOW()
		`, item.ClassID, item.DayOfWeek, item.LessonNumber, item.SubjectID, item.StartDate, item.EndDate)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Dars jadvalini saqlashda xatolik (%s sinfi, %d-dars): %v", item.ClassName, item.LessonNumber, err),
			})
			return
		}
		insertedCount++
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Muvaffaqiyatli! %d ta dars jadvali yoppasiga saqlandi va yangilandi.", insertedCount),
		"count":   insertedCount,
	})
}
