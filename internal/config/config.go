package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                string
	CentralDBURL        string
	PGRootURL           string // Administrative connection to run CREATE DATABASE
	JWTSecret           string
	AllowedOriginDomain string // Production domain e.g. farzandim.uz
	TelegramBotToken    string
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

	if centralDBURL == "" {
		log.Println("WARNING: CENTRAL_DB_URL is not set")
	}
	if pgRootURL == "" {
		log.Println("WARNING: PG_ROOT_URL is not set (required for creating tenant databases)")
	}

	return &Config{
		Port:                port,
		CentralDBURL:        centralDBURL,
		PGRootURL:           pgRootURL,
		JWTSecret:           jwtSecret,
		AllowedOriginDomain: allowedOriginDomain,
		TelegramBotToken:    telegramBotToken,
	}
}

func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}
