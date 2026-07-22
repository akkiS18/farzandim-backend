package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

type DashboardHandler struct{}

func NewDashboardHandler() *DashboardHandler {
	return &DashboardHandler{}
}

type StudentAttendanceStat struct {
	StudentID           int     `json:"student_id"`
	UserID              int     `json:"user_id"`
	FirstName           string  `json:"first_name"`
	LastName            string  `json:"last_name"`
	MiddleName          *string `json:"middle_name,omitempty"`
	ClassID             int     `json:"class_id"`
	ClassName           string  `json:"class_name"`
	ClassLevel          int     `json:"class_level"`
	AbsentCount         int     `json:"absent_count"`
	PresentOrTardyCount int     `json:"present_or_tardy_count"`
	Status              string  `json:"status"` // "absent", "partial", "present", "no_data"
}

type DashboardStatsResponse struct {
	Date                  string                  `json:"date"`
	TotalStudents         int                     `json:"total_students"`
	CompletelyAbsentCount int                     `json:"completely_absent_count"`
	PartiallyAbsentCount  int                     `json:"partially_absent_count"`
	Students              []StudentAttendanceStat `json:"students"`
}

// GetStats returns total student count and attendance metrics (completely absent, partially absent) for a target date
func (h *DashboardHandler) GetStats(c *gin.Context) {
	tenantDBVal, exists := c.Get("tenantDB")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}
	dbConn := tenantDBVal.(*sql.DB)

	dateParam := c.Query("date")
	if dateParam == "" {
		dateParam = time.Now().Format("2006-01-02")
	}

	classIDFilter := c.Query("class_id")
	levelFilter := c.Query("level")

	var query string
	var args []interface{}
	args = append(args, dateParam)
	argIndex := 2

	query = `
		SELECT 
			s.id as student_id,
			u.id as user_id,
			u.first_name,
			u.last_name,
			u.middle_name,
			c.id as class_id,
			c.name as class_name,
			c.level as class_level,
			COALESCE(att.absent_count, 0) as absent_count,
			COALESCE(att.present_or_tardy_count, 0) as present_or_tardy_count
		FROM students s
		JOIN users u ON s.user_id = u.id
		JOIN classes c ON s.class_id = c.id
		LEFT JOIN (
			SELECT 
				g.student_id,
				COUNT(CASE WHEN g.value = '-' THEN 1 END) as absent_count,
				COUNT(CASE WHEN g.value IN ('+', 'k') THEN 1 END) as present_or_tardy_count
			FROM grades g
			WHERE g.grade_type = 'ATTENDANCE'
			  AND g.grade_date::date = $1::date
			  AND g.is_deleted = false
			GROUP BY g.student_id
		) att ON s.id = att.student_id
		WHERE s.is_deleted = false
		  AND u.is_deleted = false
		  AND c.is_deleted = false`

	if classIDFilter != "" {
		cid, err := strconv.Atoi(classIDFilter)
		if err == nil {
			query += ` AND s.class_id = $` + strconv.Itoa(argIndex)
			args = append(args, cid)
			argIndex++
		}
	}

	if levelFilter != "" {
		lvl, err := strconv.Atoi(levelFilter)
		if err == nil {
			query += ` AND c.level = $` + strconv.Itoa(argIndex)
			args = append(args, lvl)
			argIndex++
		}
	}

	query += ` ORDER BY c.level ASC, c.name ASC, u.last_name ASC, u.first_name ASC`

	rows, err := dbConn.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch dashboard statistics", "details": err.Error()})
		return
	}
	defer rows.Close()

	studentList := []StudentAttendanceStat{}
	totalStudents := 0
	completelyAbsent := 0
	partiallyAbsent := 0

	for rows.Next() {
		var st StudentAttendanceStat
		var middleNameNull sql.NullString
		err := rows.Scan(
			&st.StudentID,
			&st.UserID,
			&st.FirstName,
			&st.LastName,
			&middleNameNull,
			&st.ClassID,
			&st.ClassName,
			&st.ClassLevel,
			&st.AbsentCount,
			&st.PresentOrTardyCount,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse dashboard rows", "details": err.Error()})
			return
		}
		if middleNameNull.Valid {
			st.MiddleName = &middleNameNull.String
		}

		if st.AbsentCount > 0 && st.PresentOrTardyCount == 0 {
			st.Status = "absent"
			completelyAbsent++
		} else if st.AbsentCount > 0 && st.PresentOrTardyCount > 0 {
			st.Status = "partial"
			partiallyAbsent++
		} else if st.AbsentCount == 0 && st.PresentOrTardyCount > 0 {
			st.Status = "present"
		} else {
			st.Status = "no_data"
		}

		totalStudents++
		studentList = append(studentList, st)
	}

	resp := DashboardStatsResponse{
		Date:                  dateParam,
		TotalStudents:         totalStudents,
		CompletelyAbsentCount: completelyAbsent,
		PartiallyAbsentCount:  partiallyAbsent,
		Students:              studentList,
	}

	c.JSON(http.StatusOK, resp)
}
