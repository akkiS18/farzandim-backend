package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	var orgID, studentTypeID, teacherTypeID sql.NullInt64
	var lastSyncedAt sql.NullTime
	var isEnabled bool
	configured := false

	err := dbConn.QueryRow(`
		SELECT username, password, organization_id, organization_name, student_type_id, teacher_type_id, is_enabled, last_synced_at
		FROM qorovul_settings
		ORDER BY id ASC LIMIT 1
	`).Scan(&username, &password, &orgID, &orgName, &studentTypeID, &teacherTypeID, &isEnabled, &lastSyncedAt)

	if err == nil {
		configured = username != "" && password != ""
	} else if err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Qorovul sozlamalarini o'qishda xatolik", "details": err.Error()})
		return
	}

	var totalStudents, mappedStudents int
	_ = dbConn.QueryRow(`
		SELECT COUNT(*), COUNT(s.qorovul_person_id)
		FROM students s
		JOIN users u ON s.user_id = u.id
		WHERE s.is_deleted = false AND u.is_deleted = false
	`).Scan(&totalStudents, &mappedStudents)

	var totalTeachers, mappedTeachers int
	_ = dbConn.QueryRow(`
		SELECT COUNT(*), COUNT(u.qorovul_person_id)
		FROM users u
		JOIN roles r ON u.role_id = r.id
		WHERE u.is_deleted = false AND r.name IN ('MAIN_TEACHER', 'SUBJECT_TEACHER')
	`).Scan(&totalTeachers, &mappedTeachers)

	var orgIDPtr, stTypePtr, tTypePtr *int
	if orgID.Valid {
		v := int(orgID.Int64)
		orgIDPtr = &v
	}
	if studentTypeID.Valid {
		v := int(studentTypeID.Int64)
		stTypePtr = &v
	}
	if teacherTypeID.Valid {
		v := int(teacherTypeID.Int64)
		tTypePtr = &v
	}
	var lastSyncPtr *time.Time
	if lastSyncedAt.Valid {
		v := lastSyncedAt.Time
		lastSyncPtr = &v
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
		"teacher_type_id":   tTypePtr,
		"is_enabled":        isEnabled,
		"last_synced_at":    lastSyncPtr,
		"total_students":    totalStudents,
		"mapped_students":   mappedStudents,
		"total_teachers":    totalTeachers,
		"mapped_teachers":   mappedTeachers,
		"qorovul_types":     qorovulTypes,
	})
}

