package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type ReportHandler struct{}

func NewReportHandler() *ReportHandler {
	return &ReportHandler{}
}

func (h *ReportHandler) ExportSocialPassport(c *gin.Context) {
	tenantDB, exists := c.Get("tenantDB")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database connection not found"})
		return
	}
	db := tenantDB.(*sql.DB)

	classIDStr := c.Query("class_id")
	if classIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "class_id is required"})
		return
	}

	classID, err := strconv.Atoi(classIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid class_id"})
		return
	}

	var className string
	err = db.QueryRow("SELECT name FROM classes WHERE id = $1", classID).Scan(&className)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Class not found"})
		return
	}

	query := `
		SELECT 
			s.id AS student_id,
			u.first_name, u.last_name, u.middle_name,
			s.ina, s.address, s.birthdate, s.enrollment_date,
			p.id AS parent_id,
			pu.first_name AS p_first, pu.last_name AS p_last, pu.middle_name AS p_middle,
			pu.passport AS p_passport, pu.phone AS p_phone,
			sp.is_primary, sp.relation_type
		FROM students s
		JOIN users u ON s.user_id = u.id
		LEFT JOIN student_parents sp ON s.id = sp.student_id
		LEFT JOIN parents p ON sp.parent_id = p.id
		LEFT JOIN users pu ON p.user_id = pu.id
		WHERE s.class_id = $1 AND s.is_deleted = false AND u.is_deleted = false
		ORDER BY u.last_name, u.first_name, s.id
	`
	rows, err := db.Query(query, classID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query students"})
		return
	}
	defer rows.Close()

	type ParentInfo struct {
		FirstName  string
		LastName   string
		MiddleName string
		Passport   string
		Phone      string
		Relation   string
	}

	type StudentInfo struct {
		ID             int
		FirstName      string
		LastName       string
		MiddleName     string
		INA            string
		Address        string
		Birthdate      string
		EnrollmentDate string
		Parents        []ParentInfo
	}

	studentMap := make(map[int]*StudentInfo)
	var studentOrder []int

	for rows.Next() {
		var sID int
		var sFirst, sLast string
		var sMiddle, sINA, sAddress, sBirthdate, sEnrollmentDate *string
		var pID *int
		var pFirst, pLast, pMiddle, pPassport, pPhone, pRelation *string
		var pIsPrimary *bool

		err := rows.Scan(
			&sID, &sFirst, &sLast, &sMiddle,
			&sINA, &sAddress, &sBirthdate, &sEnrollmentDate,
			&pID, &pFirst, &pLast, &pMiddle,
			&pPassport, &pPhone, &pIsPrimary, &pRelation,
		)
		if err != nil {
			continue
		}

		if _, ok := studentMap[sID]; !ok {
			studentMap[sID] = &StudentInfo{
				ID:             sID,
				FirstName:      sFirst,
				LastName:       sLast,
				MiddleName:     safeString(sMiddle),
				INA:            safeString(sINA),
				Address:        safeString(sAddress),
				Birthdate:      safeString(sBirthdate),
				EnrollmentDate: safeString(sEnrollmentDate),
			}
			studentOrder = append(studentOrder, sID)
		}

		if pID != nil {
			studentMap[sID].Parents = append(studentMap[sID].Parents, ParentInfo{
				FirstName:  safeString(pFirst),
				LastName:   safeString(pLast),
				MiddleName: safeString(pMiddle),
				Passport:   safeString(pPassport),
				Phone:      safeString(pPhone),
				Relation:   safeString(pRelation),
			})
		}
	}

	f := excelize.NewFile()
	sheet := "Ijtimoiy pasport"
	f.SetSheetName("Sheet1", sheet)

	f.MergeCell(sheet, "A1", "O1")
	f.SetCellValue(sheet, "A1", fmt.Sprintf("%s sinf o'quvchilarining ijtimoiy pasporti", className))
	
	styleTitle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 14},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	f.SetCellStyle(sheet, "A1", "O1", styleTitle)

	headers := []string{
		"T/r", "Sinf", "Familiyasi", "Ismi", "Sharifi", "Tug'ilgan sana",
		"Metrika / Pasport", "Yashash manzili", "Otasi F.I.Sh", "Otasi Pasport", "Otasi Tel",
		"Onasi F.I.Sh", "Onasi Pasport", "Onasi Tel", "Maktabga kirish sanasi",
	}

	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		f.SetCellValue(sheet, cell, h)
	}

	styleHeader, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#E0E0E0"}, Pattern: 1},
		Border:    []excelize.Border{{Type: "left", Color: "000000", Style: 1}, {Type: "top", Color: "000000", Style: 1}, {Type: "bottom", Color: "000000", Style: 1}, {Type: "right", Color: "000000", Style: 1}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	f.SetCellStyle(sheet, "A2", "O2", styleHeader)

	f.SetColWidth(sheet, "A", "A", 5)
	f.SetColWidth(sheet, "B", "B", 10)
	f.SetColWidth(sheet, "C", "D", 20)
	f.SetColWidth(sheet, "E", "G", 15)
	f.SetColWidth(sheet, "H", "H", 30)
	f.SetColWidth(sheet, "I", "I", 25)
	f.SetColWidth(sheet, "J", "K", 15)
	f.SetColWidth(sheet, "L", "L", 25)
	f.SetColWidth(sheet, "M", "O", 15)

	rowIdx := 3
	styleBorder, _ := f.NewStyle(&excelize.Style{
		Border: []excelize.Border{{Type: "left", Color: "000000", Style: 1}, {Type: "top", Color: "000000", Style: 1}, {Type: "bottom", Color: "000000", Style: 1}, {Type: "right", Color: "000000", Style: 1}},
	})

	for i, sID := range studentOrder {
		student := studentMap[sID]
		f.SetCellValue(sheet, fmt.Sprintf("A%d", rowIdx), i+1)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", rowIdx), className)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", rowIdx), student.LastName)
		f.SetCellValue(sheet, fmt.Sprintf("D%d", rowIdx), student.FirstName)
		f.SetCellValue(sheet, fmt.Sprintf("E%d", rowIdx), student.MiddleName)
		
		bd := student.Birthdate
		if len(bd) >= 10 {
			bd = bd[:10]
		}
		f.SetCellValue(sheet, fmt.Sprintf("F%d", rowIdx), bd)
		f.SetCellValue(sheet, fmt.Sprintf("G%d", rowIdx), student.INA)
		f.SetCellValue(sheet, fmt.Sprintf("H%d", rowIdx), student.Address)

		var father, mother ParentInfo
		for _, p := range student.Parents {
			rel := strings.ToLower(p.Relation)
			if strings.Contains(rel, "ota") || strings.Contains(rel, "father") {
				father = p
			} else if strings.Contains(rel, "ona") || strings.Contains(rel, "mother") {
				mother = p
			} else {
				if father.FirstName == "" {
					father = p
				}
			}
		}
		
		fatherName := ""
		if father.FirstName != "" {
			fatherName = fmt.Sprintf("%s %s %s", father.LastName, father.FirstName, father.MiddleName)
		}
		motherName := ""
		if mother.FirstName != "" {
			motherName = fmt.Sprintf("%s %s %s", mother.LastName, mother.FirstName, mother.MiddleName)
		}

		f.SetCellValue(sheet, fmt.Sprintf("I%d", rowIdx), strings.TrimSpace(fatherName))
		f.SetCellValue(sheet, fmt.Sprintf("J%d", rowIdx), father.Passport)
		f.SetCellValue(sheet, fmt.Sprintf("K%d", rowIdx), father.Phone)
		
		f.SetCellValue(sheet, fmt.Sprintf("L%d", rowIdx), strings.TrimSpace(motherName))
		f.SetCellValue(sheet, fmt.Sprintf("M%d", rowIdx), mother.Passport)
		f.SetCellValue(sheet, fmt.Sprintf("N%d", rowIdx), mother.Phone)

		ed := student.EnrollmentDate
		if len(ed) >= 10 {
			ed = ed[:10]
		}
		f.SetCellValue(sheet, fmt.Sprintf("O%d", rowIdx), ed)

		f.SetCellStyle(sheet, fmt.Sprintf("A%d", rowIdx), fmt.Sprintf("O%d", rowIdx), styleBorder)
		rowIdx++
	}

	fileName := fmt.Sprintf("ijtimoiy_pasport_%s.xlsx", className)
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileName))

	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write file"})
	}
}
