package services

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/farzandim/backend/internal/db"
	"github.com/farzandim/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
)

type BotState struct {
	Step  string // "none", "waiting_phone", "waiting_password"
	Phone string
}

type BotManager struct {
	mu          sync.RWMutex
	cancelFuncs map[string]context.CancelFunc // map[schoolID]CancelFunc
	chatStates  map[int64]*BotState
	statesMu    sync.Mutex
}

var Manager = &BotManager{
	cancelFuncs: make(map[string]context.CancelFunc),
	chatStates:  make(map[int64]*BotState),
}

// StartBotForSchool starts the long polling bot loop for a specific school
func (bm *BotManager) StartBotForSchool(schoolID string, token string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	// If there's an existing bot for this school, stop it first
	if cancel, exists := bm.cancelFuncs[schoolID]; exists {
		cancel()
		delete(bm.cancelFuncs, schoolID)
	}

	if token == "" {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	bm.cancelFuncs[schoolID] = cancel

	log.Printf("Starting dynamic Telegram Bot for school %s...", schoolID)

	go func() {
		offset := 0
		client := &http.Client{Timeout: 30 * time.Second}
		apiURL := fmt.Sprintf("https://api.telegram.org/bot%s", token)

		for {
			select {
			case <-ctx.Done():
				log.Printf("Stopping Telegram Bot loop for school %s", schoolID)
				return
			default:
				url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=20&allowed_updates=[\"message\",\"poll_answer\",\"poll\"]", apiURL, offset)
				req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
				if err != nil {
					log.Printf("[%s] Poll request creation error: %v", schoolID, err)
					time.Sleep(5 * time.Second)
					continue
				}

				resp, err := client.Do(req)
				if err != nil {
					// Check if context was cancelled
					if ctx.Err() != nil {
						log.Printf("[%s] Bot context cancelled, exiting loop", schoolID)
						return
					}
					log.Printf("[%s] Telegram Bot poll error: %v, retrying in 5 seconds...", schoolID, err)
					time.Sleep(5 * time.Second)
					continue
				}

				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					log.Printf("[%s] Telegram Bot body read error: %v", schoolID, err)
					time.Sleep(2 * time.Second)
					continue
				}

				var result struct {
					Ok     bool `json:"ok"`
					Result []struct {
						UpdateID int `json:"update_id"`
						Message  *struct {
							Chat struct {
								ID int64 `json:"id"`
							} `json:"chat"`
							Text string `json:"text"`
						} `json:"message"`
						PollAnswer *struct {
							PollID string `json:"poll_id"`
							User   struct {
								ID int64 `json:"id"`
							} `json:"user"`
							OptionIDs []int `json:"option_ids"`
						} `json:"poll_answer"`
					} `json:"result"`
				}

				if err := json.Unmarshal(body, &result); err != nil {
					log.Printf("[%s] Telegram Bot json parse error: %v", schoolID, err)
					time.Sleep(2 * time.Second)
					continue
				}

				if !result.Ok {
					log.Printf("[%s] Telegram API returned error status: %s", schoolID, string(body))
					time.Sleep(5 * time.Second)
					continue
				}

				for _, update := range result.Result {
					offset = update.UpdateID + 1
					if update.Message != nil {
						bm.handleMessage(schoolID, token, update.Message.Chat.ID, update.Message.Text)
					}
					if update.PollAnswer != nil {
						bm.handlePollAnswer(schoolID, update.PollAnswer.PollID, update.PollAnswer.User.ID, update.PollAnswer.OptionIDs)
					}
				}
			}
		}
	}()
}

// StopBotForSchool stops the bot for a specific school
func (bm *BotManager) StopBotForSchool(schoolID string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if cancel, exists := bm.cancelFuncs[schoolID]; exists {
		cancel()
		delete(bm.cancelFuncs, schoolID)
		log.Printf("Stopped Telegram Bot for school %s", schoolID)
	}
}

