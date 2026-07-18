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
	"github.com/xuri/excelize/v2"
)

type MenuHandler struct{}

func NewMenuHandler() *MenuHandler {
	return &MenuHandler{}
}

type SaveMenuCycleRequest struct {
	IntervalID int             `json:"interval_id" binding:"required"`
	WeekNumber int             `json:"week_number" binding:"required,min=1"`
	DayOfWeek  int             `json:"day_of_week" binding:"required,min=1,max=6"`
	Meals      json.RawMessage `json:"meals" binding:"required"`
}

type SaveMenuIntervalRequest struct {
	ID         *int   `json:"id"`
	Name       string `json:"name" binding:"required"`
	StartDate  string `json:"start_date" binding:"required"` // YYYY-MM-DD
	EndDate    string `json:"end_date" binding:"required"`   // YYYY-MM-DD
	CycleWeeks int    `json:"cycle_weeks" binding:"required,min=1"`
}

type SaveMenuExceptionRequest struct {
	MenuDate string           `json:"menu_date" binding:"required"` // Format: YYYY-MM-DD
	Meals    *json.RawMessage `json:"meals"`                        // Nullable to represent cancellations
}

// getCycleWeek calculates which cycle week a given date belongs to based on a cycle length and an interval start date reference.
func getCycleWeek(queryDate time.Time, startDate time.Time, cycleLength int) int {
	if cycleLength <= 1 {
		return 1
	}

	// Align startDate to the preceding Monday
	daysToMonday := int(startDate.Weekday()) - 1
	if daysToMonday < 0 {
		daysToMonday = 6 // Sunday
	}
	refMonday := startDate.AddDate(0, 0, -daysToMonday)

	// Calculate weeks elapsed
	duration := queryDate.Sub(refMonday)
	weeksElapsed := int(duration.Hours() / (24 * 7))

	if weeksElapsed < 0 {
		weeksElapsed = (weeksElapsed % cycleLength) + cycleLength
	}

	cycleWeek := (weeksElapsed % cycleLength) + 1
	return cycleWeek
}

// GetMenu fetches the meal plan for a specific date
func (h *MenuHandler) GetMenu(c *gin.Context) {
	dateParam := c.Query("date")
	if dateParam == "" {
		dateParam = time.Now().Format("2006-01-02")
	}

	parsedDate, err := time.Parse("2006-01-02", dateParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format. Use YYYY-MM-DD"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	// 1. Check exceptions first
	var excMeals sql.NullString
	var excID int
	err = dbConn.QueryRow("SELECT id, meals FROM menu_exceptions WHERE menu_date = $1", parsedDate).Scan(&excID, &excMeals)
	if err == nil {
		// Exception exists
		var mealsJSON interface{}
		if excMeals.Valid {
			json.Unmarshal([]byte(excMeals.String), &mealsJSON)
		}
		c.JSON(http.StatusOK, gin.H{
			"date":         dateParam,
			"source":       "exception",
			"meals":        mealsJSON,
			"exception_id": excID,
		})
		return
	}

	// 2. Fetch templates if no exception exists
	var intervalID, cycleWeeks int
	var startDateStr, endDateStr string
	err = dbConn.QueryRow(
		"SELECT id, start_date, end_date, cycle_weeks FROM menu_intervals WHERE $1 BETWEEN start_date AND end_date LIMIT 1",
		parsedDate,
	).Scan(&intervalID, &startDateStr, &endDateStr, &cycleWeeks)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusOK, gin.H{
				"date":   dateParam,
				"source": "empty",
				"meals":  nil,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to look up menu interval", "details": err.Error()})
		}
		return
	}

	startDate, _ := time.Parse(time.RFC3339, startDateStr)
	if startDate.IsZero() {
		// Fallback parse if it returned as simple date string YYYY-MM-DD
		startDate, _ = time.Parse("2006-01-02", strings.Split(startDateStr, "T")[0])
	}

	cycleWeek := getCycleWeek(parsedDate, startDate, cycleWeeks)
	dayOfWeek := int(parsedDate.Weekday())
	if dayOfWeek == 0 {
		dayOfWeek = 7 // Map Sunday to 7
	}

	// Sunday meals are typically empty, check constraints (meals only Mon-Sat in template)
	if dayOfWeek == 7 {
		c.JSON(http.StatusOK, gin.H{
			"date":   dateParam,
			"source": "weekend",
			"meals":  nil,
		})
		return
	}

	var templateMeals string
	var templateID int
	err = dbConn.QueryRow(
		"SELECT id, meals FROM menu_cycles WHERE interval_id = $1 AND week_number = $2 AND day_of_week = $3",
		intervalID, cycleWeek, dayOfWeek,
	).Scan(&templateID, &templateMeals)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusOK, gin.H{
				"date":       dateParam,
				"source":     "template_not_found",
				"meals":      nil,
				"cycle_week": cycleWeek,
				"weekday":    dayOfWeek,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch menu template", "details": err.Error()})
		}
		return
	}

	var mealsJSON interface{}
	json.Unmarshal([]byte(templateMeals), &mealsJSON)

	c.JSON(http.StatusOK, gin.H{
		"date":        dateParam,
		"source":      "template",
		"meals":       mealsJSON,
		"template_id": templateID,
		"cycle_week":  cycleWeek,
		"weekday":     dayOfWeek,
	})
}

