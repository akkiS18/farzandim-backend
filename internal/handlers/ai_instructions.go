package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
)

type AIInstructionHandler struct{}

func NewAIInstructionHandler() *AIInstructionHandler {
	return &AIInstructionHandler{}
}

func ensureAIInstructionsTable(db *sql.DB) {
	if db == nil {
		return
	}
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS ai_instructions (
			id SERIAL PRIMARY KEY,
			title VARCHAR(255) NOT NULL DEFAULT 'Haftalik Pedagogik Tahlil Prompti',
			system_instruction TEXT NOT NULL,
			max_tokens INT NOT NULL DEFAULT 1000,
			temperature DOUBLE PRECISION NOT NULL DEFAULT 0.7,
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			updated_by_user_id INT REFERENCES users(id) ON DELETE SET NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS ai_instruction_logs (
			id SERIAL PRIMARY KEY,
			instruction_id INT REFERENCES ai_instructions(id) ON DELETE CASCADE,
			system_instruction TEXT NOT NULL,
			max_tokens INT NOT NULL,
			temperature DOUBLE PRECISION NOT NULL DEFAULT 0.7,
			changed_by_user_id INT REFERENCES users(id) ON DELETE SET NULL,
			changed_by_user_name VARCHAR(255),
			change_reason TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
}

// GetAIInstruction returns the currently active AI system instruction
func (h *AIInstructionHandler) GetAIInstruction(c *gin.Context) {
	tenantDBVal, exists := c.Get("tenantDB")
	if !exists || tenantDBVal == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant DB ulanishi topilmadi"})
		return
	}
	dbConn := tenantDBVal.(*sql.DB)
	ensureAIInstructionsTable(dbConn)

	var inst models.AIInstruction
	var updatedUserID sql.NullInt64
	var firstName, lastName sql.NullString

	err := dbConn.QueryRow(`
		SELECT i.id, i.title, i.system_instruction, i.max_tokens, i.temperature, i.is_active, 
		       i.updated_by_user_id, u.first_name, u.last_name, i.created_at, i.updated_at
		FROM ai_instructions i
		LEFT JOIN users u ON i.updated_by_user_id = u.id
		WHERE i.is_active = true
		ORDER BY i.id DESC LIMIT 1
	`).Scan(
		&inst.ID, &inst.Title, &inst.SystemInstruction, &inst.MaxTokens, &inst.Temperature, &inst.IsActive,
		&updatedUserID, &firstName, &lastName, &inst.CreatedAt, &inst.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			// Seed initial instruction if missing
			defaultPrompt := `Siz 'Farzandim' maktab o'quvchilarini baholash tizimining tajribali, mehribon va samimiy Pedagogik Maslahatchi AI yordamchisiz.
Murojaatingiz ota-onaga nisbatan juda hurmatli, iliq va qo'llab-quvvatlovchi bo'lsin. Hech qanday rasmiy ravishda '0', '0.00' yoki 'yo'q' kabi sovuq iboralarni ishlatmang.

Farzand ismi: {StudentName} ({ClassName} sinf o'quvchisi).
Hafta sanasi: {WeekStartDate} dan {WeekEndDate} gacha.

Haftalik ko'rsatkichlar:
- O'rtacha baho: {CurrentAverageGrade}
- Baholar: {Grades}
- O'qituvchi izohlari: {TeacherComments}
- O'qilgan kitoblar: {BooksRead}
- O'tgan haftalik o'rtacha baho: {PrevAverageGrade}

MUHIM TASHKILIY QOIDALAR:
1. Hech qanday emojilardan foydalanmang.
2. Javobingizni aniq belgilangan sarlavhalar bilan taqdim eting:

---SECTION: HAFTALIK XULOSA---
(Farzandning haftalik o'quv faoliyati va holati bo'yicha 2-3 jumlali samimiy va ilhomlantiruvchi xulosa.)

---SECTION: FANLAR VA KITOBXONLIK TAHLILI---
(Qaysi fanlarda a'lo o'zlashtiryapti va mutolaa ko'rsatkichlari bo'yicha tahlil.)

---SECTION: OTA-ONAGA AMALIY TAVSIYA---
(Uydan turib farzandiga yordam berish bo'yicha yagona, qisqa va eng muhim bitta pedagogik maslahat. Ro'yxat qilmang, faqat 1-2 jumlada.)`

			var newID int
			errInsert := dbConn.QueryRow(`
				INSERT INTO ai_instructions (title, system_instruction, max_tokens, temperature, is_active)
				VALUES ($1, $2, 1000, 0.7, true)
				RETURNING id, created_at, updated_at
			`, "Haftalik Pedagogik Tahlil Prompti", defaultPrompt).Scan(&newID, &inst.CreatedAt, &inst.UpdatedAt)

			if errInsert != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Boshlang'ich AI instruction yaratishda xatolik: " + errInsert.Error()})
				return
			}
			inst.ID = newID
			inst.Title = "Haftalik Pedagogik Tahlil Prompti"
			inst.SystemInstruction = defaultPrompt
			inst.MaxTokens = 1000
			inst.Temperature = 0.7
			inst.IsActive = true
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "AI instruction yuklashda xatolik: " + err.Error()})
			return
		}
	}

	if updatedUserID.Valid {
		uid := int(updatedUserID.Int64)
		inst.UpdatedByUserID = &uid
		if firstName.Valid || lastName.Valid {
			inst.UpdatedByName = strings.TrimSpace(firstName.String + " " + lastName.String)
		}
	}

	c.JSON(http.StatusOK, gin.H{"instruction": inst})
}

