package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port         string
	CentralDBURL string
	PGRootURL    string // Administrative connection to run CREATE DATABASE
	JWTSecret    string
}

func LoadConfig() *Config {
	// Load .env if present
	_ = godotenv.Load()

	port := getEnv("PORT", "8080")
	centralDBURL := getEnv("CENTRAL_DB_URL", "")
	pgRootURL := getEnv("PG_ROOT_URL", "")
	jwtSecret := getEnv("JWT_SECRET", "super-secret-key")

	if centralDBURL == "" {
		log.Println("WARNING: CENTRAL_DB_URL is not set")
	}
	if pgRootURL == "" {
		log.Println("WARNING: PG_ROOT_URL is not set (required for creating tenant databases)")
	}

	return &Config{
		Port:         port,
		CentralDBURL: centralDBURL,
		PGRootURL:    pgRootURL,
		JWTSecret:    jwtSecret,
	}
}

func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}
