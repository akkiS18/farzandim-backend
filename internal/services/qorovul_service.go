package services

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const QorovulBaseURL = "https://api.qorovul.uz/api/v1"

type QorovulService struct {
	httpClient *http.Client
}

func NewQorovulService() *QorovulService {
	return &QorovulService{
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

type QorovulLoginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	Role         string `json:"role"`
	ExpiresAt    string `json:"expiresAt"`
	Active       bool   `json:"active"`
}

type QorovulProfile struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	DirectorName   string `json:"directorName"`
	Login          string `json:"login"`
	Active         bool   `json:"active"`
	BusinessSphere string `json:"businessSphere"`
}

type QorovulType struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	Deleted   bool   `json:"deleted"`
}

type QorovulPerson struct {
	ID            int     `json:"id"`
	FirstName     string  `json:"firstname"`
	LastName      string  `json:"lastname"`
	MiddleName    *string `json:"middlename"`
	DirectionID   int     `json:"directionId"`
	DirectionName string  `json:"directionName"`
	TypeID        int     `json:"typeId"`
	TypeName      string  `json:"typeName"`
	PhotoURL      *string `json:"photoUrl"`
	PhotoStatus   int     `json:"photoStatus"`
	PhoneNumber   string  `json:"phoneNumber"`
	Gender        string  `json:"gender"`
	Deleted       bool    `json:"deleted"`
}

type QorovulPagination struct {
	CurrentPage int  `json:"currentPage"`
	PageSize    int  `json:"pageSize"`
	TotalCount  int  `json:"totalCount"`
	TotalPages  int  `json:"totalPages"`
	HasNext     bool `json:"hasNext"`
}

type QorovulPersonListResponse struct {
	Data       []QorovulPerson   `json:"data"`
	Pagination QorovulPagination `json:"pagination"`
}

func (s *QorovulService) Login(username, password string) (*QorovulLoginResponse, error) {
	payload, _ := json.Marshal(map[string]string{
		"username": strings.TrimSpace(username),
		"password": password,
	})
	resp, err := s.httpClient.Post(QorovulBaseURL+"/Auth/Login", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("Qorovul serveriga ulanib bo'lmadi: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Qorovul login xatosi (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var res QorovulLoginResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("Qorovul login javobini o'qishda xatolik: %w", err)
	}
	if res.Token == "" {
		return nil, fmt.Errorf("Qorovul login token qaytarmadi (2FA/SMS talab qilingan bo'lishi mumkin)")
	}
	return &res, nil
}

func (s *QorovulService) RefreshToken(refreshToken string) (*QorovulLoginResponse, error) {
	payload, _ := json.Marshal(map[string]string{
		"refreshToken": refreshToken,
	})
	resp, err := s.httpClient.Post(QorovulBaseURL+"/Auth/RefreshToken", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed (%d): %s", resp.StatusCode, string(body))
	}
	var res QorovulLoginResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func parseQorovulTime(ts string) *time.Time {
	if ts == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.9999999-07:00",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, ts); err == nil {
			u := t.UTC()
			return &u
		}
	}
	// Fallback: 30 minutes from now
	t := time.Now().UTC().Add(30 * time.Minute)
	return &t
}

