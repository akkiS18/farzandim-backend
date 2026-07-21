package db

import (
	"database/sql"
	"log"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

var CentralDB *sql.DB

func InitCentralDB(connStr string) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Error opening Central DB: %v", err)
	}

	err = db.Ping()
	if err != nil {
		log.Fatalf("Error pinging Central DB: %v", err)
	}

	CentralDB = db
	log.Println("Successfully connected to Central Database")
}

// MigrateCentralDB runs migrations on the central database
func MigrateCentralDB() {
	driver, err := postgres.WithInstance(CentralDB, &postgres.Config{})
	if err != nil {
		log.Fatalf("Could not create postgres driver for Central DB migration: %v", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations/central",
		"postgres", driver,
	)
	if err != nil {
		log.Fatalf("Could not initialize Central DB migration: %v", err)
	}

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		log.Fatalf("Failed to run Central DB migration: %v", err)
	}

	log.Println("Central DB migrations ran successfully")
}