// SaveMenuCycle updates/inserts a cycle template row
func (h *MenuHandler) SaveMenuCycle(c *gin.Context) {
	var req SaveMenuCycleRequest
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

	// Perform Upsert
	var cycleID int
	upsertQuery := `
		INSERT INTO menu_cycles (interval_id, week_number, day_of_week, meals)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (interval_id, week_number, day_of_week) DO UPDATE SET meals = EXCLUDED.meals, updated_at = NOW()
		RETURNING id`

	err = tx.QueryRow(upsertQuery, req.IntervalID, req.WeekNumber, req.DayOfWeek, string(req.Meals)).Scan(&cycleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save cycle template", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "SAVE_CYCLE_TEMPLATE",
		TableName: "menu_cycles",
		RecordID:  fmt.Sprintf("%d-%d-%d", req.IntervalID, req.WeekNumber, req.DayOfWeek),
		NewValues: req,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit template save"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Aylanma taomnoma shabloni saqlandi", "id": cycleID})
}

// SaveMenuException updates/inserts a daily menu exception row
func (h *MenuHandler) SaveMenuException(c *gin.Context) {
	var req SaveMenuExceptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
		return
	}

	menuDate, err := time.Parse("2006-01-02", req.MenuDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "menu_date must be in YYYY-MM-DD format"})
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

	// Parse meals
	var mealsVal interface{}
	if req.Meals != nil {
		mealsVal = string(*req.Meals)
	}

	var exceptionID int
	upsertQuery := `
		INSERT INTO menu_exceptions (menu_date, meals)
		VALUES ($1, $2)
		ON CONFLICT (menu_date) DO UPDATE SET meals = EXCLUDED.meals, updated_at = NOW()
		RETURNING id`

	err = tx.QueryRow(upsertQuery, menuDate, mealsVal).Scan(&exceptionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save menu exception", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "SAVE_MENU_EXCEPTION",
		TableName: "menu_exceptions",
		RecordID:  req.MenuDate,
		NewValues: req,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit exception save"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Kunlik istisno taomnoma muvaffaqiyatli saqlandi", "id": exceptionID})
}

