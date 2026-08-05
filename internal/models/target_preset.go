package models

import "time"

type TargetPreset struct {
	ID             int        `json:"id" db:"id"`
	Name           string     `json:"name" db:"name"`
	TargetLevels   []int      `json:"target_levels"`
	TargetClasses  []int      `json:"target_classes"`
	TargetStudents []int      `json:"target_students"`
	CreatedBy      *int       `json:"created_by,omitempty" db:"created_by"`
	IsDeleted      bool       `json:"is_deleted" db:"is_deleted"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

type CreateTargetPresetRequest struct {
	Name           string `json:"name" binding:"required"`
	TargetLevels   []int  `json:"target_levels"`
	TargetClasses  []int  `json:"target_classes"`
	TargetStudents []int  `json:"target_students"`
}
