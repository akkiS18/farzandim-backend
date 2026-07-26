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

type DailyAttendanceStat struct {
	Day           string  `json:"day"`            // "Dush", "Sesh", "Chor", "Pay", "Jum", "Shan"
	Date          string  `json:"date"`           // "2026-07-20"
	AttendancePct float64 `json:"attendance_pct"` // 0 to 100
}

type DashboardStatsResponse struct {
	Date                  string                  `json:"date"`
	TotalStudents         int                     `json:"total_students"`
	TotalClasses          int                     `json:"total_classes"`
	TotalClubs            int                     `json:"total_clubs"`
	CompletelyAbsentCount int                     `json:"completely_absent_count"`
	PartiallyAbsentCount  int                     `json:"partially_absent_count"`
	Students              []StudentAttendanceStat `json:"students"`
	WeeklyAttendance      []DailyAttendanceStat   `json:"weekly_attendance"`
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

	var totalClasses int
	_ = dbConn.QueryRow("SELECT COUNT(*) FROM classes WHERE is_deleted = false").Scan(&totalClasses)

	var totalClubs int
	_ = dbConn.QueryRow("SELECT COUNT(*) FROM clubs WHERE is_deleted = false").Scan(&totalClubs)

	// Calculate weekly attendance stats (Monday to Saturday)
	weekDaysLabels := []string{"Dush", "Sesh", "Chor", "Pay", "Jum", "Shan"}
	weeklyStats := make([]DailyAttendanceStat, 6)

	var mondayDate time.Time
	errMon := dbConn.QueryRow("SELECT date_trunc('week', $1::date)::date", dateParam).Scan(&mondayDate)
	if errMon == nil {
		weeklyQuery := `
			SELECT 
				EXTRACT(DOW FROM g.grade_date::date)::int as dow,
				COUNT(CASE WHEN g.value IN ('+', 'k') THEN 1 END) as present_cnt,
				COUNT(CASE WHEN g.value = '-' THEN 1 END) as absent_cnt
			FROM grades g
			JOIN students s ON g.student_id = s.id
			JOIN classes c ON s.class_id = c.id
			WHERE g.grade_type = 'ATTENDANCE'
			  AND g.grade_date::date >= date_trunc('week', $1::date)::date
			  AND g.grade_date::date <= (date_trunc('week', $1::date)::date + INTERVAL '5 days')
			  AND g.is_deleted = false
			  AND s.is_deleted = false
			  AND c.is_deleted = false`

		var wArgs []interface{}
		wArgs = append(wArgs, dateParam)
		wArgIndex := 2

		if classIDFilter != "" {
			cid, err := strconv.Atoi(classIDFilter)
			if err == nil {
				weeklyQuery += ` AND s.class_id = $` + strconv.Itoa(wArgIndex)
				wArgs = append(wArgs, cid)
				wArgIndex++
			}
		}

		if levelFilter != "" {
			lvl, err := strconv.Atoi(levelFilter)
			if err == nil {
				weeklyQuery += ` AND c.level = $` + strconv.Itoa(wArgIndex)
				wArgs = append(wArgs, lvl)
				wArgIndex++
			}
		}

		weeklyQuery += ` GROUP BY dow ORDER BY dow ASC`

		dayCounts := make(map[int]struct {
			present int
			absent  int
		})

		wRows, wErr := dbConn.Query(weeklyQuery, wArgs...)
		if wErr == nil {
			for wRows.Next() {
				var dow, pCnt, aCnt int
				if err := wRows.Scan(&dow, &pCnt, &aCnt); err == nil {
					// DOW: 1=Mon, 2=Tue, 3=Wed, 4=Thu, 5=Fri, 6=Sat
					dayCounts[dow] = struct {
						present int
						absent  int
					}{present: pCnt, absent: aCnt}
				}
			}
			wRows.Close()
		}

		for i := 0; i < 6; i++ {
			dowTarget := i + 1 // 1..6
			dDate := mondayDate.AddDate(0, 0, i).Format("2006-01-02")
			pct := 100.0
			if counts, ok := dayCounts[dowTarget]; ok && (counts.present+counts.absent) > 0 {
				pct = float64(counts.present) / float64(counts.present+counts.absent) * 100.0
			}
			weeklyStats[i] = DailyAttendanceStat{
				Day:           weekDaysLabels[i],
				Date:          dDate,
				AttendancePct: float64(int(pct*10)) / 10.0,
			}
		}
	}

	resp := DashboardStatsResponse{
		Date:                  dateParam,
		TotalStudents:         totalStudents,
		TotalClasses:          totalClasses,
		TotalClubs:            totalClubs,
		CompletelyAbsentCount: completelyAbsent,
		PartiallyAbsentCount:  partiallyAbsent,
		Students:              studentList,
		WeeklyAttendance:      weeklyStats,
	}

	c.JSON(http.StatusOK, resp)
}
