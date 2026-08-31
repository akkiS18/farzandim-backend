package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/farzandim/backend/internal/models"
	"github.com/farzandim/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type AIReportHandler struct{}

func NewAIReportHandler() *AIReportHandler {
	return &AIReportHandler{}
}

func ensureAITable(db *sql.DB) {
	if db == nil {
		return
	}
	// Try enabling pgcrypto extension for gen_random_uuid support
	_, _ = db.Exec(`CREATE EXTENSION IF NOT EXISTS "pgcrypto";`)

	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ai_weekly_reports (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			student_id INT NOT NULL REFERENCES students(id) ON DELETE CASCADE,
			year INT NOT NULL,
			week_number INT NOT NULL,
			start_date DATE NOT NULL,
			end_date DATE NOT NULL,
			report_text TEXT NOT NULL,
			summary_json JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			CONSTRAINT uk_ai_report_student_year_week UNIQUE (student_id, year, week_number)
		);
	`)
	if err != nil {
		log.Printf("[ensureAITable Warning] Primary attempt failed: %v. Retrying with TEXT ID fallback...", err)
		_, err2 := db.Exec(`
			CREATE TABLE IF NOT EXISTS ai_weekly_reports (
				id TEXT PRIMARY KEY DEFAULT md5(random()::text || clock_timestamp()::text),
				student_id INT NOT NULL REFERENCES students(id) ON DELETE CASCADE,
				year INT NOT NULL,
				week_number INT NOT NULL,
				start_date DATE NOT NULL,
				end_date DATE NOT NULL,
				report_text TEXT NOT NULL,
				summary_json JSONB DEFAULT '{}'::jsonb,
				created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
				updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
				CONSTRAINT uk_ai_report_student_year_week UNIQUE (student_id, year, week_number)
			);
		`)
		if err2 != nil {
			log.Printf("[ensureAITable Critical Error] Fallback attempt also failed: %v", err2)
		}
	}

	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_weekly_reports_student ON ai_weekly_reports(student_id);`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_weekly_reports_year_week ON ai_weekly_reports(year, week_number);`)

	// Ensure ai_generation_jobs table for asynchronous batch processing
	_, errJob := db.Exec(`
		CREATE TABLE IF NOT EXISTS ai_generation_jobs (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			target_date DATE NOT NULL,
			class_id INT,
			status TEXT NOT NULL DEFAULT 'STARTED',
			total_students INT NOT NULL DEFAULT 0,
			processed_students INT NOT NULL DEFAULT 0,
			generated_count INT NOT NULL DEFAULT 0,
			error_count INT NOT NULL DEFAULT 0,
			current_student_name TEXT,
			error_message TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			finished_at TIMESTAMP WITH TIME ZONE
		);
	`)
	if errJob != nil {
		_, _ = db.Exec(`
			CREATE TABLE IF NOT EXISTS ai_generation_jobs (
				id TEXT PRIMARY KEY DEFAULT md5(random()::text || clock_timestamp()::text),
				target_date DATE NOT NULL,
				class_id INT,
				status TEXT NOT NULL DEFAULT 'STARTED',
				total_students INT NOT NULL DEFAULT 0,
				processed_students INT NOT NULL DEFAULT 0,
				generated_count INT NOT NULL DEFAULT 0,
				error_count INT NOT NULL DEFAULT 0,
				current_student_name TEXT,
				error_message TEXT,
				created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
				updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
				finished_at TIMESTAMP WITH TIME ZONE
			);
		`)
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_generation_jobs_status ON ai_generation_jobs(status, created_at DESC);`)
}

func getTenantDB(c *gin.Context) (*sql.DB, error) {
	tenantDBVal, exists := c.Get("tenantDB")
	if !exists || tenantDBVal == nil {
		return nil, fmt.Errorf("tenant database ulanishi topilmadi")
	}

	tenantDB, ok := tenantDBVal.(*sql.DB)
	if !ok || tenantDB == nil {
		return nil, fmt.Errorf("invalid tenant database instance")
	}

	ensureAITable(tenantDB)
	return tenantDB, nil
}

