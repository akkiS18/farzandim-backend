package services

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/farzandim/backend/internal/models"
	"github.com/farzandim/backend/internal/utils"
	"golang.org/x/crypto/bcrypt"
)

type ParentInput struct {
	FirstName    string
	LastName     string
	MiddleName   *string
	Passport     *string
	Phone        string
	Email        *string
	Password     string
	RelationType string
	StudentID    *int
}

type ParentResult struct {
	ParentID      int
	IsNewUser     bool
	AlreadyLinked bool
	User          models.User
}

type ParentService struct{}

func NewParentService() *ParentService {
	return &ParentService{}
}

// CreateOrLinkParent resolves existing parent identity (Passport-first, then Phone+Name)
// or registers a new parent, and links them to the student.
func (s *ParentService) CreateOrLinkParent(tx *sql.Tx, input ParentInput) (*ParentResult, error) {
	firstName := strings.TrimSpace(input.FirstName)
	lastName := strings.TrimSpace(input.LastName)
	var middleNamePtr *string
	if input.MiddleName != nil {
		m := strings.TrimSpace(*input.MiddleName)
		if m != "" && m != "-" {
			middleNamePtr = &m
		}
	}

	cleanPhone := utils.NormalizePhone(input.Phone)
	var pPhonePtr *string
	if cleanPhone != "" {
		pPhonePtr = &cleanPhone
	}

	var normalizedPassport *string
	if input.Passport != nil {
		cleanPass := strings.TrimSpace(*input.Passport)
		if cleanPass != "" && cleanPass != "-" && !strings.EqualFold(cleanPass, "yo'q") && len(cleanPass) >= 4 {
			norm := utils.NormalizeDocumentNo(cleanPass)
			normalizedPassport = &norm
		}
	}

	relationType := strings.TrimSpace(strings.ToLower(input.RelationType))
	if relationType == "" {
		relationType = "vasiy"
	}

	var parentID int
	var existingRoleName string
	found := false

	// 1. Primary lookup: by Passport Serial Number (just like in Ijtimoiy Pasport)
	if normalizedPassport != nil {
		err := tx.QueryRow(`
			SELECT u.id, r.name FROM users u
			JOIN roles r ON u.role_id = r.id
			WHERE (
				UPPER(TRIM(u.passport)) = UPPER(TRIM($1))
				OR REGEXP_REPLACE(UPPER(u.passport), '[^A-Z0-9]', '', 'g') = REGEXP_REPLACE(UPPER($1), '[^A-Z0-9]', '', 'g')
				OR UPPER(TRIM(u.document_no)) = UPPER(TRIM($1))
				OR REGEXP_REPLACE(UPPER(u.document_no), '[^A-Z0-9]', '', 'g') = REGEXP_REPLACE(UPPER($1), '[^A-Z0-9]', '', 'g')
			) AND u.is_deleted = false
			LIMIT 1
		`, *normalizedPassport).Scan(&parentID, &existingRoleName)

		if err == nil {
			if existingRoleName != "PARENT" {
				return nil, fmt.Errorf("Ushbu pasportli foydalanuvchi tizimda mavjud, lekin roli '%s'. Faqat PARENT roldagi profilni bog'lash mumkin.", existingRoleName)
			}
			found = true
			// Update phone if parent had none in database
			if pPhonePtr != nil {
				_, _ = tx.Exec("UPDATE users SET phone = $1 WHERE id = $2 AND (phone IS NULL OR phone = '')", *pPhonePtr, parentID)
			}
		} else if err != sql.ErrNoRows {
			return nil, fmt.Errorf("Pasport bo'yicha tekshirishda xatolik: %w", err)
		}
	}

	// 2. Secondary lookup: by Phone + Name (if not found by passport)
	if !found && pPhonePtr != nil && firstName != "" && lastName != "" {
		err := tx.QueryRow(`
			SELECT u.id, r.name FROM users u
			JOIN roles r ON u.role_id = r.id
			WHERE (u.phone = $1 OR REGEXP_REPLACE(u.phone, '\D', '', 'g') = $1)
			  AND LOWER(TRIM(u.first_name)) = LOWER(TRIM($2))
			  AND LOWER(TRIM(u.last_name)) = LOWER(TRIM($3))
			  AND u.is_deleted = false
			LIMIT 1
		`, *pPhonePtr, firstName, lastName).Scan(&parentID, &existingRoleName)

		if err == nil {
			if existingRoleName != "PARENT" {
				return nil, fmt.Errorf("Ushbu telefonli foydalanuvchi tizimda mavjud, lekin roli '%s'. Faqat PARENT roldagi profilni bog'lash mumkin.", existingRoleName)
			}
			found = true
			// Backfill passport if parent in DB had no passport
			if normalizedPassport != nil {
				_, _ = tx.Exec("UPDATE users SET passport = $1, document_no = $1 WHERE id = $2 AND (passport IS NULL OR passport = '')", *normalizedPassport, parentID)
			}
		} else if err != sql.ErrNoRows {
			return nil, fmt.Errorf("Telefon va ism bo'yicha tekshirishda xatolik: %w", err)
		}
	}

	// 3. Fallback lookup: if passport was NOT provided, match by phone alone
	if !found && pPhonePtr != nil && normalizedPassport == nil {
		err := tx.QueryRow(`
			SELECT u.id, r.name FROM users u
			JOIN roles r ON u.role_id = r.id
			WHERE (u.phone = $1 OR REGEXP_REPLACE(u.phone, '\D', '', 'g') = $1)
			  AND u.is_deleted = false
			LIMIT 1
		`, *pPhonePtr).Scan(&parentID, &existingRoleName)

		if err == nil {
			if existingRoleName != "PARENT" {
				return nil, fmt.Errorf("Ushbu telefonli foydalanuvchi tizimda mavjud, lekin roli '%s'. Faqat PARENT roldagi profilni bog'lash mumkin.", existingRoleName)
			}
			found = true
		} else if err != sql.ErrNoRows {
			return nil, fmt.Errorf("Telefon bo'yicha tekshirishda xatolik: %w", err)
		}
	}

	isNewUser := false
	var parentRoleID int

	if !found {
		// Verify phone does not belong to a non-parent user
		if pPhonePtr != nil {
			var conflictRole string
			conflictErr := tx.QueryRow(`
				SELECT r.name FROM users u
				JOIN roles r ON u.role_id = r.id
				WHERE (u.phone = $1 OR REGEXP_REPLACE(u.phone, '\D', '', 'g') = $1)
				  AND u.is_deleted = false
				LIMIT 1
			`, *pPhonePtr).Scan(&conflictRole)
			if conflictErr == nil {
				return nil, fmt.Errorf("Telefon raqam '%s' tizimda '%s' sifatida ro'yxatdan o'tgan. Boshqa telefon raqam kiriting.", *pPhonePtr, conflictRole)
			}
		}

		// Resolve role ID for PARENT
		err := tx.QueryRow("SELECT id FROM roles WHERE name = 'PARENT'").Scan(&parentRoleID)
		if err != nil {
			return nil, fmt.Errorf("Role 'PARENT' topilmadi: %w", err)
		}

		pwd := input.Password
		if strings.TrimSpace(pwd) == "" {
			pwd = "password123"
		}

		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("Parolni shifrlashda xatolik: %w", err)
		}

		insertUserQuery := `
			INSERT INTO users (first_name, last_name, middle_name, passport, document_no, phone, email, password_hash, role_id, password_reset_required)
			VALUES ($1, $2, $3, $4, $4, $5, $6, $7, $8, true)
			RETURNING id`
		err = tx.QueryRow(insertUserQuery, firstName, lastName, middleNamePtr, normalizedPassport, pPhonePtr, input.Email, string(hashedPassword), parentRoleID).Scan(&parentID)
		if err != nil {
			if strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "users_phone_key") || strings.Contains(err.Error(), "idx_staff_unique_phone") {
				return nil, fmt.Errorf("Telefon raqam allaqachon ro'yxatdan o'tgan. Iltimos, boshqa raqam kiriting.")
			}
			if strings.Contains(err.Error(), "idx_parents_unique_passport") {
				return nil, fmt.Errorf("Ushbu pasport seriyasi allaqachon ro'yxatdan o'tgan.")
			}
			return nil, fmt.Errorf("Ota-ona profilini yaratishda xatolik: %w", err)
		}
		isNewUser = true
	}

	alreadyLinked := false
	if input.StudentID != nil && *input.StudentID > 0 {
		studentID := *input.StudentID

		// Check if link already exists
		var linkExists bool
		err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM student_parents WHERE student_id = $1 AND parent_id = $2)", studentID, parentID).Scan(&linkExists)
		if err != nil {
			return nil, fmt.Errorf("Bog'liqlikni tekshirishda xatolik: %w", err)
		}

		if linkExists {
			alreadyLinked = true
			_, _ = tx.Exec("UPDATE student_parents SET relation_type = $1 WHERE student_id = $2 AND parent_id = $3", relationType, studentID, parentID)
		} else {
			_, err = tx.Exec(`
				INSERT INTO student_parents (student_id, parent_id, relation_type)
				VALUES ($1, $2, $3)
				ON CONFLICT (student_id, parent_id) DO UPDATE SET relation_type = EXCLUDED.relation_type
			`, studentID, parentID, relationType)
			if err != nil {
				return nil, fmt.Errorf("Ota-onani o'quvchiga biriktirishda xatolik: %w", err)
			}
		}
	}

	return &ParentResult{
		ParentID:      parentID,
		IsNewUser:     isNewUser,
		AlreadyLinked: alreadyLinked,
		User: models.User{
			ID:         parentID,
			FirstName:  firstName,
			LastName:   lastName,
			MiddleName: middleNamePtr,
			Passport:   normalizedPassport,
			Phone:      pPhonePtr,
			Email:      input.Email,
			RoleID:     parentRoleID,
			IsDeleted:  false,
		},
	}, nil
}
