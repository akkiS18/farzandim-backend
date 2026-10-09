package services

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type userFloodState struct {
	lastMsgTime  time.Time
	lastText     string
	msgCount     int
	blockedUntil time.Time
	warnedUntil  time.Time
}

type replyTarget struct {
	FeedbackID string
	UserID     int64
}

type FeedbackBotService struct {
	token       string
	adminIDs    []int64
	db          *sql.DB
	client      *http.Client
	mu          sync.RWMutex
	replyMap    map[string]replyTarget // key: "adminChatID:adminMsgID" -> target
	adminSet    map[int64]bool
	cancelFunc  context.CancelFunc

	// Anti-spam & Rate Limiting
	floodMu       sync.Mutex
	userFloodMap  map[int64]*userFloodState

	// Outgoing Rate Limiter & Cooldown
	sendMu         sync.Mutex
	lastGlobalSend time.Time
	lastChatSend   map[int64]time.Time
}

var GlobalFeedbackBot *FeedbackBotService

// StartFeedbackBot initializes and runs the parent feedback bot
func StartFeedbackBot(token string, adminIDs []int64, centralDB *sql.DB) {
	if token == "" {
		log.Println("[FeedbackBot] FEEDBACK_BOT_TOKEN is not set. Feedback bot will not start.")
		return
	}

	adminSet := make(map[int64]bool)
	for _, id := range adminIDs {
		adminSet[id] = true
	}

	ctx, cancel := context.WithCancel(context.Background())

	bot := &FeedbackBotService{
		token:         token,
		adminIDs:      adminIDs,
		db:            centralDB,
		client:        &http.Client{Timeout: 35 * time.Second},
		replyMap:      make(map[string]replyTarget),
		adminSet:      adminSet,
		cancelFunc:    cancel,
		userFloodMap:  make(map[int64]*userFloodState),
		lastChatSend:  make(map[int64]time.Time),
	}
	GlobalFeedbackBot = bot

	log.Printf("[FeedbackBot] Starting lightning feedback bot with %d admin(s) and anti-spam protection...", len(adminIDs))
	go bot.pollLoop(ctx)
}

func (b *FeedbackBotService) pollLoop(ctx context.Context) {
	offset := 0
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s", b.token)

	for {
		select {
		case <-ctx.Done():
			log.Println("[FeedbackBot] Polling loop stopped.")
			return
		default:
			url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=25&allowed_updates=[\"message\"]", apiURL, offset)
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				time.Sleep(3 * time.Second)
				continue
			}

			resp, err := b.client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				time.Sleep(3 * time.Second)
				continue
			}

			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}

			var updateResp struct {
				Ok     bool `json:"ok"`
				Result []struct {
					UpdateID int          `json:"update_id"`
					Message  *incomingMsg `json:"message"`
				} `json:"result"`
			}

			if err := json.Unmarshal(body, &updateResp); err != nil {
				time.Sleep(2 * time.Second)
				continue
			}

			if !updateResp.Ok {
				time.Sleep(3 * time.Second)
				continue
			}

			for _, upd := range updateResp.Result {
				offset = upd.UpdateID + 1
				if upd.Message != nil {
					go b.handleIncomingMessage(upd.Message)
				}
			}
		}
	}
}

type incomingMsg struct {
	MessageID int `json:"message_id"`
	From      struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	} `json:"from"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text           string `json:"text"`
	Caption        string `json:"caption"`
	ReplyToMessage *struct {
		MessageID int `json:"message_id"`
		Text      string `json:"text"`
	} `json:"reply_to_message"`
	Photo []struct {
		FileID   string `json:"file_id"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		FileSize int    `json:"file_size"`
	} `json:"photo"`
	Voice *struct {
		FileID   string `json:"file_id"`
		Duration int    `json:"duration"`
	} `json:"voice"`
	Audio *struct {
		FileID   string `json:"file_id"`
		Duration int    `json:"duration"`
	} `json:"audio"`
	Document *struct {
		FileID   string `json:"file_id"`
		FileName string `json:"file_name"`
	} `json:"document"`
	Video *struct {
		FileID string `json:"file_id"`
	} `json:"video"`
}

