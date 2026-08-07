package db

import (
	"database/sql"
	"log"
	"time"

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

	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)

	CentralDB = db
	log.Println("Successfully connected to Central Database")
}
