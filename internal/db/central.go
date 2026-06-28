package db

import (
	"database/sql"
	"log"

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
