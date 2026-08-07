package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

type ClubHandler struct{}

func NewClubHandler() *ClubHandler {
	return &ClubHandler{}
}

// CreateClub handler
func (h *ClubHandler) CreateClub(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	var req models.CreateClubRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiya boshlashda xatolik"})
		return
	}
	defer tx.Rollback()

	// Insert club
	var clubID int
	err = tx.QueryRow(`
		INSERT INTO clubs (name, subject_id, teacher_id, allowed_class_levels)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, req.Name, req.SubjectID, userID, pq.Int64Array(req.AllowedClassLevels)).Scan(&clubID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "To'garak yaratishda xatolik: " + err.Error()})
		return
	}

	// Insert extra/direct students
	for _, studentID := range req.ExtraStudentIDs {
		_, err = tx.Exec(`
			INSERT INTO club_students (club_id, student_id, status)
			VALUES ($1, $2, 'APPROVED')
			ON CONFLICT (club_id, student_id) DO UPDATE SET status = 'APPROVED'
		`, clubID, studentID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "O'quvchilarni to'garakka biriktirishda xatolik"})
			return
		}
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "clubs",
		RecordID:  strconv.Itoa(clubID),
		NewValues: req,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Commit xatoligi"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "To'garak muvaffaqiyatli yaratildi", "club_id": clubID})
}

// GetClubs list handler
func (h *ClubHandler) GetClubs(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	studentIDParam := c.Query("student_id")

	var clubs []models.Club

	// Query base for clubs
	query := `
		SELECT c.id, c.name, c.subject_id, s.name as subject_name,
		       c.teacher_id, u.first_name || ' ' || u.last_name as teacher_name,
		       c.allowed_class_levels, c.created_at, c.updated_at
		FROM clubs c
		JOIN subjects s ON c.subject_id = s.id
		JOIN users u ON c.teacher_id = u.id
		WHERE c.is_deleted = false
	`
	var rows *sql.Rows
	var err error

	if role == "ADMIN" {
		rows, err = db.Query(query)
	} else if role == "MAIN_TEACHER" || role == "SUBJECT_TEACHER" {
		query += " AND c.teacher_id = $1"
		rows, err = db.Query(query, userID)
	} else if role == "PARENT" {
		// Parent sees clubs matching the child's class level or where child is already registered
		var childID int
		if studentIDParam != "" {
			childID, _ = strconv.Atoi(studentIDParam)
		} else {
			// Find first child of this parent
			err = db.QueryRow("SELECT student_id FROM student_parents WHERE parent_id = $1 LIMIT 1", userID).Scan(&childID)
			if err != nil {
				c.JSON(http.StatusOK, []models.Club{})
				return
			}
		}

		// Find class level (grade level/number) of the student
		var classLevel int
		err = db.QueryRow(`
			SELECT cls.level 
			FROM students s 
			JOIN classes cls ON s.class_id = cls.id 
			WHERE s.id = $1 AND s.is_deleted = false
		`, childID).Scan(&classLevel)
		if err != nil {
			c.JSON(http.StatusOK, []models.Club{})
			return
		}

		// Query clubs that are either matching child's class level or where child has a pending/approved status
		query = `
			SELECT c.id, c.name, c.subject_id, s.name as subject_name,
			       c.teacher_id, u.first_name || ' ' || u.last_name as teacher_name,
			       c.allowed_class_levels, c.created_at, c.updated_at
			FROM clubs c
			JOIN subjects s ON c.subject_id = s.id
			JOIN users u ON c.teacher_id = u.id
			WHERE c.is_deleted = false AND (
				$1 = ANY(c.allowed_class_levels)
				OR
				EXISTS (SELECT 1 FROM club_students cs WHERE cs.club_id = c.id AND cs.student_id = $2)
			)
		`
		rows, err = db.Query(query, classLevel, childID)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "To'garaklarni yuklashda xatolik: " + err.Error()})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var club models.Club
		err := rows.Scan(
			&club.ID, &club.Name, &club.SubjectID, &club.SubjectName,
			&club.TeacherID, &club.TeacherName, &club.AllowedClassLevels,
			&club.CreatedAt, &club.UpdatedAt,
		)
		if err == nil {
			// Fetch schedules for each club
			sRows, err := db.Query(`
				SELECT id, day_of_week, start_time, end_time
				FROM club_schedules
				WHERE club_id = $1 AND is_deleted = false
				ORDER BY day_of_week ASC, start_time ASC
			`, club.ID)
			if err == nil {
				for sRows.Next() {
					var sch models.ClubSchedule
					if err := sRows.Scan(&sch.ID, &sch.DayOfWeek, &sch.StartTime, &sch.EndTime); err == nil {
						club.Schedules = append(club.Schedules, sch)
					}
				}
				sRows.Close()
			}

			// If parent, fetch the child's enrollment status for this club
			if role == "PARENT" {
				var childID int
				if studentIDParam != "" {
					childID, _ = strconv.Atoi(studentIDParam)
				} else {
					db.QueryRow("SELECT student_id FROM student_parents WHERE parent_id = $1 LIMIT 1", userID).Scan(&childID)
				}

				var status string
				err = db.QueryRow("SELECT status FROM club_students WHERE club_id = $1 AND student_id = $2", club.ID, childID).Scan(&status)
				if err == nil {
					club.Students = []models.ClubStudent{{
						StudentID: childID,
						Status:    status,
					}}
				}
			}

			clubs = append(clubs, club)
		}
	}

	c.JSON(http.StatusOK, clubs)
}

// RequestJoinClub (Parent request)
func (h *ClubHandler) RequestJoinClub(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	clubID, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		StudentID int `json:"student_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify student belongs to this parent
	var belongs bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM student_parents WHERE parent_id = $1 AND student_id = $2)", userID, req.StudentID).Scan(&belongs)
	if err != nil || !belongs {
		c.JSON(http.StatusForbidden, gin.H{"error": "Siz ushbu o'quvchining ota-onasi emassiz"})
		return
	}

	_, err = db.Exec(`
		INSERT INTO club_students (club_id, student_id, status)
		VALUES ($1, $2, 'PENDING')
		ON CONFLICT (club_id, student_id) DO UPDATE SET status = 'PENDING'
	`, clubID, req.StudentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Qatnashish so'rovini yuborishda xatolik: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Qatnashish so'rovi yuborildi"})
}