type SaveQorovulSettingsRequest struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	StudentTypeID *int   `json:"student_type_id"`
	TeacherTypeID *int   `json:"teacher_type_id"`
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
	var existingTypeID, existingTeacherTypeID sql.NullInt64
	var existingEnabled bool
	rowExists := false

	err := dbConn.QueryRow(`
		SELECT id, username, password, student_type_id, teacher_type_id, is_enabled
		FROM qorovul_settings
		ORDER BY id ASC LIMIT 1
	`).Scan(&existingID, &existingUsername, &existingPassword, &existingTypeID, &existingTeacherTypeID, &existingEnabled)
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

	// Resolve student_type_id
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

	// Resolve teacher_type_id
	var teacherTypeID *int
	if req.TeacherTypeID != nil && *req.TeacherTypeID > 0 {
		teacherTypeID = req.TeacherTypeID
	} else if existingTeacherTypeID.Valid {
		v := int(existingTeacherTypeID.Int64)
		teacherTypeID = &v
	} else {
		for _, t := range types {
			if t.Deleted {
				continue
			}
			sn := strings.ToLower(t.ShortName)
			nm := strings.ToLower(t.Name)
			if sn == "teachers" || strings.Contains(nm, "qituvchi") || strings.Contains(nm, "учител") {
				v := t.ID
				teacherTypeID = &v
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
			    organization_id = $5, organization_name = $6, student_type_id = $7, teacher_type_id = $8,
			    is_enabled = $9, updated_at = NOW()
			WHERE id = $10
		`, username, password, loginRes.Token, loginRes.RefreshToken, orgID, orgName, studentTypeID, teacherTypeID, isEnabled, existingID)
	} else {
		_, err = dbConn.Exec(`
			INSERT INTO qorovul_settings (username, password, access_token, refresh_token, token_expires_at, organization_id, organization_name, student_type_id, teacher_type_id, is_enabled)
			VALUES ($1, $2, $3, $4, NOW() + INTERVAL '30 minutes', $5, $6, $7, $8, $9)
		`, username, password, loginRes.Token, loginRes.RefreshToken, orgID, orgName, studentTypeID, teacherTypeID, isEnabled)
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
		"teacher_type_id":   teacherTypeID,
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


func uzTodayStr() string {
	loc := time.FixedZone("Asia/Tashkent", 5*3600)
	return time.Now().In(loc).Format("2006-01-02")
}

func formatHHMM(t sql.NullTime) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.Format("15:04")
	return &s
}

// SyncNow manually triggers a Qorovul gate attendance sync for the specified date (default: today).
func (h *QorovulHandler) SyncNow(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	dateStr := strings.TrimSpace(c.Query("date"))
	if dateStr == "" {
		dateStr = uzTodayStr()
	}

	synced, err := h.svc.SyncGateAttendanceForTenant(dbConn, dateStr)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Qorovul bilan sinxronlashda xatolik", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("%s sanasi uchun %d ta turniket yozuvi sinxronlandi", dateStr, synced),
		"synced":  synced,
		"date":    dateStr,
	})
}

type GateAttendanceItem struct {
	Mapped       bool    `json:"mapped"`
	IncomingTime *string `json:"incoming_time"`
	OutgoingTime *string `json:"outgoing_time"`
	LateMinutes  int     `json:"late_minutes"`
	EarlyMinutes int     `json:"early_minutes"`
	IsInside     bool    `json:"is_inside"`
}

// GetClassGateAttendance returns gate scan times for students of a class on a given date (for Teacher Journal).
func (h *QorovulHandler) GetClassGateAttendance(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	classIDStr := c.Query("class_id")
	classID, err := strconv.Atoi(classIDStr)
	if err != nil || classID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "class_id kerak"})
		return
	}

	dateStr := strings.TrimSpace(c.Query("date"))
	if dateStr == "" {
		dateStr = uzTodayStr()
	}

	var isEnabled bool
	var lastSynced sql.NullTime
	err = dbConn.QueryRow("SELECT is_enabled, last_synced_at FROM qorovul_settings ORDER BY id ASC LIMIT 1").Scan(&isEnabled, &lastSynced)
	if err == sql.ErrNoRows || !isEnabled {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "date": dateStr, "items": map[string]GateAttendanceItem{}})
		return
	}

	// Opportunistic refresh if viewing today and last sync is > 2 minutes old
	if dateStr == uzTodayStr() && (!lastSynced.Valid || time.Since(lastSynced.Time) > 2*time.Minute) {
		_, _ = h.svc.SyncGateAttendanceForTenant(dbConn, dateStr)
	}

	rows, err := dbConn.Query(`
		SELECT s.id, (s.qorovul_person_id IS NOT NULL) AS mapped,
		       g.incoming_time, g.outgoing_time,
		       COALESCE(g.late_minutes, 0), COALESCE(g.early_minutes, 0), COALESCE(g.is_inside, false)
		FROM students s
		LEFT JOIN gate_attendance_logs g
		       ON g.student_id = s.id AND g.log_date = $1::date
		WHERE s.class_id = $2 AND s.is_deleted = false
	`, dateStr, classID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Turniket ma'lumotlarini olishda xatolik"})
		return
	}
	defer rows.Close()

	items := make(map[string]GateAttendanceItem)
	for rows.Next() {
		var stID int
		var mapped, isInside bool
		var inTime, outTime sql.NullTime
		var lateMin, earlyMin int
		if err := rows.Scan(&stID, &mapped, &inTime, &outTime, &lateMin, &earlyMin, &isInside); err != nil {
			continue
		}
		items[strconv.Itoa(stID)] = GateAttendanceItem{
			Mapped:       mapped,
			IncomingTime: formatHHMM(inTime),
			OutgoingTime: formatHHMM(outTime),
			LateMinutes:  lateMin,
			EarlyMinutes: earlyMin,
			IsInside:     isInside,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled": true,
		"date":    dateStr,
		"items":   items,
	})
}

type LiveGateRecord struct {
	ID              int     `json:"id"` // student_id or user_id
	FullName        string  `json:"full_name"`
	SubLabel        string  `json:"sub_label"` // class_name or role
	PhotoURL        *string `json:"photo_url"`
	QorovulPersonID int     `json:"qorovul_person_id"`
	IncomingTime    *string `json:"incoming_time"`
	OutgoingTime    *string `json:"outgoing_time"`
	LateMinutes     int     `json:"late_minutes"`
	EarlyMinutes    int     `json:"early_minutes"`
	IsInside        bool    `json:"is_inside"`
	Status          string  `json:"status"` // "inside", "left", "absent"
}

// GetAdminLiveGate returns live gate attendance summary, records (students or teachers), and terminal status.
func (h *QorovulHandler) GetAdminLiveGate(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	dateStr := strings.TrimSpace(c.Query("date"))
	if dateStr == "" {
		dateStr = uzTodayStr()
	}
	roleFilter := strings.ToUpper(strings.TrimSpace(c.Query("role")))
	if roleFilter != "TEACHER" {
		roleFilter = "STUDENT"
	}

	var isEnabled bool
	var lastSynced sql.NullTime
	err := dbConn.QueryRow("SELECT is_enabled, last_synced_at FROM qorovul_settings ORDER BY id ASC LIMIT 1").Scan(&isEnabled, &lastSynced)
	if err == sql.ErrNoRows || !isEnabled {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "date": dateStr, "records": []LiveGateRecord{}})
		return
	}

	// Sync on demand if viewing today and > 2 min since last sync (or force=1)
	force := c.Query("force") == "1"
	if force || (dateStr == uzTodayStr() && (!lastSynced.Valid || time.Since(lastSynced.Time) > 2*time.Minute)) {
		_, _ = h.svc.SyncGateAttendanceForTenant(dbConn, dateStr)
		_ = dbConn.QueryRow("SELECT last_synced_at FROM qorovul_settings ORDER BY id ASC LIMIT 1").Scan(&lastSynced)
	}

	// Terminal health check
	var terminals *services.QorovulTerminalsSummary
	if tok, _, tErr := h.svc.GetValidToken(dbConn); tErr == nil {
		if ts, err2 := h.svc.GetTerminalsSummary(tok); err2 == nil {
			terminals = ts
		}
	}

	var query string
	if roleFilter == "TEACHER" {
		query = `
			SELECT u.id, u.last_name || ' ' || u.first_name || COALESCE(' ' || u.middle_name, ''),
			       CASE WHEN r.name = 'MAIN_TEACHER' THEN 'Sinf rahbari' ELSE 'Fan o''qituvchisi' END,
			       u.qorovul_photo_url, u.qorovul_person_id,
			       g.incoming_time, g.outgoing_time,
			       COALESCE(g.late_minutes, 0), COALESCE(g.early_minutes, 0), COALESCE(g.is_inside, false)
			FROM users u
			JOIN roles r ON u.role_id = r.id
			LEFT JOIN gate_attendance_logs g ON g.user_id = u.id AND g.log_date = $1::date
			WHERE u.is_deleted = false AND u.qorovul_person_id IS NOT NULL
			  AND r.name IN ('MAIN_TEACHER', 'SUBJECT_TEACHER')
			ORDER BY (g.incoming_time IS NULL) ASC, g.incoming_time ASC, u.last_name ASC
		`
	} else {
		query = `
			SELECT s.id, u.last_name || ' ' || u.first_name || COALESCE(' ' || u.middle_name, ''),
			       COALESCE(cl.name, ''),
			       s.qorovul_photo_url, s.qorovul_person_id,
			       g.incoming_time, g.outgoing_time,
			       COALESCE(g.late_minutes, 0), COALESCE(g.early_minutes, 0), COALESCE(g.is_inside, false)
			FROM students s
			JOIN users u ON s.user_id = u.id
			LEFT JOIN classes cl ON s.class_id = cl.id
			LEFT JOIN gate_attendance_logs g ON g.student_id = s.id AND g.log_date = $1::date
			WHERE s.is_deleted = false AND u.is_deleted = false AND s.qorovul_person_id IS NOT NULL
			ORDER BY cl.level ASC, cl.name ASC, (g.incoming_time IS NULL) ASC, u.last_name ASC
		`
	}

	rows, err := dbConn.Query(query, dateStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Jonli davomatni olishda xatolik", "details": err.Error()})
		return
	}
	defer rows.Close()

	records := []LiveGateRecord{}
	presentCount, insideCount, lateCount, absentCount := 0, 0, 0, 0

	for rows.Next() {
		var rec LiveGateRecord
		var photoNull sql.NullString
		var inTime, outTime sql.NullTime
		if err := rows.Scan(
			&rec.ID, &rec.FullName, &rec.SubLabel, &photoNull, &rec.QorovulPersonID,
			&inTime, &outTime, &rec.LateMinutes, &rec.EarlyMinutes, &rec.IsInside,
		); err != nil {
			continue
		}
		if photoNull.Valid && photoNull.String != "" {
			v := photoNull.String
			rec.PhotoURL = &v
		}
		rec.IncomingTime = formatHHMM(inTime)
		rec.OutgoingTime = formatHHMM(outTime)

		if rec.IncomingTime == nil && rec.OutgoingTime == nil {
			rec.Status = "absent"
			absentCount++
		} else {
			presentCount++
			if rec.LateMinutes > 0 {
				lateCount++
			}
			if rec.IsInside || rec.OutgoingTime == nil {
				rec.Status = "inside"
				insideCount++
			} else {
				rec.Status = "left"
			}
		}
		records = append(records, rec)
	}

	var lastSyncPtr *time.Time
	if lastSynced.Valid {
		v := lastSynced.Time
		lastSyncPtr = &v
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled":        true,
		"date":           dateStr,
		"role":           roleFilter,
		"last_synced_at": lastSyncPtr,
		"terminals":      terminals,
		"summary": gin.H{
			"total_mapped":  len(records),
			"present_count": presentCount,
			"inside_count":  insideCount,
			"late_count":    lateCount,
			"absent_count":  absentCount,
		},
		"records": records,
	})
}

type ParentGateDay struct {
	Date         string  `json:"date"`
	IncomingTime *string `json:"incoming_time"`
	OutgoingTime *string `json:"outgoing_time"`
	LateMinutes  int     `json:"late_minutes"`
	IsInside     bool    `json:"is_inside"`
}

// GetParentGateAttendance returns today's gate status and recent history for a parent's child.
func (h *QorovulHandler) GetParentGateAttendance(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	studentID, err := strconv.Atoi(c.Query("student_id"))
	if err != nil || studentID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "student_id kerak"})
		return
	}

	userRole, _ := c.Get("role")
	userIDVal, _ := c.Get("userID")
	currentUserID, _ := strconv.Atoi(fmt.Sprintf("%v", userIDVal))

	if userRole == "PARENT" {
		var owns bool
		_ = dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM student_parents WHERE student_id = $1 AND parent_id = $2)", studentID, currentUserID).Scan(&owns)
		if !owns {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan"})
			return
		}
	}

	var isEnabled bool
	var lastSynced sql.NullTime
	err = dbConn.QueryRow("SELECT is_enabled, last_synced_at FROM qorovul_settings ORDER BY id ASC LIMIT 1").Scan(&isEnabled, &lastSynced)
	if err == sql.ErrNoRows || !isEnabled {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "mapped": false, "history": []ParentGateDay{}})
		return
	}

	var qPersonID sql.NullInt64
	_ = dbConn.QueryRow("SELECT qorovul_person_id FROM students WHERE id = $1 AND is_deleted = false", studentID).Scan(&qPersonID)
	if !qPersonID.Valid {
		c.JSON(http.StatusOK, gin.H{"enabled": true, "mapped": false, "history": []ParentGateDay{}})
		return
	}

	today := uzTodayStr()
	if !lastSynced.Valid || time.Since(lastSynced.Time) > 3*time.Minute {
		_, _ = h.svc.SyncGateAttendanceForTenant(dbConn, today)
	}

	days := 14
	if dParam, err := strconv.Atoi(c.Query("days")); err == nil && dParam > 0 && dParam <= 60 {
		days = dParam
	}

	rows, err := dbConn.Query(`
		SELECT TO_CHAR(log_date, 'YYYY-MM-DD'), incoming_time, outgoing_time,
		       COALESCE(late_minutes, 0), COALESCE(is_inside, false)
		FROM gate_attendance_logs
		WHERE student_id = $1 AND log_date >= ($2::date - ($3 || ' days')::interval)
		ORDER BY log_date DESC
	`, studentID, today, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Turniket tarixini olishda xatolik"})
		return
	}
	defer rows.Close()

	history := []ParentGateDay{}
	var todayRec *ParentGateDay
	for rows.Next() {
		var item ParentGateDay
		var inTime, outTime sql.NullTime
		if err := rows.Scan(&item.Date, &inTime, &outTime, &item.LateMinutes, &item.IsInside); err != nil {
			continue
		}
		item.IncomingTime = formatHHMM(inTime)
		item.OutgoingTime = formatHHMM(outTime)
		history = append(history, item)
		if item.Date == today {
			copyItem := item
			todayRec = &copyItem
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled": true,
		"mapped":  true,
		"today":   todayRec,
		"history": history,
	})
}

// --- #5: Teacher Mapping (Admin-only, assisted manual) ---

type FarzandimTeacherMappingItem struct {
	UserID          int                       `json:"user_id"`
	FirstName       string                    `json:"first_name"`
	LastName        string                    `json:"last_name"`
	MiddleName      string                    `json:"middle_name"`
	Phone           string                    `json:"phone"`
	RoleName        string                    `json:"role_name"`
	QorovulPersonID *int                      `json:"qorovul_person_id"`
	QorovulPhotoURL *string                   `json:"qorovul_photo_url"`
	Suggestion      *services.MatchSuggestion `json:"suggestion"`
}

func (h *QorovulHandler) GetTeacherMappingData(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	token, _, teacherTypeID, err := h.svc.GetValidTokenAndTypes(dbConn)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	qPersons, err := h.svc.GetAllPersons(token, teacherTypeID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Qorovuldan o'qituvchilar ro'yxatini olishda xatolik", "details": err.Error()})
		return
	}

	rows, err := dbConn.Query(`
		SELECT u.id, u.first_name, u.last_name, COALESCE(u.middle_name, ''),
		       COALESCE(u.phone, ''), r.name, u.qorovul_person_id, u.qorovul_photo_url
		FROM users u
		JOIN roles r ON u.role_id = r.id
		WHERE u.is_deleted = false AND r.name IN ('MAIN_TEACHER', 'SUBJECT_TEACHER')
		ORDER BY u.last_name ASC, u.first_name ASC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "O'qituvchilarni olishda xatolik"})
		return
	}
	defer rows.Close()

	var teachers []FarzandimTeacherMappingItem
	alreadyMapped := make(map[int]bool)

	for rows.Next() {
		var item FarzandimTeacherMappingItem
		var qID sql.NullInt64
		var qPhoto sql.NullString
		if err := rows.Scan(
			&item.UserID, &item.FirstName, &item.LastName, &item.MiddleName,
			&item.Phone, &item.RoleName, &qID, &qPhoto,
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
		teachers = append(teachers, item)
	}

	usedSuggestions := make(map[int]bool)
	for k, v := range alreadyMapped {
		usedSuggestions[k] = v
	}

	for i := range teachers {
		if teachers[i].QorovulPersonID != nil {
			continue
		}
		sug := services.SuggestBestMatch("", teachers[i].LastName, teachers[i].FirstName, qPersons, usedSuggestions)
		if sug != nil {
			teachers[i].Suggestion = sug
			if sug.Confidence >= 85 {
				usedSuggestions[sug.PersonID] = true
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"teachers":        teachers,
		"qorovul_persons": qPersons,
		"mapped_count":    len(alreadyMapped),
		"total_teachers":  len(teachers),
	})
}

func (h *QorovulHandler) MapTeacher(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri o'qituvchi ID"})
		return
	}

	var req MapStudentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri so'rov formati"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	if req.QorovulPersonID != nil && *req.QorovulPersonID > 0 {
		var otherName string
		err := dbConn.QueryRow(`
			SELECT last_name || ' ' || first_name FROM users
			WHERE qorovul_person_id = $1 AND id <> $2 AND is_deleted = false LIMIT 1
		`, *req.QorovulPersonID, userID).Scan(&otherName)
		if err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Bu Qorovul profili allaqachon %s ga bog'langan", otherName)})
			return
		}
	}

	res, err := dbConn.Exec(`
		UPDATE users SET qorovul_person_id = $1, qorovul_photo_url = $2, updated_at = NOW()
		WHERE id = $3 AND is_deleted = false
	`, req.QorovulPersonID, req.QorovulPhotoURL, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Saqlashda xatolik", "details": err.Error()})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "O'qituvchi topilmadi"})
		return
	}

	msg := "O'qituvchi Qorovul bilan bog'landi"
	if req.QorovulPersonID == nil {
		msg = "Bog'lanish uzildi"
	}
	c.JSON(http.StatusOK, gin.H{"message": msg})
}

