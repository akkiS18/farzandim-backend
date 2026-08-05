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

type TargetPresetHandler struct{}

func NewTargetPresetHandler() *TargetPresetHandler {
	return &TargetPresetHandler{}
}

// List returns all active target presets for a tenant
func (h *TargetPresetHandler) List(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	query := `
		SELECT id, name, target_levels, target_classes, target_students, created_by, created_at, updated_at
		FROM target_presets
		WHERE is_deleted = false
		ORDER BY created_at DESC`

	rows, err := dbConn.Query(query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch target presets", "details": err.Error()})
		return
	}
	defer rows.Close()

	presets := []models.TargetPreset{}
	for rows.Next() {
		var item models.TargetPreset
		var tLevels, tClasses, tStudents pq.Int64Array
		var createdBy sql.NullInt64

		err := rows.Scan(&item.ID, &item.Name, &tLevels, &tClasses, &tStudents, &createdBy, &item.CreatedAt, &item.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan preset row", "details": err.Error()})
			return
		}

		item.TargetLevels = make([]int, len(tLevels))
		for i, v := range tLevels {
			item.TargetLevels[i] = int(v)
		}

		item.TargetClasses = make([]int, len(tClasses))
		for i, v := range tClasses {
			item.TargetClasses[i] = int(v)
		}

		item.TargetStudents = make([]int, len(tStudents))
		for i, v := range tStudents {
			item.TargetStudents[i] = int(v)
		}

		if createdBy.Valid {
			cID := int(createdBy.Int64)
			item.CreatedBy = &cID
		}

		presets = append(presets, item)
	}

	c.JSON(http.StatusOK, presets)
}

// Create saves a new target preset
func (h *TargetPresetHandler) Create(c *gin.Context) {
	var req models.CreateTargetPresetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

	tLevels := make(pq.Int64Array, len(req.TargetLevels))
	for i, v := range req.TargetLevels {
		tLevels[i] = int64(v)
	}

	tClasses := make(pq.Int64Array, len(req.TargetClasses))
	for i, v := range req.TargetClasses {
		tClasses[i] = int64(v)
	}

	tStudents := make(pq.Int64Array, len(req.TargetStudents))
	for i, v := range req.TargetStudents {
		tStudents[i] = int64(v)
	}

	var newID int
	query := `
		INSERT INTO target_presets (name, target_levels, target_classes, target_students, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING id`

	err = tx.QueryRow(query, req.Name, tLevels, tClasses, tStudents, userID).Scan(&newID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create target preset", "details": err.Error()})
		return
	}

	preset := models.TargetPreset{
		ID:             newID,
		Name:           req.Name,
		TargetLevels:   req.TargetLevels,
		TargetClasses:  req.TargetClasses,
		TargetStudents: req.TargetStudents,
		CreatedBy:      userID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "target_presets",
		RecordID:  strconv.Itoa(newID),
		NewValues: preset,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, preset)
}

// Delete soft-deletes a target preset
func (h *TargetPresetHandler) Delete(c *gin.Context) {
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

	var oldPreset models.TargetPreset
	var createdBy sql.NullInt64
	var tLevels, tClasses, tStudents pq.Int64Array

	err = tx.QueryRow(`
		SELECT id, name, target_levels, target_classes, target_students, created_by
		FROM target_presets WHERE id = $1 AND is_deleted = false`, id).Scan(&oldPreset.ID, &oldPreset.Name, &tLevels, &tClasses, &tStudents, &createdBy)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Target preset not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch preset", "details": err.Error()})
		return
	}

	if createdBy.Valid {
		cID := int(createdBy.Int64)
		oldPreset.CreatedBy = &cID
	}

	_, err = tx.Exec(`UPDATE target_presets SET is_deleted = true, deleted_at = NOW() WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete target preset", "details": err.Error()})
		return
	}

	// Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "target_presets",
		RecordID:  strconv.Itoa(id),
		OldValues: oldPreset,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Target preset deleted successfully"})
}