// extractDigits removes all non-numeric characters from a string
func extractDigits(s string) string {
	var result strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// parseWeekdayStr parses weekday name or number from string
func parseWeekdayStr(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if val, err := strconv.Atoi(s); err == nil {
		if val >= 1 && val <= 6 {
			return val, nil
		}
	}

	digits := extractDigits(s)
	if digits != "" {
		if val, err := strconv.Atoi(digits); err == nil {
			if val >= 1 && val <= 6 {
				return val, nil
			}
		}
	}

	if strings.Contains(s, "dush") || strings.Contains(s, "mon") {
		return 1, nil
	}
	if strings.Contains(s, "sesh") || strings.Contains(s, "tue") {
		return 2, nil
	}
	if strings.Contains(s, "chor") || strings.Contains(s, "wed") {
		return 3, nil
	}
	if strings.Contains(s, "pay") || strings.Contains(s, "thu") {
		return 4, nil
	}
	if strings.Contains(s, "jum") || strings.Contains(s, "fri") {
		return 5, nil
	}
	if strings.Contains(s, "shan") || strings.Contains(s, "sat") || strings.Contains(s, "sha") {
		return 6, nil
	}

	return 0, fmt.Errorf("invalid weekday")
}

// ImportMenuCycles parses an Excel file and inserts/updates cycle templates
func (h *MenuHandler) ImportMenuCycles(c *gin.Context) {
	intervalIDStr := c.Query("interval_id")
	if intervalIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "interval_id query parametru talab etiladi"})
		return
	}
	intervalID, err := strconv.Atoi(intervalIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "interval_id noto'g'ri formatda"})
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Fayl yuklanmadi"})
		return
	}

	openedFile, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Faylni ochib bo'lmadi"})
		return
	}
	defer openedFile.Close()

	xlsx, err := excelize.OpenReader(openedFile)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel fayl formati noto'g'ri"})
		return
	}
	defer xlsx.Close()

	sheetName := xlsx.GetSheetName(0)
	rows, err := xlsx.GetRows(sheetName)
	if err != nil || len(rows) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel sahifasi bo'sh yoki o'qib bo'lmadi"})
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

	imported := 0
	var rowErrors []RowError

	for i, row := range rows {
		if i == 0 {
			continue // Skip header row
		}
		if len(row) < 2 {
			continue // Skip incomplete row
		}

		weekNumStr := strings.TrimSpace(row[0])
		dayOfWeekStr := strings.TrimSpace(row[1])
		if weekNumStr == "" && dayOfWeekStr == "" {
			continue // Skip empty rows
		}

		weekNumDigits := extractDigits(weekNumStr)
		weekNum, err := strconv.Atoi(weekNumDigits)
		if err != nil || weekNum < 1 {
			rowErrors = append(rowErrors, RowError{Row: i + 1, Error: "Hafta raqami noto'g'ri (musbat butun son bo'lishi kerak)"})
			continue
		}

		dayOfWeek, err := parseWeekdayStr(dayOfWeekStr)
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: i + 1, Error: "Hafta kuni noto'g'ri (dushanba-shanba yoki 1-6 soni bo'lishi kerak)"})
			continue
		}

		breakfast := ""
		if len(row) > 2 {
			breakfast = strings.TrimSpace(row[2])
		}
		lunch := ""
		if len(row) > 3 {
			lunch = strings.TrimSpace(row[3])
		}
		snack := ""
		if len(row) > 4 {
			snack = strings.TrimSpace(row[4])
		}

		mealsJSON, err := json.Marshal(map[string]string{
			"breakfast": breakfast,
			"lunch":     lunch,
			"snack":     snack,
		})
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: i + 1, Error: "JSON formatlashda xatolik"})
			continue
		}

		upsertQuery := `
			INSERT INTO menu_cycles (interval_id, week_number, day_of_week, meals)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (interval_id, week_number, day_of_week) DO UPDATE SET meals = EXCLUDED.meals, updated_at = NOW()
		`
		_, err = tx.Exec(upsertQuery, intervalID, weekNum, dayOfWeek, string(mealsJSON))
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: i + 1, Error: fmt.Sprintf("Ma'lumotlar bazasida xatolik: %v", err)})
			continue
		}

		imported++
	}

	if len(rowErrors) > 0 && imported == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":        false,
			"imported_count": 0,
			"failed_count":   len(rowErrors),
			"errors":         rowErrors,
		})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "IMPORT_CYCLE_TEMPLATES",
		TableName: "menu_cycles",
		RecordID:  "excel-import",
		NewValues: map[string]interface{}{"imported": imported, "errors": len(rowErrors)},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction commit failed"})
		return
	}

	c.JSON(http.StatusOK, ImportResult{
		Success:       true,
		ImportedCount: imported,
		FailedCount:   len(rowErrors),
		Errors:        rowErrors,
	})
}