func (bm *BotManager) handleMessage(schoolID string, token string, chatID int64, text string) {
	bm.statesMu.Lock()
	state, exists := bm.chatStates[chatID]
	if !exists {
		state = &BotState{Step: "none"}
		bm.chatStates[chatID] = state
	}
	bm.statesMu.Unlock()

	text = strings.TrimSpace(text)

	if strings.HasPrefix(text, "/start") {
		state.Step = "waiting_phone"
		state.Phone = ""
		bm.sendTextMessage(token, chatID, "📚 *Online Jurnal* tizimiga xush kelibsiz!\n\nTizimga ulanish va bildirishnomalarni olish uchun telefon raqamingizni kiriting (Faqat raqamlar, masalan: 998901234567):")
		return
	}

	switch state.Step {
	case "waiting_phone":
		cleanPhone := sanitizePhoneNumber(text)
		if len(cleanPhone) < 9 {
			bm.sendTextMessage(token, chatID, "❌ Noto'g'ri telefon raqami formati. Iltimos qaytadan kiriting (masalan: 998901234567):")
			return
		}
		state.Phone = cleanPhone
		state.Step = "waiting_password"
		bm.sendTextMessage(token, chatID, "🔑 Parolingizni kiriting:")

	case "waiting_password":
		phone := state.Phone
		password := text

		bm.sendTextMessage(token, chatID, "🔄 Tizimdan tekshirilmoqda, iltimos kuting...")

		success, err := bm.authenticateAndRegisterForSchool(schoolID, chatID, phone, password)
		if err != nil {
			log.Printf("[%s] Authentication error for phone %s: %v", schoolID, phone, err)
			bm.sendTextMessage(token, chatID, "⚠️ Tizimda xatolik yuz berdi. Iltimos keyinroq qayta urining.")
			state.Step = "none"
			return
		}

		if success {
			bm.sendTextMessage(token, chatID, "✅ Muvaffaqiyatli kirdingiz!\n\nEndi maktab e'lonlari va farzandingiz baholari ushbu bot orqali yuboriladi.")
			state.Step = "none"
		} else {
			bm.sendTextMessage(token, chatID, "❌ Telefon raqami yoki parol noto'g'ri.\n\nIltimos, telefon raqamingizni qaytadan kiriting:")
			state.Step = "waiting_phone"
			state.Phone = ""
		}

	default:
		if bm.isUserLinked(schoolID, chatID) {
			bm.sendTextMessage(token, chatID, "Siz tizimga muvaffaqiyatli ulangan holdasiz. Parolni o'zgartirish yoki qayta ro'yxatdan o'tish uchun /start buyrug'ini yuboring.")
		} else {
			bm.sendTextMessage(token, chatID, "Siz tizimga hali ulanmagansiz. Ro'yxatdan o'tish va bildirishnomalarni olish uchun iltimos /start buyrug'ini yuboring.")
		}
	}
}

func (bm *BotManager) isUserLinked(schoolID string, chatID int64) bool {
	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		return false
	}
	telegramIDStr := fmt.Sprintf("%d", chatID)
	var exists bool
	err = tenantDB.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE telegram_id = $1 AND is_deleted = false)", telegramIDStr).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

func sanitizePhoneNumber(phone string) string {
	var sb strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func (bm *BotManager) authenticateAndRegisterForSchool(schoolID string, chatID int64, phone, password string) (bool, error) {
	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		return false, fmt.Errorf("failed to connect to tenant DB: %w", err)
	}

	telegramIDStr := fmt.Sprintf("%d", chatID)
	phoneWithPlus := phone
	if !strings.HasPrefix(phone, "+") {
		phoneWithPlus = "+" + phone
	}
	phoneWithoutPlus := strings.TrimPrefix(phone, "+")

	var userID int
	var passwordHash string

	err = tenantDB.QueryRow(`
		SELECT u.id, u.password_hash 
		FROM users u
		JOIN roles r ON u.role_id = r.id
		WHERE (u.phone = $1 OR u.phone = $2) AND r.name = 'PARENT' AND u.is_deleted = false
	`, phoneWithPlus, phoneWithoutPlus).Scan(&userID, &passwordHash)

	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password))
	if err != nil {
		return false, nil
	}

	_, err = tenantDB.Exec("UPDATE users SET telegram_id = $1 WHERE id = $2", telegramIDStr, userID)
	if err != nil {
		return false, err
	}

	return true, nil
}

