package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/farzandim/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type AnnouncementHandler struct{}

func NewAnnouncementHandler() *AnnouncementHandler {
	return &AnnouncementHandler{}
}

type CreateAnnouncementRequest struct {
	Title      string `json:"title" binding:"required"`
	Content    string `json:"content" binding:"required"`
	ClassIDs   []int  `json:"class_ids"`   // Optional.
	LevelIDs   []int  `json:"level_ids"`   // Optional.
	StudentIDs []int  `json:"student_ids"` // Optional.
}

func (h *AnnouncementHandler) CreateAnnouncement(c *gin.Context) {
	var req CreateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	if role != "ADMIN" {
		// O'qituvchilar butun maktabga yubora olmaydilar
		if len(req.ClassIDs) == 0 && len(req.LevelIDs) == 0 && len(req.StudentIDs) == 0 {
			c.JSON(http.StatusForbidden, gin.H{"error": "O'qituvchilar butun maktabga e'lon yubora olmaydilar. Kamida bitta sinf, level yoki o'quvchini tanlang"})
			return
		}

		// 1. Fetch allowed class IDs for the teacher
		rows, err := dbConn.Query("SELECT class_id FROM class_teachers WHERE teacher_id = $1 AND is_deleted = false", userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch teacher classes", "details": err.Error()})
			return
		}
		defer rows.Close()

		allowedClasses := make(map[int]bool)
		var allowedClassIDs []interface{}
		for rows.Next() {
			var cid int
			if err := rows.Scan(&cid); err == nil {
				allowedClasses[cid] = true
				allowedClassIDs = append(allowedClassIDs, cid)
			}
		}

		// Validate ClassIDs
		for _, cid := range req.ClassIDs {
			if !allowedClasses[cid] {
				c.JSON(http.StatusForbidden, gin.H{"error": "Siz ushbu sinfga e'lon yubora olmaysiz"})
				return
			}
		}

		// Validate LevelIDs
		if len(req.LevelIDs) > 0 {
			if len(allowedClassIDs) == 0 {
				c.JSON(http.StatusForbidden, gin.H{"error": "Sizga hech qanday sinf biriktirilmagan"})
				return
			}
			placeholders := make([]string, len(allowedClassIDs))
			for i := range allowedClassIDs {
				placeholders[i] = fmt.Sprintf("$%d", i+1)
			}
			queryLevels := fmt.Sprintf(`
				SELECT DISTINCT level FROM classes 
				WHERE id IN (%s) AND is_deleted = false
			`, strings.Join(placeholders, ","))

			levelRows, err := dbConn.Query(queryLevels, allowedClassIDs...)
			if err == nil {
				allowedLevels := make(map[int]bool)
				for levelRows.Next() {
					var lvl int
					if err := levelRows.Scan(&lvl); err == nil {
						allowedLevels[lvl] = true
					}
				}
				levelRows.Close()

				for _, lvl := range req.LevelIDs {
					if !allowedLevels[lvl] {
						c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu sinf darajalariga (level) e'lon yuborishga ruxsatingiz yo'q"})
						return
					}
				}
			}
		}

		// Validate StudentIDs
		if len(req.StudentIDs) > 0 {
			if len(allowedClassIDs) == 0 {
				c.JSON(http.StatusForbidden, gin.H{"error": "Sizga hech qanday sinf biriktirilmagan"})
				return
			}
			placeholders := make([]string, len(allowedClassIDs))
			for i := range allowedClassIDs {
				placeholders[i] = fmt.Sprintf("$%d", i+1)
			}
			placeholdersStud := make([]string, len(req.StudentIDs))
			for i := range req.StudentIDs {
				placeholdersStud[i] = fmt.Sprintf("$%d", len(allowedClassIDs)+i+1)
			}

			var args []interface{}
			args = append(args, allowedClassIDs...)
			for _, sid := range req.StudentIDs {
				args = append(args, sid)
			}

			queryStudents := fmt.Sprintf(`
				SELECT COUNT(id) FROM students 
				WHERE class_id IN (%s) AND id IN (%s) AND is_deleted = false
			`, strings.Join(placeholders, ","), strings.Join(placeholdersStud, ","))

			var count int
			err = dbConn.QueryRow(queryStudents, args...).Scan(&count)
			if err != nil || count != len(req.StudentIDs) {
				c.JSON(http.StatusForbidden, gin.H{"error": "Tanlangan o'quvchilar orasida sizga biriktirilmagan o'quvchilar bor"})
				return
			}
		}
	}

	// Start database transaction
	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Insert Announcement
	var announcementID int
	query := `
		INSERT INTO announcements (title, content, author_id) 
		VALUES ($1, $2, $3) 
		RETURNING id, created_at, updated_at
	`
	var createdAt, updatedAt time.Time
	err = tx.QueryRow(query, req.Title, req.Content, userID).Scan(&announcementID, &createdAt, &updatedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create announcement", "details": err.Error()})
		return
	}

	// Insert into announcement_classes if specific classes are targetted
	if len(req.ClassIDs) > 0 {
		for _, classID := range req.ClassIDs {
			_, err = tx.Exec(
				"INSERT INTO announcement_classes (announcement_id, class_id) VALUES ($1, $2)",
				announcementID, classID,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to link classes to announcement", "details": err.Error()})
				return
			}
		}
	}

	// Insert into announcement_levels if specific levels are targetted
	if len(req.LevelIDs) > 0 {
		for _, level := range req.LevelIDs {
			_, err = tx.Exec(
				"INSERT INTO announcement_levels (announcement_id, level) VALUES ($1, $2)",
				announcementID, level,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to link levels to announcement", "details": err.Error()})
				return
			}
		}
	}

	// Insert into announcement_students if specific students are targetted
	if len(req.StudentIDs) > 0 {
		for _, studentID := range req.StudentIDs {
			_, err = tx.Exec(
				"INSERT INTO announcement_students (announcement_id, student_id) VALUES ($1, $2)",
				announcementID, studentID,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to link students to announcement", "details": err.Error()})
				return
			}
		}
	}

	ann := models.Announcement{
		ID:         announcementID,
		Title:      req.Title,
		Content:    req.Content,
		AuthorID:   userID,
		ClassIDs:   req.ClassIDs,
		LevelIDs:   req.LevelIDs,
		StudentIDs: req.StudentIDs,
		IsDeleted:  false,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}

	// Write Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "announcements",
		RecordID:  strconv.Itoa(announcementID),
		NewValues: ann,
	})

	// Commit Transaction
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	// Fetch current school ID for sending notification
	currentSchoolID := c.GetString("currentSchoolID")

	// Trigger Telegram notification asynchronously (SendAnnouncementNotification is already async internally for message delivery)
	services.SendAnnouncementNotification(currentSchoolID, &ann)

	c.JSON(http.StatusCreated, ann)
}

