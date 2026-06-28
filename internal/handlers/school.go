package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/db"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type SchoolHandler struct {
	pgRootURL string
}

func NewSchoolHandler(pgRootURL string) *SchoolHandler {
	return &SchoolHandler{pgRootURL: pgRootURL}
}

type CreateSchoolRequest struct {
	Name string `json:"name" binding:"required"`
}

func (h *SchoolHandler) CreateSchool(c *gin.Context) {
	var req CreateSchoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	// 1. Generate new unique identifier for the school
	schoolUUID := uuid.New()

	// 2. Call Tenant connection manager to provision the database and execute migrations
	connStr, err := db.TenantConnManager.CreateAndMigrateTenantDB(h.pgRootURL, schoolUUID.String(), req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to provision school database and apply schemas",
			"details": err.Error(),
		})
		return
	}

	// 3. Save school metadata to the central database
	_, err = db.CentralDB.Exec(
		"INSERT INTO schools (id, name, db_connection_string) VALUES ($1, $2, $3)",
		schoolUUID, req.Name, connStr,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to save school metadata to central repository",
			"details": err.Error(),
		})
		return
	}

	school := models.School{
		ID:                 schoolUUID,
		Name:               req.Name,
		DBConnectionString: connStr,
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "School registered and database provisioned successfully",
		"school":  school,
	})
}

// ListSchools lists all registered schools from Central DB
func (h *SchoolHandler) ListSchools(c *gin.Context) {
	rows, err := db.CentralDB.Query("SELECT id, name, db_connection_string FROM schools WHERE is_deleted = false ORDER BY created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query schools list", "details": err.Error()})
		return
	}
	defer rows.Close()

	schools := []models.School{}
	for rows.Next() {
		var s models.School
		err := rows.Scan(&s.ID, &s.Name, &s.DBConnectionString)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse school data", "details": err.Error()})
			return
		}
		schools = append(schools, s)
	}

	c.JSON(http.StatusOK, schools)
}

// GetSchool gets details for a single school
func (h *SchoolHandler) GetSchool(c *gin.Context) {
	schoolID := c.Param("id")

	var s models.School
	err := db.CentralDB.QueryRow("SELECT id, name, db_connection_string FROM schools WHERE id = $1 AND is_deleted = false", schoolID).
		Scan(&s.ID, &s.Name, &s.DBConnectionString)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "School not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query school record", "details": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, s)
}

// ListSchoolAdmins queries the tenant database to list all active admin users
func (h *SchoolHandler) ListSchoolAdmins(c *gin.Context) {
	schoolID := c.Param("id")

	// Resolve dynamic connection pool
	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to school database", "details": err.Error()})
		return
	}

	// Fetch role_id for 'ADMIN' role in the tenant DB
	var adminRoleID int
	err = tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'ADMIN'").Scan(&adminRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Role 'ADMIN' is not initialized in the tenant DB", "details": err.Error()})
		return
	}

	// Fetch active admins
	query := `
		SELECT id, email, phone, first_name, last_name, middle_name, role_id, is_deleted, created_at, updated_at 
		FROM users 
		WHERE role_id = $1 AND is_deleted = false 
		ORDER BY first_name, last_name`
	rows, err := tenantDB.Query(query, adminRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query school admin list", "details": err.Error()})
		return
	}
	defer rows.Close()

	admins := []models.User{}
	for rows.Next() {
		var u models.User
		err := rows.Scan(&u.ID, &u.Email, &u.Phone, &u.FirstName, &u.LastName, &u.MiddleName, &u.RoleID, &u.IsDeleted, &u.CreatedAt, &u.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse admin user record", "details": err.Error()})
			return
		}
		admins = append(admins, u)
	}

	c.JSON(http.StatusOK, admins)
}

type CreateSchoolAdminRequest struct {
	FirstName  string  `json:"first_name" binding:"required"`
	LastName   string  `json:"last_name" binding:"required"`
	MiddleName *string `json:"middle_name"`
	Phone      string  `json:"phone" binding:"required"`
	Email      *string `json:"email"`
	Password   string  `json:"password" binding:"required"`
}

// CreateSchoolAdmin inserts a new school admin into the isolated tenant database
func (h *SchoolHandler) CreateSchoolAdmin(c *gin.Context) {
	schoolID := c.Param("id")

	var req CreateSchoolAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	// Resolve connection pool dynamically
	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to school database", "details": err.Error()})
		return
	}

	// Fetch role_id for 'ADMIN'
	var adminRoleID int
	err = tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'ADMIN'").Scan(&adminRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Role 'ADMIN' is not initialized in the tenant DB", "details": err.Error()})
		return
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt credentials"})
		return
	}

	// Begin transaction to ensure atomic insert & audit logging
	tx, err := tenantDB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open database transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	var userID int
	insertQuery := `
		INSERT INTO users (first_name, last_name, middle_name, phone, email, password_hash, role_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`
	err = tx.QueryRow(insertQuery, req.FirstName, req.LastName, req.MiddleName, req.Phone, req.Email, string(hashedPassword), adminRoleID).Scan(&userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write admin record to school DB", "details": err.Error()})
		return
	}

	newUser := models.User{
		ID:         userID,
		FirstName:  req.FirstName,
		LastName:   req.LastName,
		MiddleName: req.MiddleName,
		Phone:      &req.Phone,
		Email:      req.Email,
		RoleID:     adminRoleID,
		IsDeleted:  false,
	}

	// Log change in the tenant audit logs
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "users",
		RecordID:  strconv.Itoa(userID),
		NewValues: newUser,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newUser)
}