type RateLimiter struct {
	mu           sync.Mutex
	lastRequest  time.Time
	minInterval  time.Duration
	blockedUntil time.Time
}

type GlobalTelegramLimiter struct {
	mu       sync.Mutex
	limiters map[string]*RateLimiter
}

var TokenLimiter = &GlobalTelegramLimiter{
	limiters: make(map[string]*RateLimiter),
}

// Wait enforces Telegram's global limit (max 30 msgs/sec = ~36ms minimum interval between API calls)
func (gtl *GlobalTelegramLimiter) Wait(token string) {
	if token == "" {
		return
	}
	gtl.mu.Lock()
	limiter, exists := gtl.limiters[token]
	if !exists {
		limiter = &RateLimiter{
			minInterval: 36 * time.Millisecond, // ~27.7 requests per second (safe margin below 30/sec Telegram rule)
		}
		gtl.limiters[token] = limiter
	}
	gtl.mu.Unlock()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now()
	if now.Before(limiter.blockedUntil) {
		sleepDur := limiter.blockedUntil.Sub(now)
		log.Printf("[Telegram RateLimiter] Token paused due to 429 Retry-After. Waiting for %v...", sleepDur)
		time.Sleep(sleepDur)
		now = time.Now()
	}

	elapsed := now.Sub(limiter.lastRequest)
	if elapsed < limiter.minInterval {
		time.Sleep(limiter.minInterval - elapsed)
	}
	limiter.lastRequest = time.Now()
}

// BlockToken pauses requests for a specific token when a 429 Retry-After is encountered
func (gtl *GlobalTelegramLimiter) BlockToken(token string, duration time.Duration) {
	if token == "" {
		return
	}
	gtl.mu.Lock()
	limiter, exists := gtl.limiters[token]
	if !exists {
		limiter = &RateLimiter{
			minInterval: 36 * time.Millisecond,
		}
		gtl.limiters[token] = limiter
	}
	gtl.mu.Unlock()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.blockedUntil = time.Now().Add(duration)
}

