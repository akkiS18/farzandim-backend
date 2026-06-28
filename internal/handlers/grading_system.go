package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
)

type GradingSystemHandler struct{}

func NewGradingSystemHandler() *GradingSystemHandler {
	return &GradingSystemHandler{}
}

type GradingSystemOption struct {
	Label        string   `json:"label" binding:"required"`
	NumericValue *float64 `json:"numeric_value"`
}

type CreateGradingSystemRequest struct {
	Name     string                `json:"name" binding:"required"`
	Type     string                `json:"type" binding:"required"` // "NUMERIC", "PERCENTAGE", "LETTER"
	MinValue *float64              `json:"min_value"`
	MaxValue *float64              `json:"max_value"`
	Options  []GradingSystemOption `json:"options"`
}

func (h *GradingSystemHandler) ListGradingSystems(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	rows, err := db.Query("SELECT id, name, type, min_value, max_value, options, is_active, created_at, updated_at FROM grading_systems ORDER BY created_at ASC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query grading systems", "details": err.Error()})
		return
	}
	defer rows.Close()

	systems := []models.GradingSystem{}
	for rows.Next() {
		var s models.GradingSystem
		var minVal, maxVal sql.NullFloat64
		var optionsBytes []byte

		err := rows.Scan(&s.ID, &s.Name, &s.Type, &minVal, &maxVal, &optionsBytes, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan grading system data", "details": err.Error()})
			return
		}

		if minVal.Valid {
			s.MinValue = &minVal.Float64
		}
		if maxVal.Valid {
			s.MaxValue = &maxVal.Float64
		}
		if optionsBytes != nil {
			s.Options = json.RawMessage(optionsBytes)
		} else {
			s.Options = json.RawMessage("null")
		}

		systems = append(systems, s)
	}

	c.JSON(http.StatusOK, systems)
}

func (h *GradingSystemHandler) GetActiveGradingSystem(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	var s models.GradingSystem
	var minVal, maxVal sql.NullFloat64
	var optionsBytes []byte

	err := db.QueryRow("SELECT id, name, type, min_value, max_value, options, is_active, created_at, updated_at FROM grading_systems WHERE is_active = true").
		Scan(&s.ID, &s.Name, &s.Type, &minVal, &maxVal, &optionsBytes, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "No active grading system configured"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch active grading system", "details": err.Error()})
		}
		return
	}

	if minVal.Valid {
		s.MinValue = &minVal.Float64
	}
	if maxVal.Valid {
		s.MaxValue = &maxVal.Float64
	}
	if optionsBytes != nil {
		s.Options = json.RawMessage(optionsBytes)
	} else {
		s.Options = json.RawMessage("null")
	}

	c.JSON(http.StatusOK, s)
}

func (h *GradingSystemHandler) CreateGradingSystem(c *gin.Context) {
	var req CreateGradingSystemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request fields", "details": err.Error()})
		return
	}

	// Validate request based on type
	if req.Type != "NUMERIC" && req.Type != "PERCENTAGE" && req.Type != "LETTER" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid grading system type. Must be NUMERIC, PERCENTAGE, or LETTER"})
		return
	}

	if req.Type == "NUMERIC" || req.Type == "PERCENTAGE" {
		if req.MinValue == nil || req.MaxValue == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "min_value and max_value are required for numeric or percentage systems"})
			return
		}
		if *req.MinValue >= *req.MaxValue {
			c.JSON(http.StatusBadRequest, gin.H{"error": "min_value must be less than max_value"})
			return
		}
	}

	var optionsJSON []byte
	var err error
	if req.Type == "LETTER" {
		if len(req.Options) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "At least one option is required for letter/custom grading systems"})
			return
		}
		optionsJSON, err = json.Marshal(req.Options)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process options format"})
			return
		}
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Insert new grading system
	var newID int
	query := `
		INSERT INTO grading_systems (name, type, min_value, max_value, options, is_active)
		VALUES ($1, $2, $3, $4, $5, FALSE)
		RETURNING id`

	var minValParam, maxValParam interface{}
	if req.MinValue != nil {
		minValParam = *req.MinValue
	}
	if req.MaxValue != nil {
		maxValParam = *req.MaxValue
	}

	var optionsParam interface{}
	if req.Type == "LETTER" {
		optionsParam = optionsJSON
	} else {
		optionsParam = nil
	}

	err = tx.QueryRow(query, req.Name, req.Type, minValParam, maxValParam, optionsParam).Scan(&newID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create grading system record", "details": err.Error()})
		return
	}

	newSystem := models.GradingSystem{
		ID:        newID,
		Name:      req.Name,
		Type:      req.Type,
		MinValue:  req.MinValue,
		MaxValue:  req.MaxValue,
		Options:   optionsJSON,
		IsActive:  false,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "grading_systems",
		RecordID:  strconv.Itoa(newID),
		NewValues: newSystem,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newSystem)
}

func (h *GradingSystemHandler) ActivateGradingSystem(c *gin.Context) {
	idStr := c.Param("id")
	systemID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid grading system ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	// Check if the system exists
	var exists bool
	err = dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM grading_systems WHERE id = $1)", systemID).Scan(&exists)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify grading system existence", "details": err.Error()})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Grading system not found"})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin database transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Deactivate current active systems
	_, err = tx.Exec("UPDATE grading_systems SET is_active = FALSE, updated_at = NOW() WHERE is_active = TRUE")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to deactivate current systems", "details": err.Error()})
		return
	}

	// Activate target system
	_, err = tx.Exec("UPDATE grading_systems SET is_active = TRUE, updated_at = NOW() WHERE id = $1", systemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate grading system", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "ACTIVATE",
		TableName: "grading_systems",
		RecordID:  strconv.Itoa(systemID),
		NewValues: map[string]interface{}{"id": systemID, "is_active": true},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Grading system activated successfully"})
}

func (h *GradingSystemHandler) DeleteGradingSystem(c *gin.Context) {
	idStr := c.Param("id")
	systemID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid grading system ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	// Check if the system exists and is not active
	var isActive bool
	var name string
	err = dbConn.QueryRow("SELECT name, is_active FROM grading_systems WHERE id = $1", systemID).Scan(&name, &isActive)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Grading system not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check grading system status", "details": err.Error()})
		}
		return
	}

	if isActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete the active grading system. Please activate another grading system first."})
		return
	}

	// We cannot delete default seeded systems to prevent breaking initial setup
	if name == "5 ballik sistema" || name == "100 ballik sistema" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete default seeded grading systems"})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin database transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM grading_systems WHERE id = $1", systemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete grading system record", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "grading_systems",
		RecordID:  strconv.Itoa(systemID),
		OldValues: map[string]interface{}{"id": systemID, "name": name},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Grading system deleted successfully"})
}
