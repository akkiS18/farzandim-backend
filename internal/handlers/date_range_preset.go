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
)

type DateRangePresetHandler struct{}

func NewDateRangePresetHandler() *DateRangePresetHandler {
	return &DateRangePresetHandler{}
}

func parseFlexibleDate(dateStr string) (time.Time, error) {
	s := strings.TrimSpace(dateStr)
	if s == "" {
		return time.Time{}, fmt.Errorf("sana bo'sh")
	}

	formats := []string{
		"2006-01-02",
		"02.01.2006",
		"02/01/2006",
		"2006/01/02",
		"01/02/2006",
		"2.1.2006",
		"2-1-2006",
		"2006-1-2",
		"2006.01.02",
		"02-01-2006",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}

	// If numeric Excel serial day (e.g. 45536)
	if num, err := strconv.ParseFloat(s, 64); err == nil && num > 20000 && num < 70000 {
		excelEpoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		return excelEpoch.Add(time.Duration(num * 24 * float64(time.Hour))), nil
	}

	return time.Time{}, fmt.Errorf("sana formati noto'g'ri (kutilgan: YYYY-MM-DD yoki DD.MM.YYYY): %s", s)
}

// List returns all active date range presets for a tenant
func (h *DateRangePresetHandler) List(c *gin.Context) {
	category := c.DefaultQuery("category", "schedule")

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	query := `
		SELECT id, name, start_date, end_date, category, created_by, created_at, updated_at
		FROM date_range_presets
		WHERE is_deleted = false AND (category = $1 OR $1 = '')
		ORDER BY created_at DESC`

	rows, err := dbConn.Query(query, category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch date range presets", "details": err.Error()})
		return
	}
	defer rows.Close()

	presets := []models.DateRangePreset{}
	for rows.Next() {
		var item models.DateRangePreset
		var startDate, endDate time.Time
		var createdBy sql.NullInt64

		err := rows.Scan(&item.ID, &item.Name, &startDate, &endDate, &item.Category, &createdBy, &item.CreatedAt, &item.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan preset row", "details": err.Error()})
			return
		}
		item.StartDate = startDate.Format("2006-01-02")
		item.EndDate = endDate.Format("2006-01-02")
		if createdBy.Valid {
			cID := int(createdBy.Int64)
			item.CreatedBy = &cID
		}
		presets = append(presets, item)
	}

	c.JSON(http.StatusOK, presets)
}

// Create saves a new date range preset
func (h *DateRangePresetHandler) Create(c *gin.Context) {
	var req models.CreateDateRangePresetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Category == "" {
		req.Category = "schedule"
	}

	// Flexible Date Parsing
	sTime, err := parseFlexibleDate(req.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid start_date format. Expected YYYY-MM-DD or DD.MM.YYYY"})
		return
	}
	eTime, err := parseFlexibleDate(req.EndDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid end_date format. Expected YYYY-MM-DD or DD.MM.YYYY"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	userIDVal, _ := c.Get("userID")
	var userID *int
	if uidStr, ok := userIDVal.(string); ok && uidStr != "" {
		if id, err := strconv.Atoi(uidStr); err == nil && id > 0 {
			userID = &id
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	var newID int
	query := `
		INSERT INTO date_range_presets (name, start_date, end_date, category, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING id`

	err = tx.QueryRow(query, req.Name, sTime, eTime, req.Category, userID).Scan(&newID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create date range preset", "details": err.Error()})
		return
	}

	formattedStart := sTime.Format("2006-01-02")
	formattedEnd := eTime.Format("2006-01-02")

	preset := models.DateRangePreset{
		ID:        newID,
		Name:      req.Name,
		StartDate: formattedStart,
		EndDate:   formattedEnd,
		Category:  req.Category,
		CreatedBy: userID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "date_range_presets",
		RecordID:  strconv.Itoa(newID),
		NewValues: preset,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, preset)
}

// Delete soft-deletes a date range preset
func (h *DateRangePresetHandler) Delete(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid preset ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	var oldPreset models.DateRangePreset
	var sTime, eTime time.Time
	var createdBy sql.NullInt64
	err = tx.QueryRow(`
		SELECT id, name, start_date, end_date, category, created_by
		FROM date_range_presets WHERE id = $1 AND is_deleted = false`, id).Scan(&oldPreset.ID, &oldPreset.Name, &sTime, &eTime, &oldPreset.Category, &createdBy)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Preset not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch preset", "details": err.Error()})
		return
	}
	oldPreset.StartDate = sTime.Format("2006-01-02")
	oldPreset.EndDate = eTime.Format("2006-01-02")
	if createdBy.Valid {
		cID := int(createdBy.Int64)
		oldPreset.CreatedBy = &cID
	}

	_, err = tx.Exec(`UPDATE date_range_presets SET is_deleted = true, deleted_at = NOW() WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete preset", "details": err.Error()})
		return
	}

	// Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "date_range_presets",
		RecordID:  strconv.Itoa(id),
		OldValues: oldPreset,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Preset deleted successfully"})
}
