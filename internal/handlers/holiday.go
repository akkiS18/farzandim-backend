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

type HolidayHandler struct{}

func NewHolidayHandler() *HolidayHandler {
	return &HolidayHandler{}
}

type SaveHolidayRequest struct {
	HolidayDate   string  `json:"holiday_date" binding:"required"` // Format: YYYY-MM-DD
	Name          string  `json:"name" binding:"required"`
	TargetLevels  []int64 `json:"target_levels"`
	TargetClasses []int64 `json:"target_classes"`
}

func ensureHolidayColumns(db *sql.DB) {
	_, _ = db.Exec(`
		ALTER TABLE school_holidays ADD COLUMN IF NOT EXISTS target_levels INT[];
		ALTER TABLE school_holidays ADD COLUMN IF NOT EXISTS target_classes INT[];
	`)
}

// ListHolidays lists all active school holidays
func (h *HolidayHandler) ListHolidays(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	ensureHolidayColumns(dbConn)

	rows, err := dbConn.Query(`
		SELECT id, holiday_date, name, COALESCE(target_levels, '{}'), COALESCE(target_classes, '{}'), created_at, updated_at 
		FROM school_holidays 
		WHERE is_deleted = false 
		ORDER BY holiday_date ASC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query holidays", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []models.SchoolHoliday{}
	for rows.Next() {
		var hol models.SchoolHoliday
		var holidayDate time.Time
		var targetLevels pq.Int64Array
		var targetClasses pq.Int64Array
		if err := rows.Scan(&hol.ID, &holidayDate, &hol.Name, &targetLevels, &targetClasses, &hol.CreatedAt, &hol.UpdatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan holiday record", "details": err.Error()})
			return
		}
		hol.HolidayDate = holidayDate
		hol.TargetLevels = targetLevels
		hol.TargetClasses = targetClasses
		list = append(list, hol)
	}

	c.JSON(http.StatusOK, list)
}

// SaveHoliday creates or updates a holiday
func (h *HolidayHandler) SaveHoliday(c *gin.Context) {
	var req SaveHolidayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request fields", "details": err.Error()})
		return
	}

	holidayDate, err := time.Parse("2006-01-02", req.HolidayDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "holiday_date must be in YYYY-MM-DD format"})
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

	// Check if already exists (even deleted)
	var holidayID int
	var isDeleted bool
	var oldName string
	err = tx.QueryRow("SELECT id, is_deleted, name FROM school_holidays WHERE holiday_date = $1", holidayDate).Scan(&holidayID, &isDeleted, &oldName)

	if err != nil {
		if err == sql.ErrNoRows {
			// Insert new holiday
			err = tx.QueryRow(`
				INSERT INTO school_holidays (holiday_date, name, target_levels, target_classes)
				VALUES ($1, $2, $3, $4)
				RETURNING id`, holidayDate, req.Name, pq.Int64Array(req.TargetLevels), pq.Int64Array(req.TargetClasses)).Scan(&holidayID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to insert holiday", "details": err.Error()})
				return
			}
			audit.LogChange(c, tx, audit.LogData{
				Action:    "CREATE",
				TableName: "school_holidays",
				RecordID:  strconv.Itoa(holidayID),
				NewValues: models.SchoolHoliday{
					ID:            holidayID,
					HolidayDate:   holidayDate,
					Name:          req.Name,
					TargetLevels:  req.TargetLevels,
					TargetClasses: req.TargetClasses,
				},
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error checking holiday", "details": err.Error()})
			return
		}
	} else {
		// Update existing
		_, err = tx.Exec(`
			UPDATE school_holidays 
			SET name = $1, target_levels = $2, target_classes = $3, is_deleted = false, deleted_at = NULL, updated_at = NOW() 
			WHERE id = $4`, req.Name, pq.Int64Array(req.TargetLevels), pq.Int64Array(req.TargetClasses), holidayID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update holiday", "details": err.Error()})
			return
		}
		audit.LogChange(c, tx, audit.LogData{
			Action:    "UPDATE",
			TableName: "school_holidays",
			RecordID:  strconv.Itoa(holidayID),
			OldValues: map[string]interface{}{"name": oldName, "is_deleted": isDeleted},
			NewValues: map[string]interface{}{"name": req.Name, "target_levels": req.TargetLevels, "target_classes": req.TargetClasses, "is_deleted": false},
		})
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit holiday transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Dam olish kuni muvaffaqiyatli saqlandi", "id": holidayID})
}

// DeleteHoliday soft-deletes a holiday
func (h *HolidayHandler) DeleteHoliday(c *gin.Context) {
	idStr := c.Param("id")
	holidayID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid holiday ID"})
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

	// Verify exists and is active
	var exists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM school_holidays WHERE id = $1 AND is_deleted = false)", holidayID).Scan(&exists)
	if err != nil || !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Dam olish kuni topilmadi"})
		return
	}

	_, err = tx.Exec("UPDATE school_holidays SET is_deleted = true, deleted_at = NOW(), updated_at = NOW() WHERE id = $1", holidayID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete holiday", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "school_holidays",
		RecordID:  strconv.Itoa(holidayID),
		OldValues: map[string]interface{}{"id": holidayID, "is_deleted": false},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit delete action"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Dam olish kuni muvaffaqiyatli o'chirildi"})
}
