package models

import "time"

type LessonPlan struct {
	ID           int        `json:"id" db:"id"`
	TeacherID    int        `json:"teacher_id" db:"teacher_id"`
	ClassID      int        `json:"class_id" db:"class_id"`
	SubjectID    int        `json:"subject_id" db:"subject_id"`
	DayOfWeek    int        `json:"day_of_week" db:"day_of_week"`
	LessonNumber int        `json:"lesson_number" db:"lesson_number"`
	StartDate    time.Time  `json:"start_date" db:"start_date"`
	TopicName    string     `json:"topic_name" db:"topic_name"`
	Notes        string     `json:"notes" db:"notes"`
	IsDeleted    bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

type LessonPlanResponse struct {
	ID           int    `json:"id"`
	TeacherID    int    `json:"teacher_id"`
	TeacherName  string `json:"teacher_name,omitempty"`
	ClassID      int    `json:"class_id"`
	ClassName    string `json:"class_name"`
	SubjectID    int    `json:"subject_id"`
	SubjectName  string `json:"subject_name"`
	DayOfWeek    int    `json:"day_of_week"`
	LessonNumber int    `json:"lesson_number"`
	StartDate    string `json:"start_date"`
	TopicName    string `json:"topic_name"`
	Notes        string `json:"notes"`
	CreatedAt    string `json:"created_at"`
}

type CreateLessonPlanRequest struct {
	ClassID      int    `json:"class_id" binding:"required"`
	SubjectID    int    `json:"subject_id" binding:"required"`
	DayOfWeek    int    `json:"day_of_week" binding:"required,min=1,max=7"`
	LessonNumber int    `json:"lesson_number" binding:"required,min=1"`
	StartDate    string `json:"start_date" binding:"required"`
	TopicName    string `json:"topic_name" binding:"required"`
	Notes        string `json:"notes"`
}

type UpdateLessonPlanRequest struct {
	ClassID      int    `json:"class_id" binding:"required"`
	SubjectID    int    `json:"subject_id" binding:"required"`
	DayOfWeek    int    `json:"day_of_week" binding:"required,min=1,max=7"`
	LessonNumber int    `json:"lesson_number" binding:"required,min=1"`
	StartDate    string `json:"start_date" binding:"required"`
	TopicName    string `json:"topic_name" binding:"required"`
	Notes        string `json:"notes"`
}

type BatchLessonPlanItem struct {
	StartDate    string `json:"start_date" binding:"required"`
	DayOfWeek    int    `json:"day_of_week"`
	LessonNumber int    `json:"lesson_number"`
	TopicName    string `json:"topic_name" binding:"required"`
	Notes        string `json:"notes"`
}

type BatchLessonPlanRequest struct {
	ClassID       int                   `json:"class_id" binding:"required"`
	SubjectID     int                   `json:"subject_id" binding:"required"`
	StartDateFrom string                `json:"start_date_from,omitempty"`
	StartDateTo   string                `json:"start_date_to,omitempty"`
	Overwrite     bool                  `json:"overwrite"`
	Items         []BatchLessonPlanItem `json:"items" binding:"required"`
}
