package main

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

func main() {
	db, err := sql.Open("postgres", "postgres://postgres:postgres@localhost:5432/db_f_test_school?sslmode=disable")
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		log.Fatalf("driver error: %v", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations/tenant",
		"db_f_test_school", driver,
	)
	if err != nil {
		log.Fatalf("migrate new error: %v", err)
	}

	// Force clear dirty state
	fmt.Println("Forcing version 2...")
	if err := m.Force(2); err != nil {
		log.Printf("Force err: %v", err)
	}

	// Run up
	fmt.Println("Running up migrations...")
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Printf("m.Up error: %v", err)
	} else {
		fmt.Println("Successfully migrated db_f_test_school to latest!")
	}

	// Ensure users_phone_key is dropped
	_, _ = db.Exec("ALTER TABLE users DROP CONSTRAINT IF EXISTS users_phone_key")

	var v int
	var d bool
	_ = db.QueryRow("SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&v, &d)
	fmt.Printf("db_f_test_school now at version: %d, dirty: %v\n", v, d)
}