// GetValidToken loads credentials from qorovul_settings, refreshes or re-logs in if needed, and returns a valid token.
func (s *QorovulService) GetValidToken(db *sql.DB) (string, *int, error) {
	var id int
	var username, password string
	var accessToken, refreshToken sql.NullString
	var expiresAt sql.NullTime
	var studentTypeID sql.NullInt64

	err := db.QueryRow(`
		SELECT id, username, password, access_token, refresh_token, token_expires_at, student_type_id
		FROM qorovul_settings
		ORDER BY id ASC LIMIT 1
	`).Scan(&id, &username, &password, &accessToken, &refreshToken, &expiresAt, &studentTypeID)
	if err == sql.ErrNoRows {
		return "", nil, fmt.Errorf("Qorovul sozlamalari kiritilmagan")
	}
	if err != nil {
		return "", nil, err
	}

	var stTypePtr *int
	if studentTypeID.Valid {
		v := int(studentTypeID.Int64)
		stTypePtr = &v
	}

	// Return cached token if still valid for at least 2 minutes
	if accessToken.Valid && accessToken.String != "" && expiresAt.Valid && time.Now().UTC().Add(2*time.Minute).Before(expiresAt.Time.UTC()) {
		return accessToken.String, stTypePtr, nil
	}

	// Try refresh token first
	if refreshToken.Valid && refreshToken.String != "" {
		if refreshed, rErr := s.RefreshToken(refreshToken.String); rErr == nil && refreshed.Token != "" {
			exp := parseQorovulTime(refreshed.ExpiresAt)
			_, _ = db.Exec(`
				UPDATE qorovul_settings
				SET access_token = $1, refresh_token = $2, token_expires_at = $3, updated_at = NOW()
				WHERE id = $4
			`, refreshed.Token, refreshed.RefreshToken, exp, id)
			return refreshed.Token, stTypePtr, nil
		}
	}

	// Fallback: full login
	if username == "" || password == "" {
		return "", stTypePtr, fmt.Errorf("Qorovul login yoki paroli saqlanmagan")
	}
	loggedIn, lErr := s.Login(username, password)
	if lErr != nil {
		return "", stTypePtr, lErr
	}
	exp := parseQorovulTime(loggedIn.ExpiresAt)
	_, _ = db.Exec(`
		UPDATE qorovul_settings
		SET access_token = $1, refresh_token = $2, token_expires_at = $3, updated_at = NOW()
		WHERE id = $4
	`, loggedIn.Token, loggedIn.RefreshToken, exp, id)

	return loggedIn.Token, stTypePtr, nil
}

func (s *QorovulService) doGet(token, path string, query url.Values) ([]byte, int, error) {
	fullURL := QorovulBaseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("token", token)
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

func (s *QorovulService) GetProfile(token string) (*QorovulProfile, error) {
	body, status, err := s.doGet(token, "/Profile/View", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Profile/View status %d: %s", status, string(body))
	}
	var p QorovulProfile
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *QorovulService) GetTypes(token string) ([]QorovulType, error) {
	body, status, err := s.doGet(token, "/organizations/Types/GetAll", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Types/GetAll status %d: %s", status, string(body))
	}
	var list []QorovulType
	if err := json.Unmarshal(body, &list); err != nil {
		// Handle wrapped { data: [...] } just in case
		var wrapped struct {
			Data []QorovulType `json:"data"`
		}
		if err2 := json.Unmarshal(body, &wrapped); err2 == nil {
			return wrapped.Data, nil
		}
		return nil, err
	}
	return list, nil
}

func (s *QorovulService) GetAllPersons(token string, typeID *int) ([]QorovulPerson, error) {
	var all []QorovulPerson
	page := 1
	for page <= 50 { // safety cap = 5000 persons
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", "100")
		if typeID != nil && *typeID > 0 {
			q.Set("typeId", strconv.Itoa(*typeID))
		}
		body, status, err := s.doGet(token, "/organizations/Person/GetAll", q)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("Person/GetAll status %d: %s", status, string(body))
		}
		var resp QorovulPersonListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}
		for _, p := range resp.Data {
			if !p.Deleted {
				all = append(all, p)
			}
		}
		if !resp.Pagination.HasNext || len(resp.Data) == 0 {
			break
		}
		page++
	}
	return all, nil
}

// --- Uzbek-aware assisted matching helpers ---

func normalizeUzName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		"'", "",
		"`", "",
		"’", "",
		"‘", "",
		"ʻ", "",
		"ʼ", "",
		"\"", "",
		"-", "",
		" ", "",
		"x", "h",
		"ya", "a",
		"ye", "e",
		"yo", "o",
		"sh", "s",
		"ch", "c",
	)
	return replacer.Replace(s)
}

func normalizeClassName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "sinf", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

func levenshteinRatio(a, b string) float64 {
	ra := []rune(a)
	rb := []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 && lb == 0 {
		return 1.0
	}
	if la == 0 || lb == 0 {
		return 0.0
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			minVal := del
			if ins < minVal {
				minVal = ins
			}
			if sub < minVal {
				minVal = sub
			}
			curr[j] = minVal
		}
		prev, curr = curr, prev
	}
	maxLen := la
	if lb > maxLen {
		maxLen = lb
	}
	return 1.0 - float64(prev[lb])/float64(maxLen)
}

