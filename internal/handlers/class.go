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

type ClassHandler struct{}

func NewClassHandler() *ClassHandler {
	return &ClassHandler{}
}

type CreateClassRequest struct {
	Name  string `json:"name" binding:"required"`
	Level *int   `json:"level"`
}

func (h *ClassHandler) ListClasses(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	roleVal, exists := c.Get("role")
	role := ""
	if exists {
		role = roleVal.(string)
	}

	userIDVal, exists := c.Get("userID")
	userIDStr := ""
	if exists {
		userIDStr = userIDVal.(string)
	}

	var rows *sql.Rows
	var err error

	if role == "PARENT" {
		parentID, errConv := strconv.Atoi(userIDStr)
		if errConv != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parent user ID"})
			return
		}
		query := `
			SELECT DISTINCT c.id, c.name, c.level, c.is_deleted, c.deleted_at 
			FROM classes c
			JOIN students s ON s.class_id = c.id
			JOIN student_parents sp ON sp.student_id = s.id
			WHERE c.is_deleted = false AND s.is_deleted = false AND sp.parent_id = $1
			ORDER BY c.name ASC`
		rows, err = dbConn.Query(query, parentID)
	} else if role == "STUDENT" {
		studentUserID, errConv := strconv.Atoi(userIDStr)
		if errConv != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student user ID"})
			return
		}
		query := `
			SELECT DISTINCT c.id, c.name, c.level, c.is_deleted, c.deleted_at 
			FROM classes c
			JOIN students s ON s.class_id = c.id
			WHERE c.is_deleted = false AND s.is_deleted = false AND s.user_id = $1
			ORDER BY c.name ASC`
		rows, err = dbConn.Query(query, studentUserID)
	} else if role == "MAIN_TEACHER" || role == "SUBJECT_TEACHER" {
		teacherID, errConv := strconv.Atoi(userIDStr)
		if errConv != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid teacher user ID"})
			return
		}
		query := `
			SELECT c.id, c.name, c.level, s.id as subject_id, s.name as subject_name
			FROM classes c
			JOIN class_teachers ct ON ct.class_id = c.id
			JOIN subjects s ON ct.subject_id = s.id
			WHERE c.is_deleted = false AND ct.is_deleted = false AND s.is_deleted = false AND ct.teacher_id = $1
			ORDER BY c.name ASC`
		rows, err = dbConn.Query(query, teacherID)
	} else {
		rows, err = dbConn.Query("SELECT id, name, level, is_deleted, deleted_at FROM classes WHERE is_deleted = false ORDER BY name ASC")
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query classes", "details": err.Error()})
		return
	}
	defer rows.Close()

	if role == "MAIN_TEACHER" || role == "SUBJECT_TEACHER" {
		type TeacherClass struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			Level       int    `json:"level"`
			SubjectID   int    `json:"subject_id"`
			SubjectName string `json:"subject_name"`
		}
		classesList := []TeacherClass{}
		for rows.Next() {
			var tc TeacherClass
			if err := rows.Scan(&tc.ID, &tc.Name, &tc.Level, &tc.SubjectID, &tc.SubjectName); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse records", "details": err.Error()})
				return
			}
			classesList = append(classesList, tc)
		}
		c.JSON(http.StatusOK, classesList)
		return
	}

	classes := []models.Class{}
	for rows.Next() {
		var class models.Class
		err := rows.Scan(&class.ID, &class.Name, &class.Level, &class.IsDeleted, &class.DeletedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan class data", "details": err.Error()})
			return
		}
		classes = append(classes, class)
	}

	c.JSON(http.StatusOK, classes)
}

func (h *ClassHandler) CreateClass(c *gin.Context) {
	var req CreateClassRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	// Start database transaction to guarantee atomic audit logs
	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin database transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Insert Class record
	var classID int
	lvlVal := 1
	if req.Level != nil {
		lvlVal = *req.Level
	}
	err = tx.QueryRow("INSERT INTO classes (name, level) VALUES ($1, $2) RETURNING id", req.Name, lvlVal).Scan(&classID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write class record", "details": err.Error()})
		return
	}

	newClass := models.Class{
		ID:        classID,
		Name:      req.Name,
		Level:     lvlVal,
		IsDeleted: false,
	}

	// Write Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "classes",
		RecordID:  strconv.Itoa(classID),
		NewValues: newClass,
	})

	// Commit Transaction
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newClass)
}

func (h *ClassHandler) UpdateClass(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Malformed class ID parameter"})
		return
	}

	var req CreateClassRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin database transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Query old class data before mutating
	var oldClass models.Class
	err = tx.QueryRow("SELECT id, name, level, is_deleted, deleted_at FROM classes WHERE id = $1 AND is_deleted = false", classID).
		Scan(&oldClass.ID, &oldClass.Name, &oldClass.Level, &oldClass.IsDeleted, &oldClass.DeletedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Class not found or already deleted"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing class record", "details": err.Error()})
		}
		return
	}

	lvlVal := oldClass.Level
	if req.Level != nil {
		lvlVal = *req.Level
	}

	// Perform database update
	_, err = tx.Exec("UPDATE classes SET name = $1, level = $2 WHERE id = $3", req.Name, lvlVal, classID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update class details", "details": err.Error()})
		return
	}

	updatedClass := models.Class{
		ID:        classID,
		Name:      req.Name,
		Level:     lvlVal,
		IsDeleted: false,
	}

	// Write Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "classes",
		RecordID:  strconv.Itoa(classID),
		OldValues: oldClass,
		NewValues: updatedClass,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, updatedClass)
}

func (h *ClassHandler) DeleteClass(c *gin.Context) {
	classIDStr := c.Param("id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Malformed class ID parameter"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin database transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Query old class data before mutating
	var oldClass models.Class
	err = tx.QueryRow("SELECT id, name, level, is_deleted, deleted_at FROM classes WHERE id = $1 AND is_deleted = false", classID).
		Scan(&oldClass.ID, &oldClass.Name, &oldClass.Level, &oldClass.IsDeleted, &oldClass.DeletedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Class not found or already deleted"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing class record", "details": err.Error()})
		}
		return
	}

	// Perform Soft Delete (flag changes instead of physical deletion)
	now := time.Now()
	_, err = tx.Exec("UPDATE classes SET is_deleted = true, deleted_at = $1 WHERE id = $2", now, classID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to execute soft delete database operation", "details": err.Error()})
		return
	}

	// Write Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "SOFT_DELETE",
		TableName: "classes",
		RecordID:  strconv.Itoa(classID),
		OldValues: oldClass,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Class soft deleted successfully"})
}