// CancelClubRequest / Leave club
func (h *ClubHandler) CancelClubRequest(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	clubID, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		StudentID int `json:"student_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify student belongs to this parent
	var belongs bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM student_parents WHERE parent_id = $1 AND student_id = $2)", userID, req.StudentID).Scan(&belongs)
	if err != nil || !belongs {
		c.JSON(http.StatusForbidden, gin.H{"error": "Siz ushbu o'quvchining ota-onasi emassiz"})
		return
	}

	_, err = db.Exec("DELETE FROM club_students WHERE club_id = $1 AND student_id = $2", clubID, req.StudentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "O'chirishda xatolik: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Muvaffaqiyatli bekor qilindi"})
}

// GetClubStudents for teacher
func (h *ClubHandler) GetClubStudents(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	clubID, _ := strconv.Atoi(c.Param("id"))

	// Verify teacher owns the club (or admin)
	if role != "ADMIN" {
		var teacherID int
		err := db.QueryRow("SELECT teacher_id FROM clubs WHERE id = $1 AND is_deleted = false", clubID).Scan(&teacherID)
		if err != nil || teacherID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Siz ushbu to'garakka mas'ul emassiz"})
			return
		}
	}

	rows, err := db.Query(`
		SELECT cs.id, cs.club_id, cs.student_id, cs.status, cs.created_at, cs.updated_at,
		       stu_u.first_name || ' ' || stu_u.last_name as student_name,
		       cls.name as class_name
		FROM club_students cs
		JOIN students s ON cs.student_id = s.id
		JOIN users stu_u ON s.user_id = stu_u.id
		JOIN classes cls ON s.class_id = cls.id
		WHERE cs.club_id = $1
		ORDER BY cs.status DESC, student_name ASC
	`, clubID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "O'quvchilarni yuklashda xatolik: " + err.Error()})
		return
	}
	defer rows.Close()

	var students []models.ClubStudent
	for rows.Next() {
		var s models.ClubStudent
		err := rows.Scan(&s.ID, &s.ClubID, &s.StudentID, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.StudentName, &s.ClassName)
		if err == nil {
			students = append(students, s)
		}
	}

	c.JSON(http.StatusOK, students)
}

// ApproveClubStudent
func (h *ClubHandler) ApproveClubStudent(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	clubID, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		StudentID int `json:"student_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify teacher owns the club
	if role != "ADMIN" {
		var teacherID int
		err := db.QueryRow("SELECT teacher_id FROM clubs WHERE id = $1 AND is_deleted = false", clubID).Scan(&teacherID)
		if err != nil || teacherID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat etilmagan"})
			return
		}
	}

	_, err := db.Exec(`
		UPDATE club_students 
		SET status = 'APPROVED', updated_at = NOW() 
		WHERE club_id = $1 AND student_id = $2
	`, clubID, req.StudentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tasdiqlashda xatolik: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "O'quvchi to'garakka muvaffaqiyatli qo'shildi"})
}

