package models

import "time"

type ScheduleException struct {
	ID           int        `json:"id" db:"id"`
	ClassID      int        `json:"class_id" db:"class_id"`
	Date         time.Time  `json:"date" db:"date"`
	LessonNumber int        `json:"lesson_number" db:"lesson_number"`
	SubjectID    *int       `json:"subject_id" db:"subject_id"` // Nullable for cancellations
	IsDeleted    bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

type ScheduleExceptionResponse struct {
	ID           int       `json:"id"`
	ClassID      int       `json:"class_id"`
	Date         string    `json:"date"`
	LessonNumber int       `json:"lesson_number"`
	SubjectID    *int      `json:"subject_id,omitempty"`
	SubjectName  string    `json:"subject_name,omitempty"` // Empty/null if cancelled
	IsDeleted    bool      `json:"is_deleted"`
	CreatedAt    time.Time `json:"created_at"`
}

type SaveExceptionRequest struct {
	Date         string `json:"date" binding:"required"`
	LessonNumber int    `json:"lesson_number" binding:"required,min=1,max=10"`
	SubjectID    *int   `json:"subject_id"` // null means cancel lesson on that day
}