// GetStudentAIReports returns all weekly AI reports for a given student
func (h *AIReportHandler) GetStudentAIReports(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	studentIDStr := c.Query("student_id")
	if studentIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "student_id parametri talab qilinadi"})
		return
	}

	studentID, err := strconv.Atoi(studentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri student_id"})
		return
	}

	rows, err := dbConn.Query(`
		SELECT id, student_id, year, week_number, start_date, end_date, report_text, summary_json, created_at, updated_at
		FROM ai_weekly_reports
		WHERE student_id = $1
		ORDER BY year DESC, week_number DESC`, studentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI hisobotlarini olishda xatolik: " + err.Error()})
		return
	}
	defer rows.Close()

	var reports []models.AIWeeklyReport
	for rows.Next() {
		var r models.AIWeeklyReport
		var startStr, endStr string
		var summaryBytes []byte
		if err := rows.Scan(&r.ID, &r.StudentID, &r.Year, &r.WeekNumber, &startStr, &endStr, &r.ReportText, &summaryBytes, &r.CreatedAt, &r.UpdatedAt); err == nil {
			r.StartDate = startStr
			r.EndDate = endStr
			if len(summaryBytes) > 0 {
				json.Unmarshal(summaryBytes, &r.SummaryJSON)
			}
			reports = append(reports, r)
		}
	}

	c.JSON(http.StatusOK, gin.H{"reports": reports})
}

// GetLatestAIReport returns the single latest weekly report for a student
func (h *AIReportHandler) GetLatestAIReport(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	studentIDStr := c.Query("student_id")
	if studentIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "student_id parametri talab qilinadi"})
		return
	}

	studentID, err := strconv.Atoi(studentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri student_id"})
		return
	}

	var r models.AIWeeklyReport
	var startStr, endStr string
	var summaryBytes []byte
	err = dbConn.QueryRow(`
		SELECT id, student_id, year, week_number, start_date, end_date, report_text, summary_json, created_at, updated_at
		FROM ai_weekly_reports
		WHERE student_id = $1
		ORDER BY year DESC, week_number DESC LIMIT 1`, studentID).
		Scan(&r.ID, &r.StudentID, &r.Year, &r.WeekNumber, &startStr, &endStr, &r.ReportText, &summaryBytes, &r.CreatedAt, &r.UpdatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusOK, gin.H{"report": nil})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "So'nggi AI hisobotini olishda xatolik"})
		return
	}

	r.StartDate = startStr
	r.EndDate = endStr
	if len(summaryBytes) > 0 {
		json.Unmarshal(summaryBytes, &r.SummaryJSON)
	}

	c.JSON(http.StatusOK, gin.H{"report": r})
}

type AdminBatchGenerateRequest struct {
	StudentIDs []int  `json:"student_ids"`
	ClassID    *int   `json:"class_id"`
	TargetDate string `json:"target_date"`
}

func runBatchGenerationJob(dbConn *sql.DB, jobID string, targetStudentIDs []int, targetTime time.Time) {
	// Mark status as IN_PROGRESS
	_, _ = dbConn.Exec(`UPDATE ai_generation_jobs SET status = 'IN_PROGRESS', updated_at = NOW() WHERE id = $1`, jobID)

	generatedCount := 0
	errorCount := 0
	var lastError string

	for idx, sID := range targetStudentIDs {
		// Check if job was cancelled
		var currentStatus string
		err := dbConn.QueryRow(`SELECT status FROM ai_generation_jobs WHERE id = $1`, jobID).Scan(&currentStatus)
		if err == nil && currentStatus == "CANCELLED" {
			log.Printf("[AI Batch Job] Job %s bekor qilindi (CANCELLED). Fon jarayoni to'xtatildi.", jobID)
			return
		}

		// Fetch student name for live UI progress
		var studentName string
		_ = dbConn.QueryRow(`
			SELECT u.first_name || ' ' || u.last_name 
			FROM students s 
			JOIN users u ON s.user_id = u.id 
			WHERE s.id = $1`, sID).Scan(&studentName)
		if studentName == "" {
			studentName = fmt.Sprintf("O'quvchi #%d", sID)
		}

		// Update currently processing student in DB
		_, _ = dbConn.Exec(`
			UPDATE ai_generation_jobs 
			SET current_student_name = $1, processed_students = $2, generated_count = $3, error_count = $4, updated_at = NOW() 
			WHERE id = $5`, studentName, idx, generatedCount, errorCount, jobID)

		// Throttling / Rate Limit safety delay (350ms)
		time.Sleep(350 * time.Millisecond)

		_, err = generateReportForStudent(dbConn, sID, targetTime)
		if err != nil {
			log.Printf("[AI Batch Error] Job %s Student %d error: %v", jobID, sID, err)
			lastError = err.Error()
			errorCount++
		} else {
			generatedCount++
		}

		// Update counters after student processing
		_, _ = dbConn.Exec(`
			UPDATE ai_generation_jobs 
			SET processed_students = $1, generated_count = $2, error_count = $3, updated_at = NOW() 
			WHERE id = $4`, idx+1, generatedCount, errorCount, jobID)
	}

	finalStatus := "FINISHED"
	var errMsg *string
	if generatedCount == 0 && errorCount > 0 {
		finalStatus = "FAILED"
		errMsg = &lastError
	}

	_, _ = dbConn.Exec(`
		UPDATE ai_generation_jobs 
		SET status = $1, processed_students = $2, generated_count = $3, error_count = $4, current_student_name = NULL, error_message = $5, finished_at = NOW(), updated_at = NOW() 
		WHERE id = $6`, finalStatus, len(targetStudentIDs), generatedCount, errorCount, errMsg, jobID)
}