// checkUserFlood checks if user is sending too many messages too quickly
func (b *FeedbackBotService) checkUserFlood(userID int64, text string) (allow bool, warnMsg string) {
	// Admins are not subject to flood control
	if b.adminSet[userID] {
		return true, ""
	}

	b.floodMu.Lock()
	defer b.floodMu.Unlock()

	now := time.Now()
	state, exists := b.userFloodMap[userID]
	if !exists {
		b.userFloodMap[userID] = &userFloodState{
			lastMsgTime: now,
			lastText:    text,
			msgCount:    1,
		}
		return true, ""
	}

	// 1. Check if user is currently in penalty cooldown
	if now.Before(state.blockedUntil) {
		// Silently drop spam without generating any Telegram/DB load
		return false, ""
	}

	// 2. Debounce: exact duplicate text within 2 seconds
	if text != "" && text == state.lastText && now.Sub(state.lastMsgTime) < 2*time.Second {
		return false, ""
	}

	// 3. Rate limiting: less than 3 seconds since last message
	if now.Sub(state.lastMsgTime) < 3*time.Second {
		state.msgCount++
		state.lastMsgTime = now

		// If user repeatedly spams (3+ messages under 3s interval), mute for 30s
		if state.msgCount >= 3 {
			state.blockedUntil = now.Add(30 * time.Second)
			return false, "⚠️ *Iltimos, ketma-ket xabar yubormang!*\n\nSpamdan himoyalanish maqsadida xabarlaringiz 30 soniyaga cheklandi."
		}

		// Send warning once every 5 seconds to avoid spamming the warning itself
		if now.After(state.warnedUntil) {
			state.warnedUntil = now.Add(5 * time.Second)
			return false, "⏳ *Iltimos, biroz kuting.*\n\nXabarlarni har 3 soniyada 1 martadan ko'p yuborish mumkin emas."
		}

		return false, ""
	}

	// Reset counter if more than 10 seconds have passed
	if now.Sub(state.lastMsgTime) > 10*time.Second {
		state.msgCount = 0
	}

	state.msgCount++
	state.lastMsgTime = now
	state.lastText = text
	return true, ""
}

func (b *FeedbackBotService) handleIncomingMessage(msg *incomingMsg) {
	senderID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	// Check if this is an admin interaction
	if b.adminSet[senderID] || b.adminSet[chatID] {
		// Admin commands
		if text == "/stats" {
			b.sendAdminStats(chatID)
			return
		}
		if text == "/help" {
			b.sendMessage(chatID, "ℹ️ *Admin yordam:*\n\n1. Kelgan har qanday murojaatga to'g'ridan-to'g'ri *Reply* qilib javob yozsangiz, bot orqali ota-onaga yetkaziladi.\n2. /stats — umumiy statistika.", 0)
			return
		}

		// Admin replied to a forwarded feedback message
		if msg.ReplyToMessage != nil {
			b.handleAdminReply(msg)
			return
		}
	}

	// Layer 1: Inbound Flood & Spam Check
	allow, warnMsg := b.checkUserFlood(senderID, text)
	if !allow {
		if warnMsg != "" {
			b.sendMessage(chatID, warnMsg, msg.MessageID)
		}
		return
	}

	// Normal user (/start)
	if text == "/start" {
		welcomeText := "Assalomu alaykum!\n\n" +
			"Ushbu bot *Farzandim* platformasi bo'yicha taklif, fikr-mulohaza yoki texnik kamchiliklar haqida to'g'ridan-to'g'ri dasturchilar jamoasiga xabar berish uchun mo'ljallangan.\n\n" +
			"⚠️ *Muhim eslatma:*\n" +
			"1. Murojaat yozishdan oldin *farzandingizning ismi-familiyasi* va *qaysi maktabda o'qishi* haqida ma'lumot berib keting (aniqlik va tezkor ko'mak uchun).\n" +
			"2. Bu bot *FAQAT* vebsayt va dasturiy ta'minotning ishlashidagi kamchiliklar hamda takliflar uchun!\n" +
			"3. Agar maktab ma'muriyati yoki o'qituvchilarga murojaatingiz bo'lsa — iltimos, *sinf rahbari telefoniga* yoki vebsaytdagi *\"Izohlar\"* bo'limiga murojaat qiling.\n\n" +
			"✍️ Fikringizni matn, rasm yoki ovozli xabar (audio) ko'rinishida yuborishingiz mumkin:"
		b.sendMessage(chatID, welcomeText, 0)
		return
	}

	// Normal user sent feedback (text, photo, voice, audio, document)
	b.processUserFeedback(msg)
}