func (h *AnnouncementHandler) ListAnnouncements(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	roleVal, exists := c.Get("role")
	role := ""
	if exists {
		role = roleVal.(string)
	}

	userIDVal, exists := c.Get("userID")
	userIDStr := ""
	if exists {
		userIDStr = userIDVal.(string)
	}

	var rows *sql.Rows
	var err error

	// If role is parent, filter announcements by target levels, classes, students, or general announcements
	if role == "PARENT" {
		parentID, errConv := strconv.Atoi(userIDStr)
		if errConv != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parent user ID"})
			return
		}
		query := `
			SELECT DISTINCT a.id, a.title, a.content, a.author_id, 
				u.first_name || ' ' || u.last_name as author_name, 
				a.created_at, a.updated_at
			FROM announcements a
			JOIN users u ON a.author_id = u.id
			LEFT JOIN announcement_classes ac ON a.id = ac.announcement_id
			LEFT JOIN announcement_levels al ON a.id = al.announcement_id
			LEFT JOIN announcement_students ast ON a.id = ast.announcement_id
			WHERE a.is_deleted = false AND (
				(ac.class_id IS NULL AND al.level IS NULL AND ast.student_id IS NULL) OR
				ac.class_id IN (
					SELECT s.class_id 
					FROM students s
					JOIN student_parents sp ON s.id = sp.student_id
					WHERE sp.parent_id = $1 AND s.is_deleted = false
				) OR
				al.level IN (
					SELECT c.level 
					FROM classes c
					JOIN students s ON s.class_id = c.id
					JOIN student_parents sp ON s.id = sp.student_id
					WHERE sp.parent_id = $1 AND c.is_deleted = false AND s.is_deleted = false
				) OR
				ast.student_id IN (
					SELECT sp.student_id 
					FROM student_parents sp
					WHERE sp.parent_id = $1
				)
			)
			ORDER BY a.created_at DESC`
		rows, err = dbConn.Query(query, parentID)
	} else if role == "STUDENT" {
		studentUserID, errConv := strconv.Atoi(userIDStr)
		if errConv != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student user ID"})
			return
		}
		query := `
			SELECT DISTINCT a.id, a.title, a.content, a.author_id, 
				u.first_name || ' ' || u.last_name as author_name, 
				a.created_at, a.updated_at
			FROM announcements a
			JOIN users u ON a.author_id = u.id
			LEFT JOIN announcement_classes ac ON a.id = ac.announcement_id
			LEFT JOIN announcement_levels al ON a.id = al.announcement_id
			LEFT JOIN announcement_students ast ON a.id = ast.announcement_id
			WHERE a.is_deleted = false AND (
				(ac.class_id IS NULL AND al.level IS NULL AND ast.student_id IS NULL) OR
				ac.class_id IN (
					SELECT s.class_id 
					FROM students s
					WHERE s.user_id = $1 AND s.is_deleted = false
				) OR
				al.level IN (
					SELECT c.level 
					FROM classes c
					JOIN students s ON s.class_id = c.id
					WHERE s.user_id = $1 AND c.is_deleted = false AND s.is_deleted = false
				) OR
				ast.student_id IN (
					SELECT s.id 
					FROM students s
					WHERE s.user_id = $1 AND s.is_deleted = false
				)
			)
			ORDER BY a.created_at DESC`
		rows, err = dbConn.Query(query, studentUserID)
	} else {
		// ADMIN or TEACHER can see all announcements
		query := `
			SELECT a.id, a.title, a.content, a.author_id, 
				u.first_name || ' ' || u.last_name as author_name, 
				a.created_at, a.updated_at
			FROM announcements a
			JOIN users u ON a.author_id = u.id
			WHERE a.is_deleted = false
			ORDER BY a.created_at DESC`
		rows, err = dbConn.Query(query)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query announcements", "details": err.Error()})
		return
	}
	defer rows.Close()

	announcements := []models.Announcement{}
	for rows.Next() {
		var ann models.Announcement
		err := rows.Scan(&ann.ID, &ann.Title, &ann.Content, &ann.AuthorID, &ann.AuthorName, &ann.CreatedAt, &ann.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan announcement data", "details": err.Error()})
			return
		}

		// Fetch class IDs linked to this announcement
		classRows, err := dbConn.Query("SELECT class_id FROM announcement_classes WHERE announcement_id = $1", ann.ID)
		if err == nil {
			var classIDs []int
			for classRows.Next() {
				var cid int
				if err := classRows.Scan(&cid); err == nil {
					classIDs = append(classIDs, cid)
				}
			}
			classRows.Close()
			ann.ClassIDs = classIDs
		}

		// Fetch level IDs linked to this announcement
		levelRows, err := dbConn.Query("SELECT level FROM announcement_levels WHERE announcement_id = $1", ann.ID)
		if err == nil {
			var levelIDs []int
			for levelRows.Next() {
				var lvl int
				if err := levelRows.Scan(&lvl); err == nil {
					levelIDs = append(levelIDs, lvl)
				}
			}
			levelRows.Close()
			ann.LevelIDs = levelIDs
		}

		// Fetch student IDs linked to this announcement
		studentRows, err := dbConn.Query("SELECT student_id FROM announcement_students WHERE announcement_id = $1", ann.ID)
		if err == nil {
			var studentIDs []int
			for studentRows.Next() {
				var sid int
				if err := studentRows.Scan(&sid); err == nil {
					studentIDs = append(studentIDs, sid)
				}
			}
			studentRows.Close()
			ann.StudentIDs = studentIDs
		}

		announcements = append(announcements, ann)
	}

	c.JSON(http.StatusOK, announcements)
}