type MatchSuggestion struct {
	PersonID   int    `json:"person_id"`
	Confidence int    `json:"confidence"` // 0..100
	SameClass  bool   `json:"same_class"`
	Reason     string `json:"reason"`
}

// SuggestBestMatch finds the best unmapped Qorovul candidate for a Farzandim student (suggestion only, never auto-saved).
func SuggestBestMatch(className, lastName, firstName string, candidates []QorovulPerson, alreadyMapped map[int]bool) *MatchSuggestion {
	dbCls := normalizeClassName(className)
	dbFull := normalizeUzName(lastName) + " " + normalizeUzName(firstName)
	dbLast := normalizeUzName(lastName)
	dbFirst := normalizeUzName(firstName)

	var best *MatchSuggestion
	bestScore := 0.0

	for _, p := range candidates {
		if alreadyMapped[p.ID] {
			continue
		}
		qCls := normalizeClassName(p.DirectionName)
		sameClass := dbCls != "" && qCls != "" && dbCls == qCls

		qLast := normalizeUzName(p.LastName)
		qFirst := normalizeUzName(p.FirstName)
		qFull1 := qLast + " " + qFirst
		qFull2 := qFirst + " " + qLast

		sim1 := levenshteinRatio(dbFull, qFull1)
		sim2 := levenshteinRatio(dbFull, qFull2)
		sim := sim1
		if sim2 > sim {
			sim = sim2
		}

		// Also check component-wise average in case one name is slightly longer
		comp1 := (levenshteinRatio(dbLast, qLast) + levenshteinRatio(dbFirst, qFirst)) / 2.0
		comp2 := (levenshteinRatio(dbLast, qFirst) + levenshteinRatio(dbFirst, qLast)) / 2.0
		if comp1 > sim {
			sim = comp1
		}
		if comp2 > sim {
			sim = comp2
		}

		// Require stronger name similarity if class does NOT match
		effective := sim
		if sameClass {
			effective = sim*0.90 + 0.10
		} else {
			effective = sim * 0.85
		}

		if effective > bestScore {
			bestScore = effective
			conf := int(sim*100 + 0.5)
			if conf > 100 {
				conf = 100
			}
			reason := "Ism-familiya o'xshashligi"
			if sameClass && conf >= 95 {
				reason = "Sinf va F.I.Sh aniq mos"
			} else if sameClass {
				reason = "Sinf mos, F.I.Sh o'xshash"
			} else {
				reason = "F.I.Sh o'xshash (boshqa sinf)"
			}
			best = &MatchSuggestion{
				PersonID:   p.ID,
				Confidence: conf,
				SameClass:  sameClass,
				Reason:     reason,
			}
		}
	}

	// Only suggest if reasonable confidence (>= 65% in same class, or >= 85% across classes)
	if best == nil {
		return nil
	}
	if best.SameClass && best.Confidence >= 65 {
		return best
	}
	if !best.SameClass && best.Confidence >= 85 {
		return best
	}
	return nil
}


// NormalizeClassName exported helper for matching Farzandim class names to Qorovul directions.
func NormalizeClassName(s string) string {
	return normalizeClassName(s)
}

type QorovulTerminalsSummary struct {
	TotalCount  int `json:"totalCount"`
	OnlineCount int `json:"onlineCount"`
}

type QorovulDirection struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	TypeID    int    `json:"typeId"`
	TypeName  string `json:"typeName"`
	GraphicID int    `json:"graphicId"`
}

type QorovulGraphic struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type QorovulAction struct {
	ID            int64   `json:"id"`
	PersonID      int     `json:"personId"`
	DirectionID   int     `json:"directionId"`
	TypeID        int     `json:"typeId"`
	IncomingTime  *string `json:"incomingTime"`
	OutgoingTime  *string `json:"outgoingTime"`
	LateMinutes   int     `json:"lateMinutes"`
	EarlyMinutes  int     `json:"earlyMinutes"`
	IsInside      bool    `json:"isInside"`
	Date          string  `json:"date"`
	FirstName     string  `json:"firstname"`
	LastName      string  `json:"lastname"`
	DirectionName string  `json:"directionName"`
	PhotoURL      *string `json:"photoUrl"`
}

