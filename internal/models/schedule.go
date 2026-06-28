package models

import "time"

type ClassSchedule struct {
	ID           int        `json:"id" db:"id"`
	ClassID      int        `json:"class_id" db:"class_id"`
	DayOfWeek    int        `json:"day_of_week" db:"day_of_week"`
	LessonNumber int        `json:"lesson_number" db:"lesson_number"`
	SubjectID    int        `json:"subject_id" db:"subject_id"`
	StartDate    time.Time  `json:"start_date" db:"start_date"`
	EndDate      time.Time  `json:"end_date" db:"end_date"`
	IsDeleted    bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

type ClassScheduleResponse struct {
	ID           int    `json:"id"`
	ClassID      int    `json:"class_id"`
	DayOfWeek    int    `json:"day_of_week"`
	LessonNumber int    `json:"lesson_number"`
	SubjectID    int    `json:"subject_id"`
	SubjectName  string `json:"subject_name"`
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
}

type SaveScheduleRequest struct {
	StartDate string                `json:"start_date" binding:"required"`
	EndDate   string                `json:"end_date" binding:"required"`
	Lessons   []ScheduleLessonInput `json:"lessons" binding:"required"`
}

type ScheduleLessonInput struct {
	DayOfWeek    int `json:"day_of_week" binding:"required,min=1,max=6"`
	LessonNumber int `json:"lesson_number" binding:"required,min=1,max=10"`
	SubjectID    int `json:"subject_id" binding:"required"`
}