func (h *AnnouncementHandler) DeleteAnnouncement(c *gin.Context) {
	announcementIDStr := c.Param("id")
	announcementID, err := strconv.Atoi(announcementIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Malformed announcement ID parameter"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	// Verify permission: admin can delete any, teacher can only delete their own
	if role != "ADMIN" {
		var authorID int
		err = dbConn.QueryRow("SELECT author_id FROM announcements WHERE id = $1 AND is_deleted = false", announcementID).Scan(&authorID)
		if err != nil {
			if err == sql.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{"error": "Announcement not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify announcement ownership"})
			return
		}

		if authorID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Siz faqat o'zingiz yozgan e'lonlarni o'chira olasiz"})
			return
		}
	}

	// Start database transaction
	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// Soft delete: set is_deleted = true, deleted_at = now()
	query := `
		UPDATE announcements 
		SET is_deleted = true, deleted_at = $1 
		WHERE id = $2 AND is_deleted = false
		RETURNING id, title, content, author_id, created_at, updated_at
	`
	var ann models.Announcement
	err = tx.QueryRow(query, time.Now(), announcementID).Scan(&ann.ID, &ann.Title, &ann.Content, &ann.AuthorID, &ann.CreatedAt, &ann.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Announcement not found or already deleted"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete announcement", "details": err.Error()})
		return
	}

	ann.IsDeleted = true

	// Write Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "announcements",
		RecordID:  strconv.Itoa(announcementID),
		OldValues: ann,
	})

	// Commit Transaction
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Announcement successfully deleted"})
}