// ImportMenuExceptions parses an Excel file and inserts/updates daily overrides
func (h *MenuHandler) ImportMenuExceptions(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Fayl yuklanmadi"})
		return
	}

	openedFile, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Faylni ochib bo'lmadi"})
		return
	}
	defer openedFile.Close()

	xlsx, err := excelize.OpenReader(openedFile)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel fayl formati noto'g'ri"})
		return
	}
	defer xlsx.Close()

	sheetName := xlsx.GetSheetName(0)
	rows, err := xlsx.GetRows(sheetName)
	if err != nil || len(rows) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel sahifasi bo'sh yoki o'qib bo'lmadi"})
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

	imported := 0
	var rowErrors []RowError

	for i, row := range rows {
		if i == 0 {
			continue // Skip header
		}
		if len(row) < 1 {
			continue
		}

		dateStr := strings.TrimSpace(row[0])
		if dateStr == "" {
			continue
		}

		parsedDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: i + 1, Error: "Sana formati noto'g'ri (YYYY-MM-DD kutilmoqda)"})
			continue
		}

		breakfast := ""
		if len(row) > 1 {
			breakfast = strings.TrimSpace(row[1])
		}
		lunch := ""
		if len(row) > 2 {
			lunch = strings.TrimSpace(row[2])
		}
		snack := ""
		if len(row) > 3 {
			snack = strings.TrimSpace(row[3])
		}

		var mealsJSONVal interface{}
		if breakfast == "" && lunch == "" && snack == "" {
			mealsJSONVal = nil
		} else {
			mBytes, _ := json.Marshal(map[string]string{
				"breakfast": breakfast,
				"lunch":     lunch,
				"snack":     snack,
			})
			mealsJSONVal = string(mBytes)
		}

		upsertQuery := `
			INSERT INTO menu_exceptions (menu_date, meals)
			VALUES ($1, $2)
			ON CONFLICT (menu_date) DO UPDATE SET meals = EXCLUDED.meals, updated_at = NOW()
		`
		_, err = tx.Exec(upsertQuery, parsedDate, mealsJSONVal)
		if err != nil {
			rowErrors = append(rowErrors, RowError{Row: i + 1, Error: fmt.Sprintf("Ma'lumotlar bazasida xatolik: %v", err)})
			continue
		}

		imported++
	}

	if len(rowErrors) > 0 && imported == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":        false,
			"imported_count": 0,
			"failed_count":   len(rowErrors),
			"errors":         rowErrors,
		})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "IMPORT_MENU_EXCEPTIONS",
		TableName: "menu_exceptions",
		RecordID:  "excel-import",
		NewValues: map[string]interface{}{"imported": imported, "errors": len(rowErrors)},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction commit failed"})
		return
	}

	c.JSON(http.StatusOK, ImportResult{
		Success:       true,
		ImportedCount: imported,
		FailedCount:   len(rowErrors),
		Errors:        rowErrors,
	})
}

// ExportMenuCycleTemplate generates a blank template for menu cycles
func (h *MenuHandler) ExportMenuCycleTemplate(c *gin.Context) {
	f := excelize.NewFile()
	defer f.Close()

	sheet := "CycleTemplate"
	index, _ := f.NewSheet(sheet)
	f.SetActiveSheet(index)
	f.DeleteSheet("Sheet1")

	// Set headers
	headers := []string{"Hafta raqami", "Hafta kuni (1=Dush, 6=Shan)", "Nonushta", "Tushlik", "Kechki/Peshinlik"}
	for colIdx, text := range headers {
		colName, _ := excelize.ColumnNumberToName(colIdx + 1)
		f.SetCellValue(sheet, colName+"1", text)
	}

	// Add some sample data
	f.SetCellValue(sheet, "A2", 1)
	f.SetCellValue(sheet, "B2", 1)
	f.SetCellValue(sheet, "C2", "Tuxum, non, choy 🍳")
	f.SetCellValue(sheet, "D2", "Mastava, osh, salat 🍲")
	f.SetCellValue(sheet, "E2", "Mevalar, pechenye 🍎")

	f.SetCellValue(sheet, "A3", 1)
	f.SetCellValue(sheet, "B3", 2)
	f.SetCellValue(sheet, "C3", "Bo'tqa, sut 🥛")
	f.SetCellValue(sheet, "D3", "Moxora, somsa, choy 🥟")
	f.SetCellValue(sheet, "E3", "Kefir, bulochka 🥐")

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=aylanma_taomnoma_shablon.xlsx")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Faylni yuklashda xatolik"})
	}
}

