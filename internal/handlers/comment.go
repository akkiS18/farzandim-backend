package handlers

import (
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/farzandim/backend/internal/models"
	"github.com/farzandim/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type CommentHandler struct{}

func NewCommentHandler() *CommentHandler {
	return &CommentHandler{}
}

type CreateCommentRequest struct {
	Content string `json:"content" binding:"required"`
}

type CreateMenuCommentRequest struct {
	MenuDate string `json:"menu_date" binding:"required"` // format "YYYY-MM-DD"
	Content  string `json:"content" binding:"required"`
	ParentID *int   `json:"parent_id,omitempty"`
}

// CreateGradeComment handles POST /api/schools/grades/:id/comments
func (h *CommentHandler) CreateGradeComment(c *gin.Context) {
	gradeIDStr := c.Param("id")
	gradeID, err := strconv.Atoi(gradeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid grade ID"})
		return
	}

	var req CreateCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	userID, _ := strconv.Atoi(userIDStr)

	// Verify access based on role
	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	if role == "PARENT" {
		var hasAccess bool
		err = dbConn.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM student_parents sp
				JOIN grades g ON sp.student_id = g.student_id
				WHERE sp.parent_id = $1 AND g.id = $2
			)
		`, userID, gradeID).Scan(&hasAccess)

		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu bahoga komment qoldirishga ruxsatingiz yo'q"})
			return
		}
	} else if role == "MAIN_TEACHER" || role == "SUBJECT_TEACHER" {
		var hasAccess bool
		queryAccess := `
			SELECT EXISTS (
				SELECT 1 FROM grades WHERE id = $2 AND teacher_id = $1
				UNION
				SELECT 1 FROM class_teachers ct
				JOIN students s ON ct.class_id = s.class_id
				JOIN grades g ON s.id = g.student_id
				WHERE ct.teacher_id = $1 AND ct.is_main_teacher = true AND g.id = $2 AND ct.is_deleted = false AND s.is_deleted = false
			)
		`
		err = dbConn.QueryRow(queryAccess, userID, gradeID).Scan(&hasAccess)
		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu bahoga javob yozishga ruxsatingiz yo'q"})
			return
		}
	} else if role != "ADMIN" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu amalni bajarishga ruxsatingiz yo'q"})
		return
	}

	query := `
		INSERT INTO grade_comments (grade_id, author_id, content) 
		VALUES ($1, $2, $3) 
		RETURNING id, created_at
	`
	var commentID int
	var createdAt time.Time
	err = dbConn.QueryRow(query, gradeID, userID, req.Content).Scan(&commentID, &createdAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save comment", "details": err.Error()})
		return
	}

	currentSchoolID := c.GetString("currentSchoolID")
	go services.SendGradeCommentNotificationToTeachers(currentSchoolID, gradeID, req.Content, userID)

	c.JSON(http.StatusCreated, models.GradeComment{
		ID:        commentID,
		GradeID:   gradeID,
		AuthorID:  userID,
		Content:   req.Content,
		CreatedAt: createdAt,
	})
}

// CreateMenuComment handles POST /api/schools/menu/comments
func (h *CommentHandler) CreateMenuComment(c *gin.Context) {
	var req CreateMenuCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	menuDate, err := time.Parse("2006-01-02", req.MenuDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sana noto'g'ri formatda. YYYY-MM-DD kiriting"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	userID, _ := strconv.Atoi(userIDStr)

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	var targetParentID int
	if role == "PARENT" {
		targetParentID = userID
	} else if role == "MAIN_TEACHER" || role == "SUBJECT_TEACHER" || role == "ADMIN" {
		if req.ParentID == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Javob yozish uchun parent_id kiritilishi shart"})
			return
		}
		targetParentID = *req.ParentID

		// O'qituvchilar uchun xavfsizlik tekshiruvi
		if role == "MAIN_TEACHER" || role == "SUBJECT_TEACHER" {
			var hasAccess bool
			queryAccess := `
				SELECT EXISTS (
					SELECT 1 FROM student_parents sp
					JOIN students s ON sp.student_id = s.id
					JOIN class_teachers ct ON s.class_id = ct.class_id
					WHERE ct.teacher_id = $1 AND sp.parent_id = $2 AND ct.is_deleted = false AND s.is_deleted = false
				)
			`
			err = dbConn.QueryRow(queryAccess, userID, targetParentID).Scan(&hasAccess)
			if err != nil || !hasAccess {
				c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu ota-onaga javob yozishga ruxsatingiz yo'q"})
				return
			}
		}
	} else {
		c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu amalni bajarishga ruxsatingiz yo'q"})
		return
	}

	query := `
		INSERT INTO menu_comments (menu_date, parent_id, author_id, content) 
		VALUES ($1, $2, $3, $4) 
		RETURNING id, created_at
	`
	var commentID int
	var createdAt time.Time
	err = dbConn.QueryRow(query, menuDate, targetParentID, userID, req.Content).Scan(&commentID, &createdAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save comment", "details": err.Error()})
		return
	}

	currentSchoolID := c.GetString("currentSchoolID")
	// Faqat ota-ona yozganda trigger qilamiz
	if role == "PARENT" {
		go services.SendMenuCommentNotificationToAdvisors(currentSchoolID, menuDate, req.Content, userID)
	}

	c.JSON(http.StatusCreated, models.MenuComment{
		ID:        commentID,
		MenuDate:  menuDate,
		ParentID:  targetParentID,
		AuthorID:  userID,
		Content:   req.Content,
		CreatedAt: createdAt,
	})
}

// GetCommentsFeed handles GET /api/schools/comments/feed
func (h *CommentHandler) GetCommentsFeed(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	userID, _ := strconv.Atoi(userIDStr)

	var feed []models.FeedbackComment

	// 1. Fetch grade comments based on role
	gradeQuery := `
		SELECT gc.id, 'GRADE' as type, g.id as grade_id, 
		       COALESCE((SELECT sp.parent_id FROM student_parents sp WHERE sp.student_id = g.student_id LIMIT 1), 0) as parent_id,
		       gc.author_id, gc.content, gc.created_at, 
		       au.first_name || ' ' || au.last_name as author_name,
		       s.name as subject_name, g.value as grade_value,
		       stu_u.first_name || ' ' || stu_u.last_name as student_name,
		       cls.name as class_name
		FROM grade_comments gc
		JOIN users au ON gc.author_id = au.id
		JOIN grades g ON gc.grade_id = g.id
		JOIN subjects s ON g.subject_id = s.id
		JOIN students stu ON g.student_id = stu.id
		JOIN users stu_u ON stu.user_id = stu_u.id
		JOIN classes cls ON stu.class_id = cls.id
		WHERE (
			$2 = 'ADMIN'
			OR
			($2 = 'PARENT' AND g.student_id IN (SELECT student_id FROM student_parents WHERE parent_id = $1))
			OR
			($2 IN ('MAIN_TEACHER', 'SUBJECT_TEACHER') AND (
				g.teacher_id = $1
				OR
				stu.class_id IN (
					SELECT class_id FROM class_teachers 
					WHERE teacher_id = $1 AND is_main_teacher = true AND is_deleted = false
				)
			))
		)
	`
	rows, err := dbConn.Query(gradeQuery, userID, role)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var fc models.FeedbackComment
			var gradeID int
			err := rows.Scan(
				&fc.ID, &fc.Type, &gradeID, &fc.ParentID, &fc.AuthorID, &fc.Content, &fc.CreatedAt,
				&fc.AuthorName, &fc.SubjectName, &fc.GradeValue,
				&fc.StudentName, &fc.ClassName,
			)
			if err == nil {
				fc.GradeID = &gradeID
				feed = append(feed, fc)
			}
		}
	}

	// 2. Fetch menu comments based on role
	menuQuery := `
		SELECT mc.id, 'MENU' as type, 0 as grade_id, mc.parent_id, mc.author_id, mc.content, mc.created_at, 
		       au.first_name || ' ' || au.last_name as author_name,
		       mc.menu_date
		FROM menu_comments mc
		JOIN users au ON mc.author_id = au.id
		WHERE (
			$2 = 'ADMIN'
			OR
			($2 = 'PARENT' AND mc.parent_id = $1)
			OR
			($2 IN ('MAIN_TEACHER', 'SUBJECT_TEACHER') AND EXISTS (
				SELECT 1 FROM class_teachers ct
				JOIN students s ON ct.class_id = s.class_id
				JOIN student_parents sp ON s.id = sp.student_id
				WHERE ct.teacher_id = $1 AND ct.is_main_teacher = true AND sp.parent_id = mc.parent_id AND ct.is_deleted = false AND s.is_deleted = false
			))
		)
	`
	mRows, err := dbConn.Query(menuQuery, userID, role)
	if err == nil {
		defer mRows.Close()
		for mRows.Next() {
			var fc models.FeedbackComment
			var menuDate time.Time
			err := mRows.Scan(
				&fc.ID, &fc.Type, &fc.GradeID, &fc.ParentID, &fc.AuthorID, &fc.Content, &fc.CreatedAt,
				&fc.AuthorName, &menuDate,
			)
			if err == nil {
				fc.MenuDate = &menuDate
				fc.GradeID = nil // set explicit nil for Menu comments
				feed = append(feed, fc)
			}
		}
	}

	// Sort feedback by CreatedAt in descending order (newest first)
	sort.Slice(feed, func(i, j int) bool {
		return feed[i].CreatedAt.After(feed[j].CreatedAt)
	})

	c.JSON(http.StatusOK, feed)
}

type ChatMessage struct {
	ID         int       `json:"id"`
	AuthorID   int       `json:"author_id"`
	AuthorName string    `json:"author_name"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

// GetGradeComments handles GET /api/schools/grades/:id/comments
func (h *CommentHandler) GetGradeComments(c *gin.Context) {
	gradeIDStr := c.Param("id")
	gradeID, err := strconv.Atoi(gradeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid grade ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	userID, _ := strconv.Atoi(userIDStr)

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	// Access verification based on role
	if role == "PARENT" {
		var hasAccess bool
		err = dbConn.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM student_parents sp
				JOIN grades g ON sp.student_id = g.student_id
				WHERE sp.parent_id = $1 AND g.id = $2
			)
		`, userID, gradeID).Scan(&hasAccess)

		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu baho chatini ko'rishga ruxsatingiz yo'q"})
			return
		}
	} else if role == "MAIN_TEACHER" || role == "SUBJECT_TEACHER" {
		var hasAccess bool
		queryAccess := `
			SELECT EXISTS (
				SELECT 1 FROM grades WHERE id = $2 AND teacher_id = $1
				UNION
				SELECT 1 FROM class_teachers ct
				JOIN students s ON ct.class_id = s.class_id
				JOIN grades g ON s.id = g.student_id
				WHERE ct.teacher_id = $1 AND ct.is_main_teacher = true AND g.id = $2 AND ct.is_deleted = false AND s.is_deleted = false
			)
		`
		err = dbConn.QueryRow(queryAccess, userID, gradeID).Scan(&hasAccess)
		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu baho chatini ko'rishga ruxsatingiz yo'q"})
			return
		}
	} else if role != "ADMIN" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu amalni bajarishga ruxsatingiz yo'q"})
		return
	}

	query := `
		SELECT gc.id, gc.author_id, u.first_name || ' ' || u.last_name as author_name, 
		       r.name as role, gc.content, gc.created_at
		FROM grade_comments gc
		JOIN users u ON gc.author_id = u.id
		JOIN roles r ON u.role_id = r.id
		WHERE gc.grade_id = $1
		ORDER BY gc.created_at ASC
	`
	rows, err := dbConn.Query(query, gradeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch comments", "details": err.Error()})
		return
	}
	defer rows.Close()

	messages := []ChatMessage{}
	for rows.Next() {
		var msg ChatMessage
		err := rows.Scan(&msg.ID, &msg.AuthorID, &msg.AuthorName, &msg.Role, &msg.Content, &msg.CreatedAt)
		if err == nil {
			messages = append(messages, msg)
		}
	}

	c.JSON(http.StatusOK, messages)
}

// GetMenuComments handles GET /api/schools/menu/comments
func (h *CommentHandler) GetMenuComments(c *gin.Context) {
	menuDateStr := c.Query("menu_date")
	parentIDStr := c.Query("parent_id")

	if menuDateStr == "" || parentIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "menu_date va parent_id parametrlari majburiy"})
		return
	}

	menuDate, err := time.Parse("2006-01-02", menuDateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sana formati noto'g'ri (YYYY-MM-DD)"})
		return
	}

	parentID, err := strconv.Atoi(parentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parent_id"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	userID, _ := strconv.Atoi(userIDStr)

	roleVal, _ := c.Get("role")
	role := roleVal.(string)

	// Access verification based on role
	if role == "PARENT" {
		if userID != parentID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu taomnoma chatini ko'rishga ruxsatingiz yo'q"})
			return
		}
	} else if role == "MAIN_TEACHER" || role == "SUBJECT_TEACHER" {
		var hasAccess bool
		queryAccess := `
			SELECT EXISTS (
				SELECT 1 FROM student_parents sp
				JOIN students s ON sp.student_id = s.id
				JOIN class_teachers ct ON s.class_id = ct.class_id
				WHERE ct.teacher_id = $1 AND sp.parent_id = $2 AND ct.is_deleted = false AND s.is_deleted = false
			)
		`
		err = dbConn.QueryRow(queryAccess, userID, parentID).Scan(&hasAccess)
		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu taomnoma chatini ko'rishga ruxsatingiz yo'q"})
			return
		}
	} else if role != "ADMIN" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Ushbu amalni bajarishga ruxsatingiz yo'q"})
		return
	}

	query := `
		SELECT mc.id, mc.author_id, u.first_name || ' ' || u.last_name as author_name, 
		       r.name as role, mc.content, mc.created_at
		FROM menu_comments mc
		JOIN users u ON mc.author_id = u.id
		JOIN roles r ON u.role_id = r.id
		WHERE mc.menu_date = $1 AND mc.parent_id = $2
		ORDER BY mc.created_at ASC
	`
	rows, err := dbConn.Query(query, menuDate, parentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch comments", "details": err.Error()})
		return
	}
	defer rows.Close()

	messages := []ChatMessage{}
	for rows.Next() {
		var msg ChatMessage
		err := rows.Scan(&msg.ID, &msg.AuthorID, &msg.AuthorName, &msg.Role, &msg.Content, &msg.CreatedAt)
		if err == nil {
			messages = append(messages, msg)
		}
	}

	c.JSON(http.StatusOK, messages)
}
