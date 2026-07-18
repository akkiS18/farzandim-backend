package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/db"
	"github.com/farzandim/backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	jwtSecret string
}

func NewAuthHandler(jwtSecret string) *AuthHandler {
	return &AuthHandler{jwtSecret: jwtSecret}
}

type SuperAdminRegisterRequest struct {
	Email     *string `json:"email"`
	Phone     string  `json:"phone" binding:"required"`
	Password  string  `json:"password" binding:"required"`
	FirstName string  `json:"first_name" binding:"required"`
	LastName  string  `json:"last_name" binding:"required"`
}

type LoginRequest struct {
	Phone    string `json:"phone" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	roleVal, exists := c.Get("role")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Role missing"})
		return
	}
	role := roleVal.(string)

	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: User ID missing"})
		return
	}
	userIDStr := userIDVal.(string)

	if role == "SUPER_ADMIN" {
		// Super Admin password change in Central DB
		var passwordHash string
		err := db.CentralDB.QueryRow(
			"SELECT password_hash FROM super_admins WHERE id = $1 AND is_deleted = false",
			userIDStr,
		).Scan(&passwordHash)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Super Admin not found or error occurred"})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.OldPassword)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Eski parol noto'g'ri"})
			return
		}

		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt new credentials"})
			return
		}

		_, err = db.CentralDB.Exec(
			"UPDATE super_admins SET password_hash = $1, updated_at = NOW() WHERE id = $2",
			string(newHash), userIDStr,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password", "details": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Parol muvaffaqiyatli o'zgartirildi"})
		return
	}

	// For Tenant Users
	tenantDBVal, exists := c.Get("tenantDB")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database mapping failure"})
		return
	}
	tenantDB := tenantDBVal.(*sql.DB)

	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Malformed User ID"})
		return
	}

	tx, err := tenantDB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Baza tranzaksiyasini boshlab bo'lmadi"})
		return
	}
	defer tx.Rollback()

	var passwordHash string
	err = tx.QueryRow(
		"SELECT password_hash FROM users WHERE id = $1 AND is_deleted = false",
		userID,
	).Scan(&passwordHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Foydalanuvchi topilmadi"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.OldPassword)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Eski parol noto'g'ri"})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt new credentials"})
		return
	}

	_, err = tx.Exec(
		"UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2",
		string(newHash), userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Parolni yangilashda xatolik", "details": err.Error()})
		return
	}

	// Log change
	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "users",
		RecordID:  strconv.Itoa(userID),
		OldValues: map[string]interface{}{"password_status": "old"},
		NewValues: map[string]interface{}{"password_status": "changed"},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiyani yakunlab bo'lmadi (Commit)"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Parol muvaffaqiyatli o'zgartirildi"})
}

func (h *AuthHandler) RegisterSuperAdmin(c *gin.Context) {
	var req SuperAdminRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt credentials"})
		return
	}

	superAdminID := uuid.New()
	_, err = db.CentralDB.Exec(
		`INSERT INTO super_admins (id, email, phone, password_hash, first_name, last_name) 
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		superAdminID, req.Email, req.Phone, string(hashedPassword), req.FirstName, req.LastName,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write super admin record", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Super Admin registered successfully",
		"id":      superAdminID,
	})
}

func (h *AuthHandler) LoginSuperAdmin(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var id uuid.UUID
	var passwordHash string
	var firstName, lastName string
	err := db.CentralDB.QueryRow(
		"SELECT id, password_hash, first_name, last_name FROM super_admins WHERE phone = $1 AND is_deleted = false",
		req.Phone,
	).Scan(&id, &passwordHash, &firstName, &lastName)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid phone number or password"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid phone number or password"})
		return
	}

	// Generate superadmin JWT
	token, err := h.generateJWT(id.String(), "SUPER_ADMIN", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue auth token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":         id,
			"first_name": firstName,
			"last_name":  lastName,
			"role":       "SUPER_ADMIN",
		},
	})
}

func (h *AuthHandler) LoginTenantUser(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	schoolIDVal, exists := c.Get("currentSchoolID")
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dynamic tenant routing failed: School context missing"})
		return
	}
	schoolID := schoolIDVal.(string)

	tenantDBVal, exists := c.Get("tenantDB")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database mapping failure"})
		return
	}
	tenantDB := tenantDBVal.(*sql.DB)

	var userID int
	var passwordHash string
	var firstName, lastName string
	var roleName string
	var passportNull, phoneNull sql.NullString
	query := `
		SELECT u.id, u.password_hash, u.first_name, u.last_name, r.name, u.passport, u.phone 
		FROM users u 
		JOIN roles r ON u.role_id = r.id 
		WHERE u.phone = $1 AND u.is_deleted = false`
	err := tenantDB.QueryRow(query, req.Phone).Scan(&userID, &passwordHash, &firstName, &lastName, &roleName, &passportNull, &phoneNull)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid phone or password"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid phone or password"})
		return
	}

	// Generate tenant user JWT
	token, err := h.generateJWT(strconv.Itoa(userID), roleName, schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue auth token"})
		return
	}

	var passport *string
	if passportNull.Valid {
		passport = &passportNull.String
	}
	var phone *string
	if phoneNull.Valid {
		phone = &phoneNull.String
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":         userID,
			"first_name": firstName,
			"last_name":  lastName,
			"role":       roleName,
			"school_id":  schoolID,
			"passport":   passport,
			"phone":      phone,
		},
	})
}

func (h *AuthHandler) generateJWT(userID, role, schoolID string) (string, error) {
	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &middleware.Claims{
		UserID:   userID,
		Role:     role,
		SchoolID: schoolID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.jwtSecret))
}
