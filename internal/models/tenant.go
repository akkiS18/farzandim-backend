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
	Passport     *string    `json:"passport,omitempty" db:"passport"`
	TelegramID   *string    `json:"telegram_id,omitempty" db:"telegram_id"`
	RoleID       int        `json:"role_id" db:"role_id"`
	IsDeleted    bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

type Class struct {
	ID        int        `json:"id" db:"id"`
	Name      string     `json:"name" db:"name"`
	Level     int        `json:"level" db:"level"`
	IsDeleted bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type Student struct {
	ID        int        `json:"id" db:"id"`
	UserID    int        `json:"user_id" db:"user_id"`
	ClassID   int        `json:"class_id" db:"class_id"`
	Address   *string    `json:"address,omitempty" db:"address"`
	BirthDate *time.Time `json:"birthdate,omitempty" db:"birthdate"`
	INA       *string    `json:"ina,omitempty" db:"ina"`
	Balance   float64    `json:"balance" db:"balance"`
	IsDeleted bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type Subject struct {
	ID           int        `json:"id" db:"id"`
	Name         string     `json:"name" db:"name"`
	TargetLevels []int64    `json:"target_levels"`
	IsDeleted    bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
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
	GradeType        string     `json:"grade_type" db:"grade_type"`
	GradeCategory    string     `json:"grade_category" db:"grade_category"`
	LessonNumber     *int       `json:"lesson_number,omitempty" db:"lesson_number"`
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

type PaymentTransaction struct {
	ID          int       `json:"id" db:"id"`
	StudentID   int       `json:"student_id" db:"student_id"`
	Amount      float64   `json:"amount" db:"amount"`
	PaidAmount  float64   `json:"paid_amount" db:"paid_amount"`
	BonusAmount float64   `json:"bonus_amount" db:"bonus_amount"`
	Type        string    `json:"type" db:"type"`
	Description *string   `json:"description,omitempty" db:"description"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

type SchoolHoliday struct {
	ID            int        `json:"id" db:"id"`
	HolidayDate   time.Time  `json:"holiday_date" db:"holiday_date"`
	Name          string     `json:"name" db:"name"`
	TargetLevels  []int64    `json:"target_levels"`
	TargetClasses []int64    `json:"target_classes"`
	IsDeleted     bool       `json:"is_deleted" db:"is_deleted"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

type MenuInterval struct {
	ID         int       `json:"id" db:"id"`
	Name       string    `json:"name" db:"name"`
	StartDate  time.Time `json:"start_date" db:"start_date"`
	EndDate    time.Time `json:"end_date" db:"end_date"`
	CycleWeeks int       `json:"cycle_weeks" db:"cycle_weeks"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}

type MenuCycle struct {
	ID         int             `json:"id" db:"id"`
	IntervalID int             `json:"interval_id" db:"interval_id"`
	WeekNumber int             `json:"week_number" db:"week_number"`
	DayOfWeek  int             `json:"day_of_week" db:"day_of_week"`
	Meals      json.RawMessage `json:"meals" db:"meals"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at" db:"updated_at"`
}

type MenuException struct {
	ID        int             `json:"id" db:"id"`
	MenuDate  time.Time       `json:"menu_date" db:"menu_date"`
	Meals     json.RawMessage `json:"meals,omitempty" db:"meals"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt time.Time       `json:"updated_at" db:"updated_at"`
}

type ChargePlan struct {
	ID        int       `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Amount    float64   `json:"amount" db:"amount"`
	StartDate time.Time `json:"start_date" db:"start_date"`
	EndDate   time.Time `json:"end_date" db:"end_date"`
	ChargeDay int       `json:"charge_day" db:"charge_day"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	Levels   []int `json:"levels,omitempty"`
	Classes  []int `json:"classes,omitempty"`
	Students []int `json:"students,omitempty"`
}

type ChargePlanHistory struct {
	ID             int             `json:"id" db:"id"`
	ChargePlanID   int             `json:"charge_plan_id" db:"charge_plan_id"`
	EditedByUserID *int            `json:"edited_by_user_id,omitempty" db:"edited_by_user_id"`
	EditedUserName string          `json:"edited_by_user_name" db:"edited_by_user_name"`
	EditedAt       time.Time       `json:"edited_at" db:"edited_at"`
	OldState       json.RawMessage `json:"old_state" db:"old_state"`
	NewState       json.RawMessage `json:"new_state" db:"new_state"`
	ChangeSummary  string          `json:"change_summary" db:"change_summary"`
}

type ChargeLog struct {
	ID            int       `json:"id" db:"id"`
	ChargePlanID  int       `json:"charge_plan_id" db:"charge_plan_id"`
	StudentID     int       `json:"student_id" db:"student_id"`
	BillingMonth  time.Time `json:"billing_month" db:"billing_month"`
	TransactionID *int      `json:"transaction_id" db:"transaction_id"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

type PollOptionResponse struct {
	ID         int    `json:"id"`
	OptionText string `json:"option_text"`
	VoteCount  int    `json:"vote_count"`
	UserVoted  bool   `json:"user_voted"`
}

type Announcement struct {
	ID          int                  `json:"id" db:"id"`
	Title       string               `json:"title" db:"title"`
	Content     string               `json:"content" db:"content"`
	AuthorID    int                  `json:"author_id" db:"author_id"`
	AuthorName  string               `json:"author_name,omitempty" db:"author_name"`
	IsPoll      bool                 `json:"is_poll" db:"is_poll"`
	PollOptions []PollOptionResponse `json:"poll_options,omitempty"`
	IsDeleted   bool                 `json:"is_deleted" db:"is_deleted"`
	DeletedAt   *time.Time           `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedAt   time.Time            `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at" db:"updated_at"`
	ClassIDs    []int                `json:"class_ids,omitempty"`   // Helper for API
	LevelIDs    []int                `json:"level_ids,omitempty"`   // Helper for API
	StudentIDs  []int                `json:"student_ids,omitempty"` // Helper for API
}

type AnnouncementClass struct {
	AnnouncementID int `json:"announcement_id" db:"announcement_id"`
	ClassID        int `json:"class_id" db:"class_id"`
}

type AnnouncementLevel struct {
	AnnouncementID int `json:"announcement_id" db:"announcement_id"`
	Level          int `json:"level" db:"level"`
}

type AnnouncementStudent struct {
	AnnouncementID int `json:"announcement_id" db:"announcement_id"`
	StudentID      int `json:"student_id" db:"student_id"`
}

type GradeComment struct {
	ID        int       `json:"id" db:"id"`
	GradeID   int       `json:"grade_id" db:"grade_id"`
	AuthorID  int       `json:"author_id" db:"author_id"`
	Content   string    `json:"content" db:"content"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type MenuComment struct {
	ID        int       `json:"id" db:"id"`
	MenuDate  time.Time `json:"menu_date" db:"menu_date"`
	ParentID  int       `json:"parent_id" db:"parent_id"`
	AuthorID  int       `json:"author_id" db:"author_id"`
	Content   string    `json:"content" db:"content"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type FeedbackComment struct {
	ID          int        `json:"id" db:"id"`
	Type        string     `json:"type" db:"type"` // "GRADE" or "MENU"
	GradeID     *int       `json:"grade_id,omitempty" db:"grade_id"`
	ParentID    int        `json:"parent_id" db:"parent_id"`
	AuthorID    int        `json:"author_id" db:"author_id"`
	Content     string     `json:"content" db:"content"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	AuthorName  string     `json:"author_name" db:"author_name"`
	SubjectName string     `json:"subject_name,omitempty" db:"subject_name"`
	GradeValue  string     `json:"grade_value,omitempty" db:"grade_value"`
	StudentName string     `json:"student_name,omitempty" db:"student_name"`
	ClassName   string     `json:"class_name,omitempty" db:"class_name"`
	MenuDate    *time.Time `json:"menu_date,omitempty" db:"menu_date"`
}

type AIInstruction struct {
	ID                int       `json:"id" db:"id"`
	Title             string    `json:"title" db:"title"`
	SystemInstruction string    `json:"system_instruction" db:"system_instruction"`
	MaxTokens         int       `json:"max_tokens" db:"max_tokens"`
	Temperature       float64   `json:"temperature" db:"temperature"`
	IsActive          bool      `json:"is_active" db:"is_active"`
	UpdatedByUserID   *int      `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`
	UpdatedByName     string    `json:"updated_by_name,omitempty"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}

type AIInstructionLog struct {
	ID                int       `json:"id" db:"id"`
	InstructionID     int       `json:"instruction_id" db:"instruction_id"`
	SystemInstruction string    `json:"system_instruction" db:"system_instruction"`
	MaxTokens         int       `json:"max_tokens" db:"max_tokens"`
	Temperature       float64   `json:"temperature" db:"temperature"`
	ChangedByUserID   *int      `json:"changed_by_user_id,omitempty" db:"changed_by_user_id"`
	ChangedByName     string    `json:"changed_by_user_name,omitempty" db:"changed_by_user_name"`
	ChangeReason      string    `json:"change_reason,omitempty" db:"change_reason"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
}


