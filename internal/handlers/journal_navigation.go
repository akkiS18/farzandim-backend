package handlers

import (
	"database/sql"
	"fmt"
	"github.com/gin-gonic/gin"
	"strconv"
	"time"
)

// AdjacentJournalLesson uses the effective timetable, including replacements,
// cancellations, schedule periods and class/level holidays. Same-day periods count.
func (h *ScheduleHandler) AdjacentJournalLesson(c *gin.Context) {
	classID, e1 := strconv.Atoi(c.Param("id"))
	subjectID, e2 := strconv.Atoi(c.Query("subject_id"))
	lesson, e3 := strconv.Atoi(c.Query("lesson_number"))
	date, e4 := time.Parse("2006-01-02", c.Query("date"))
	direction := c.Query("direction")
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || classID < 1 || subjectID < 1 || lesson < 1 || (direction != "next" && direction != "previous") {
		c.JSON(400, gin.H{"error": "Dars, sana va yo‘nalishni to‘g‘ri tanlang"})
		return
	}
	conn := c.MustGet("tenantDB").(*sql.DB)
	if c.GetString("role") != "ADMIN" {
		var allowed bool
		err := conn.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM class_teachers 
			WHERE class_id=$1 AND teacher_id=$2 AND is_deleted=false
		)`, classID, c.GetString("userID")).Scan(&allowed)
		if err != nil {
			c.JSON(500, gin.H{"error": "Ruxsatni tekshirib bo‘lmadi"})
			return
		}
		if !allowed {
			c.JSON(403, gin.H{"error": "Siz ushbu sinfga biriktirilmagansiz"})
			return
		}
	}
	op, order := ">", "ASC"
	start, end := date, date.AddDate(1, 0, 0)
	if direction == "previous" {
		op, order = "<", "DESC"
		start, end = date.AddDate(-1, 0, 0), date
	}
	query := fmt.Sprintf(`WITH days AS (
 SELECT d::date AS day FROM generate_series($4::date,$5::date,interval '1 day') d
 ), base AS (
 SELECT d.day, cs.lesson_number, cs.subject_id FROM days d JOIN class_schedules cs ON cs.class_id=$1
 AND cs.is_deleted=false AND d.day BETWEEN cs.start_date AND cs.end_date
 AND cs.day_of_week=extract(isodow from d.day)
 AND cs.start_date=(SELECT MAX(s.start_date) FROM class_schedules s WHERE s.class_id=$1 AND s.is_deleted=false AND d.day BETWEEN s.start_date AND s.end_date)
 ), effective AS (
 SELECT b.day,b.lesson_number,b.subject_id FROM base b WHERE NOT EXISTS(
 SELECT 1 FROM class_schedule_exceptions e WHERE e.class_id=$1 AND e.date=b.day AND e.lesson_number=b.lesson_number AND e.is_deleted=false)
 UNION ALL
 SELECT e.date,e.lesson_number,e.subject_id FROM class_schedule_exceptions e WHERE e.class_id=$1 AND e.is_deleted=false AND e.date BETWEEN $4::date AND $5::date
 ) SELECT e.day::text,e.lesson_number FROM effective e
 JOIN subjects s ON s.id=e.subject_id AND s.is_deleted=false
 JOIN classes cls ON cls.id=$1 AND cls.is_deleted=false
 WHERE e.subject_id=$2 AND (e.day,e.lesson_number) %s ($3::date,$6::integer)
 AND NOT EXISTS(SELECT 1 FROM school_holidays h WHERE h.is_deleted=false AND h.holiday_date=e.day AND (
 (COALESCE(cardinality(h.target_levels),0)=0 AND COALESCE(cardinality(h.target_classes),0)=0)
 OR cls.level=ANY(h.target_levels) OR cls.id=ANY(h.target_classes)))
 ORDER BY e.day %s,e.lesson_number %s LIMIT 1`, op, order, order)
	var nextDate string
	var nextLesson int
	err := conn.QueryRow(query, classID, subjectID, date.Format("2006-01-02"), start.Format("2006-01-02"), end.Format("2006-01-02"), lesson).Scan(&nextDate, &nextLesson)
	if err == sql.ErrNoRows {
		c.JSON(200, gin.H{"lesson": nil})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "Navbatdagi darsni topib bo‘lmadi"})
		return
	}
	c.JSON(200, gin.H{"lesson": gin.H{"date": nextDate, "lesson_number": nextLesson, "subject_id": subjectID}})
}
