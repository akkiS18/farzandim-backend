package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/gin-gonic/gin"
)

type MenuHandler struct{}

func NewMenuHandler() *MenuHandler {
	return &MenuHandler{}
}

type SaveMenuCycleRequest struct {
	WeekNumber int             `json:"week_number" binding:"required,min=1"`
	DayOfWeek  int             `json:"day_of_week" binding:"required,min=1,max=6"`
	Meals      json.RawMessage `json:"meals" binding:"required"`
}

type SaveMenuExceptionRequest struct {
	MenuDate string           `json:"menu_date" binding:"required"` // Format: YYYY-MM-DD
	Meals    *json.RawMessage `json:"meals"`                        // Nullable to represent cancellations
}

// getCycleWeek calculates which cycle week a given date belongs to based on a cycle length and September 1st reference.
func getCycleWeek(queryDate time.Time, cycleLength int) int {
	if cycleLength <= 1 {
		return 1
	}
	year := queryDate.Year()
	refDate := time.Date(year, time.September, 1, 0, 0, 0, 0, time.UTC)
	if queryDate.Before(refDate) {
		refDate = time.Date(year-1, time.September, 1, 0, 0, 0, 0, time.UTC)
	}

	// Align refDate to the preceding Monday
	daysToMonday := int(refDate.Weekday()) - 1
	if daysToMonday < 0 {
		daysToMonday = 6 // Sunday
	}
	refMonday := refDate.AddDate(0, 0, -daysToMonday)

	// Calculate weeks elapsed
	duration := queryDate.Sub(refMonday)
	weeksElapsed := int(duration.Hours() / (24 * 7))

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
	var cycleLength int
	err = dbConn.QueryRow("SELECT COALESCE(MAX(week_number), 0) FROM menu_cycles").Scan(&cycleLength)
	if err != nil || cycleLength == 0 {
		c.JSON(http.StatusOK, gin.H{
			"date":   dateParam,
			"source": "empty",
			"meals":  nil,
		})
		return
	}

	cycleWeek := getCycleWeek(parsedDate, cycleLength)
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
	err = dbConn.QueryRow("SELECT id, meals FROM menu_cycles WHERE week_number = $1 AND day_of_week = $2", cycleWeek, dayOfWeek).Scan(&templateID, &templateMeals)
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
		INSERT INTO menu_cycles (week_number, day_of_week, meals)
		VALUES ($1, $2, $3)
		ON CONFLICT (week_number, day_of_week) DO UPDATE SET meals = EXCLUDED.meals, updated_at = NOW()
		RETURNING id`

	err = tx.QueryRow(upsertQuery, req.WeekNumber, req.DayOfWeek, string(req.Meals)).Scan(&cycleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save cycle template", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "SAVE_CYCLE_TEMPLATE",
		TableName: "menu_cycles",
		RecordID:  string(req.WeekNumber) + "-" + string(req.DayOfWeek),
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
