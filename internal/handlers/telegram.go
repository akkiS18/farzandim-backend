package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/db"
	"github.com/farzandim/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type TelegramHandler struct{}

func NewTelegramHandler() *TelegramHandler {
	return &TelegramHandler{}
}

// GetTelegramConfig handler
func (h *TelegramHandler) GetTelegramConfig(c *gin.Context) {
	schoolID := c.GetHeader("X-School-ID")
	if schoolID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "X-School-ID header talab qilinadi"})
		return
	}

	var botToken, botUsername string
	err := db.CentralDB.QueryRow(`
		SELECT COALESCE(bot_token, ''), COALESCE(bot_username, '') 
		FROM schools 
		WHERE id = $1 AND is_deleted = false
	`, schoolID).Scan(&botToken, &botUsername)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Central DB-dan maktab ma'lumotlarini yuklashda xatolik"})
		return
	}

	maskedToken := ""
	if botToken != "" {
		parts := strings.Split(botToken, ":")
		if len(parts) > 1 {
			maskedToken = parts[0] + ":********************"
		} else {
			maskedToken = "********************"
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"bot_token":    maskedToken,
		"bot_username": botUsername,
		"has_token":    botToken != "",
	})
}

// SaveTelegramConfig handler
func (h *TelegramHandler) SaveTelegramConfig(c *gin.Context) {
	schoolID := c.GetHeader("X-School-ID")
	if schoolID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "X-School-ID header talab qilinadi"})
		return
	}

	var req struct {
		BotToken string `json:"bot_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Token kiritish majburiy"})
		return
	}

	token := strings.TrimSpace(req.BotToken)

	// 1. Verify token with Telegram API getMe
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Telegram API bilan bog'lanib bo'lmadi, token xato yoki internet yo'q"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Telegram Bot Tokeni yaroqsiz (API xatosi)"})
		return
	}

	var getMeRes struct {
		Ok     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&getMeRes); err != nil || !getMeRes.Ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Telegram API javobini qayta ishlashda xatolik"})
		return
	}

	botUsername := getMeRes.Result.Username

	// 2. Save to Central DB
	_, err = db.CentralDB.Exec(`
		UPDATE schools 
		SET bot_token = $1, bot_username = $2, updated_at = NOW() 
		WHERE id = $3 AND is_deleted = false
	`, token, botUsername, schoolID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Sozlamani saqlashda bazada xatolik: " + err.Error()})
		return
	}

	// 3. Dynamically reload bot loop in BotManager
	services.Manager.StartBotForSchool(schoolID, token)

	c.JSON(http.StatusOK, gin.H{
		"message":      "Telegram Bot muvaffaqiyatli sozlandi va ishga tushirildi",
		"bot_username": botUsername,
	})
}
