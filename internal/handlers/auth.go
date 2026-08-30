package handlers

import (
	"database/sql"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/db"
	"github.com/farzandim/backend/internal/middleware"
	"github.com/farzandim/backend/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var bcryptSemaphore chan struct{}

func init() {
	// Limit bcrypt operations to half of the CPU cores (minimum 2)
	cores := runtime.GOMAXPROCS(0)
	limit := cores / 2
	if limit < 2 {
		limit = 2
	}
	bcryptSemaphore = make(chan struct{}, limit)
}

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
	Phone      string `json:"phone"`
	DocumentNo string `json:"document_no"`
	Password   string `json:"password" binding:"required"`
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
		"UPDATE users SET password_hash = $1, password_reset_required = false, updated_at = NOW() WHERE id = $2",
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
		`SELECT id, password_hash, first_name, last_name FROM super_admins 
		 WHERE (phone = $1 OR REGEXP_REPLACE(phone, '\D', '', 'g') = REGEXP_REPLACE($1, '\D', '', 'g')) AND is_deleted = false`,
		req.Phone,
	).Scan(&id, &passwordHash, &firstName, &lastName)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Ushbu telefon raqamiga ega Super Admin topilmadi", "error_code": "USER_NOT_FOUND"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query Super Admin", "details": err.Error()})
		return
	}

	bcryptSemaphore <- struct{}{}
	err = bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password))
	<-bcryptSemaphore

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Kiritilgan parol noto'g'ri", "error_code": "INVALID_PASSWORD"})
		return
	}

	// Generate superadmin JWT tokens (Access: 24h, Refresh: 365d)
	token, err := h.generateJWT(id.String(), "SUPER_ADMIN", "", "access", 24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue auth token"})
		return
	}
	refreshToken, err := h.generateJWT(id.String(), "SUPER_ADMIN", "", "refresh", 365*24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue refresh token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":         token,
		"refresh_token": refreshToken,
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
	var passportNull, phoneNull, docNoNull sql.NullString
	var passwordResetRequired bool

	docNoClean := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(req.DocumentNo), " ", ""))
	phoneClean := strings.TrimSpace(req.Phone)

	// If phone input contains a passport format (e.g. AD1234567 or AA 1234567), treat it as document_no
	if docNoClean == "" && phoneClean != "" && regexp.MustCompile(`(?i)^[A-Z]{2}\s*\d{7}$`).MatchString(phoneClean) {
		docNoClean = strings.ToUpper(strings.ReplaceAll(phoneClean, " ", ""))
	}

	var err error
	if docNoClean != "" {
		query := `
			SELECT u.id, u.password_hash, u.first_name, u.last_name, r.name, u.passport, u.phone, u.document_no, u.password_reset_required 
			FROM users u 
			JOIN roles r ON u.role_id = r.id 
			WHERE (
				UPPER(TRIM(u.document_no)) = $1 
				OR UPPER(TRIM(u.passport)) = $1 
			) AND u.is_deleted = false`
		err = tenantDB.QueryRow(query, docNoClean).Scan(&userID, &passwordHash, &firstName, &lastName, &roleName, &passportNull, &phoneNull, &docNoNull, &passwordResetRequired)
		if err != nil {
			if err == sql.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{
					"error":      "Ushbu pasport seriyasiga ega foydalanuvchi topilmadi",
					"error_code": "PASSPORT_NOT_FOUND",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query tenant user", "details": err.Error()})
			return
		}
	} else if phoneClean != "" {
		normPhone := utils.NormalizePhone(phoneClean)
		query := `
			SELECT u.id, u.password_hash, u.first_name, u.last_name, r.name, u.passport, u.phone, u.document_no, u.password_reset_required 
			FROM users u 
			JOIN roles r ON u.role_id = r.id 
			WHERE (u.phone = $1 OR REGEXP_REPLACE(u.phone, '\D', '', 'g') = $1) AND u.is_deleted = false`
		err = tenantDB.QueryRow(query, normPhone).Scan(&userID, &passwordHash, &firstName, &lastName, &roleName, &passportNull, &phoneNull, &docNoNull, &passwordResetRequired)
		if err != nil {
			if err == sql.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{
					"error":      "Ushbu telefon raqamiga ega foydalanuvchi topilmadi",
					"error_code": "PHONE_NOT_FOUND",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query tenant user", "details": err.Error()})
			return
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Pasport seriyasi yoki telefon raqami kiritilishi shart"})
		return
	}

	bcryptSemaphore <- struct{}{}
	err = bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password))
	<-bcryptSemaphore

	if err != nil {
		// Fallback for parents created during past imports with auto-generated passwords:
		// If role is PARENT and password entered is default "123" or "123456", update hash to req.Password and allow login!
		if roleName == "PARENT" && (req.Password == "123" || req.Password == "123456") {
			newHash, hashErr := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
			if hashErr == nil {
				_, _ = tenantDB.Exec("UPDATE users SET password_hash = $1 WHERE id = $2", string(newHash), userID)
				err = nil
			}
		}
	}

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "Kiritilgan parol noto'g'ri",
			"error_code": "INVALID_PASSWORD",
		})
		return
	}

	// Generate tenant user JWT tokens (Access: 24h, Refresh: 365d)
	token, err := h.generateJWT(strconv.Itoa(userID), roleName, schoolID, "access", 24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue auth token"})
		return
	}
	refreshToken, err := h.generateJWT(strconv.Itoa(userID), roleName, schoolID, "refresh", 365*24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue refresh token"})
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
		"token":         token,
		"refresh_token": refreshToken,
		"user": gin.H{
			"id":                      userID,
			"first_name":              firstName,
			"last_name":               lastName,
			"role":                    roleName,
			"school_id":               schoolID,
			"passport":                passport,
			"phone":                   phone,
			"password_reset_required": passwordResetRequired,
		},
	})
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Refresh token is required"})
		return
	}

	claims := &middleware.Claims{}
	token, err := jwt.ParseWithClaims(req.RefreshToken, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(h.jwtSecret), nil
	})

	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
		return
	}

	if claims.TokenType != "refresh" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Token is not a valid refresh token"})
		return
	}

	// Super Admin refresh
	if claims.Role == "SUPER_ADMIN" {
		var exists bool
		err := db.CentralDB.QueryRow("SELECT EXISTS(SELECT 1 FROM super_admins WHERE id = $1 AND is_deleted = false)", claims.UserID).Scan(&exists)
		if err != nil || !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Super admin account inactive or deleted"})
			return
		}

		newAccessToken, err := h.generateJWT(claims.UserID, claims.Role, claims.SchoolID, "access", 24*time.Hour)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue access token"})
			return
		}
		newRefreshToken, err := h.generateJWT(claims.UserID, claims.Role, claims.SchoolID, "refresh", 365*24*time.Hour)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue refresh token"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"token":         newAccessToken,
			"refresh_token": newRefreshToken,
		})
		return
	}

	// Tenant User refresh
	if claims.SchoolID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "School context missing in refresh token"})
		return
	}

	tDB, err := db.TenantConnManager.GetTenantDB(claims.SchoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to school database"})
		return
	}

	userID, err := strconv.Atoi(claims.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID in token"})
		return
	}

	var userActive bool
	err = tDB.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND is_deleted = false)", userID).Scan(&userActive)
	if err != nil || !userActive {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Foydalanuvchi hisobi o'chirilgan yoki faol emas"})
		return
	}

	newAccessToken, err := h.generateJWT(claims.UserID, claims.Role, claims.SchoolID, "access", 24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue access token"})
		return
	}
	newRefreshToken, err := h.generateJWT(claims.UserID, claims.Role, claims.SchoolID, "refresh", 365*24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue refresh token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":         newAccessToken,
		"refresh_token": newRefreshToken,
	})
}

func (h *AuthHandler) generateJWT(userID, role, schoolID, tokenType string, duration time.Duration) (string, error) {
	expirationTime := time.Now().Add(duration)
	claims := &middleware.Claims{
		UserID:    userID,
		Role:      role,
		SchoolID:  schoolID,
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.jwtSecret))
}
