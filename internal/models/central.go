package models

import (
	"time"

	"github.com/google/uuid"
)

type School struct {
	ID                 uuid.UUID  `json:"id" db:"id"`
	Name               string     `json:"name" db:"name"`
	DBConnectionString string     `json:"db_connection_string" db:"db_connection_string"`
	IsDeleted          bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt          *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
}

type SuperAdmin struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	Email        *string    `json:"email" db:"email"`
	Phone        string     `json:"phone" db:"phone"`
	PasswordHash string     `json:"-" db:"password_hash"`
	FirstName    string     `json:"first_name" db:"first_name"`
	LastName     string     `json:"last_name" db:"last_name"`
	IsDeleted    bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
}