type TelegramAPIResponse struct {
	Ok          bool            `json:"ok"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Result      json.RawMessage `json:"result"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// sendTelegramRequestRaw executes a POST request to Telegram API with rate limiting and 429 Retry-After handling
func sendTelegramRequestRaw(token string, method string, payload interface{}) ([]byte, error) {
	if token == "" {
		return nil, fmt.Errorf("empty bot token")
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	maxAttempts := 3

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		TokenLimiter.Wait(token)

		resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
		if err != nil {
			log.Printf("[Telegram API Error] POST %s failed: %v (Attempt %d/%d)", method, err, attempt, maxAttempts)
			time.Sleep(1 * time.Second)
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("[Telegram API Error] Read body failed: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		var apiResp TelegramAPIResponse
		_ = json.Unmarshal(bodyBytes, &apiResp)

		if resp.StatusCode == 429 || apiResp.ErrorCode == 429 {
			retryAfter := 3
			if apiResp.Parameters != nil && apiResp.Parameters.RetryAfter > 0 {
				retryAfter = apiResp.Parameters.RetryAfter
			}
			blockDuration := time.Duration(retryAfter+1) * time.Second
			log.Printf("[Telegram 429 Rate Limit] Method: %s, Retry After: %d sec. Pausing token...", method, retryAfter)
			TokenLimiter.BlockToken(token, blockDuration)
			time.Sleep(blockDuration)
			continue
		}

		if resp.StatusCode == http.StatusOK && apiResp.Ok {
			return bodyBytes, nil
		}

		log.Printf("[Telegram API Response Warning] Method: %s, Status: %d, Response: %s", method, resp.StatusCode, string(bodyBytes))
		return bodyBytes, fmt.Errorf("telegram API error: status %d", resp.StatusCode)
	}

	return nil, fmt.Errorf("failed to send telegram request to %s after retries", method)
}

func (bm *BotManager) sendTextMessage(token string, chatID int64, text string) {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	_, err := sendTelegramRequestRaw(token, "sendMessage", payload)
	if err != nil {
		plainPayload := map[string]interface{}{
			"chat_id": chatID,
			"text":    text,
		}
		_, _ = sendTelegramRequestRaw(token, "sendMessage", plainPayload)
	}
}

func isValidTelegramButtonURL(urlStr string) bool {
	if urlStr == "" {
		return false
	}
	lower := strings.ToLower(urlStr)
	if strings.Contains(lower, "localhost") || strings.Contains(lower, "127.0.0.1") {
		return false
	}
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "tg://")
}

func (bm *BotManager) sendTextMessageWithButton(token string, chatID interface{}, text string, buttonText string, buttonURL string) {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	if isValidTelegramButtonURL(buttonURL) {
		payload["reply_markup"] = map[string]interface{}{
			"inline_keyboard": [][]map[string]interface{}{
				{
					{"text": buttonText, "url": buttonURL},
				},
			},
		}
	}

	_, err := sendTelegramRequestRaw(token, "sendMessage", payload)
	if err != nil {
		plainPayload := map[string]interface{}{
			"chat_id": chatID,
			"text":    text,
		}
		if isValidTelegramButtonURL(buttonURL) {
			plainPayload["reply_markup"] = map[string]interface{}{
				"inline_keyboard": [][]map[string]interface{}{
					{
						{"text": buttonText, "url": buttonURL},
					},
				},
			}
		}
		_, _ = sendTelegramRequestRaw(token, "sendMessage", plainPayload)
	}
}

func (bm *BotManager) sendPollMessage(token string, chatID interface{}, question string, options []string) string {
	if len(question) > 290 {
		question = question[:287] + "..."
	}

	payload := map[string]interface{}{
		"chat_id":      chatID,
		"question":     question,
		"options":      options,
		"is_anonymous": false,
	}

	respBytes, err := sendTelegramRequestRaw(token, "sendPoll", payload)
	if err != nil {
		return ""
	}

	var res struct {
		Ok     bool `json:"ok"`
		Result struct {
			Poll struct {
				ID string `json:"poll"`
			} `json:"poll"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &res); err == nil && res.Ok {
		return res.Result.Poll.ID
	}
	return ""
}

func (bm *BotManager) handlePollAnswer(schoolID string, pollID string, telegramUserID int64, optionIDs []int) {
	if pollID == "" || len(optionIDs) == 0 {
		return
	}

	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		log.Printf("[%s] handlePollAnswer failed to get tenant DB: %v", schoolID, err)
		return
	}

	// 1. Find announcement ID from telegram_polls table or announcements table
	var annID int
	err = tenantDB.QueryRow("SELECT announcement_id FROM telegram_polls WHERE telegram_poll_id = $1", pollID).Scan(&annID)
	if err != nil {
		err = tenantDB.QueryRow("SELECT id FROM announcements WHERE telegram_poll_id = $1 AND is_deleted = false", pollID).Scan(&annID)
	}
	if err != nil {
		log.Printf("[%s] Poll answer received for poll_id %s but announcement not found: %v", schoolID, pollID, err)
		return
	}

	// 2. Find user ID by telegram_id
	var userID int
	telegramIDStr := fmt.Sprintf("%d", telegramUserID)
	err = tenantDB.QueryRow("SELECT id FROM users WHERE telegram_id = $1 AND is_deleted = false", telegramIDStr).Scan(&userID)
	if err != nil {
		err = tenantDB.QueryRow("SELECT id FROM users WHERE telegram_id LIKE $1 AND is_deleted = false", "%"+telegramIDStr+"%").Scan(&userID)
	}
	if err != nil {
		log.Printf("[%s] Poll answer received from unknown telegram user %s: %v", schoolID, telegramIDStr, err)
		return
	}

	// 3. Find option ID from announcement_poll_options by index
	optionIndex := optionIDs[0]
	rows, err := tenantDB.Query("SELECT id FROM announcement_poll_options WHERE announcement_id = $1 ORDER BY id ASC", annID)
	if err != nil {
		return
	}
	defer rows.Close()

	var optionList []int
	for rows.Next() {
		var optID int
		if err := rows.Scan(&optID); err == nil {
			optionList = append(optionList, optID)
		}
	}

	if optionIndex < 0 || optionIndex >= len(optionList) {
		log.Printf("[%s] Option index %d out of bounds for poll %s", schoolID, optionIndex, pollID)
		return
	}

	chosenOptionID := optionList[optionIndex]

	// 4. Upsert vote into announcement_poll_votes
	_, err = tenantDB.Exec(`
		INSERT INTO announcement_poll_votes (announcement_id, option_id, user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (announcement_id, user_id)
		DO UPDATE SET option_id = EXCLUDED.option_id, created_at = NOW()
	`, annID, chosenOptionID, userID)

	if err != nil {
		log.Printf("[%s] Failed to record telegram poll vote: %v", schoolID, err)
	} else {
		log.Printf("[%s] Successfully synced Telegram poll vote! User %d voted for option %d on announcement %d", schoolID, userID, chosenOptionID, annID)
	}
}

// SendAnnouncementNotification sends the announcement to configured channel and all parents
func SendAnnouncementNotification(schoolID string, ann *models.Announcement) {
	var token string
	var schoolName string
	var telegramChannelID string
	err := db.CentralDB.QueryRow("SELECT bot_token, name, COALESCE(telegram_channel_id, '') FROM schools WHERE id = $1 AND is_deleted = false", schoolID).Scan(&token, &schoolName, &telegramChannelID)
	if err != nil || token == "" {
		return
	}

	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		log.Printf("SendAnnouncementNotification failed to get tenant DB: %v", err)
		return
	}

	// Build target details text
	var targetParts []string
	if len(ann.ClassIDs) > 0 {
		placeholders := make([]string, len(ann.ClassIDs))
		args := make([]interface{}, len(ann.ClassIDs))
		for i, id := range ann.ClassIDs {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = id
		}
		cRows, err := tenantDB.Query(fmt.Sprintf("SELECT name FROM classes WHERE id IN (%s) AND is_deleted = false", strings.Join(placeholders, ",")), args...)
		if err == nil {
			var cNames []string
			for cRows.Next() {
				var name string
				if err := cRows.Scan(&name); err == nil {
					cNames = append(cNames, name)
				}
			}
			cRows.Close()
			if len(cNames) > 0 {
				targetParts = append(targetParts, fmt.Sprintf("Sinflar: %s", strings.Join(cNames, ", ")))
			}
		}
	}
	if len(ann.LevelIDs) > 0 {
		var lStrs []string
		for _, lvl := range ann.LevelIDs {
			lStrs = append(lStrs, fmt.Sprintf("%d-sinflar", lvl))
		}
		targetParts = append(targetParts, strings.Join(lStrs, ", "))
	}
	if len(ann.StudentIDs) > 0 {
		placeholders := make([]string, len(ann.StudentIDs))
		args := make([]interface{}, len(ann.StudentIDs))
		for i, id := range ann.StudentIDs {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = id
		}
		sRows, err := tenantDB.Query(fmt.Sprintf(`
			SELECT u.first_name || ' ' || u.last_name 
			FROM students s 
			JOIN users u ON s.user_id = u.id 
			WHERE s.id IN (%s) AND s.is_deleted = false
		`, strings.Join(placeholders, ",")), args...)
		if err == nil {
			var sNames []string
			for sRows.Next() {
				var name string
				if err := sRows.Scan(&name); err == nil {
					sNames = append(sNames, name)
				}
			}
			sRows.Close()
			if len(sNames) > 0 {
				targetParts = append(targetParts, fmt.Sprintf("O'quvchilar: %s", strings.Join(sNames, ", ")))
			}
		}
	}

	targetGroupText := "Barcha sinflar va ota-onalar"
	if len(targetParts) > 0 {
		targetGroupText = strings.Join(targetParts, " | ")
	}

	var optTexts []string
	if ann.IsPoll && len(ann.PollOptions) > 0 {
		for _, opt := range ann.PollOptions {
			if strings.TrimSpace(opt.OptionText) != "" {
				optTexts = append(optTexts, strings.TrimSpace(opt.OptionText))
			}
		}
	}

	createdStr := ann.CreatedAt.Format("2006-01-02 15:04")
	if ann.CreatedAt.IsZero() {
		createdStr = time.Now().Format("2006-01-02 15:04")
	}

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:6501"
	}

	safeSchoolName := html.EscapeString(schoolName)
	safeTitle := html.EscapeString(ann.Title)
	safeContent := html.EscapeString(ann.Content)
	safeTargetGroup := html.EscapeString(targetGroupText)

	var msgText string
	if ann.IsPoll {
		var optListStr strings.Builder
		numberIcons := []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟"}
		for i, opt := range optTexts {
			icon := "🔹"
			if i < len(numberIcons) {
				icon = numberIcons[i]
			}
			optListStr.WriteString(fmt.Sprintf("%s %s\n", icon, html.EscapeString(opt)))
		}

		msgText = fmt.Sprintf(
			"📊 <b>Yangi So'rovnoma! (%s)</b>\n\n📌 <b>%s</b>\n\n%s\n\n🗳️ <b>So'rovnoma variantlari:</b>\n%s\n🎯 <b>Maqsadli guruh:</b> %s\n📅 <b>Sana:</b> %s",
			safeSchoolName, safeTitle, safeContent, optListStr.String(), safeTargetGroup, createdStr,
		)
	} else {
		msgText = fmt.Sprintf(
			"📢 <b>Yangi e'lon! (%s)</b>\n\n📌 <b>%s</b>\n\n%s\n\n🎯 <b>Maqsadli guruh:</b> %s\n📅 <b>Sana:</b> %s",
			safeSchoolName, safeTitle, safeContent, safeTargetGroup, createdStr,
		)
	}

	// Fetch parent Telegram IDs
	var rows *sql.Rows
	var queryParts []string
	var args []interface{}
	argCount := 1

	if len(ann.ClassIDs) > 0 {
		placeholders := make([]string, len(ann.ClassIDs))
		for i, id := range ann.ClassIDs {
			placeholders[i] = fmt.Sprintf("$%d", argCount)
			args = append(args, id)
			argCount++
		}
		queryParts = append(queryParts, fmt.Sprintf(`
			SELECT DISTINCT u.telegram_id 
			FROM users u
			JOIN student_parents sp ON u.id = sp.parent_id
			JOIN students s ON sp.student_id = s.id
			WHERE s.class_id IN (%s) AND u.telegram_id IS NOT NULL AND u.is_deleted = false AND s.is_deleted = false
		`, strings.Join(placeholders, ",")))
	}

	if len(ann.LevelIDs) > 0 {
		placeholders := make([]string, len(ann.LevelIDs))
		for i, lvl := range ann.LevelIDs {
			placeholders[i] = fmt.Sprintf("$%d", argCount)
			args = append(args, lvl)
			argCount++
		}
		queryParts = append(queryParts, fmt.Sprintf(`
			SELECT DISTINCT u.telegram_id 
			FROM users u
			JOIN student_parents sp ON u.id = sp.parent_id
			JOIN students s ON sp.student_id = s.id
			JOIN classes c ON s.class_id = c.id
			WHERE c.level IN (%s) AND u.telegram_id IS NOT NULL AND u.is_deleted = false AND s.is_deleted = false AND c.is_deleted = false
		`, strings.Join(placeholders, ",")))
	}

	if len(ann.StudentIDs) > 0 {
		placeholders := make([]string, len(ann.StudentIDs))
		for i, sid := range ann.StudentIDs {
			placeholders[i] = fmt.Sprintf("$%d", argCount)
			args = append(args, sid)
			argCount++
		}
		queryParts = append(queryParts, fmt.Sprintf(`
			SELECT DISTINCT u.telegram_id 
			FROM users u
			JOIN student_parents sp ON u.id = sp.parent_id
			WHERE sp.student_id IN (%s) AND u.telegram_id IS NOT NULL AND u.is_deleted = false
		`, strings.Join(placeholders, ",")))
	}

	if len(queryParts) == 0 {
		query := `
			SELECT telegram_id 
			FROM users u
			JOIN roles r ON u.role_id = r.id
			WHERE r.name = 'PARENT' AND u.telegram_id IS NOT NULL AND u.is_deleted = false
		`
		rows, err = tenantDB.Query(query)
	} else {
		fullQuery := strings.Join(queryParts, " UNION ")
		rows, err = tenantDB.Query(fullQuery, args...)
	}

	var telegramIDs []string
	if err == nil {
		for rows.Next() {
			var tid string
			if err := rows.Scan(&tid); err == nil && tid != "" {
				telegramIDs = append(telegramIDs, tid)
			}
		}
		rows.Close()
	}

	go func() {
		btnLabel := "🌐 Portalda ko'rish"
		if ann.IsPoll {
			btnLabel = "🌐 Portalda ko'rish va ovoz berish"
		}

		// 1. Send to configured Telegram Channel / Group if present
		if telegramChannelID != "" {
			Manager.sendTextMessageWithButton(token, telegramChannelID, msgText, btnLabel, fmt.Sprintf("%s/parents", frontendURL))
			if ann.IsPoll && len(optTexts) >= 2 {
				time.Sleep(1000 * time.Millisecond) // Respect Telegram 1 msg/sec per chat rule
				pollQuestion := fmt.Sprintf("📊 %s", ann.Title)
				tgPollID := Manager.sendPollMessage(token, telegramChannelID, pollQuestion, optTexts)
				if tgPollID != "" {
					_, _ = tenantDB.Exec("INSERT INTO telegram_polls (announcement_id, telegram_poll_id, chat_id) VALUES ($1, $2, 0) ON CONFLICT (telegram_poll_id) DO NOTHING", ann.ID, tgPollID)
					_, _ = tenantDB.Exec("UPDATE announcements SET telegram_poll_id = $1 WHERE id = $2", tgPollID, ann.ID)
				}
			}
		}

		// 2. Send to target parents' private chats
		for _, tid := range telegramIDs {
			chatID := int64(0)
			fmt.Sscanf(tid, "%d", &chatID)
			if chatID != 0 {
				Manager.sendTextMessageWithButton(token, chatID, msgText, btnLabel, fmt.Sprintf("%s/parents", frontendURL))

				if ann.IsPoll && len(optTexts) >= 2 {
					time.Sleep(1000 * time.Millisecond) // Respect Telegram 1 msg/sec per chat rule
					pollQuestion := fmt.Sprintf("📊 %s", ann.Title)
					tgPollID := Manager.sendPollMessage(token, chatID, pollQuestion, optTexts)
					if tgPollID != "" {
						_, _ = tenantDB.Exec("INSERT INTO telegram_polls (announcement_id, telegram_poll_id, chat_id) VALUES ($1, $2, $3) ON CONFLICT (telegram_poll_id) DO NOTHING", ann.ID, tgPollID, chatID)
						_, _ = tenantDB.Exec("UPDATE announcements SET telegram_poll_id = $1 WHERE id = $2", tgPollID, ann.ID)
					}
				}
			}
		}
	}()
}

// SendGradeCommentNotificationToTeachers sends parent comments on grades directly to the grading teacher and class advisor
func SendGradeCommentNotificationToTeachers(schoolID string, gradeID int, commentText string, parentID int) {
	var token string
	err := db.CentralDB.QueryRow("SELECT bot_token FROM schools WHERE id = $1 AND is_deleted = false", schoolID).Scan(&token)
	if err != nil || token == "" {
		return
	}

	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		log.Printf("SendGradeCommentNotificationToTeachers failed to get tenant DB: %v", err)
		return
	}

	var parentName string
	err = tenantDB.QueryRow("SELECT first_name || ' ' || last_name FROM users WHERE id = $1", parentID).Scan(&parentName)
	if err != nil {
		parentName = "Ota-ona"
	}

	var studentName, subjectName, gradeValue string
	queryInfo := `
		SELECT 
			stu_u.first_name || ' ' || stu_u.last_name as student_name,
			sub.name as subject_name,
			g.value as grade_value
		FROM grades g
		JOIN students stu ON g.student_id = stu.id
		JOIN users stu_u ON stu.user_id = stu_u.id
		JOIN subjects sub ON g.subject_id = sub.id
		WHERE g.id = $1
	`
	err = tenantDB.QueryRow(queryInfo, gradeID).Scan(&studentName, &subjectName, &gradeValue)
	if err != nil {
		log.Printf("SendGradeCommentNotificationToTeachers failed to fetch grade info: %v", err)
		return
	}

	queryTIDs := `
		SELECT DISTINCT u.telegram_id 
		FROM users u
		JOIN grades g ON u.id = g.teacher_id
		WHERE g.id = $1 AND u.telegram_id IS NOT NULL AND u.is_deleted = false
		
		UNION
		
		SELECT DISTINCT u.telegram_id 
		FROM users u
		JOIN class_teachers ct ON u.id = ct.teacher_id
		JOIN students s ON ct.class_id = s.class_id
		JOIN grades g ON s.id = g.student_id
		WHERE g.id = $1 AND ct.is_main_teacher = true AND u.telegram_id IS NOT NULL AND u.is_deleted = false AND ct.is_deleted = false AND s.is_deleted = false
	`
	rows, err := tenantDB.Query(queryTIDs, gradeID)
	if err != nil {
		log.Printf("SendGradeCommentNotificationToTeachers failed to query teacher Telegram IDs: %v", err)
		return
	}
	defer rows.Close()

	var tids []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err == nil {
			tids = append(tids, tid)
		}
	}

	if len(tids) == 0 {
		return
	}

	msgText := fmt.Sprintf(
		"💬 *Yangi fikr-mulohaza! (Bahoga)*\n\n*Ota-ona:* %s\n*O'quvchi:* %s\n*Fan:* %s\n*Baho:* %s\n\n*Izoh:* %s",
		parentName, studentName, subjectName, gradeValue, commentText,
	)

	go func() {
		for _, tid := range tids {
			chatID := int64(0)
			fmt.Sscanf(tid, "%d", &chatID)
			if chatID != 0 {
				Manager.sendTextMessage(token, chatID, msgText)
				time.Sleep(35 * time.Millisecond)
			}
		}
	}()
}

// SendMenuCommentNotificationToAdvisors sends parent comments on food menus directly to the child's class advisor
func SendMenuCommentNotificationToAdvisors(schoolID string, menuDate time.Time, commentText string, parentID int) {
	var token string
	err := db.CentralDB.QueryRow("SELECT bot_token FROM schools WHERE id = $1 AND is_deleted = false", schoolID).Scan(&token)
	if err != nil || token == "" {
		return
	}

	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		log.Printf("SendMenuCommentNotificationToAdvisors failed to get tenant DB: %v", err)
		return
	}

	var parentName string
	err = tenantDB.QueryRow("SELECT first_name || ' ' || last_name FROM users WHERE id = $1", parentID).Scan(&parentName)
	if err != nil {
		parentName = "Ota-ona"
	}

	queryTIDs := `
		SELECT DISTINCT u.telegram_id
		FROM users u
		JOIN class_teachers ct ON u.id = ct.teacher_id
		JOIN students s ON ct.class_id = s.class_id
		JOIN student_parents sp ON s.id = sp.student_id
		WHERE sp.parent_id = $1 AND ct.is_main_teacher = true AND u.telegram_id IS NOT NULL AND u.is_deleted = false AND ct.is_deleted = false AND s.is_deleted = false
	`
	rows, err := tenantDB.Query(queryTIDs, parentID)
	if err != nil {
		log.Printf("SendMenuCommentNotificationToAdvisors failed to query advisor Telegram IDs: %v", err)
		return
	}
	defer rows.Close()

	var tids []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err == nil {
			tids = append(tids, tid)
		}
	}

	if len(tids) == 0 {
		return
	}

	msgText := fmt.Sprintf(
		"💬 *Yangi fikr-mulohaza! (Taomnomaga)*\n\n*Ota-ona:* %s\n*Sana:* %s\n\n*Izoh:* %s",
		parentName, menuDate.Format("2006-01-02"), commentText,
	)

	go func() {
		for _, tid := range tids {
			chatID := int64(0)
			fmt.Sscanf(tid, "%d", &chatID)
			if chatID != 0 {
				Manager.sendTextMessage(token, chatID, msgText)
				time.Sleep(35 * time.Millisecond)
			}
		}
	}()
}