// ExportMenuExceptionTemplate generates a blank template for menu exceptions
func (h *MenuHandler) ExportMenuExceptionTemplate(c *gin.Context) {
	f := excelize.NewFile()
	defer f.Close()

	sheet := "ExceptionTemplate"
	index, _ := f.NewSheet(sheet)
	f.SetActiveSheet(index)
	f.DeleteSheet("Sheet1")

	// Set headers
	headers := []string{"Sana (YYYY-MM-DD)", "Nonushta", "Tushlik", "Kechki/Peshinlik"}
	for colIdx, text := range headers {
		colName, _ := excelize.ColumnNumberToName(colIdx + 1)
		f.SetCellValue(sheet, colName+"1", text)
	}

	// Add sample data
	f.SetCellValue(sheet, "A2", "2026-09-10")
	f.SetCellValue(sheet, "B2", "Sutli bo'tqa, pishloq 🧀")
	f.SetCellValue(sheet, "C2", "Borsh, palov, sharbat 🍹")
	f.SetCellValue(sheet, "D2", "Yogurt, mevalar 🍌")

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=maxsus_taomnoma_shablon.xlsx")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Faylni yuklashda xatolik"})
	}
}

// ListMenuIntervals returns all defined menu intervals
func (h *MenuHandler) ListMenuIntervals(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	rows, err := dbConn.Query("SELECT id, name, start_date, end_date, cycle_weeks, created_at, updated_at FROM menu_intervals ORDER BY start_date ASC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch menu intervals", "details": err.Error()})
		return
	}
	defer rows.Close()

	var intervals []models.MenuInterval
	for rows.Next() {
		var item models.MenuInterval
		var startStr, endStr string
		err := rows.Scan(&item.ID, &item.Name, &startStr, &endStr, &item.CycleWeeks, &item.CreatedAt, &item.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan menu intervals", "details": err.Error()})
			return
		}
		item.StartDate, _ = time.Parse(time.RFC3339, startStr)
		if item.StartDate.IsZero() {
			item.StartDate, _ = time.Parse("2006-01-02", strings.Split(startStr, "T")[0])
		}
		item.EndDate, _ = time.Parse(time.RFC3339, endStr)
		if item.EndDate.IsZero() {
			item.EndDate, _ = time.Parse("2006-01-02", strings.Split(endStr, "T")[0])
		}
		intervals = append(intervals, item)
	}

	c.JSON(http.StatusOK, intervals)
}

// SaveMenuInterval creates or updates a menu interval
func (h *MenuHandler) SaveMenuInterval(c *gin.Context) {
	var req SaveMenuIntervalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
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

	if endDate.Before(startDate) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Tugash sanasi boshlanish sanasidan oldin bo'la olmaydi"})
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

	// Check overlapping intervals
	overlapQuery := `
		SELECT COUNT(*) FROM menu_intervals 
		WHERE id != $1 AND (
			(start_date <= $2 AND end_date >= $2) OR
			(start_date <= $3 AND end_date >= $3) OR
			(start_date >= $2 AND end_date <= $3)
		)
	`
	var overlapCount int
	excludeID := 0
	if req.ID != nil {
		excludeID = *req.ID
	}
	err = tx.QueryRow(overlapQuery, excludeID, startDate, endDate).Scan(&overlapCount)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check overlapping intervals", "details": err.Error()})
		return
	}

	if overlapCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ushbu muddat boshqa faol taomnoma intervali bilan ustma-ust tushadi!"})
		return
	}

	var intervalID int
	if req.ID != nil {
		// Update
		updateQuery := `
			UPDATE menu_intervals 
			SET name = $1, start_date = $2, end_date = $3, cycle_weeks = $4, updated_at = NOW() 
			WHERE id = $5 
			RETURNING id
		`
		err = tx.QueryRow(updateQuery, req.Name, startDate, endDate, req.CycleWeeks, *req.ID).Scan(&intervalID)
	} else {
		// Insert
		insertQuery := `
			INSERT INTO menu_intervals (name, start_date, end_date, cycle_weeks) 
			VALUES ($1, $2, $3, $4) 
			RETURNING id
		`
		err = tx.QueryRow(insertQuery, req.Name, startDate, endDate, req.CycleWeeks).Scan(&intervalID)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save menu interval", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "SAVE_MENU_INTERVAL",
		TableName: "menu_intervals",
		RecordID:  fmt.Sprintf("%d", intervalID),
		NewValues: req,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit interval save"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Taomnoma intervali muvaffaqiyatli saqlandi", "id": intervalID})
}

