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
				url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=20", apiURL, offset)
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

func (bm *BotManager) sendTextMessage(token string, chatID int64, text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	jsonBytes, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		log.Printf("Failed to send telegram message: %v", err)
		return
	}
	defer resp.Body.Close()
}

// SendAnnouncementNotification sends the announcement to all configured parents
func SendAnnouncementNotification(schoolID string, ann *models.Announcement) {
	var token string
	var schoolName string
	err := db.CentralDB.QueryRow("SELECT bot_token, name FROM schools WHERE id = $1 AND is_deleted = false", schoolID).Scan(&token, &schoolName)
	if err != nil || token == "" {
		return
	}

	tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
	if err != nil {
		log.Printf("SendAnnouncementNotification failed to get tenant DB: %v", err)
		return
	}

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

	if err != nil {
		log.Printf("Failed to query parent Telegram IDs: %v", err)
		return
	}
	defer rows.Close()

	var telegramIDs []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err == nil {
			telegramIDs = append(telegramIDs, tid)
		}
	}

	if len(telegramIDs) == 0 {
		return
	}

	msgText := fmt.Sprintf("📢 *Yangi e'lon! (%s)*\n\n📌 *%s*\n\n%s", schoolName, ann.Title, ann.Content)

	go func() {
		for _, tid := range telegramIDs {
			chatID := int64(0)
			fmt.Sscanf(tid, "%d", &chatID)
			if chatID != 0 {
				Manager.sendTextMessage(token, chatID, msgText)
				time.Sleep(35 * time.Millisecond)
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
