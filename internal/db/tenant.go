package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

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

	// Tune connection pool settings for high concurrency and resource protection
	maxOpen := 35
	if envMax := os.Getenv("DB_MAX_OPEN_CONNS"); envMax != "" {
		if val, err := strconv.Atoi(envMax); err == nil && val > 0 {
			maxOpen = val
		}
	}
	maxIdle := maxOpen / 2
	if maxIdle < 15 {
		maxIdle = 15
	}

	newDB.SetMaxOpenConns(maxOpen)
	newDB.SetMaxIdleConns(maxIdle)
	newDB.SetConnMaxLifetime(10 * time.Minute)

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
	if dbName == "db_f_" || len(dbName) > 63 {
		return "", fmt.Errorf("school name must produce a non-empty database identifier of at most 63 bytes")
	}

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

	if exists {
		return "", fmt.Errorf("database identifier already exists; choose a different school name")
	}
	{
		// CREATE DATABASE cannot be executed in a transaction, directExec is safe since dbName contains only lowercase ASCII letters, digits and underscores
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

	if err := migrateTenantConnection(tenantConnStr); err != nil {
		// Do not drop a database automatically after a failed migration.
		// An operator must inspect and recover the unregistered database.
		return "", err
	}
	return tenantConnStr, nil
}

// Migrate only the registered connection; never derive an existing tenant DB from its name.
func migrateTenantConnection(connStr string) error {
	tenantDB, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to open tenant database")
	}
	defer tenantDB.Close()
	driver, err := postgres.WithInstance(tenantDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create tenant migration driver")
	}
	m, err := migrate.NewWithDatabaseInstance("file://migrations/tenant", "postgres", driver)
	if err != nil {
		return fmt.Errorf("failed to load tenant migrations")
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		// Dirty migrations require operator recovery; never silently Force the version.
		return fmt.Errorf("tenant migration failed: %w", err)
	}
	return nil
}

// MigrateAllTenants runs migrations on all existing tenant databases listed in the central DB
func (tm *TenantManager) MigrateAllTenants(_ string) error {
	rows, err := CentralDB.Query("SELECT id, name, db_connection_string FROM schools WHERE is_deleted = false")
	if err != nil {
		return fmt.Errorf("failed to query schools for migration: %w", err)
	}
	defer rows.Close()

	var migrationErrors []error
	for rows.Next() {
		var id, name, connStr string
		if err := rows.Scan(&id, &name, &connStr); err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("failed to read tenant metadata"))
			continue
		}

		log.Printf("Running migrations for school: %s (%s)", name, id)
		err = migrateTenantConnection(connStr)
		if err != nil {
			log.Printf("Failed to migrate database for school %s: %v", name, err)
			migrationErrors = append(migrationErrors, fmt.Errorf("school %s migration failed", id))
		}
	}
	if err := rows.Err(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to read tenant list"))
	}
	return errors.Join(migrationErrors...)
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

	rows, err := CentralDB.Query("SELECT id, name, db_connection_string FROM schools WHERE is_deleted = false ORDER BY created_at ASC")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var firstSchoolID string

	for rows.Next() {
		var id, name, connStr string
		if err := rows.Scan(&id, &name, &connStr); err != nil {
			continue
		}

		if firstSchoolID == "" {
			firstSchoolID = id
		}

		u, err := url.Parse(connStr)
		if err != nil {
			continue
		}
		dbName := strings.TrimPrefix(u.Path, "/")

		// Match by db_name (db_f_test_school == db_f_test_school, or cleanSub without prefix)
		if dbName == searchName || dbName == cleanSub || strings.TrimPrefix(dbName, "db_f_") == cleanSub {
			return id, nil
		}

		// Also match by school name (case-insensitive, normalized)
		normalizedName := sanitizeSubdomain(name)
		if normalizedName == cleanSub {
			return id, nil
		}
	}

	// For localhost development, fallback to first available school
	if (subdomain == "localhost" || cleanSub == "localhost") && firstSchoolID != "" {
		return firstSchoolID, nil
	}

	return "", fmt.Errorf("no school database found matching subdomain: %s", subdomain)
}

// FindSchoolIDByUserIdentifier searches all active school databases for a user matching the phone number or passport
func FindSchoolIDByUserIdentifier(identifier string) (string, error) {
	reg := regexp.MustCompile("[^0-9]+")
	digits := reg.ReplaceAllString(identifier, "")
	cleanDoc := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(identifier), " ", ""))

	if digits == "" && cleanDoc == "" {
		return "", fmt.Errorf("empty identifier provided")
	}

	rows, err := CentralDB.Query("SELECT id FROM schools WHERE is_deleted = false ORDER BY created_at ASC")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var schoolIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			schoolIDs = append(schoolIDs, id)
		}
	}

	var matchedSchoolID string
	var matchCount int

	for _, sID := range schoolIDs {
		tenantDB, err := TenantConnManager.GetTenantDB(sID)
		if err != nil {
			continue
		}
		var exists bool
		query := `SELECT EXISTS(
			SELECT 1 FROM users 
			WHERE (
				($1 != '' AND (phone = $1 OR REGEXP_REPLACE(phone, '\D', '', 'g') = $1))
				OR ($2 != '' AND (UPPER(TRIM(document_no)) = $2 OR UPPER(TRIM(passport)) = $2))
			) AND is_deleted = false
		)`
		if err := tenantDB.QueryRow(query, digits, cleanDoc).Scan(&exists); err == nil && exists {
			matchedSchoolID = sID
			matchCount++
		}
	}

	if matchCount == 1 {
		return matchedSchoolID, nil
	} else if matchCount > 1 {
		return "", fmt.Errorf("foydalanuvchi bir nechta maktabda mavjud, iltimos maktabni tanlang")
	}

	return "", fmt.Errorf("ushbu raqam yoki pasport bo'yicha hech qaysi maktabda foydalanuvchi topilmadi")
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

