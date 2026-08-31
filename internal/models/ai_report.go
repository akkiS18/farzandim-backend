package models

import (
	"time"
)

type AIWeeklyReport struct {
	ID          string                 `json:"id"`
	StudentID   int                    `json:"student_id"`
	Year        int                    `json:"year"`
	WeekNumber  int                    `json:"week_number"`
	StartDate   string                 `json:"start_date"`
	EndDate     string                 `json:"end_date"`
	ReportText  string                 `json:"report_text"`
	SummaryJSON map[string]interface{} `json:"summary_json"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

type AIReportSummaryData struct {
	AverageGrade     float64 `json:"average_grade"`
	PrevAverageGrade float64 `json:"prev_average_grade"`
	GradeTrend       string  `json:"grade_trend"` // "UP", "DOWN", "STABLE"
	TotalGrades      int     `json:"total_grades"`
	BooksReadCount   int     `json:"books_read_count"`
	PositiveFeedback int     `json:"positive_feedback_count"`
}

type AIGenerationJob struct {
	ID                 string     `json:"id"`
	TargetDate         string     `json:"target_date"`
	ClassID            *int       `json:"class_id,omitempty"`
	Status             string     `json:"status"` // "STARTED", "IN_PROGRESS", "FINISHED", "FAILED", "CANCELLED"
	TotalStudents      int        `json:"total_students"`
	ProcessedStudents  int        `json:"processed_students"`
	GeneratedCount     int        `json:"generated_count"`
	ErrorCount         int        `json:"error_count"`
	CurrentStudentName *string    `json:"current_student_name,omitempty"`
	ErrorMessage       *string    `json:"error_message,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
}
