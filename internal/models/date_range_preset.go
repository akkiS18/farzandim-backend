package models

import "time"

type DateRangePreset struct {
	ID        int        `json:"id" db:"id"`
	Name      string     `json:"name" db:"name"`
	StartDate string     `json:"start_date" db:"start_date"`
	EndDate   string     `json:"end_date" db:"end_date"`
	Category  string     `json:"category" db:"category"`
	CreatedBy *int       `json:"created_by,omitempty" db:"created_by"`
	IsDeleted bool       `json:"is_deleted" db:"is_deleted"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
}

type CreateDateRangePresetRequest struct {
	Name      string `json:"name" binding:"required"`
	StartDate string `json:"start_date" binding:"required"`
	EndDate   string `json:"end_date" binding:"required"`
	Category  string `json:"category"`
}