// AdminBatchGenerateAIReports asynchronously starts AI reports generation in the background
func (h *AIReportHandler) AdminBatchGenerateAIReports(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 1. Check if there's already an active job in progress (created/updated within last 15 minutes)
	var activeJobID, activeStatus string
	var activeTotal, activeProcessed int
	activeErr := dbConn.QueryRow(`
		SELECT id, status, total_students, processed_students 
		FROM ai_generation_jobs 
		WHERE status IN ('STARTED', 'IN_PROGRESS') AND updated_at >= NOW() - INTERVAL '15 minutes'
		ORDER BY created_at DESC LIMIT 1
	`).Scan(&activeJobID, &activeStatus, &activeTotal, &activeProcessed)

	if activeErr == nil && activeJobID != "" {
		c.JSON(http.StatusOK, gin.H{
			"message":            "AI hisobot generatsiyasi allaqachon fonda ishlamoqda",
			"job_id":             activeJobID,
			"status":             activeStatus,
			"total_students":     activeTotal,
			"processed_students": activeProcessed,
			"is_existing":        true,
		})
		return
	}

	var req AdminBatchGenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri so'rov formati: " + err.Error()})
		return
	}

	targetStudentIDs := req.StudentIDs

	targetTime := time.Now()
	targetDateStr := targetTime.Format("2006-01-02")
	if req.TargetDate != "" {
		if parsedTime, err := time.Parse("2006-01-02", req.TargetDate); err == nil {
			targetTime = parsedTime
			targetDateStr = req.TargetDate
		}
	}

	// Determine Sunday of the target week to evaluate student enrollment eligibility
	wd := targetTime.Weekday()
	if wd == time.Sunday {
		wd = 7
	}
	monday := targetTime.AddDate(0, 0, -int(wd-time.Monday))
	sunday := monday.AddDate(0, 0, 6)
	sundayStr := sunday.Format("2006-01-02")

	// If class_id is provided and student_ids is empty, fetch all students in that class who were enrolled during/before report week
	if len(targetStudentIDs) == 0 && req.ClassID != nil && *req.ClassID > 0 {
		rows, err := dbConn.Query(`
			SELECT id FROM students 
			WHERE class_id = $1 AND is_deleted = false 
			  AND (enrollment_date IS NULL OR enrollment_date <= $2::date)`, *req.ClassID, sundayStr)
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

	// If still empty, fetch ALL active students in the school who were enrolled during/before report week
	if len(targetStudentIDs) == 0 {
		rows, err := dbConn.Query(`
			SELECT id FROM students 
			WHERE is_deleted = false 
			  AND (enrollment_date IS NULL OR enrollment_date <= $1::date)`, sundayStr)
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

	if len(targetStudentIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Generatsiya qilish uchun birorta o'quvchi topilmadi"})
		return
	}

	// Insert Job Record
	var jobID string
	var createdAt, updatedAt time.Time
	err = dbConn.QueryRow(`
		INSERT INTO ai_generation_jobs (target_date, class_id, status, total_students, processed_students, generated_count, error_count)
		VALUES ($1, $2, 'STARTED', $3, 0, 0, 0)
		RETURNING id, created_at, updated_at`,
		targetDateStr, req.ClassID, len(targetStudentIDs)).Scan(&jobID, &createdAt, &updatedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Job yaratishda xatolik: " + err.Error()})
		return
	}

	// Launch background goroutine
	go runBatchGenerationJob(dbConn, jobID, targetStudentIDs, targetTime)

	c.JSON(http.StatusOK, gin.H{
		"message":            "AI hisobot generatsiyasi boshlandi (fonda ishlamoqda)",
		"job_id":             jobID,
		"status":             "STARTED",
		"total_students":     len(targetStudentIDs),
		"processed_students": 0,
		"is_existing":        false,
	})
}

// GetActiveGenerationJob returns the active job if currently running or completed very recently
func (h *AIReportHandler) GetActiveGenerationJob(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Clean up stale orphaned jobs stuck in STARTED or IN_PROGRESS for > 15 minutes
	_, _ = dbConn.Exec(`
		UPDATE ai_generation_jobs 
		SET status = 'FAILED', error_message = 'Vaqt limiti oshib ketdi yoki server qayta yuklandi', updated_at = NOW(), finished_at = NOW() 
		WHERE status IN ('STARTED', 'IN_PROGRESS') AND updated_at < NOW() - INTERVAL '15 minutes'
	`)

	var job models.AIGenerationJob
	var classIDNull sql.NullInt64
	var currentStudentNull sql.NullString
	var errorMsgNull sql.NullString
	var finishedAtNull sql.NullTime

	err = dbConn.QueryRow(`
		SELECT id, target_date::text, class_id, status, total_students, processed_students, generated_count, error_count, current_student_name, error_message, created_at, updated_at, finished_at
		FROM ai_generation_jobs
		WHERE status IN ('STARTED', 'IN_PROGRESS') 
		   OR (status IN ('FINISHED', 'FAILED', 'CANCELLED') AND finished_at >= NOW() - INTERVAL '20 seconds')
		ORDER BY created_at DESC LIMIT 1
	`).Scan(
		&job.ID, &job.TargetDate, &classIDNull, &job.Status, &job.TotalStudents,
		&job.ProcessedStudents, &job.GeneratedCount, &job.ErrorCount,
		&currentStudentNull, &errorMsgNull, &job.CreatedAt, &job.UpdatedAt, &finishedAtNull,
	)

	if err != nil {
		c.JSON(http.StatusOK, gin.H{"has_active_job": false, "job": nil})
		return
	}

	if classIDNull.Valid {
		val := int(classIDNull.Int64)
		job.ClassID = &val
	}
	if currentStudentNull.Valid {
		job.CurrentStudentName = &currentStudentNull.String
	}
	if errorMsgNull.Valid {
		job.ErrorMessage = &errorMsgNull.String
	}
	if finishedAtNull.Valid {
		job.FinishedAt = &finishedAtNull.Time
	}

	c.JSON(http.StatusOK, gin.H{"has_active_job": true, "job": job})
}

// GetGenerationJobStatus returns full status of a specific job
func (h *AIReportHandler) GetGenerationJobStatus(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	jobID := c.Param("id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id majburiy"})
		return
	}

	var job models.AIGenerationJob
	var classIDNull sql.NullInt64
	var currentStudentNull sql.NullString
	var errorMsgNull sql.NullString
	var finishedAtNull sql.NullTime

	err = dbConn.QueryRow(`
		SELECT id, target_date::text, class_id, status, total_students, processed_students, generated_count, error_count, current_student_name, error_message, created_at, updated_at, finished_at
		FROM ai_generation_jobs
		WHERE id = $1`, jobID).Scan(
		&job.ID, &job.TargetDate, &classIDNull, &job.Status, &job.TotalStudents,
		&job.ProcessedStudents, &job.GeneratedCount, &job.ErrorCount,
		&currentStudentNull, &errorMsgNull, &job.CreatedAt, &job.UpdatedAt, &finishedAtNull,
	)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job topilmadi"})
		return
	}

	if classIDNull.Valid {
		val := int(classIDNull.Int64)
		job.ClassID = &val
	}
	if currentStudentNull.Valid {
		job.CurrentStudentName = &currentStudentNull.String
	}
	if errorMsgNull.Valid {
		job.ErrorMessage = &errorMsgNull.String
	}
	if finishedAtNull.Valid {
		job.FinishedAt = &finishedAtNull.Time
	}

	c.JSON(http.StatusOK, gin.H{"job": job})
}

