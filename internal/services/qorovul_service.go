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