// AddClubStudentDirectly
func (h *ClubHandler) AddClubStudentDirectly(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	clubID, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		StudentID int `json:"student_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify teacher owns the club
	if role != "ADMIN" {
		var teacherID int
		err := db.QueryRow("SELECT teacher_id FROM clubs WHERE id = $1 AND is_deleted = false", clubID).Scan(&teacherID)
		if err != nil || teacherID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat etilmagan"})
			return
		}
	}

	_, err := db.Exec(`
		INSERT INTO club_students (club_id, student_id, status)
		VALUES ($1, $2, 'APPROVED')
		ON CONFLICT (club_id, student_id) DO UPDATE SET status = 'APPROVED', updated_at = NOW()
	`, clubID, req.StudentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Qo'shishda xatolik: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "O'quvchi to'garakka qo'shildi"})
}

// RemoveClubStudent
func (h *ClubHandler) RemoveClubStudent(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	clubID, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		StudentID int `json:"student_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify teacher owns the club
	if role != "ADMIN" {
		var teacherID int
		err := db.QueryRow("SELECT teacher_id FROM clubs WHERE id = $1 AND is_deleted = false", clubID).Scan(&teacherID)
		if err != nil || teacherID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat etilmagan"})
			return
		}
	}

	_, err := db.Exec("DELETE FROM club_students WHERE club_id = $1 AND student_id = $2", clubID, req.StudentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "O'chirishda xatolik: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "O'quvchi to'garakdan chiqarildi"})
}

// CreateClubSchedule
func (h *ClubHandler) CreateClubSchedule(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	clubID, _ := strconv.Atoi(c.Param("id"))

	var req models.CreateScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify teacher owns the club
	if role != "ADMIN" {
		var teacherID int
		err := db.QueryRow("SELECT teacher_id FROM clubs WHERE id = $1 AND is_deleted = false", clubID).Scan(&teacherID)
		if err != nil || teacherID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat etilmagan"})
			return
		}
	}

	_, err := db.Exec(`
		INSERT INTO club_schedules (club_id, day_of_week, start_time, end_time)
		VALUES ($1, $2, $3, $4)
	`, clubID, req.DayOfWeek, req.StartTime, req.EndTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Jadval qo'shishda xatolik: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Jadval muvaffaqiyatli qo'shildi"})
}

// DeleteClubSchedule
func (h *ClubHandler) DeleteClubSchedule(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	scheduleID, _ := strconv.Atoi(c.Param("schedule_id"))

	// Verify teacher owns the club for this schedule
	var clubID int
	err := db.QueryRow("SELECT club_id FROM club_schedules WHERE id = $1 AND is_deleted = false", scheduleID).Scan(&clubID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Jadval topilmadi"})
		return
	}

	if role != "ADMIN" {
		var teacherID int
		err = db.QueryRow("SELECT teacher_id FROM clubs WHERE id = $1 AND is_deleted = false", clubID).Scan(&teacherID)
		if err != nil || teacherID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat etilmagan"})
			return
		}
	}

	_, err = db.Exec("UPDATE club_schedules SET is_deleted = true, deleted_at = NOW() WHERE id = $1", scheduleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Jadvalni o'chirishda xatolik"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Jadval muvaffaqiyatli o'chirildi"})
}

