package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/farzandim/backend/internal/middleware"
	"github.com/joho/godotenv"
)

type Config struct {
	Port                string
	CentralDBURL        string
	PGRootURL           string // Administrative connection to run CREATE DATABASE
	JWTSecret           string
	// AllowedOriginDomains is parsed from ALLOWED_ORIGIN_DOMAIN, a comma-separated list of
	// base domains, e.g. "akademx.uz,farzandim-edu.uz". Each domain and all its subdomains
	// are allowed as https CORS origins.
	AllowedOriginDomains []string
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
	jwtSecret := getEnv("JWT_SECRET", "")
	if len(strings.TrimSpace(jwtSecret)) < 32 || jwtSecret == "super-secret-key" {
		log.Fatal("JWT_SECRET must be explicitly set to a strong secret of at least 32 bytes")
	}
	// The platform domains are always allowed (the old and the new one), so CORS keeps working
	// even if ALLOWED_ORIGIN_DOMAIN is not updated; the env value only adds extra domains.
	allowedOriginDomains := middleware.ParseAllowedDomains("akademx.uz,farzandim-edu.uz," + getEnv("ALLOWED_ORIGIN_DOMAIN", ""))
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
		AllowedOriginDomains: allowedOriginDomains,
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

