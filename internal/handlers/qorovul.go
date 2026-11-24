package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/farzandim/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type QorovulHandler struct {
	svc *services.QorovulService
}

func NewQorovulHandler() *QorovulHandler {
	return &QorovulHandler{
		svc: services.NewQorovulService(),
	}
}

// GetSettings returns Qorovul integration settings (without exposing raw password) and mapping counts.
func (h *QorovulHandler) GetSettings(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	var username, password string
	var orgName sql.NullString
	var orgID, studentTypeID sql.NullInt64
	var isEnabled bool
	configured := false

	err := dbConn.QueryRow(`
		SELECT username, password, organization_id, organization_name, student_type_id, is_enabled
		FROM qorovul_settings
		ORDER BY id ASC LIMIT 1
	`).Scan(&username, &password, &orgID, &orgName, &studentTypeID, &isEnabled)

	if err == nil {
		configured = username != "" && password != ""
	} else if err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Qorovul sozlamalarini o'qishda xatolik", "details": err.Error()})
		return
	}

	var totalStudents, mappedStudents int
	_ = dbConn.QueryRow(`
		SELECT COUNT(*), COUNT(qorovul_person_id)
		FROM students s
		JOIN users u ON s.user_id = u.id
		WHERE s.is_deleted = false AND u.is_deleted = false
	`).Scan(&totalStudents, &mappedStudents)

	var orgIDPtr *int
	if orgID.Valid {
		v := int(orgID.Int64)
		orgIDPtr = &v
	}
	var stTypePtr *int
	if studentTypeID.Valid {
		v := int(studentTypeID.Int64)
		stTypePtr = &v
	}

	// Optionally fetch available Qorovul types if already configured
	var qorovulTypes []services.QorovulType
	if configured {
		if tok, _, tErr := h.svc.GetValidToken(dbConn); tErr == nil {
			if types, err2 := h.svc.GetTypes(tok); err2 == nil {
				qorovulTypes = types
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"configured":        configured,
		"username":          username,
		"has_password":      password != "",
		"organization_id":   orgIDPtr,
		"organization_name": orgName.String,
		"student_type_id":   stTypePtr,
		"is_enabled":        isEnabled,
		"total_students":    totalStudents,
		"mapped_students":   mappedStudents,
		"qorovul_types":     qorovulTypes,
	})
}

type SaveQorovulSettingsRequest struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	StudentTypeID *int   `json:"student_type_id"`
	IsEnabled     *bool  `json:"is_enabled"`
}

// SaveSettings validates credentials against Qorovul API, detects org/types, and saves settings.
func (h *QorovulHandler) SaveSettings(c *gin.Context) {
	var req SaveQorovulSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri so'rov formati"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	// Load existing row if any so password can be kept when editing only typeId
	var existingID int
	var existingUsername, existingPassword string
	var existingTypeID sql.NullInt64
	var existingEnabled bool
	rowExists := false

	err := dbConn.QueryRow(`
		SELECT id, username, password, student_type_id, is_enabled
		FROM qorovul_settings
		ORDER BY id ASC LIMIT 1
	`).Scan(&existingID, &existingUsername, &existingPassword, &existingTypeID, &existingEnabled)
	if err == nil {
		rowExists = true
	}

	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = existingUsername
	}
	password := req.Password
	if password == "" {
		password = existingPassword
	}
	if username == "" || password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Qorovul login va parolini kiriting"})
		return
	}

	// Test login immediately against Qorovul
	loginRes, err := h.svc.Login(username, password)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Fetch organization profile and types
	profile, _ := h.svc.GetProfile(loginRes.Token)
	types, _ := h.svc.GetTypes(loginRes.Token)

	var orgID *int
	var orgName string
	if profile != nil {
		orgID = &profile.ID
		orgName = profile.Name
	}

	// Resolve student_type_id (use requested, else existing, else auto-detect "students" / "O'quvchilar")
	var studentTypeID *int
	if req.StudentTypeID != nil && *req.StudentTypeID > 0 {
		studentTypeID = req.StudentTypeID
	} else if existingTypeID.Valid {
		v := int(existingTypeID.Int64)
		studentTypeID = &v
	} else {
		for _, t := range types {
			if t.Deleted {
				continue
			}
			sn := strings.ToLower(t.ShortName)
			nm := strings.ToLower(t.Name)
			if sn == "students" || strings.Contains(nm, "quvchi") || strings.Contains(nm, "ученик") {
				v := t.ID
				studentTypeID = &v
				break
			}
		}
	}

	isEnabled := true
	if req.IsEnabled != nil {
		isEnabled = *req.IsEnabled
	}

	if rowExists {
		_, err = dbConn.Exec(`
			UPDATE qorovul_settings
			SET username = $1, password = $2, access_token = $3, refresh_token = $4,
			    token_expires_at = NOW() + INTERVAL '30 minutes',
			    organization_id = $5, organization_name = $6, student_type_id = $7,
			    is_enabled = $8, updated_at = NOW()
			WHERE id = $9
		`, username, password, loginRes.Token, loginRes.RefreshToken, orgID, orgName, studentTypeID, isEnabled, existingID)
	} else {
		_, err = dbConn.Exec(`
			INSERT INTO qorovul_settings (username, password, access_token, refresh_token, token_expires_at, organization_id, organization_name, student_type_id, is_enabled)
			VALUES ($1, $2, $3, $4, NOW() + INTERVAL '30 minutes', $5, $6, $7, $8)
		`, username, password, loginRes.Token, loginRes.RefreshToken, orgID, orgName, studentTypeID, isEnabled)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Sozlamalarni saqlashda xatolik", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":           "Qorovul muvaffaqiyatli ulandi!",
		"organization_id":   orgID,
		"organization_name": orgName,
		"student_type_id":   studentTypeID,
		"is_enabled":        isEnabled,
		"qorovul_types":     types,
	})
}

