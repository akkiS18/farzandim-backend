package db

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

type TenantManager struct {
	connections map[string]*sql.DB
	mu          sync.RWMutex
}

var TenantConnManager *TenantManager

func InitTenantManager() {
	TenantConnManager = &TenantManager{
		connections: make(map[string]*sql.DB),
	}
}

// GetTenantDB resolves or opens a connection pool to a school's tenant database
func (tm *TenantManager) GetTenantDB(schoolID string) (*sql.DB, error) {
	tm.mu.RLock()
	db, exists := tm.connections[schoolID]
	tm.mu.RUnlock()

	if exists {
		return db, nil
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Double-checked locking
	if db, exists = tm.connections[schoolID]; exists {
		return db, nil
	}

	// Fetch connection string from Central Database
	var connStr string
	err := CentralDB.QueryRow(
		"SELECT db_connection_string FROM schools WHERE id = $1 AND is_deleted = false",
		schoolID,
	).Scan(&connStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("school with ID %s not found", schoolID)
		}
		return nil, fmt.Errorf("failed to query central DB: %w", err)
	}

	// Open connection
	newDB, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open tenant DB connection: %w", err)
	}

	if err := newDB.Ping(); err != nil {
		newDB.Close()
		return nil, fmt.Errorf("failed to ping tenant DB: %w", err)
	}

	tm.connections[schoolID] = newDB
	log.Printf("Successfully established connection to Tenant DB for School ID: %s", schoolID)

	return newDB, nil
}

// CreateAndMigrateTenantDB provisions a new database and executes golang-migrate
func (tm *TenantManager) CreateAndMigrateTenantDB(pgRootURL string, schoolUUID string, schoolName string) (string, error) {
	// Clean school name to make safe PostgreSQL database name starting with db_f_
	dbName := sanitizeDBName(schoolName)

	// 1. Connect to root DB and execute CREATE DATABASE
	rootDB, err := sql.Open("postgres", pgRootURL)
	if err != nil {
		return "", fmt.Errorf("failed to open pg root connection: %w", err)
	}
	defer rootDB.Close()

	// Check if DB already exists
	var exists bool
	err = rootDB.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", dbName).Scan(&exists)
	if err != nil {
		return "", fmt.Errorf("failed to check database existence: %w", err)
	}

	if !exists {
		// CREATE DATABASE cannot be executed in a transaction, directExec is safe since dbName is formatted from UUID
		_, err = rootDB.Exec(fmt.Sprintf("CREATE DATABASE %s", dbName))
		if err != nil {
			return "", fmt.Errorf("failed to execute CREATE DATABASE: %w", err)
		}
		log.Printf("Database %s created successfully", dbName)
	}

	// 2. Build connection string for the new tenant DB
	tenantConnStr, err := buildTenantConnStr(pgRootURL, dbName)
	if err != nil {
		return "", fmt.Errorf("failed to build connection string: %w", err)
	}

	// 3. Open connection to new tenant DB to run migrations
	tenantDB, err := sql.Open("postgres", tenantConnStr)
	if err != nil {
		return "", fmt.Errorf("failed to connect to new tenant DB for migration: %w", err)
	}
	defer tenantDB.Close()

	// 4. Run migrations
	driver, err := postgres.WithInstance(tenantDB, &postgres.Config{})
	if err != nil {
		return "", fmt.Errorf("failed to create migration driver: %w", err)
	}

	// golang-migrate will load SQL from backend/migrations/tenant folder
	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations/tenant",
		dbName, driver,
	)
	if err != nil {
		return "", fmt.Errorf("failed to load migration files: %w", err)
	}

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return "", fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Printf("Migrations applied successfully to %s", dbName)
	return tenantConnStr, nil
}

// MigrateAllTenants runs migrations on all existing tenant databases listed in the central DB
func (tm *TenantManager) MigrateAllTenants(pgRootURL string) error {
	rows, err := CentralDB.Query("SELECT id, name, db_connection_string FROM schools WHERE is_deleted = false")
	if err != nil {
		return fmt.Errorf("failed to query schools for migration: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, connStr string
		if err := rows.Scan(&id, &name, &connStr); err != nil {
			log.Printf("Failed to scan school row: %v", err)
			continue
		}

		log.Printf("Running migrations for school: %s (%s)", name, id)
		_, err = tm.CreateAndMigrateTenantDB(pgRootURL, id, name)
		if err != nil {
			log.Printf("Failed to migrate database for school %s: %v", name, err)
		}
	}
	return nil
}

func sanitizeDBName(schoolName string) string {
	// Convert to lowercase
	name := strings.ToLower(schoolName)

	// Replace non-alphanumeric characters with underscores
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}

	cleaned := sb.String()
	// Replace multiple consecutive underscores with a single one
	for strings.Contains(cleaned, "__") {
		cleaned = strings.ReplaceAll(cleaned, "__", "_")
	}
	cleaned = strings.Trim(cleaned, "_")

	return "db_f_" + cleaned
}

func buildTenantConnStr(rootURL string, dbName string) (string, error) {
	u, err := url.Parse(rootURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

func FindSchoolIDBySubdomain(subdomain string) (string, error) {
	cleanSub := sanitizeSubdomain(subdomain)
	searchName := "db_f_" + cleanSub

	if cleanSub == "localhost" {
		searchName = "db_f_test_school"
	}

	rows, err := CentralDB.Query("SELECT id, db_connection_string FROM schools WHERE is_deleted = false")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var fallbackID string
	for rows.Next() {
		var id, connStr string
		if err := rows.Scan(&id, &connStr); err != nil {
			continue
		}

		u, err := url.Parse(connStr)
		if err != nil {
			continue
		}
		dbName := strings.TrimPrefix(u.Path, "/")

		if dbName == searchName {
			return id, nil
		}

		// Fallback for previous school setups (maktab-21) on localhost
		if cleanSub == "localhost" && dbName == "db_f_maktab_21" {
			fallbackID = id
		}
	}

	if fallbackID != "" {
		return fallbackID, nil
	}

	return "", fmt.Errorf("no school found matching subdomain: %s (target database: %s)", subdomain, searchName)
}

func sanitizeSubdomain(subdomain string) string {
	name := strings.ToLower(subdomain)
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	cleaned := sb.String()
	for strings.Contains(cleaned, "__") {
		cleaned = strings.ReplaceAll(cleaned, "__", "_")
	}
	return strings.Trim(cleaned, "_")
}
