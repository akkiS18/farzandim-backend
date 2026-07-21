package models

import (
	"time"

	"github.com/lib/pq"
)

type Club struct {
	ID                 int            `json:"id"`
	Name               string         `json:"name"`
	SubjectID          int            `json:"subject_id"`
	SubjectName        string         `json:"subject_name,omitempty"`
	TeacherID          int            `json:"teacher_id"`
	TeacherName        string         `json:"teacher_name,omitempty"`
	AllowedClassLevels pq.Int64Array  `json:"allowed_class_levels"`
	IsDeleted          bool           `json:"is_deleted"`
	DeletedAt          *time.Time     `json:"deleted_at,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	Students           []ClubStudent  `json:"students,omitempty"`
	Schedules          []ClubSchedule `json:"schedules,omitempty"`
}

type ClubStudent struct {
	ID          int       `json:"id"`
	ClubID      int       `json:"club_id"`
	StudentID   int       `json:"student_id"`
	StudentName string    `json:"student_name,omitempty"`
	ClassName   string    `json:"class_name,omitempty"`
	Status      string    `json:"status"` // 'PENDING' or 'APPROVED'
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ClubSchedule struct {
	ID        int        `json:"id"`
	ClubID    int        `json:"club_id"`
	DayOfWeek int        `json:"day_of_week"` // 1-7 (7 is Sunday)
	StartTime string     `json:"start_time"`
	EndTime   string     `json:"end_time"`
	IsDeleted bool       `json:"is_deleted"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type CreateClubRequest struct {
	Name               string  `json:"name" binding:"required"`
	SubjectID          int     `json:"subject_id" binding:"required"`
	AllowedClassLevels []int64 `json:"allowed_class_levels" binding:"required"`
	ExtraStudentIDs    []int   `json:"extra_student_ids"`
}

type CreateScheduleRequest struct {
	DayOfWeek int    `json:"day_of_week" binding:"required"`
	StartTime string `json:"start_time" binding:"required"`
	EndTime   string `json:"end_time" binding:"required"`
}
