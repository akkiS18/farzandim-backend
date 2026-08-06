package models

import (
	"time"

	"github.com/lib/pq"
)

type Book struct {
	ID           int           `json:"id" db:"id"`
	Title        string        `json:"title" db:"title"`
	Author       string        `json:"author" db:"author"`
	Description  string        `json:"description" db:"description"`
	CoverURL     string        `json:"cover_url" db:"cover_url"`
	FileURL      string        `json:"file_url" db:"file_url"`
	FileSize     string        `json:"file_size" db:"file_size"`
	TargetLevels pq.Int64Array `json:"target_levels" db:"target_levels"`
	ClassIDs     pq.Int64Array `json:"class_ids" db:"class_ids"`
	IsDeleted    bool          `json:"is_deleted" db:"is_deleted"`
	DeletedAt    *time.Time    `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt    time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at" db:"updated_at"`
}

type CreateBookInput struct {
	Title        string  `json:"title" binding:"required"`
	Author       string  `json:"author"`
	Description  string  `json:"description"`
	CoverURL     string  `json:"cover_url"`
	FileURL      string  `json:"file_url" binding:"required"`
	FileSize     string  `json:"file_size"`
	TargetLevels []int64 `json:"target_levels"`
	ClassIDs     []int64 `json:"class_ids"`
}

type UpdateBookInput struct {
	Title        string  `json:"title"`
	Author       string  `json:"author"`
	Description  string  `json:"description"`
	CoverURL     string  `json:"cover_url"`
	FileURL      string  `json:"file_url"`
	FileSize     string  `json:"file_size"`
	TargetLevels []int64 `json:"target_levels"`
	ClassIDs     []int64 `json:"class_ids"`
}
