package handlers

import (
	"database/sql"
	"fmt"
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
	HolidayDate    string  `json:"holiday_date" binding:"required"` // Format: YYYY-MM-DD
	Name           string  `json:"name" binding:"required"`
	TargetLevels   []int64 `json:"target_levels"`
	TargetClasses  []int64 `json:"target_classes"`
	ForceOverwrite bool    `json:"force_overwrite"`
}

func ensureHolidayColumns(db *sql.DB) {
	_, _ = db.Exec(`
		ALTER TABLE school_holidays ADD COLUMN IF NOT EXISTS target_levels INT[];
		ALTER TABLE school_holidays ADD COLUMN IF NOT EXISTS target_classes INT[];
	`)
}

func convertInt64Array(arr []int64) []int {
	result := make([]int, len(arr))
	for i, v := range arr {
		result[i] = int(v)
	}
	return result
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
		var targetLevels, targetClasses []int64

		if err := rows.Scan(&hol.ID, &holidayDate, &hol.Name, pq.Array(&targetLevels), pq.Array(&targetClasses), &hol.CreatedAt, &hol.UpdatedAt); err != nil {
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

	// Check if active grades exist on this holiday date if force_overwrite is false
	if !req.ForceOverwrite {
		var gradeCount int
		var sampleSubject, sampleClass string
		err = dbConn.QueryRow(`
			SELECT COUNT(g.id), COALESCE(MAX(s.name), ''), COALESCE(MAX(c.name), '')
			FROM grades g
			JOIN students st ON g.student_id = st.id
			LEFT JOIN subjects s ON g.subject_id = s.id
			LEFT JOIN classes c ON st.class_id = c.id
			WHERE DATE(g.grade_date) = $1 AND g.is_deleted = false AND st.is_deleted = false
		`, holidayDate.Format("2006-01-02")).Scan(&gradeCount, &sampleSubject, &sampleClass)

		if err == nil && gradeCount > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"has_existing_grades": true,
				"grade_count":         gradeCount,
				"sample_class":        sampleClass,
				"sample_subject":      sampleSubject,
				"error":               fmt.Sprintf("Diqqat! '%s' kunida allaqachon %d ta baho qo'yilgan (masalan: %s sinfi, %s fani). Ushbu sanaga dam olish kuni belgilash uchun o'sha kungi baholarni bekor qilishni tasdiqlang!", req.HolidayDate, gradeCount, sampleClass, sampleSubject),
			})
			return
		}
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// If force_overwrite is true, soft-delete existing grades on this holiday date
	if req.ForceOverwrite {
		_, err = tx.Exec(`
			UPDATE grades 
			SET is_deleted = true, deleted_at = NOW(), updated_at = NOW() 
			WHERE DATE(grade_date) = $1 AND is_deleted = false
		`, holidayDate.Format("2006-01-02"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to overwrite grades for holiday", "details": err.Error()})
			return
		}
	}

	// Insert or Update holiday
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
				RETURNING id`, holidayDate, req.Name, pq.Array(req.TargetLevels), pq.Array(req.TargetClasses)).Scan(&holidayID)
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
			WHERE id = $4`, req.Name, pq.Array(req.TargetLevels), pq.Array(req.TargetClasses), holidayID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update holiday", "details": err.Error()})
			return
		}
		audit.LogChange(c, tx, audit.LogData{
			Action:    "UPDATE",
			TableName: "school_holidays",
			RecordID:  strconv.Itoa(holidayID),
			OldValues: map[string]interface{}{"name": oldName, "is_deleted": isDeleted},
			NewValues: models.SchoolHoliday{
				ID:            holidayID,
				HolidayDate:   holidayDate,
				Name:          req.Name,
				TargetLevels:  req.TargetLevels,
				TargetClasses: req.TargetClasses,
			},
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