type QorovulAddPersonRequest struct {
	FirstName   string `json:"firstname"`
	LastName    string `json:"lastname"`
	MiddleName  string `json:"middlename,omitempty"`
	Gender      string `json:"gender"`
	TypeID      int    `json:"typeId"`
	DirectionID int    `json:"directionId"`
	GraphicID   int    `json:"graphicId"`
}

func (s *QorovulService) doPost(token, path string, bodyObj interface{}) ([]byte, int, error) {
	payload, err := json.Marshal(bodyObj)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPost, QorovulBaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("token", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// GetValidTokenAndTypes returns a valid Qorovul access token along with student_type_id and teacher_type_id.
func (s *QorovulService) GetValidTokenAndTypes(db *sql.DB) (string, *int, *int, error) {
	tok, stType, err := s.GetValidToken(db)
	if err != nil {
		return "", stType, nil, err
	}
	var teacherTypeID sql.NullInt64
	_ = db.QueryRow("SELECT teacher_type_id FROM qorovul_settings ORDER BY id ASC LIMIT 1").Scan(&teacherTypeID)
	var tTypePtr *int
	if teacherTypeID.Valid {
		v := int(teacherTypeID.Int64)
		tTypePtr = &v
	}
	return tok, stType, tTypePtr, nil
}

func (s *QorovulService) GetTerminalsSummary(token string) (*QorovulTerminalsSummary, error) {
	body, status, err := s.doGet(token, "/organizations/Terminals/GetTerminals", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GetTerminals status %d: %s", status, string(body))
	}
	var res QorovulTerminalsSummary
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *QorovulService) GetDirections(token string, typeID *int) ([]QorovulDirection, error) {
	q := url.Values{}
	q.Set("page", "1")
	q.Set("pageSize", "200")
	if typeID != nil && *typeID > 0 {
		q.Set("typeId", strconv.Itoa(*typeID))
	}
	body, status, err := s.doGet(token, "/organizations/Directions/GetAll", q)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Directions/GetAll status %d: %s", status, string(body))
	}
	var wrapped struct {
		Data []QorovulDirection `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Data != nil {
		return wrapped.Data, nil
	}
	var direct []QorovulDirection
	if err := json.Unmarshal(body, &direct); err != nil {
		return nil, err
	}
	return direct, nil
}

func (s *QorovulService) GetGraphics(token string) ([]QorovulGraphic, error) {
	q := url.Values{}
	q.Set("page", "1")
	q.Set("pageSize", "100")
	body, status, err := s.doGet(token, "/organizations/Graphics/GetAll", q)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Graphics/GetAll status %d: %s", status, string(body))
	}
	var wrapped struct {
		Data []QorovulGraphic `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Data != nil {
		return wrapped.Data, nil
	}
	var direct []QorovulGraphic
	if err := json.Unmarshal(body, &direct); err != nil {
		return nil, err
	}
	return direct, nil
}

// AddPerson creates a new person in Qorovul and returns the created person ID.
func (s *QorovulService) AddPerson(token string, req QorovulAddPersonRequest) (int, error) {
	body, status, err := s.doPost(token, "/organizations/Person/Add", req)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return 0, fmt.Errorf("Qorovul Person/Add xatosi (%d): %s", status, strings.TrimSpace(string(body)))
	}
	var obj struct {
		ID   int `json:"id"`
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &obj); err == nil {
		if obj.ID > 0 {
			return obj.ID, nil
		}
		if obj.Data.ID > 0 {
			return obj.Data.ID, nil
		}
	}
	// Some endpoints return bare integer ID
	if id, err2 := strconv.Atoi(strings.TrimSpace(string(body))); err2 == nil && id > 0 {
		return id, nil
	}
	return 0, nil
}

func (s *QorovulService) GetActionsByDate(token, dateStr string, typeID *int) ([]QorovulAction, error) {
	var all []QorovulAction
	page := 1
	for page <= 50 {
		q := url.Values{}
		q.Set("date", dateStr)
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", "100")
		if typeID != nil && *typeID > 0 {
			q.Set("typeId", strconv.Itoa(*typeID))
		}
		body, status, err := s.doGet(token, "/reports/ActionsByDate/Get", q)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("ActionsByDate status %d: %s", status, string(body))
		}
		var resp struct {
			Data       []QorovulAction   `json:"data"`
			Pagination QorovulPagination `json:"pagination"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Data...)
		if !resp.Pagination.HasNext || len(resp.Data) == 0 {
			break
		}
		page++
	}
	return all, nil
}

func parseActionTimestamp(raw *string) *time.Time {
	if raw == nil {
		return nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" || strings.HasPrefix(s, "0001-01-01") {
		return nil
	}
	layouts := []string{
		"2006-01-02T15:04:05.9999999",
		"2006-01-02T15:04:05",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// SyncGateAttendanceForTenant pulls Qorovul gate actions for dateStr and upserts them into gate_attendance_logs.
func (s *QorovulService) SyncGateAttendanceForTenant(db *sql.DB, dateStr string) (int, error) {
	var isEnabled bool
	err := db.QueryRow("SELECT is_enabled FROM qorovul_settings ORDER BY id ASC LIMIT 1").Scan(&isEnabled)
	if err == sql.ErrNoRows || !isEnabled {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	token, _, _, err := s.GetValidTokenAndTypes(db)
	if err != nil {
		return 0, err
	}

	// Build maps of mapped students & teachers
	studentMap := make(map[int]int) // qorovul_person_id -> student_id
	if rows, qErr := db.Query("SELECT id, qorovul_person_id FROM students WHERE is_deleted = false AND qorovul_person_id IS NOT NULL"); qErr == nil {
		for rows.Next() {
			var stID, qID int
			if rows.Scan(&stID, &qID) == nil {
				studentMap[qID] = stID
			}
		}
		rows.Close()
	}

	teacherMap := make(map[int]int) // qorovul_person_id -> user_id
	if rows, qErr := db.Query("SELECT id, qorovul_person_id FROM users WHERE is_deleted = false AND qorovul_person_id IS NOT NULL"); qErr == nil {
		for rows.Next() {
			var uID, qID int
			if rows.Scan(&uID, &qID) == nil {
				teacherMap[qID] = uID
			}
		}
		rows.Close()
	}

	if len(studentMap) == 0 && len(teacherMap) == 0 {
		_, _ = db.Exec("UPDATE qorovul_settings SET last_synced_at = NOW()")
		return 0, nil
	}

	actions, err := s.GetActionsByDate(token, dateStr, nil)
	if err != nil {
		return 0, err
	}

	synced := 0
	for _, act := range actions {
		inTime := parseActionTimestamp(act.IncomingTime)
		outTime := parseActionTimestamp(act.OutgoingTime)
		if inTime == nil && outTime == nil && !act.IsInside {
			continue
		}

		var stIDPtr *int
		var uIDPtr *int
		role := ""

		if stID, ok := studentMap[act.PersonID]; ok {
			v := stID
			stIDPtr = &v
			role = "STUDENT"
		} else if uID, ok := teacherMap[act.PersonID]; ok {
			v := uID
			uIDPtr = &v
			role = "TEACHER"
		} else {
			continue
		}

		_, uErr := db.Exec(`
			INSERT INTO gate_attendance_logs (
				student_id, user_id, qorovul_person_id, person_role, log_date,
				incoming_time, outgoing_time, late_minutes, early_minutes, is_inside, qorovul_action_id, updated_at
			)
			VALUES ($1, $2, $3, $4, $5::date, $6, $7, $8, $9, $10, $11, NOW())
			ON CONFLICT (qorovul_person_id, log_date) DO UPDATE SET
				student_id = COALESCE(EXCLUDED.student_id, gate_attendance_logs.student_id),
				user_id = COALESCE(EXCLUDED.user_id, gate_attendance_logs.user_id),
				incoming_time = COALESCE(EXCLUDED.incoming_time, gate_attendance_logs.incoming_time),
				outgoing_time = COALESCE(EXCLUDED.outgoing_time, gate_attendance_logs.outgoing_time),
				late_minutes = EXCLUDED.late_minutes,
				early_minutes = EXCLUDED.early_minutes,
				is_inside = EXCLUDED.is_inside,
				qorovul_action_id = EXCLUDED.qorovul_action_id,
				updated_at = NOW()
		`, stIDPtr, uIDPtr, act.PersonID, role, dateStr, inTime, outTime, act.LateMinutes, act.EarlyMinutes, act.IsInside, act.ID)
		if uErr == nil {
			synced++
		}
	}

	_, _ = db.Exec("UPDATE qorovul_settings SET last_synced_at = NOW()")
	return synced, nil
}