// CancelGenerationJob stops an in-progress generation job
func (h *AIReportHandler) CancelGenerationJob(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	jobID := c.Param("id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id majburiy"})
		return
	}

	res, err := dbConn.Exec(`
		UPDATE ai_generation_jobs 
		SET status = 'CANCELLED', finished_at = NOW(), updated_at = NOW() 
		WHERE id = $1 AND status IN ('STARTED', 'IN_PROGRESS')`, jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Jobni bekor qilishda xatolik: " + err.Error()})
		return
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bekor qilish uchun faol jarayon topilmadi"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Jarayon muvaffaqiyatli to'xtatildi"})
}

// GetGroupedAIReports returns list of weeks with count of generated reports
func (h *AIReportHandler) GetGroupedAIReports(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows, err := dbConn.Query(`
		SELECT year, week_number, MIN(start_date)::text, MAX(end_date)::text, COUNT(id) as report_count
		FROM ai_weekly_reports
		GROUP BY year, week_number
		ORDER BY year DESC, week_number DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Guruhlangan hisobotlarni olishda xatolik: " + err.Error()})
		return
	}
	defer rows.Close()

	type GroupedReportItem struct {
		Year        int    `json:"year"`
		WeekNumber  int    `json:"week_number"`
		StartDate   string `json:"start_date"`
		EndDate     string `json:"end_date"`
		ReportCount int    `json:"report_count"`
	}

	list := make([]GroupedReportItem, 0)
	for rows.Next() {
		var item GroupedReportItem
		if err := rows.Scan(&item.Year, &item.WeekNumber, &item.StartDate, &item.EndDate, &item.ReportCount); err != nil {
			log.Printf("[GetGroupedAIReports Scan Error]: %v", err)
			continue
		}
		list = append(list, item)
	}

	c.JSON(http.StatusOK, gin.H{"groups": list})
}

// GetAIReportsByWeek returns paginated AI reports for a specific week with search and class filters
func (h *AIReportHandler) GetAIReportsByWeek(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	yearStr := c.Query("year")
	weekStr := c.Query("week_number")
	classIDStr := c.Query("class_id")
	search := c.Query("search")
	pageStr := c.Query("page")
	limitStr := c.Query("limit")

	year, _ := strconv.Atoi(yearStr)
	weekNum, _ := strconv.Atoi(weekStr)
	page, _ := strconv.Atoi(pageStr)
	limit, _ := strconv.Atoi(limitStr)

	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 10
	}
	offset := (page - 1) * limit

	query := `
		SELECT r.id, r.student_id, r.year, r.week_number, r.start_date::text, r.end_date::text, r.report_text, r.summary_json, r.created_at,
		       u.first_name || ' ' || u.last_name as student_name, COALESCE(c.name, 'Sinf belgilanmagan') as class_name
		FROM ai_weekly_reports r
		JOIN students s ON r.student_id = s.id
		JOIN users u ON s.user_id = u.id
		LEFT JOIN classes c ON s.class_id = c.id
		WHERE r.year = $1 AND r.week_number = $2`

	args := []interface{}{year, weekNum}
	argIndex := 3

	if classIDStr != "" {
		cid, err := strconv.Atoi(classIDStr)
		if err == nil && cid > 0 {
			query += fmt.Sprintf(" AND s.class_id = $%d", argIndex)
			args = append(args, cid)
			argIndex++
		}
	}

	if search != "" {
		query += fmt.Sprintf(" AND (u.first_name ILIKE $%d OR u.last_name ILIKE $%d OR c.name ILIKE $%d)", argIndex, argIndex, argIndex)
		args = append(args, "%"+search+"%")
		argIndex++
	}

	// Count Total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s) as total_tbl", query)
	var totalCount int
	_ = dbConn.QueryRow(countQuery, args...).Scan(&totalCount)

	query += fmt.Sprintf(" ORDER BY u.first_name ASC LIMIT $%d OFFSET $%d", argIndex, argIndex+1)
	args = append(args, limit, offset)

	rows, err := dbConn.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Hisobotlarni olishda xatolik: " + err.Error()})
		return
	}
	defer rows.Close()

	type AdminStudentReportItem struct {
		models.AIWeeklyReport
		StudentName string `json:"student_name"`
		ClassName   string `json:"class_name"`
	}

	reports := make([]AdminStudentReportItem, 0)
	for rows.Next() {
		var item AdminStudentReportItem
		var summaryBytes []byte

		err := rows.Scan(&item.ID, &item.StudentID, &item.Year, &item.WeekNumber, &item.StartDate, &item.EndDate, &item.ReportText, &summaryBytes, &item.CreatedAt, &item.StudentName, &item.ClassName)
		if err == nil {
			if len(summaryBytes) > 0 {
				json.Unmarshal(summaryBytes, &item.SummaryJSON)
			}
			reports = append(reports, item)
		} else {
			log.Printf("[GetAIReportsByWeek Scan Error]: %v", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"reports":      reports,
		"total_count":  totalCount,
		"page":         page,
		"limit":        limit,
		"total_pages":  (totalCount + limit - 1) / limit,
	})
}

// DeleteWeekAIReports deletes all AI reports for a specific week
func (h *AIReportHandler) DeleteWeekAIReports(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	yearStr := c.Query("year")
	weekStr := c.Query("week_number")

	year, _ := strconv.Atoi(yearStr)
	weekNum, _ := strconv.Atoi(weekStr)

	if year <= 0 || weekNum <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "year va week_number parametrlari talab qilinadi"})
		return
	}

	res, err := dbConn.Exec("DELETE FROM ai_weekly_reports WHERE year = $1 AND week_number = $2", year, weekNum)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Haftalik hisobotlarni o'chirishda xatolik: " + err.Error()})
		return
	}

	rowsAffected, _ := res.RowsAffected()
	c.JSON(http.StatusOK, gin.H{
		"message":       fmt.Sprintf("%d ta o'quvchi hisoboti muvaffaqiyatli o'chirildi", rowsAffected),
		"deleted_count": rowsAffected,
	})
}

// DeleteSingleAIReport deletes a single student's AI report by ID
func (h *AIReportHandler) DeleteSingleAIReport(c *gin.Context) {
	dbConn, err := getTenantDB(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	idStr := c.Param("id")
	if idStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Report ID parametri talab qilinadi"})
		return
	}

	res, err := dbConn.Exec("DELETE FROM ai_weekly_reports WHERE id = $1", idStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Hisobotni o'chirishda xatolik: " + err.Error()})
		return
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "O'chirish uchun hisobot topilmadi"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Hisobot muvaffaqiyatli o'chirildi"})
}

func GenerateReportForStudentExported(dbConn *sql.DB, studentID int, targetTime time.Time) (*models.AIWeeklyReport, error) {
	return generateReportForStudent(dbConn, studentID, targetTime)
}

func generateReportForStudent(dbConn *sql.DB, studentID int, targetTime time.Time) (*models.AIWeeklyReport, error) {
	ensureAITable(dbConn)
	year, weekNum := targetTime.ISOWeek()

	wd := targetTime.Weekday()
	if wd == time.Sunday {
		wd = 7
	}
	monday := targetTime.AddDate(0, 0, -int(wd-time.Monday))
	sunday := monday.AddDate(0, 0, 6)

	startStr := monday.Format("2006-01-02")
	endStr := sunday.Format("2006-01-02")

	// 1. Check if report already exists in DB
	var existing models.AIWeeklyReport
	var summaryBytes []byte
	err := dbConn.QueryRow(`
		SELECT id, student_id, year, week_number, start_date::text, end_date::text, report_text, summary_json, created_at, updated_at
		FROM ai_weekly_reports
		WHERE student_id = $1 AND year = $2 AND week_number = $3`, studentID, year, weekNum).
		Scan(&existing.ID, &existing.StudentID, &existing.Year, &existing.WeekNumber, &existing.StartDate, &existing.EndDate, &existing.ReportText, &summaryBytes, &existing.CreatedAt, &existing.UpdatedAt)

	if err == nil {
		if len(summaryBytes) > 0 {
			json.Unmarshal(summaryBytes, &existing.SummaryJSON)
		}
		return &existing, nil
	}

	// 2. Fetch Student Details and Enrollment Date
	var studentName, className string
	var enrollmentDate sql.NullTime
	err = dbConn.QueryRow(`
		SELECT u.first_name || ' ' || u.last_name, COALESCE(c.name, 'Sinf belgilanmagan'), s.enrollment_date
		FROM students s
		JOIN users u ON s.user_id = u.id
		LEFT JOIN classes c ON s.class_id = c.id
		WHERE s.id = $1 AND s.is_deleted = false AND u.is_deleted = false`, studentID).Scan(&studentName, &className, &enrollmentDate)
	if err != nil {
		return nil, fmt.Errorf("o'quvchi topilmadi yoki o'chirilgan (student ID %d)", studentID)
	}

	if enrollmentDate.Valid {
		enrollDateOnly := time.Date(enrollmentDate.Time.Year(), enrollmentDate.Time.Month(), enrollmentDate.Time.Day(), 0, 0, 0, 0, time.UTC)
		sundayOnly := time.Date(sunday.Year(), sunday.Month(), sunday.Day(), 0, 0, 0, 0, time.UTC)
		if sundayOnly.Before(enrollDateOnly) {
			return nil, fmt.Errorf("o'quvchi ushbu hisobot haftasida (%s dan %s gacha) hali maktabga kirmagan (maktabga kirish sanasi: %s)", startStr, endStr, enrollmentDate.Time.Format("2006-01-02"))
		}
	}

	// 3. Fetch Grades for Current Week (Strict DATE boundary check + parse numeric/string values)
	var gradeStrings []string
	var totalGradeSum float64
	var totalGradeCount int

	gRows, err := dbConn.Query(`
		SELECT sub.name, COALESCE(g.value, ''), COALESCE(g.numeric_value, 0.0)
		FROM grades g
		JOIN subjects sub ON g.subject_id = sub.id
		WHERE g.student_id = $1 
		  AND DATE(g.grade_date) >= $2::date 
		  AND DATE(g.grade_date) <= $3::date 
		  AND g.is_deleted = false
		  AND (g.grade_type IS NULL OR g.grade_type != 'ATTENDANCE')
		ORDER BY g.grade_date ASC`, studentID, startStr, endStr)
	if err == nil {
		for gRows.Next() {
			var subName, rawVal string
			var numVal float64
			if err := gRows.Scan(&subName, &rawVal, &numVal); err == nil {
				if numVal == 0.0 && rawVal != "" {
					if parsed, errP := strconv.ParseFloat(rawVal, 64); errP == nil {
						numVal = parsed
					} else if rawVal == "5" || rawVal == "A" || rawVal == "A'" || rawVal == "+" {
						numVal = 5.0
					} else if rawVal == "4" || rawVal == "B" {
						numVal = 4.0
					} else if rawVal == "3" || rawVal == "C" {
						numVal = 3.0
					} else if rawVal == "2" || rawVal == "D" {
						numVal = 2.0
					}
				}

				displayVal := rawVal
				if displayVal == "" && numVal > 0 {
					displayVal = fmt.Sprintf("%.0f", numVal)
				}
				if displayVal != "" {
					gradeStrings = append(gradeStrings, fmt.Sprintf("%s: %s", subName, displayVal))
				}
				if numVal > 0 {
					totalGradeSum += numVal
					totalGradeCount++
				}
			}
		}
		gRows.Close()
	}

	currentAvg := 0.0
	if totalGradeCount > 0 {
		currentAvg = totalGradeSum / float64(totalGradeCount)
	}

	// 4. Fetch Teacher Comments for Current Week
	var comments []string
	cRows, err := dbConn.Query(`
		SELECT gc.content
		FROM grade_comments gc
		JOIN grades g ON gc.grade_id = g.id
		JOIN users u ON gc.author_id = u.id
		JOIN roles r ON u.role_id = r.id
		WHERE g.student_id = $1
		  AND DATE(gc.created_at) >= $2::date
		  AND DATE(gc.created_at) <= $3::date
		  AND r.name IN ('ADMIN', 'MAIN_TEACHER', 'SUBJECT_TEACHER')`, studentID, startStr, endStr)
	if err == nil {
		for cRows.Next() {
			var comm string
			if err := cRows.Scan(&comm); err == nil && comm != "" {
				comments = append(comments, comm)
			}
		}
		cRows.Close()
	}

	// 4.5 Fetch Attendance for Current Week
	var absentCount, lateCount int
	_ = dbConn.QueryRow(`
		SELECT 
			COUNT(CASE WHEN value IN ('NB', 'Q', '-', 'ABSENT') THEN 1 END),
			COUNT(CASE WHEN value IN ('K', 'LATE') THEN 1 END)
		FROM grades 
		WHERE student_id = $1 
		  AND grade_type = 'ATTENDANCE' 
		  AND DATE(grade_date) >= $2::date 
		  AND DATE(grade_date) <= $3::date 
		  AND is_deleted = false
	`, studentID, startStr, endStr).Scan(&absentCount, &lateCount)

	attendanceInfo := "Darslarga to'liq va faol qatnashdi"
	if absentCount > 0 && lateCount > 0 {
		attendanceInfo = fmt.Sprintf("Hafta davomida %d ta dars qoldirildi va %d ta darsga kechikildi", absentCount, lateCount)
	} else if absentCount > 0 {
		attendanceInfo = fmt.Sprintf("Hafta davomida %d ta dars qoldirildi", absentCount)
	} else if lateCount > 0 {
		attendanceInfo = fmt.Sprintf("Hafta davomida %d ta darsga kechikildi", lateCount)
	}

	// 5. Fetch Books Read / Reading Assignments
	var booksRead []string
	bRows, err := dbConn.Query(`
		SELECT b.title
		FROM student_reading_assignments sra
		JOIN books b ON sra.book_id = b.id
		WHERE sra.student_id = $1 AND sra.status = 'COMPLETED'`, studentID)
	if err == nil {
		for bRows.Next() {
			var title string
			if err := bRows.Scan(&title); err == nil {
				booksRead = append(booksRead, title)
			}
		}
		bRows.Close()
	}

	// 6. Fetch Previous Week's Report for Comparison
	prevWeekNum := weekNum - 1
	prevYear := year
	if prevWeekNum <= 0 {
		prevWeekNum = 52
		prevYear = year - 1
	}

	var prevReportText string
	var prevAvgGrade float64 = 0.0
	var prevSummaryBytes []byte
	_ = dbConn.QueryRow(`
		SELECT report_text, summary_json
		FROM ai_weekly_reports
		WHERE student_id = $1 AND year = $2 AND week_number = $3`, studentID, prevYear, prevWeekNum).
		Scan(&prevReportText, &prevSummaryBytes)

	if len(prevSummaryBytes) > 0 {
		var prevSummary map[string]interface{}
		if json.Unmarshal(prevSummaryBytes, &prevSummary) == nil {
			if avg, ok := prevSummary["average_grade"].(float64); ok {
				prevAvgGrade = avg
			}
		}
	}

	// 7. Call Gemini AI Service
	promptContext := services.StudentWeeklyDataContext{
		StudentName:         studentName,
		ClassName:           className,
		WeekStartDate:       startStr,
		WeekEndDate:         endStr,
		Grades:              gradeStrings,
		TeacherComments:     comments,
		BooksRead:           booksRead,
		AttendanceInfo:      attendanceInfo,
		PreviousReportText:  prevReportText,
		PrevAverageGrade:    prevAvgGrade,
		CurrentAverageGrade: currentAvg,
	}

	reportMarkdown, genErr := services.GenerateAIWeeklyReportWithDB(dbConn, promptContext)
	if genErr != nil {
		return nil, genErr
	}

	trend := "STABLE"
	if prevAvgGrade > 0 {
		if currentAvg > prevAvgGrade {
			trend = "UP"
		} else if currentAvg < prevAvgGrade {
			trend = "DOWN"
		}
	}

	summaryData := map[string]interface{}{
		"average_grade":      currentAvg,
		"prev_average_grade": prevAvgGrade,
		"grade_trend":        trend,
		"total_grades":       totalGradeCount,
		"books_read_count":   len(booksRead),
	}
	summaryJSONBytes, _ := json.Marshal(summaryData)

	// 8. Insert generated report into Tenant DB
	var newReport models.AIWeeklyReport
	var newID string
	var createdAt, updatedAt time.Time
	err = dbConn.QueryRow(`
		INSERT INTO ai_weekly_reports (student_id, year, week_number, start_date, end_date, report_text, summary_json)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at`,
		studentID, year, weekNum, startStr, endStr, reportMarkdown, summaryJSONBytes).
		Scan(&newID, &createdAt, &updatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to save generated report: %v", err)
	}

	newReport = models.AIWeeklyReport{
		ID:          newID,
		StudentID:   studentID,
		Year:        year,
		WeekNumber:  weekNum,
		StartDate:   startStr,
		EndDate:     endStr,
		ReportText:  reportMarkdown,
		SummaryJSON: summaryData,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}

	return &newReport, nil
}