// UpdateClub handler
func (h *ClubHandler) UpdateClub(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	clubID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}

	var req models.UpdateClubRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify permission: Admin or teacher owner
	var teacherID int
	var oldClub models.Club
	err = db.QueryRow(`
		SELECT id, name, subject_id, teacher_id
		FROM clubs WHERE id = $1 AND is_deleted = false
	`, clubID).Scan(&oldClub.ID, &oldClub.Name, &oldClub.SubjectID, &teacherID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "To'garak topilmadi"})
		return
	}

	if role != "ADMIN" && teacherID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu to'garakni tahrirlashga ruxsatingiz yo'q"})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiya boshlashda xatolik"})
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE clubs
		SET name = $1, subject_id = $2, allowed_class_levels = $3, updated_at = NOW()
		WHERE id = $4 AND is_deleted = false
	`, req.Name, req.SubjectID, pq.Int64Array(req.AllowedClassLevels), clubID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "To'garakni yangilashda xatolik: " + err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "clubs",
		RecordID:  strconv.Itoa(clubID),
		OldValues: oldClub,
		NewValues: req,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Commit xatoligi"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "To'garak muvaffaqiyatli yangilandi"})
}

// DeleteClub handler
func (h *ClubHandler) DeleteClub(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	clubID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}

	var teacherID int
	var oldClub models.Club
	err = db.QueryRow(`
		SELECT id, name, subject_id, teacher_id
		FROM clubs WHERE id = $1 AND is_deleted = false
	`, clubID).Scan(&oldClub.ID, &oldClub.Name, &oldClub.SubjectID, &teacherID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "To'garak topilmadi"})
		return
	}

	if role != "ADMIN" && teacherID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu to'garakni o'chirishga ruxsatingiz yo'q"})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiya boshlashda xatolik"})
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE clubs
		SET is_deleted = true, deleted_at = NOW()
		WHERE id = $1
	`, clubID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "To'garakni o'chirishda xatolik"})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "clubs",
		RecordID:  strconv.Itoa(clubID),
		OldValues: oldClub,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Commit xatoligi"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "To'garak muvaffaqiyatli o'chirildi"})
}

// SaveClubGradesBatch saves/updates attendance & grades for all students in a club session date
func (h *ClubHandler) SaveClubGradesBatch(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userID, _ := strconv.Atoi(userIDVal.(string))

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	clubID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}

	var req models.BatchSaveClubGradesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload", "details": err.Error()})
		return
	}

	// Verify permission: Admin or teacher owner
	if role != "ADMIN" {
		var teacherID int
		err := db.QueryRow("SELECT teacher_id FROM clubs WHERE id = $1 AND is_deleted = false", clubID).Scan(&teacherID)
		if err != nil || teacherID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu to'garakka mas'ul emassiz"})
			return
		}
	}

	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tranzaksiya boshlashda xatolik"})
		return
	}
	defer tx.Rollback()

	query := `
		INSERT INTO club_grades (club_id, student_id, lesson_date, attendance, score_value, feedback, graded_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		ON CONFLICT (club_id, student_id, lesson_date) DO UPDATE SET
			attendance = EXCLUDED.attendance,
			score_value = EXCLUDED.score_value,
			feedback = EXCLUDED.feedback,
			graded_by = EXCLUDED.graded_by,
			updated_at = NOW()
	`

	for _, g := range req.Grades {
		att := g.Attendance
		if att == "" {
			att = "PRESENT"
		}
		_, err := tx.Exec(query, clubID, g.StudentID, req.LessonDate, att, g.ScoreValue, g.Feedback, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Baholarni saqlashda xatolik: " + err.Error()})
			return
		}
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "SAVE_CLUB_GRADES",
		TableName: "club_grades",
		RecordID:  strconv.Itoa(clubID),
		NewValues: map[string]interface{}{
			"lesson_date": req.LessonDate,
			"grade_count": len(req.Grades),
		},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Commit xatoligi"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "To'garak mashg'uloti baholari va davomati muvaffaqiyatli saqlandi"})
}