type UpdateAIInstructionRequest struct {
	Title             string  `json:"title"`
	SystemInstruction string  `json:"system_instruction" binding:"required"`
	MaxTokens         int     `json:"max_tokens" binding:"required"`
	Temperature       float64 `json:"temperature"`
	ChangeReason      string  `json:"change_reason"`
}

// UpdateAIInstruction updates active AI system instruction and archives previous version
func (h *AIInstructionHandler) UpdateAIInstruction(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)
	ensureAIInstructionsTable(dbConn)

	var req UpdateAIInstructionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "So'rov parametri xato: " + err.Error()})
		return
	}

	if req.MaxTokens < 100 || req.MaxTokens > 8000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Max tokens chegarasi 100 va 8000 oralig'ida bo'lishi kerak"})
		return
	}

	if req.Temperature <= 0 {
		req.Temperature = 0.7
	}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = "Haftalik Pedagogik Tahlil Prompti"
	}

	userIDVal, _ := c.Get("userID")
	userIDStr := fmt.Sprintf("%v", userIDVal)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// Fetch current user full name for logging
	var currentUserName string
	errName := dbConn.QueryRow("SELECT first_name || ' ' || last_name FROM users WHERE id = $1", currentUserID).Scan(&currentUserName)
	if errName != nil || strings.TrimSpace(currentUserName) == "" {
		currentUserName = "Administrator"
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiya boshlashda xatolik: " + err.Error()})
		return
	}
	defer tx.Rollback()

	// Fetch current active instruction to log history
	var oldID int
	var oldPrompt string
	var oldMaxTokens int
	var oldTemp float64

	errOld := tx.QueryRow(`
		SELECT id, system_instruction, max_tokens, temperature 
		FROM ai_instructions 
		WHERE is_active = true 
		ORDER BY id DESC LIMIT 1
	`).Scan(&oldID, &oldPrompt, &oldMaxTokens, &oldTemp)

	if errOld == nil && oldID > 0 {
		reason := req.ChangeReason
		if strings.TrimSpace(reason) == "" {
			reason = "Admin tomonidan AI instruction tahrirlandi"
		}
		// Archive previous version into ai_instruction_logs
		_, errLog := tx.Exec(`
			INSERT INTO ai_instruction_logs (instruction_id, system_instruction, max_tokens, temperature, changed_by_user_id, changed_by_user_name, change_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, oldID, oldPrompt, oldMaxTokens, oldTemp, currentUserID, currentUserName, reason)

		if errLog != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Eski prompt tarixini saqlashda xatolik: " + errLog.Error()})
			return
		}

		// Update existing active instruction
		_, errUpdate := tx.Exec(`
			UPDATE ai_instructions
			SET title = $1, system_instruction = $2, max_tokens = $3, temperature = $4, updated_by_user_id = $5, updated_at = NOW()
			WHERE id = $6
		`, req.Title, req.SystemInstruction, req.MaxTokens, req.Temperature, currentUserID, oldID)

		if errUpdate != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "AI instruction yangilashda xatolik: " + errUpdate.Error()})
			return
		}
	} else {
		// Insert brand new active instruction row
		_, errInsert := tx.Exec(`
			INSERT INTO ai_instructions (title, system_instruction, max_tokens, temperature, is_active, updated_by_user_id)
			VALUES ($1, $2, $3, $4, true, $5)
		`, req.Title, req.SystemInstruction, req.MaxTokens, req.Temperature, currentUserID)

		if errInsert != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Yangi AI instruction saqlashda xatolik: " + errInsert.Error()})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiyani yakunlashda xatolik: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "AI System Instruction muvaffaqiyatli saqlandi va tarixga kiritildi",
	})
}

// GetAIInstructionHistory returns all past prompt versions from logs
func (h *AIInstructionHandler) GetAIInstructionHistory(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)
	ensureAIInstructionsTable(dbConn)

	rows, err := dbConn.Query(`
		SELECT id, instruction_id, system_instruction, max_tokens, temperature, 
		       changed_by_user_id, COALESCE(changed_by_user_name, 'Admin'), COALESCE(change_reason, ''), created_at
		FROM ai_instruction_logs
		ORDER BY id DESC LIMIT 50
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Prompt tarixini olishda xatolik: " + err.Error()})
		return
	}
	defer rows.Close()

	var logs []models.AIInstructionLog
	for rows.Next() {
		var l models.AIInstructionLog
		var userID sql.NullInt64
		if err := rows.Scan(&l.ID, &l.InstructionID, &l.SystemInstruction, &l.MaxTokens, &l.Temperature, &userID, &l.ChangedByName, &l.ChangeReason, &l.CreatedAt); err == nil {
			if userID.Valid {
				uid := int(userID.Int64)
				l.ChangedByUserID = &uid
			}
			logs = append(logs, l)
		}
	}

	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// RevertAIInstruction restores a specific historic prompt version from logs
func (h *AIInstructionHandler) RevertAIInstruction(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)
	ensureAIInstructionsTable(dbConn)

	logIDStr := c.Param("log_id")
	logID, err := strconv.Atoi(logIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri log_id parametridir"})
		return
	}

	userIDVal, _ := c.Get("userID")
	userIDStr := fmt.Sprintf("%v", userIDVal)
	currentUserID, _ := strconv.Atoi(userIDStr)

	var currentUserName string
	errName := dbConn.QueryRow("SELECT first_name || ' ' || last_name FROM users WHERE id = $1", currentUserID).Scan(&currentUserName)
	if errName != nil || strings.TrimSpace(currentUserName) == "" {
		currentUserName = "Administrator"
	}

	// Fetch target log
	var targetLog models.AIInstructionLog
	errLog := dbConn.QueryRow(`
		SELECT id, instruction_id, system_instruction, max_tokens, temperature 
		FROM ai_instruction_logs 
		WHERE id = $1
	`, logID).Scan(&targetLog.ID, &targetLog.InstructionID, &targetLog.SystemInstruction, &targetLog.MaxTokens, &targetLog.Temperature)

	if errLog != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Tanlangan tarixiy prompt versiyasi topilmadi"})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiya boshlashda xatolik"})
		return
	}
	defer tx.Rollback()

	// Fetch current active row
	var activeID int
	var activePrompt string
	var activeMaxTokens int
	var activeTemp float64

	errActive := tx.QueryRow(`
		SELECT id, system_instruction, max_tokens, temperature 
		FROM ai_instructions 
		WHERE is_active = true 
		ORDER BY id DESC LIMIT 1
	`).Scan(&activeID, &activePrompt, &activeMaxTokens, &activeTemp)

	if errActive == nil && activeID > 0 {
		// Archive current state before reverting
		_, _ = tx.Exec(`
			INSERT INTO ai_instruction_logs (instruction_id, system_instruction, max_tokens, temperature, changed_by_user_id, changed_by_user_name, change_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, activeID, activePrompt, activeMaxTokens, activeTemp, currentUserID, currentUserName, fmt.Sprintf("Revert qilindi: Version #%d ga qaytarishdan oldingi holat", logID))

		// Update active instruction to historical values
		_, errUpdate := tx.Exec(`
			UPDATE ai_instructions
			SET system_instruction = $1, max_tokens = $2, temperature = $3, updated_by_user_id = $4, updated_at = NOW()
			WHERE id = $5
		`, targetLog.SystemInstruction, targetLog.MaxTokens, targetLog.Temperature, currentUserID, activeID)

		if errUpdate != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Promptni qaytarishda xatolik: " + errUpdate.Error()})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiyani saqlashda xatolik: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("AI System Instruction Version #%d ga muvaffaqiyatli qaytarildi", logID),
	})
}
