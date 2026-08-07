package models

import (
	"time"

	"github.com/lib/pq"
)

type BookCategory struct {
	ID          int        `json:"id" db:"id"`
	Name        string     `json:"name" db:"name"`
	Description string     `json:"description" db:"description"`
	CreatedBy   *int       `json:"created_by,omitempty" db:"created_by"`
	IsDeleted   bool       `json:"is_deleted" db:"is_deleted"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

type CreateBookCategoryInput struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

type Book struct {
	ID               int           `json:"id" db:"id"`
	Title            string        `json:"title" db:"title"`
	Author           string        `json:"author" db:"author"`
	Description      string        `json:"description" db:"description"`
	CoverURL         string        `json:"cover_url" db:"cover_url"`
	FileURL          string        `json:"file_url" db:"file_url"`
	FileSize         string        `json:"file_size" db:"file_size"`
	CategoryID       *int          `json:"category_id,omitempty" db:"category_id"`
	CategoryName     string        `json:"category_name,omitempty"`
	DownloadLink     string        `json:"download_link" db:"download_link"`
	LocationInSchool string        `json:"location_in_school" db:"location_in_school"`
	CreatedBy        *int          `json:"created_by,omitempty" db:"created_by"`
	TargetLevels     pq.Int64Array `json:"target_levels" db:"target_levels"`
	ClassIDs         pq.Int64Array `json:"class_ids" db:"class_ids"`
	IsDeleted        bool          `json:"is_deleted" db:"is_deleted"`
	DeletedAt        *time.Time    `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt        time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at" db:"updated_at"`
}

type CreateBookInput struct {
	Title            string  `json:"title" binding:"required"`
	Author           string  `json:"author"`
	Description      string  `json:"description"`
	CoverURL         string  `json:"cover_url"`
	FileURL          string  `json:"file_url"`
	FileSize         string  `json:"file_size"`
	CategoryID       *int    `json:"category_id"`
	DownloadLink     string  `json:"download_link"`
	LocationInSchool string  `json:"location_in_school"`
	TargetLevels     []int64 `json:"target_levels"`
	ClassIDs         []int64 `json:"class_ids"`
}

type UpdateBookInput struct {
	Title            string  `json:"title"`
	Author           string  `json:"author"`
	Description      string  `json:"description"`
	CoverURL         string  `json:"cover_url"`
	FileURL          string  `json:"file_url"`
	FileSize         string  `json:"file_size"`
	CategoryID       *int    `json:"category_id"`
	DownloadLink     string  `json:"download_link"`
	LocationInSchool string  `json:"location_in_school"`
	TargetLevels     []int64 `json:"target_levels"`
	ClassIDs         []int64 `json:"class_ids"`
}

type ReadingAssignment struct {
	ID          int                      `json:"id" db:"id"`
	Title       string                   `json:"title" db:"title"`
	TeacherID   int                      `json:"teacher_id" db:"teacher_id"`
	TeacherName string                   `json:"teacher_name,omitempty"`
	StartDate   string                   `json:"start_date" db:"start_date"`
	EndDate     string                   `json:"end_date" db:"end_date"`
	Description string                   `json:"description" db:"description"`
	Books       []Book                   `json:"books,omitempty"`
	Students    []StudentReadingProgress `json:"students,omitempty"`
	IsDeleted   bool                     `json:"is_deleted" db:"is_deleted"`
	CreatedAt   time.Time                `json:"created_at" db:"created_at"`
}

type CreateReadingAssignmentInput struct {
	Title       string `json:"title" binding:"required"`
	StartDate   string `json:"start_date" binding:"required"`
	EndDate     string `json:"end_date" binding:"required"`
	Description string `json:"description"`
	BookIDs     []int  `json:"book_ids" binding:"required"`
	StudentIDs  []int  `json:"student_ids"`
}

type StudentReadingProgress struct {
	ID              int        `json:"id" db:"id"`
	AssignmentID    int        `json:"assignment_id" db:"assignment_id"`
	AssignmentTitle string     `json:"assignment_title,omitempty"`
	BookID          int        `json:"book_id" db:"book_id"`
	Book            *Book      `json:"book,omitempty"`
	StudentID       int        `json:"student_id" db:"student_id"`
	StudentName     string     `json:"student_name,omitempty"`
	ClassName       string     `json:"class_name,omitempty"`
	Status          string     `json:"status" db:"status"` // 'assigned', 'reading', 'completed', 'graded'
	GradeValue      string     `json:"grade_value" db:"grade_value"`
	NumericValue    *float64   `json:"numeric_value,omitempty" db:"numeric_value"`
	GradingSystemID *int       `json:"grading_system_id,omitempty" db:"grading_system_id"`
	TeacherFeedback string     `json:"teacher_feedback" db:"teacher_feedback"`
	GradedBy        *int       `json:"graded_by,omitempty" db:"graded_by"`
	GradedAt        *time.Time `json:"graded_at,omitempty" db:"graded_at"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
}

type GradeStudentBookInput struct {
	AssignmentID    int      `json:"assignment_id" binding:"required"`
	BookID          int      `json:"book_id" binding:"required"`
	StudentID       int      `json:"student_id" binding:"required"`
	Status          string   `json:"status"` // 'assigned', 'reading', 'completed', 'graded'
	GradeValue      string   `json:"grade_value"`
	NumericValue    *float64 `json:"numeric_value"`
	GradingSystemID *int     `json:"grading_system_id"`
	TeacherFeedback string   `json:"teacher_feedback"`
}