type BatchMapTeacherItem struct {
	UserID          int     `json:"user_id" binding:"required"`
	QorovulPersonID int     `json:"qorovul_person_id" binding:"required"`
	QorovulPhotoURL *string `json:"qorovul_photo_url"`
}

func (h *QorovulHandler) BatchMapTeachers(c *gin.Context) {
	var req struct {
		Mappings []BatchMapTeacherItem `json:"mappings" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Mappings) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Tasdiqlash uchun o'qituvchi tanlang"})
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
		var otherName string
		if tx.QueryRow(`SELECT last_name || ' ' || first_name FROM users WHERE qorovul_person_id = $1 AND id <> $2 AND is_deleted = false LIMIT 1`, m.QorovulPersonID, m.UserID).Scan(&otherName) == nil {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Qorovul ID %d allaqachon %s ga bog'langan", m.QorovulPersonID, otherName)})
			return
		}
		res, err := tx.Exec(`UPDATE users SET qorovul_person_id = $1, qorovul_photo_url = $2, updated_at = NOW() WHERE id = $3 AND is_deleted = false`, m.QorovulPersonID, m.QorovulPhotoURL, m.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Saqlashda xatolik"})
			return
		}
		if n, _ := res.RowsAffected(); n > 0 {
			saved++
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiya yakunlanmadi"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("%d ta o'qituvchi muvaffaqiyatli bog'landi", saved), "saved": saved})
}

// --- #5: Push Missing Student from Farzandim -> Qorovul ---

func (h *QorovulHandler) PushStudentToQorovul(c *gin.Context) {
	studentID, err := strconv.Atoi(c.Param("student_id"))
	if err != nil || studentID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri o'quvchi ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	var firstName, lastName, middleName, className string
	var existingQID sql.NullInt64
	err = dbConn.QueryRow(`
		SELECT u.first_name, u.last_name, COALESCE(u.middle_name, ''), COALESCE(cl.name, ''), s.qorovul_person_id
		FROM students s
		JOIN users u ON s.user_id = u.id
		LEFT JOIN classes cl ON s.class_id = cl.id
		WHERE s.id = $1 AND s.is_deleted = false
	`, studentID).Scan(&firstName, &lastName, &middleName, &className, &existingQID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi"})
		return
	}
	if existingQID.Valid {
		c.JSON(http.StatusConflict, gin.H{"error": "Bu o'quvchi allaqachon Qorovul bilan bog'langan"})
		return
	}

	token, studentTypeID, _, err := h.svc.GetValidTokenAndTypes(dbConn)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if studentTypeID == nil || *studentTypeID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Qorovul sozlamalarida O'quvchilar turi (typeId) tanlanmagan"})
		return
	}

	// Find matching Qorovul direction by class name
	directions, err := h.svc.GetDirections(token, studentTypeID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Qorovul sinflarini (Directions) olib bo'lmadi", "details": err.Error()})
		return
	}

	targetNorm := services.NormalizeClassName(className)
	var matchedDir *services.QorovulDirection
	for i := range directions {
		if services.NormalizeClassName(directions[i].Name) == targetNorm {
			matchedDir = &directions[i]
			break
		}
	}
	if matchedDir == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Qorovulda '%s' sinfiga mos yo'nalish (Direction) topilmadi. Avval Qorovulda shu sinfni yarating.", className)})
		return
	}

	graphicID := matchedDir.GraphicID
	if graphicID == 0 {
		if graphics, gErr := h.svc.GetGraphics(token); gErr == nil && len(graphics) > 0 {
			graphicID = graphics[0].ID
		}
	}

	// Guess gender from middle_name ("qizi" -> Female, else Male)
	gender := "Male"
	if strings.Contains(strings.ToLower(middleName), "qizi") || strings.HasSuffix(strings.ToLower(lastName), "ova") || strings.HasSuffix(strings.ToLower(lastName), "eva") {
		gender = "Female"
	}

	newPersonID, err := h.svc.AddPerson(token, services.QorovulAddPersonRequest{
		FirstName:   firstName,
		LastName:    lastName,
		MiddleName:  middleName,
		Gender:      gender,
		TypeID:      *studentTypeID,
		DirectionID: matchedDir.ID,
		GraphicID:   graphicID,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// If Qorovul returned the new ID, save mapping immediately
	if newPersonID > 0 {
		_, _ = dbConn.Exec("UPDATE students SET qorovul_person_id = $1 WHERE id = $2", newPersonID, studentID)
	}

	c.JSON(http.StatusOK, gin.H{
		"message":           fmt.Sprintf("%s %s Qorovulga (%s) muvaffaqiyatli qo'shildi!", lastName, firstName, matchedDir.Name),
		"qorovul_person_id": newPersonID,
	})
}
