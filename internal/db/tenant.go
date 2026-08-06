package db

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
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
		_, _ = db.Exec(`
			CREATE TABLE IF NOT EXISTS date_range_presets (
				id SERIAL PRIMARY KEY,
				name VARCHAR(255) NOT NULL,
				start_date DATE NOT NULL,
				end_date DATE NOT NULL,
				category VARCHAR(100) NOT NULL DEFAULT 'schedule',
				created_by INT NULL REFERENCES users(id) ON DELETE SET NULL,
				is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
				deleted_at TIMESTAMP NULL,
				created_at TIMESTAMP NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMP NOT NULL DEFAULT NOW()
			);
			CREATE TABLE IF NOT EXISTS target_presets (
				id SERIAL PRIMARY KEY,
				name VARCHAR(255) NOT NULL,
				target_levels INT[] DEFAULT '{}',
				target_classes INT[] DEFAULT '{}',
				target_students INT[] DEFAULT '{}',
				created_by INT NULL REFERENCES users(id) ON DELETE SET NULL,
				is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
				deleted_at TIMESTAMP NULL,
				created_at TIMESTAMP NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMP NOT NULL DEFAULT NOW()
			);
		`)
		return db, nil
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Double-checked locking
	if db, exists = tm.connections[schoolID]; exists {
		_, _ = db.Exec(`
			CREATE TABLE IF NOT EXISTS date_range_presets (
				id SERIAL PRIMARY KEY,
				name VARCHAR(255) NOT NULL,
				start_date DATE NOT NULL,
				end_date DATE NOT NULL,
				category VARCHAR(100) NOT NULL DEFAULT 'schedule',
				created_by INT NULL REFERENCES users(id) ON DELETE SET NULL,
				is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
				deleted_at TIMESTAMP NULL,
				created_at TIMESTAMP NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMP NOT NULL DEFAULT NOW()
			);
			CREATE TABLE IF NOT EXISTS target_presets (
				id SERIAL PRIMARY KEY,
				name VARCHAR(255) NOT NULL,
				target_levels INT[] DEFAULT '{}',
				target_classes INT[] DEFAULT '{}',
				target_students INT[] DEFAULT '{}',
				created_by INT NULL REFERENCES users(id) ON DELETE SET NULL,
				is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
				deleted_at TIMESTAMP NULL,
				created_at TIMESTAMP NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMP NOT NULL DEFAULT NOW()
			);
		`)
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

	// Tune connection pool settings for high concurrency
	newDB.SetMaxOpenConns(25)
	newDB.SetMaxIdleConns(25)
	newDB.SetConnMaxLifetime(5 * time.Minute)

	if err := newDB.Ping(); err != nil {
		newDB.Close()
		return nil, fmt.Errorf("failed to ping tenant DB: %w", err)
	}

	// Ensure paid_amount, bonus_amount, charge_plan_history, and high-performance B-tree indexes exist
	_, _ = newDB.Exec(`
		ALTER TABLE payment_transactions ADD COLUMN IF NOT EXISTS paid_amount NUMERIC(12, 2) NOT NULL DEFAULT 0.00;
		ALTER TABLE payment_transactions ADD COLUMN IF NOT EXISTS bonus_amount NUMERIC(12, 2) NOT NULL DEFAULT 0.00;
		CREATE TABLE IF NOT EXISTS charge_plan_history (
			id SERIAL PRIMARY KEY,
			charge_plan_id INTEGER NOT NULL REFERENCES charge_plans(id) ON DELETE CASCADE,
			edited_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			edited_by_user_name VARCHAR(255),
			edited_at TIMESTAMP NOT NULL DEFAULT NOW(),
			old_state JSONB NOT NULL DEFAULT '{}'::jsonb,
			new_state JSONB NOT NULL DEFAULT '{}'::jsonb,
			change_summary TEXT
		);
		CREATE TABLE IF NOT EXISTS date_range_presets (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			start_date DATE NOT NULL,
			end_date DATE NOT NULL,
			category VARCHAR(100) NOT NULL DEFAULT 'schedule',
			created_by INT NULL REFERENCES users(id) ON DELETE SET NULL,
			is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
			deleted_at TIMESTAMP NULL,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_date_range_presets_category ON date_range_presets(category) WHERE is_deleted = false;
		CREATE TABLE IF NOT EXISTS target_presets (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			target_levels INT[] DEFAULT '{}',
			target_classes INT[] DEFAULT '{}',
			target_students INT[] DEFAULT '{}',
			created_by INT NULL REFERENCES users(id) ON DELETE SET NULL,
			is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
			deleted_at TIMESTAMP NULL,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_target_presets_is_deleted ON target_presets(is_deleted) WHERE is_deleted = false;
		CREATE INDEX IF NOT EXISTS idx_users_phone ON users(phone) WHERE is_deleted = false;
		CREATE INDEX IF NOT EXISTS idx_students_user_id ON students(user_id) WHERE is_deleted = false;
		CREATE INDEX IF NOT EXISTS idx_students_class_id ON students(class_id) WHERE is_deleted = false;
		CREATE INDEX IF NOT EXISTS idx_student_parents_student ON student_parents(student_id);
		CREATE INDEX IF NOT EXISTS idx_student_parents_parent ON student_parents(parent_id);
		CREATE INDEX IF NOT EXISTS idx_grades_student_id ON grades(student_id) WHERE is_deleted = false;
		CREATE INDEX IF NOT EXISTS idx_grades_class_id ON grades(class_id) WHERE is_deleted = false;
		CREATE INDEX IF NOT EXISTS idx_grades_created_at ON grades(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_grade_comments_grade ON grade_comments(grade_id);
		CREATE INDEX IF NOT EXISTS idx_grade_comments_parent ON grade_comments(parent_id);
		CREATE INDEX IF NOT EXISTS idx_announcements_created ON announcements(created_at DESC) WHERE is_deleted = false;
		CREATE INDEX IF NOT EXISTS idx_class_teachers_class_teacher ON class_teachers(class_id, teacher_id) WHERE is_deleted = false;
	`)

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

	rows, err := CentralDB.Query("SELECT id, db_connection_string FROM schools WHERE is_deleted = false ORDER BY created_at ASC")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var firstSchoolID string

	for rows.Next() {
		var id, connStr string
		if err := rows.Scan(&id, &connStr); err != nil {
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

		// Match db_f_test_school == db_f_test_school, or without db_f_ prefix, or cleanSub
		if dbName == searchName || dbName == cleanSub || strings.TrimPrefix(dbName, "db_f_") == cleanSub {
			return id, nil
		}
	}

	if firstSchoolID != "" {
		return firstSchoolID, nil
	}

	return "", fmt.Errorf("no school database found matching subdomain: %s", subdomain)
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