type FarzandimStudentMappingItem struct {
	StudentID       int                       `json:"student_id"`
	FirstName       string                    `json:"first_name"`
	LastName        string                    `json:"last_name"`
	MiddleName      string                    `json:"middle_name"`
	ClassID         int                       `json:"class_id"`
	ClassName       string                    `json:"class_name"`
	INA             string                    `json:"ina"`
	QorovulPersonID *int                      `json:"qorovul_person_id"`
	QorovulPhotoURL *string                   `json:"qorovul_photo_url"`
	Suggestion      *services.MatchSuggestion `json:"suggestion"`
}

// GetMappingData returns all active Farzandim students, all Qorovul student candidates, and algorithm suggestions (read-only).
func (h *QorovulHandler) GetMappingData(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	token, studentTypeID, err := h.svc.GetValidToken(dbConn)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Fetch live persons from Qorovul
	qPersons, err := h.svc.GetAllPersons(token, studentTypeID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Qorovuldan o'quvchilar ro'yxatini olishda xatolik", "details": err.Error()})
		return
	}

	// Fetch active Farzandim students
	rows, err := dbConn.Query(`
		SELECT s.id, u.first_name, u.last_name, COALESCE(u.middle_name, ''),
		       s.class_id, COALESCE(c.name, ''), COALESCE(s.ina, ''),
		       s.qorovul_person_id, s.qorovul_photo_url
		FROM students s
		JOIN users u ON s.user_id = u.id
		LEFT JOIN classes c ON s.class_id = c.id
		WHERE s.is_deleted = false AND u.is_deleted = false
		ORDER BY c.level ASC, c.name ASC, u.last_name ASC, u.first_name ASC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "O'quvchilarni olishda xatolik", "details": err.Error()})
		return
	}
	defer rows.Close()

	var students []FarzandimStudentMappingItem
	alreadyMapped := make(map[int]bool)

	for rows.Next() {
		var item FarzandimStudentMappingItem
		var qID sql.NullInt64
		var qPhoto sql.NullString
		if err := rows.Scan(
			&item.StudentID, &item.FirstName, &item.LastName, &item.MiddleName,
			&item.ClassID, &item.ClassName, &item.INA,
			&qID, &qPhoto,
		); err != nil {
			continue
		}
		if qID.Valid {
			v := int(qID.Int64)
			item.QorovulPersonID = &v
			alreadyMapped[v] = true
		}
		if qPhoto.Valid && qPhoto.String != "" {
			v := qPhoto.String
			item.QorovulPhotoURL = &v
		}
		students = append(students, item)
	}

	// Compute algorithm suggestions ONLY for unmapped students (strictly read-only suggestion!)
	// Track suggested Qorovul IDs so two students aren't suggested the exact same person when avoidable
	usedSuggestions := make(map[int]bool)
	for k, v := range alreadyMapped {
		usedSuggestions[k] = v
	}

	for i := range students {
		if students[i].QorovulPersonID != nil {
			continue
		}
		sug := services.SuggestBestMatch(
			students[i].ClassName,
			students[i].LastName,
			students[i].FirstName,
			qPersons,
			usedSuggestions,
		)
		if sug != nil {
			students[i].Suggestion = sug
			if sug.Confidence >= 85 {
				usedSuggestions[sug.PersonID] = true
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"students":        students,
		"qorovul_persons": qPersons,
		"mapped_count":    len(alreadyMapped),
		"total_students":  len(students),
		"total_qorovul":   len(qPersons),
	})
}

type MapStudentRequest struct {
	QorovulPersonID *int    `json:"qorovul_person_id"`
	QorovulPhotoURL *string `json:"qorovul_photo_url"`
}

// MapStudent manually links or unlinks a single Farzandim student to a Qorovul person (Admin-only).
func (h *QorovulHandler) MapStudent(c *gin.Context) {
	studentID, err := strconv.Atoi(c.Param("student_id"))
	if err != nil || studentID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri o'quvchi ID"})
		return
	}

	var req MapStudentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri so'rov formati"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	// If mapping to a Qorovul person, verify that person isn't already linked to another active student
	if req.QorovulPersonID != nil && *req.QorovulPersonID > 0 {
		var otherID int
		var otherName string
		err := dbConn.QueryRow(`
			SELECT s.id, u.last_name || ' ' || u.first_name
			FROM students s
			JOIN users u ON s.user_id = u.id
			WHERE s.qorovul_person_id = $1 AND s.id <> $2 AND s.is_deleted = false
			LIMIT 1
		`, *req.QorovulPersonID, studentID).Scan(&otherID, &otherName)
		if err == nil {
			c.JSON(http.StatusConflict, gin.H{
				"error": fmt.Sprintf("Bu Qorovul profili allaqachon boshqa o'quvchiga (%s) bog'langan. Avval uni uzing.", otherName),
			})
			return
		}
	}

	res, err := dbConn.Exec(`
		UPDATE students
		SET qorovul_person_id = $1, qorovul_photo_url = $2
		WHERE id = $3 AND is_deleted = false
	`, req.QorovulPersonID, req.QorovulPhotoURL, studentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Bog'lashda xatolik", "details": err.Error()})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi"})
		return
	}

	msg := "Bog'lanish muvaffaqiyatli saqlandi"
	if req.QorovulPersonID == nil {
		msg = "Bog'lanish uzildi"
	}
	c.JSON(http.StatusOK, gin.H{"message": msg})
}

type BatchMapItem struct {
	StudentID       int     `json:"student_id" binding:"required"`
	QorovulPersonID int     `json:"qorovul_person_id" binding:"required"`
	QorovulPhotoURL *string `json:"qorovul_photo_url"`
}

type BatchMapStudentsRequest struct {
	Mappings []BatchMapItem `json:"mappings" binding:"required"`
}

// BatchMapStudents saves a list of Admin-reviewed and checked student mappings inside a transaction.
func (h *QorovulHandler) BatchMapStudents(c *gin.Context) {
	var req BatchMapStudentsRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Mappings) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Tasdiqlash uchun kamida bitta o'quvchi tanlang"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiya xatosi"})
		return
	}
	defer tx.Rollback()

	saved := 0
	for _, m := range req.Mappings {
		if m.StudentID <= 0 || m.QorovulPersonID <= 0 {
			continue
		}
		// Ensure Qorovul person is not already mapped to another active student
		var conflictName string
		err := tx.QueryRow(`
			SELECT u.last_name || ' ' || u.first_name
			FROM students s
			JOIN users u ON s.user_id = u.id
			WHERE s.qorovul_person_id = $1 AND s.id <> $2 AND s.is_deleted = false
			LIMIT 1
		`, m.QorovulPersonID, m.StudentID).Scan(&conflictName)
		if err == nil {
			c.JSON(http.StatusConflict, gin.H{
				"error": fmt.Sprintf("Qorovul ID %d allaqachon '%s' ga bog'langan", m.QorovulPersonID, conflictName),
			})
			return
		}

		res, err := tx.Exec(`
			UPDATE students
			SET qorovul_person_id = $1, qorovul_photo_url = $2
			WHERE id = $3 AND is_deleted = false
		`, m.QorovulPersonID, m.QorovulPhotoURL, m.StudentID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Saqlashda xatolik", "details": err.Error()})
			return
		}
		if n, _ := res.RowsAffected(); n > 0 {
			saved++
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiyani yakunlashda xatolik"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("%d ta o'quvchi Qorovul bilan muvaffaqiyatli bog'landi", saved),
		"saved":   saved,
	})
}