// GetClubGradesByDate returns student grades & attendance for a specific club date
func (h *ClubHandler) GetClubGradesByDate(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	clubID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}

	dateParam := c.Query("date")
	if dateParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "date parameter is required (YYYY-MM-DD)"})
		return
	}

	rows, err := db.Query(`
		SELECT cg.id, cg.club_id, cg.student_id,
		       stu_u.first_name || ' ' || stu_u.last_name as student_name,
		       cls.name as class_name,
		       cg.lesson_date, cg.attendance, cg.score_value, cg.feedback, cg.graded_by, cg.created_at, cg.updated_at
		FROM club_grades cg
		JOIN students s ON cg.student_id = s.id
		JOIN users stu_u ON s.user_id = stu_u.id
		JOIN classes cls ON s.class_id = cls.id
		WHERE cg.club_id = $1 AND cg.lesson_date = $2
		ORDER BY cls.name ASC, stu_u.last_name ASC
	`, clubID, dateParam)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Baholarni yuklashda xatolik: " + err.Error()})
		return
	}
	defer rows.Close()

	var grades []models.ClubGrade
	for rows.Next() {
		var g models.ClubGrade
		var lDate time.Time
		var gradedBy sql.NullInt64

		err := rows.Scan(
			&g.ID, &g.ClubID, &g.StudentID, &g.StudentName, &g.ClassName,
			&lDate, &g.Attendance, &g.ScoreValue, &g.Feedback, &gradedBy, &g.CreatedAt, &g.UpdatedAt,
		)
		if err == nil {
			g.LessonDate = lDate.Format("2006-01-02")
			if gradedBy.Valid {
				gb := int(gradedBy.Int64)
				g.GradedBy = &gb
			}
			grades = append(grades, g)
		}
	}

	c.JSON(http.StatusOK, grades)
}

// GetStudentClubGrades returns club grades & attendance history for student/parent view
func (h *ClubHandler) GetStudentClubGrades(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	db := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	currentUserID, _ := strconv.Atoi(userIDVal.(string))

	var studentIDs []int

	if userRole == "STUDENT" {
		var sid int
		err := db.QueryRow("SELECT id FROM students WHERE user_id = $1 AND is_deleted = false", currentUserID).Scan(&sid)
		if err == nil {
			studentIDs = append(studentIDs, sid)
		}
	} else if userRole == "PARENT" {
		rows, err := db.Query(`
			SELECT sp.student_id FROM student_parents sp
			JOIN students s ON sp.student_id = s.id
			WHERE sp.parent_id = $1 AND s.is_deleted = false`, currentUserID)
		if err == nil {
			for rows.Next() {
				var sid int
				if err := rows.Scan(&sid); err == nil {
					studentIDs = append(studentIDs, sid)
				}
			}
			rows.Close()
		}
	}

	if len(studentIDs) == 0 {
		c.JSON(http.StatusOK, []models.ClubGrade{})
		return
	}

	rows, err := db.Query(`
		SELECT cg.id, cg.club_id, clb.name as club_name, cg.student_id,
		       stu_u.first_name || ' ' || stu_u.last_name as student_name,
		       cls.name as class_name,
		       cg.lesson_date, cg.attendance, cg.score_value, cg.feedback, cg.graded_by, cg.created_at, cg.updated_at
		FROM club_grades cg
		JOIN clubs clb ON cg.club_id = clb.id
		JOIN students s ON cg.student_id = s.id
		JOIN users stu_u ON s.user_id = stu_u.id
		JOIN classes cls ON s.class_id = cls.id
		WHERE cg.student_id = ANY($1) AND clb.is_deleted = false
		ORDER BY cg.lesson_date DESC, clb.name ASC
	`, pq.Array(studentIDs))

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "To'garak baholarini yuklashda xatolik: " + err.Error()})
		return
	}
	defer rows.Close()

	var grades []models.ClubGrade
	for rows.Next() {
		var g models.ClubGrade
		var lDate time.Time
		var gradedBy sql.NullInt64

		err := rows.Scan(
			&g.ID, &g.ClubID, &g.ClubName, &g.StudentID, &g.StudentName, &g.ClassName,
			&lDate, &g.Attendance, &g.ScoreValue, &g.Feedback, &gradedBy, &g.CreatedAt, &g.UpdatedAt,
		)
		if err == nil {
			g.LessonDate = lDate.Format("2006-01-02")
			if gradedBy.Valid {
				gb := int(gradedBy.Int64)
				g.GradedBy = &gb
			}
			grades = append(grades, g)
		}
	}

	c.JSON(http.StatusOK, grades)
}
