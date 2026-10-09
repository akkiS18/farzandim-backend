package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                string
	CentralDBURL        string
	PGRootURL           string // Administrative connection to run CREATE DATABASE
	JWTSecret           string
	AllowedOriginDomain  string // Production domain e.g. farzandim.uz
	TelegramBotToken     string
	FeedbackBotToken     string
	FeedbackAdminChatIDs []int64
}

func LoadConfig() *Config {
	// Load .env if present
	_ = godotenv.Load()

	port := getEnv("PORT", "6560")
	centralDBURL := getEnv("CENTRAL_DB_URL", "")
	pgRootURL := getEnv("PG_ROOT_URL", "")
	jwtSecret := getEnv("JWT_SECRET", "super-secret-key")
	allowedOriginDomain := getEnv("ALLOWED_ORIGIN_DOMAIN", "")
	telegramBotToken := getEnv("TELEGRAM_BOT_TOKEN", "")
	feedbackBotToken := getEnv("FEEDBACK_BOT_TOKEN", "")
	adminIDsStr := getEnv("FEEDBACK_ADMIN_CHAT_IDS", "")

	var feedbackAdminChatIDs []int64
	if adminIDsStr != "" {
		for _, part := range strings.Split(adminIDsStr, ",") {
			part = strings.TrimSpace(part)
			if id, err := strconv.ParseInt(part, 10, 64); err == nil {
				feedbackAdminChatIDs = append(feedbackAdminChatIDs, id)
			}
		}
	}

	if centralDBURL == "" {
		log.Println("WARNING: CENTRAL_DB_URL is not set")
	}
	if pgRootURL == "" {
		log.Println("WARNING: PG_ROOT_URL is not set (required for creating tenant databases)")
	}

	return &Config{
		Port:                 port,
		CentralDBURL:         centralDBURL,
		PGRootURL:            pgRootURL,
		JWTSecret:            jwtSecret,
		AllowedOriginDomain:  allowedOriginDomain,
		TelegramBotToken:     telegramBotToken,
		FeedbackBotToken:     feedbackBotToken,
		FeedbackAdminChatIDs: feedbackAdminChatIDs,
	}
}

func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}