func (b *FeedbackBotService) processUserFeedback(msg *incomingMsg) {
	senderID := msg.From.ID
	firstName := msg.From.FirstName
	lastName := msg.From.LastName
	username := msg.From.Username

	messageType := "text"
	content := msg.Text
	fileID := ""

	if len(msg.Photo) > 0 {
		messageType = "photo"
		fileID = msg.Photo[len(msg.Photo)-1].FileID
		content = msg.Caption
	} else if msg.Voice != nil {
		messageType = "voice"
		fileID = msg.Voice.FileID
		content = msg.Caption
	} else if msg.Audio != nil {
		messageType = "audio"
		fileID = msg.Audio.FileID
		content = msg.Caption
	} else if msg.Document != nil {
		messageType = "document"
		fileID = msg.Document.FileID
		content = msg.Caption
	} else if msg.Video != nil {
		messageType = "video"
		fileID = msg.Video.FileID
		content = msg.Caption
	}

	if content == "" && fileID == "" {
		return
	}

	// 1. Save feedback to Central DB
	var feedbackID string
	err := b.db.QueryRow(`
		INSERT INTO parent_feedbacks 
		(telegram_user_id, telegram_username, first_name, last_name, message_type, content, telegram_file_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, senderID, username, firstName, lastName, messageType, content, fileID).Scan(&feedbackID)

	if err != nil {
		log.Printf("[FeedbackBot] DB insert error: %v", err)
	}

	// 2. Send instant acknowledgement to parent
	ackText := "✅ *Rahmat! Sizning murojaatingiz qabul qilindi va dasturchilar jamoasiga yetkazildi.*\n\n" +
		"Har bir fikr tizimni yanada mukammal qilishimiz uchun juda muhim. Agar zarur bo'lsa, mutaxassislarimiz shu yerda sizga javob yozishadi."
	b.sendMessage(msg.Chat.ID, ackText, msg.MessageID)

	// 3. Format notification for admins
	senderDisplay := firstName
	if lastName != "" {
		senderDisplay += " " + lastName
	}
	if username != "" {
		senderDisplay += fmt.Sprintf(" (@%s)", username)
	}

	nowStr := time.Now().Format("02.01.2006 15:04")
	adminHeader := fmt.Sprintf(
		"📩 *Yangi murojaat (Feedback)!*\n" +
			"👤 *Yuboruvchi:* %s\n" +
			"🆔 *Foydalanuvchi ID:* `%d`\n" +
			"🕒 *Vaqt:* %s\n" +
			"━━━━━━━━━━━━━━━━━━━━\n",
		senderDisplay, senderID, nowStr,
	)

	adminMsgMap := make(map[string]int)

	for _, adminID := range b.adminIDs {
		var sentMsgID int
		if messageType == "text" {
			fullText := adminHeader + content
			sentMsgID = b.sendMessage(adminID, fullText, 0)
		} else if messageType == "photo" {
			caption := adminHeader
			if content != "" {
				caption += "💬 " + content
			}
			sentMsgID = b.sendPhoto(adminID, fileID, caption, 0)
		} else if messageType == "voice" {
			caption := adminHeader
			if content != "" {
				caption += "💬 " + content
			}
			sentMsgID = b.sendVoice(adminID, fileID, caption, 0)
		} else if messageType == "audio" {
			caption := adminHeader
			if content != "" {
				caption += "💬 " + content
			}
			sentMsgID = b.sendAudio(adminID, fileID, caption, 0)
		} else if messageType == "document" {
			caption := adminHeader
			if content != "" {
				caption += "💬 " + content
			}
			sentMsgID = b.sendDocument(adminID, fileID, caption, 0)
		} else if messageType == "video" {
			caption := adminHeader
			if content != "" {
				caption += "💬 " + content
			}
			sentMsgID = b.sendVideo(adminID, fileID, caption, 0)
		}

		if sentMsgID > 0 {
			key := fmt.Sprintf("%d:%d", adminID, sentMsgID)
			b.mu.Lock()
			b.replyMap[key] = replyTarget{
				FeedbackID: feedbackID,
				UserID:     senderID,
			}
			b.mu.Unlock()

			adminMsgMap[strconv.FormatInt(adminID, 10)] = sentMsgID
		}
	}

	// Update admin_message_ids in DB
	if len(adminMsgMap) > 0 && feedbackID != "" {
		if jsonBytes, err := json.Marshal(adminMsgMap); err == nil {
			_, _ = b.db.Exec("UPDATE parent_feedbacks SET admin_message_ids = $1 WHERE id = $2", string(jsonBytes), feedbackID)
		}
	}
}

func (b *FeedbackBotService) handleAdminReply(msg *incomingMsg) {
	adminChatID := msg.Chat.ID
	replyToMsgID := msg.ReplyToMessage.MessageID

	key := fmt.Sprintf("%d:%d", adminChatID, replyToMsgID)

	b.mu.RLock()
	target, found := b.replyMap[key]
	b.mu.RUnlock()

	// If not found in memory (e.g. server restarted), search PostgreSQL Central DB
	if !found {
		adminKey := strconv.FormatInt(adminChatID, 10)
		var fbID string
		var uID int64
		err := b.db.QueryRow(`
			SELECT id, telegram_user_id 
			FROM parent_feedbacks 
			WHERE admin_message_ids->>$1 = $2 
			ORDER BY created_at DESC 
			LIMIT 1
		`, adminKey, strconv.Itoa(replyToMsgID)).Scan(&fbID, &uID)

		if err == nil && uID > 0 {
			target = replyTarget{
				FeedbackID: fbID,
				UserID:     uID,
			}
			found = true
			b.mu.Lock()
			b.replyMap[key] = target
			b.mu.Unlock()
		}
	}

	if !found {
		b.sendMessage(adminChatID, "⚠️ Ushbu xabar bo'yicha ota-ona topilmadi (ehtimol xabar eskirgan).", msg.MessageID)
		return
	}

	// Forward admin's reply to the parent
	parentUserID := target.UserID
	replyContent := msg.Text
	if replyContent == "" {
		replyContent = msg.Caption
	}

	prefix := "👨‍💻 *Farzandim dasturchilar jamoasidan javob:*\n\n"

	sentSuccess := false
	if len(msg.Photo) > 0 {
		fileID := msg.Photo[len(msg.Photo)-1].FileID
		caption := prefix + replyContent
		msgID := b.sendPhoto(parentUserID, fileID, caption, 0)
		sentSuccess = msgID > 0
	} else if msg.Voice != nil {
		caption := prefix
		if replyContent != "" {
			caption += replyContent
		}
		msgID := b.sendVoice(parentUserID, msg.Voice.FileID, caption, 0)
		sentSuccess = msgID > 0
	} else if msg.Document != nil {
		caption := prefix + replyContent
		msgID := b.sendDocument(parentUserID, msg.Document.FileID, caption, 0)
		sentSuccess = msgID > 0
	} else {
		fullText := prefix + replyContent
		msgID := b.sendMessage(parentUserID, fullText, 0)
		sentSuccess = msgID > 0
	}

	if sentSuccess {
		// Update DB
		if target.FeedbackID != "" {
			_, _ = b.db.Exec(`
				UPDATE parent_feedbacks 
				SET is_replied = true, admin_reply = $1, updated_at = NOW() 
				WHERE id = $2
			`, replyContent, target.FeedbackID)
		}
		b.sendMessage(adminChatID, "✅ *Javobingiz ota-onaga muvaffaqiyatli yetkazildi.*", msg.MessageID)
	} else {
		b.sendMessage(adminChatID, "❌ Ota-onaga xabar yetkazishda xatolik yuz berdi (foydalanuvchi botni bloklagan bo'lishi mumkin).", msg.MessageID)
	}
}

func (b *FeedbackBotService) sendAdminStats(chatID int64) {
	var total, replied, today int
	_ = b.db.QueryRow("SELECT COUNT(*) FROM parent_feedbacks").Scan(&total)
	_ = b.db.QueryRow("SELECT COUNT(*) FROM parent_feedbacks WHERE is_replied = true").Scan(&replied)
	_ = b.db.QueryRow("SELECT COUNT(*) FROM parent_feedbacks WHERE created_at >= CURRENT_DATE").Scan(&today)

	statsText := fmt.Sprintf(
		"📊 *Feedback Bot Statistikasi:*\n\n"+
			"• Jami murojaatlar: *%d*\n"+
			"• Bugungi murojaatlar: *%d*\n"+
			"• Javob berilgan: *%d*\n"+
			"• Javob kutilmoqda: *%d*",
		total, today, replied, (total - replied),
	)
	b.sendMessage(chatID, statsText, 0)
}

// Telegram API Helper Methods with Rate Limiter, Cooldown, and 429 Auto-Backoff

func (b *FeedbackBotService) sendMessage(chatID int64, text string, replyToMessageID int) int {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if replyToMessageID > 0 {
		payload["reply_to_message_id"] = replyToMessageID
	}

	return b.postJSONWithThrottle(apiURL, payload, chatID)
}

func (b *FeedbackBotService) sendPhoto(chatID int64, fileID string, caption string, replyToMessageID int) int {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", b.token)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"photo":      fileID,
		"caption":    caption,
		"parse_mode": "Markdown",
	}
	if replyToMessageID > 0 {
		payload["reply_to_message_id"] = replyToMessageID
	}

	return b.postJSONWithThrottle(apiURL, payload, chatID)
}

func (b *FeedbackBotService) sendVoice(chatID int64, fileID string, caption string, replyToMessageID int) int {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendVoice", b.token)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"voice":      fileID,
		"caption":    caption,
		"parse_mode": "Markdown",
	}
	if replyToMessageID > 0 {
		payload["reply_to_message_id"] = replyToMessageID
	}

	return b.postJSONWithThrottle(apiURL, payload, chatID)
}

func (b *FeedbackBotService) sendAudio(chatID int64, fileID string, caption string, replyToMessageID int) int {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendAudio", b.token)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"audio":      fileID,
		"caption":    caption,
		"parse_mode": "Markdown",
	}
	if replyToMessageID > 0 {
		payload["reply_to_message_id"] = replyToMessageID
	}

	return b.postJSONWithThrottle(apiURL, payload, chatID)
}

func (b *FeedbackBotService) sendDocument(chatID int64, fileID string, caption string, replyToMessageID int) int {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", b.token)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"document":   fileID,
		"caption":    caption,
		"parse_mode": "Markdown",
	}
	if replyToMessageID > 0 {
		payload["reply_to_message_id"] = replyToMessageID
	}

	return b.postJSONWithThrottle(apiURL, payload, chatID)
}

func (b *FeedbackBotService) sendVideo(chatID int64, fileID string, caption string, replyToMessageID int) int {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendVideo", b.token)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"video":      fileID,
		"caption":    caption,
		"parse_mode": "Markdown",
	}
	if replyToMessageID > 0 {
		payload["reply_to_message_id"] = replyToMessageID
	}

	return b.postJSONWithThrottle(apiURL, payload, chatID)
}

// postJSONWithThrottle enforces global rate limit, per-chat cooldown, and handles HTTP 429 auto-backoff
func (b *FeedbackBotService) postJSONWithThrottle(url string, payload interface{}, chatID int64) int {
	// 1. Rate Limiting and Spacing
	b.sendMu.Lock()

	// Global limit: minimum 50ms between any Telegram requests (~20 req/s, Telegram ceiling is 30)
	if time.Since(b.lastGlobalSend) < 50*time.Millisecond {
		time.Sleep(50*time.Millisecond - time.Since(b.lastGlobalSend))
	}

	// Per-chat cooldown: minimum 600ms between messages to the exact same chat
	if lastChatTime, exists := b.lastChatSend[chatID]; exists {
		if time.Since(lastChatTime) < 600*time.Millisecond {
			time.Sleep(600*time.Millisecond - time.Since(lastChatTime))
		}
	}

	b.lastGlobalSend = time.Now()
	b.lastChatSend[chatID] = time.Now()
	b.sendMu.Unlock()

	// 2. Execute with Retry & 429 Auto-Backoff (up to 3 attempts)
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		jsonBytes, err := json.Marshal(payload)
		if err != nil {
			return 0
		}

		resp, err := b.client.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
		if err != nil {
			log.Printf("[FeedbackBot] Post request network error (attempt %d): %v", attempt+1, err)
			time.Sleep(1 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var tgResp struct {
			Ok          bool   `json:"ok"`
			ErrorCode   int    `json:"error_code"`
			Description string `json:"description"`
			Parameters  *struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
			Result struct {
				MessageID int `json:"message_id"`
			} `json:"result"`
		}

		if err := json.Unmarshal(body, &tgResp); err == nil && tgResp.Ok {
			return tgResp.Result.MessageID
		}

		// Layer 4: Handle HTTP 429 Too Many Requests
		if resp.StatusCode == 429 || tgResp.ErrorCode == 429 {
			retryAfter := 5
			if tgResp.Parameters != nil && tgResp.Parameters.RetryAfter > 0 {
				retryAfter = tgResp.Parameters.RetryAfter
			}
			log.Printf("[FeedbackBot RATE-LIMIT] HTTP 429 detected! Backing off for %d seconds...", retryAfter)
			time.Sleep(time.Duration(retryAfter+1) * time.Second)
			continue
		}

		log.Printf("[FeedbackBot] Telegram API response not OK: %s", string(body))
		break
	}

	return 0
}