// DeleteMenuInterval deletes a menu interval and cascade deletes cycle templates
func (h *MenuHandler) DeleteMenuInterval(c *gin.Context) {
	intervalIDStr := c.Param("id")
	intervalID, err := strconv.Atoi(intervalIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid interval id"})
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

	_, err = tx.Exec("DELETE FROM menu_intervals WHERE id = $1", intervalID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete menu interval", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE_MENU_INTERVAL",
		TableName: "menu_intervals",
		RecordID:  intervalIDStr,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit interval delete"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Taomnoma intervali o'chirildi"})
}

// ListMenuCycles returns all cycle templates for a specific interval
func (h *MenuHandler) ListMenuCycles(c *gin.Context) {
	intervalIDStr := c.Query("interval_id")
	if intervalIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "interval_id query parametru talab etiladi"})
		return
	}

	intervalID, err := strconv.Atoi(intervalIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "interval_id noto'g'ri formatda"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	rows, err := dbConn.Query("SELECT id, interval_id, week_number, day_of_week, meals FROM menu_cycles WHERE interval_id = $1 ORDER BY week_number ASC, day_of_week ASC", intervalID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch menu cycles", "details": err.Error()})
		return
	}
	defer rows.Close()

	var cycles []models.MenuCycle
	for rows.Next() {
		var item models.MenuCycle
		var mealsStr string
		err := rows.Scan(&item.ID, &item.IntervalID, &item.WeekNumber, &item.DayOfWeek, &mealsStr)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan menu cycles", "details": err.Error()})
			return
		}
		item.Meals = json.RawMessage(mealsStr)
		cycles = append(cycles, item)
	}

	c.JSON(http.StatusOK, cycles)
}

// ListMenuExceptions returns all menu exceptions/overrides
func (h *MenuHandler) ListMenuExceptions(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	rows, err := dbConn.Query("SELECT id, menu_date, meals, created_at, updated_at FROM menu_exceptions ORDER BY menu_date DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch menu exceptions", "details": err.Error()})
		return
	}
	defer rows.Close()

	type MenuExceptionResponse struct {
		ID        int             `json:"id"`
		MenuDate  string          `json:"menu_date"`
		Meals     json.RawMessage `json:"meals"`
		CreatedAt time.Time       `json:"created_at"`
		UpdatedAt time.Time       `json:"updated_at"`
	}

	var exceptions []MenuExceptionResponse
	for rows.Next() {
		var item MenuExceptionResponse
		var dateStr string
		var mealsStr sql.NullString
		err := rows.Scan(&item.ID, &dateStr, &mealsStr, &item.CreatedAt, &item.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan menu exceptions", "details": err.Error()})
			return
		}
		
		parsedDate, _ := time.Parse(time.RFC3339, dateStr)
		if parsedDate.IsZero() {
			parsedDate, _ = time.Parse("2006-01-02", strings.Split(dateStr, "T")[0])
		}
		item.MenuDate = parsedDate.Format("2006-01-02")

		if mealsStr.Valid {
			item.Meals = json.RawMessage(mealsStr.String)
		} else {
			item.Meals = json.RawMessage(`null`)
		}
		exceptions = append(exceptions, item)
	}

	c.JSON(http.StatusOK, exceptions)
}

// DeleteMenuException deletes a menu exception
func (h *MenuHandler) DeleteMenuException(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid exception id"})
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

	_, err = tx.Exec("DELETE FROM menu_exceptions WHERE id = $1", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete menu exception", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE_MENU_EXCEPTION",
		TableName: "menu_exceptions",
		RecordID:  idStr,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit exception delete"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Kunlik istisno muvaffaqiyatli o'chirildi"})
}
