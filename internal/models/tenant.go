package models

import (
	"encoding/json"
	"time"
)

type Role struct {
	ID   int    `json:"id" db:"id"`
	Name string `json:"name" db:"name"`
}

type User struct {
	ID           int        `json:"id" db:"id"`
	Email        *string    `json:"email,omitempty" db:"email"`
	Phone        *string    `json:"phone,omitempty" db:"phone"`
	PasswordHash string     `json:"-" db:"password_hash"`
	FirstName    string     `json:"first_name" db:"first_name"`
	LastName     string     `json:"last_name" db:"last_name"`
	MiddleName   *string    `json:"middle_name,omitempty" db:"middle_name"`
	RoleID       int        `json:"role_id" db:"role_id"`
	IsDeleted    bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

type Class struct {
	ID        int        `json:"id" db:"id"`
	Name      string     `json:"name" db:"name"`
	IsDeleted bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type Student struct {
	ID        int        `json:"id" db:"id"`
	UserID    int        `json:"user_id" db:"user_id"`
	ClassID   int        `json:"class_id" db:"class_id"`
	IsDeleted bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type Subject struct {
	ID        int        `json:"id" db:"id"`
	Name      string     `json:"name" db:"name"`
	IsDeleted bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type ClassTeacher struct {
	ID            int        `json:"id" db:"id"`
	ClassID       int        `json:"class_id" db:"class_id"`
	SubjectID     int        `json:"subject_id" db:"subject_id"`
	TeacherID     int        `json:"teacher_id" db:"teacher_id"`
	IsMainTeacher bool       `json:"is_main_teacher" db:"is_main_teacher"`
	IsDeleted     bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type Grade struct {
	ID               int        `json:"id" db:"id"`
	StudentID        int        `json:"student_id" db:"student_id"`
	SubjectID        int        `json:"subject_id" db:"subject_id"`
	TeacherID        int        `json:"teacher_id" db:"teacher_id"`
	Value            string     `json:"value" db:"value"`
	NumericValue     *float64   `json:"numeric_value,omitempty" db:"numeric_value"`
	GradeDate        time.Time  `json:"grade_date" db:"grade_date"`
	Status           string     `json:"status" db:"status"`
	ApprovedByParent bool       `json:"approved_by_parent" db:"approved_by_parent"`
	GradingSystemID  *int       `json:"grading_system_id,omitempty" db:"grading_system_id"`
	IsDeleted        bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
}

type GradingSystem struct {
	ID        int             `json:"id" db:"id"`
	Name      string          `json:"name" db:"name"`
	Type      string          `json:"type" db:"type"`
	MinValue  *float64        `json:"min_value,omitempty" db:"min_value"`
	MaxValue  *float64        `json:"max_value,omitempty" db:"max_value"`
	Options   json.RawMessage `json:"options,omitempty" db:"options"`
	IsActive  bool            `json:"is_active" db:"is_active"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt time.Time       `json:"updated_at" db:"updated_at"`
}

type ParentAccessCode struct {
	ID          int       `json:"id" db:"id"`
	StudentID   int       `json:"student_id" db:"student_id"`
	ParentPhone string    `json:"parent_phone" db:"parent_phone"`
	Code        string    `json:"code" db:"code"`
	IsUsed      bool      `json:"is_used" db:"is_used"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	ExpiresAt   time.Time `json:"expires_at" db:"expires_at"`
}

type AuditLog struct {
	ID        int             `json:"id" db:"id"`
	UserID    *int            `json:"user_id,omitempty" db:"user_id"`
	Action    string          `json:"action" db:"action"`
	TableName string          `json:"table_name" db:"table_name"`
	RecordID  string          `json:"record_id" db:"record_id"`
	OldValues json.RawMessage `json:"old_values,omitempty" db:"old_values"`
	NewValues json.RawMessage `json:"new_values,omitempty" db:"new_values"`
	IPAddress *string         `json:"ip_address,omitempty" db:"ip_address"`
	UserAgent *string         `json:"user_agent,omitempty" db:"user_agent"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
}
