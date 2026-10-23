package handlers

import (
	"database/sql"
	"errors"
		"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/db"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/farzandim/backend/internal/utils"
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Maktab nomi 1–255 ta belgi bo‘lishi kerak"})
		return
	}
	// Serialize provisioning requests across backend instances, before creating a DB.
	metadataTx, err := db.CentralDB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not start school provisioning"})
		return
	}
	defer metadataTx.Rollback()
	if _, err := metadataTx.ExecContext(c.Request.Context(), "SELECT pg_advisory_xact_lock(72410323)"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not lock school provisioning"})
		return
	}
	var duplicate bool
	if err := metadataTx.QueryRowContext(c.Request.Context(), "SELECT EXISTS(SELECT 1 FROM schools WHERE LOWER(TRIM(name)) = LOWER($1))", req.Name).Scan(&duplicate); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not check school name"})
		return
	}
	if duplicate {
		c.JSON(http.StatusConflict, gin.H{"error": "Bu nomdagi maktab allaqachon mavjud"})
		return
	}
	// 1. Generate new unique identifier for the school
	schoolUUID := uuid.New()

	// 2. Call Tenant connection manager to provision the database and execute migrations
	connStr, err := db.TenantConnManager.CreateAndMigrateTenantDB(h.pgRootURL, schoolUUID.String(), req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to provision school database and apply schemas",
		})
		return
	}

	// 3. Save school metadata to the central database
	_, err = metadataTx.ExecContext(c.Request.Context(),
		"INSERT INTO schools (id, name, db_connection_string) VALUES ($1, $2, $3)",
		schoolUUID, req.Name, connStr,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to save school metadata to central repository",
		})
		return
	}

	if err := metadataTx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "School registration did not complete; contact the operator before retrying"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query schools list"})
		return
	}
	defer rows.Close()

	schools := []models.School{}
	for rows.Next() {
		var s models.School
		err := rows.Scan(&s.ID, &s.Name, &s.DBConnectionString)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse school data"})
			return
		}
		schools = append(schools, s)
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not finish reading schools"})
		return
	}
	c.JSON(http.StatusOK, schools)
}

// PublicSchoolDto represents safe public information about a school for tenant selection
type PublicSchoolDto struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Subdomain string `json:"subdomain"`
}

// ListPublicSchools returns active schools with safe public info for mobile/web login
func (h *SchoolHandler) ListPublicSchools(c *gin.Context) {
	rows, err := db.CentralDB.Query("SELECT id, name, db_connection_string FROM schools WHERE is_deleted = false ORDER BY name ASC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query schools list"})
		return
	}
	defer rows.Close()

	schools := []PublicSchoolDto{}
	for rows.Next() {
		var id, name, connStr string
		if err := rows.Scan(&id, &name, &connStr); err != nil {
			continue
		}

		subdomain := ""
		if u, err := url.Parse(connStr); err == nil {
			dbName := strings.TrimPrefix(u.Path, "/")
			subdomain = strings.TrimPrefix(dbName, "db_f_")
		}
		if subdomain == "" {
			subdomain = id
		}

		schools = append(schools, PublicSchoolDto{
			ID:        id,
			Name:      name,
			Subdomain: subdomain,
		})
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not finish reading schools"})
		return
	}
	c.JSON(http.StatusOK, schools)
}

// GetSchool gets details for a single school
func (h *SchoolHandler) GetSchool(c *gin.Context) {
	schoolID := c.Param("id")
	if _, err := uuid.Parse(schoolID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid school ID"})
		return
	}

	var s models.School
	err := db.CentralDB.QueryRow("SELECT id, name, db_connection_string FROM schools WHERE id = $1 AND is_deleted = false", schoolID).
		Scan(&s.ID, &s.Name, &s.DBConnectionString)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "School not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query school record"})
		}
		return
	}

	c.JSON(http.StatusOK, s)
}

// ListSchoolAdmins queries the tenant database to list all active admin users
func (h *SchoolHandler) ListSchoolAdmins(c *gin.Context) {
	schoolID := c.Param("id")
	if _, err := uuid.Parse(schoolID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid school ID"})
		return
	}

	// Resolve dynamic connection pool
	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to school database"})
		return
	}

	// Fetch role_id for 'ADMIN' role in the tenant DB
	var adminRoleID int
	err = tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'ADMIN'").Scan(&adminRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Role 'ADMIN' is not initialized in the tenant DB"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query school admin list"})
		return
	}
	defer rows.Close()

	admins := []models.User{}
	for rows.Next() {
		var u models.User
		err := rows.Scan(&u.ID, &u.Email, &u.Phone, &u.FirstName, &u.LastName, &u.MiddleName, &u.RoleID, &u.IsDeleted, &u.CreatedAt, &u.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse admin user record"})
			return
		}
		admins = append(admins, u)
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not finish reading admins"})
		return
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
	if _, err := uuid.Parse(schoolID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid school ID"})
		return
	}

	var req CreateSchoolAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.Phone = utils.NormalizePhone(req.Phone)
	if req.FirstName == "" || req.LastName == "" || len(req.Phone) < 9 || len(req.Phone) > 15 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ism, familiya va to‘g‘ri telefon majburiy"})
		return
	}
	if err := utils.ValidateAdminPassword(req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Email != nil {
		value := strings.TrimSpace(*req.Email)
		if value == "" {
			req.Email = nil
		} else {
			req.Email = &value
		}
	}
	// Resolve connection pool dynamically
	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to school database"})
		return
	}

	// Fetch role_id for 'ADMIN'
	var adminRoleID int
	err = tenantDB.QueryRow("SELECT id FROM roles WHERE name = 'ADMIN'").Scan(&adminRoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Role 'ADMIN' is not initialized in the tenant DB"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open database transaction"})
		return
	}
	defer tx.Rollback()

	var userID int
	var createdAt, updatedAt time.Time
	insertQuery := `
		INSERT INTO users (first_name, last_name, middle_name, phone, email, password_hash, role_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at`
	err = tx.QueryRow(insertQuery, req.FirstName, req.LastName, req.MiddleName, req.Phone, req.Email, string(hashedPassword), adminRoleID).Scan(&userID, &createdAt, &updatedAt)
	if err != nil {
		var pgErr *pq.Error
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "Bu telefon yoki email allaqachon ro‘yxatdan o‘tgan"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write admin record to school DB"})
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
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}

	// Log change in the tenant audit logs
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "users",
		RecordID:  strconv.Itoa(userID),
		NewValues: newUser,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit database transaction"})
		return
	}

	c.JSON(http.StatusCreated, newUser)
}

