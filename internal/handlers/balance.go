package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

type BalanceHandler struct{}

func NewBalanceHandler() *BalanceHandler {
	return &BalanceHandler{}
}

type CreateTransactionRequest struct {
	Amount      float64 `json:"amount"`
	PaidAmount  float64 `json:"paid_amount"`
	BonusAmount float64 `json:"bonus_amount"`
	Type        string  `json:"type" binding:"required"` // 'PAYMENT' or 'CHARGE'
	Description string  `json:"description"`
}

type CreateChargePlanRequest struct {
	Name           string  `json:"name" binding:"required"`
	Amount         float64 `json:"amount" binding:"required,gt=0"`
	StartDate      string  `json:"start_date" binding:"required"` // "YYYY-MM-DD"
	EndDate        string  `json:"end_date" binding:"required"`   // "YYYY-MM-DD"
	ChargeDay      int     `json:"charge_day" binding:"required,min=1,max=31"`
	Levels         []int   `json:"levels"`
	Classes        []int   `json:"classes"`
	Students       []int   `json:"students"`
	TargetLevels   []int   `json:"target_levels"`
	TargetClasses  []int   `json:"target_classes"`
	TargetStudents []int   `json:"target_students"`
}

// AddTransaction updates student balance and records the ledger transaction
func (h *BalanceHandler) AddTransaction(c *gin.Context) {
	studentIDStr := c.Param("id")
	studentID, err := strconv.Atoi(studentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student ID"})
		return
	}

	var req CreateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid fields", "details": err.Error()})
		return
	}

	if req.Type != "PAYMENT" && req.Type != "CHARGE" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Type must be either 'PAYMENT' or 'CHARGE'"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failure", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	// 1. Verify student exists and get current balance (Lookup by student.id first, then user_id fallback)
	var currentBalance float64
	var classID int
	err = tx.QueryRow("SELECT class_id, balance FROM students WHERE id = $1 AND is_deleted = false", studentID).Scan(&classID, &currentBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			// Fallback to user_id lookup in case user_id was passed
			var realStudentID int
			err = tx.QueryRow("SELECT id, class_id, balance FROM students WHERE user_id = $1 AND is_deleted = false", studentID).Scan(&realStudentID, &classID, &currentBalance)
			if err == nil {
				studentID = realStudentID
			}
		}
		if err != nil {
			if err == sql.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi yoki o'chirilgan"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify student details", "details": err.Error()})
			}
			return
		}
	}

	// 2. Calculate paid, bonus and total amount
	var paidAmount float64
	var bonusAmount float64
	var totalAmount float64

	if req.Type == "PAYMENT" {
		paidAmount = req.PaidAmount
		bonusAmount = req.BonusAmount

		if bonusAmount > 0 && paidAmount == 0 && req.Amount > bonusAmount {
			paidAmount = req.Amount - bonusAmount
		} else if paidAmount == 0 && bonusAmount == 0 && req.Amount > 0 {
			paidAmount = req.Amount
		}

		totalAmount = paidAmount + bonusAmount
		if req.Amount > totalAmount {
			totalAmount = req.Amount
		}

		if totalAmount <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "To'lov summasi musbat bo'lishi kerak"})
			return
		}
	} else {
		if req.Amount <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Yechim summasi musbat bo'lishi kerak"})
			return
		}
		totalAmount = req.Amount
	}

	newBalance := currentBalance
	signedAmount := totalAmount
	if req.Type == "PAYMENT" {
		newBalance += totalAmount
	} else {
		newBalance -= totalAmount
		signedAmount = -totalAmount
	}

	// 3. Insert transaction log
	var transactionID int
	insertTxQuery := `
		INSERT INTO payment_transactions (student_id, amount, paid_amount, bonus_amount, type, description)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`
	err = tx.QueryRow(insertTxQuery, studentID, signedAmount, paidAmount, bonusAmount, req.Type, req.Description).Scan(&transactionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record transaction log", "details": err.Error()})
		return
	}

	// 4. Update student balance
	_, err = tx.Exec("UPDATE students SET balance = $1 WHERE id = $2", newBalance, studentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update student balance", "details": err.Error()})
		return
	}

	// 5. Audit Log
	audit.LogChange(c, tx, audit.LogData{
		Action:    "BALANCE_TRANSACTION",
		TableName: "students",
		RecordID:  strconv.Itoa(studentID),
		OldValues: map[string]interface{}{"balance": currentBalance},
		NewValues: map[string]interface{}{"balance": newBalance, "transaction_id": transactionID, "amount": signedAmount, "paid_amount": paidAmount, "bonus_amount": bonusAmount, "type": req.Type},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":         "Balans muvaffaqiyatli yangilandi",
		"transaction_id":  transactionID,
		"student_id":      studentID,
		"old_balance":     currentBalance,
		"new_balance":     newBalance,
		"recorded_amount": signedAmount,
		"paid_amount":     paidAmount,
		"bonus_amount":    bonusAmount,
		"type":            req.Type,
	})
}

// GetTransactionHistory fetches payment/charge transactions for a student
func (h *BalanceHandler) GetTransactionHistory(c *gin.Context) {
	studentIDStr := c.Param("id")
	studentID, err := strconv.Atoi(studentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userRoleVal, _ := c.Get("role")
	userRole := userRoleVal.(string)
	userIDVal, _ := c.Get("userID")
	userIDStr := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// Fetch student details to get class_id for main teacher authorization check
	var classID int
	var studentUserID int
	err = dbConn.QueryRow("SELECT class_id, user_id FROM students WHERE id = $1 AND is_deleted = false", studentID).Scan(&classID, &studentUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error loading student details"})
		}
		return
	}

	authorized := false
	if userRole == "ADMIN" {
		authorized = true
	} else if userRole == "MAIN_TEACHER" {
		var isMain bool
		dbConn.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM class_teachers 
				WHERE class_id = $1 AND teacher_id = $2 AND is_main_teacher = true AND is_deleted = false
			)
		`, classID, currentUserID).Scan(&isMain)
		if isMain {
			authorized = true
		}
	} else if userRole == "STUDENT" {
		if studentUserID == currentUserID {
			authorized = true
		}
	} else if userRole == "PARENT" {
		var isLinked bool
		dbConn.QueryRow(`SELECT EXISTS(SELECT 1 FROM student_parents WHERE student_id = $1 AND parent_id = $2)`, studentID, currentUserID).Scan(&isLinked)
		if isLinked {
			authorized = true
		}
	}

	if !authorized {
		c.JSON(http.StatusForbidden, gin.H{"error": "Ruxsat berilmagan: ushbu o'quvchi balans tarixini ko'rishga huquqingiz yo'q"})
		return
	}

	rows, err := dbConn.Query(`
		SELECT id, student_id, amount,
		       CASE 
		         WHEN type = 'PAYMENT' AND COALESCE(bonus_amount, 0) > 0 THEN (amount - bonus_amount)
		         WHEN type = 'PAYMENT' AND paid_amount > 0 THEN paid_amount
		         WHEN type = 'PAYMENT' THEN amount 
		         ELSE 0 
		       END as paid_amount,
		       COALESCE(bonus_amount, 0) as bonus_amount,
		       type, COALESCE(description, '') as description, created_at 
		FROM payment_transactions 
		WHERE student_id = $1 
		ORDER BY created_at DESC`, studentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query transaction history", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []models.PaymentTransaction{}
	for rows.Next() {
		var pt models.PaymentTransaction
		var desc string
		err := rows.Scan(&pt.ID, &pt.StudentID, &pt.Amount, &pt.PaidAmount, &pt.BonusAmount, &pt.Type, &desc, &pt.CreatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan transaction record", "details": err.Error()})
			return
		}
		pt.Description = &desc
		list = append(list, pt)
	}

	c.JSON(http.StatusOK, list)
}

// ListAllTransactions retrieves all transactions globally (Admin Dashboard)
func (h *BalanceHandler) ListAllTransactions(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	rows, err := dbConn.Query(`
		SELECT pt.id, pt.student_id, pt.amount,
		       CASE 
		         WHEN pt.type = 'PAYMENT' AND COALESCE(pt.bonus_amount, 0) > 0 THEN (pt.amount - pt.bonus_amount)
		         WHEN pt.type = 'PAYMENT' AND pt.paid_amount > 0 THEN pt.paid_amount
		         WHEN pt.type = 'PAYMENT' THEN pt.amount 
		         ELSE 0 
		       END as paid_amount,
		       COALESCE(pt.bonus_amount, 0) as bonus_amount,
		       pt.type, COALESCE(pt.description, '') as description, pt.created_at,
		       u.first_name, u.last_name, c.name as class_name
		FROM payment_transactions pt
		JOIN students s ON pt.student_id = s.id
		JOIN users u ON s.user_id = u.id
		JOIN classes c ON s.class_id = c.id
		ORDER BY pt.created_at DESC LIMIT 500`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query transactions list", "details": err.Error()})
		return
	}
	defer rows.Close()

	type ExtendedTransaction struct {
		ID          int       `json:"id"`
		StudentID   int       `json:"student_id"`
		Amount      float64   `json:"amount"`
		PaidAmount  float64   `json:"paid_amount"`
		BonusAmount float64   `json:"bonus_amount"`
		Type        string    `json:"type"`
		Description *string   `json:"description,omitempty"`
		CreatedAt   time.Time `json:"created_at"`
		StudentName string    `json:"student_name"`
		ClassName   string    `json:"class_name"`
	}

	list := []ExtendedTransaction{}
	for rows.Next() {
		var pt ExtendedTransaction
		var desc string
		var fname, lname string
		err := rows.Scan(&pt.ID, &pt.StudentID, &pt.Amount, &pt.PaidAmount, &pt.BonusAmount, &pt.Type, &desc, &pt.CreatedAt, &fname, &lname, &pt.ClassName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan transaction details", "details": err.Error()})
			return
		}
		pt.Description = &desc
		pt.StudentName = fmt.Sprintf("%s %s", fname, lname)
		list = append(list, pt)
	}

	c.JSON(http.StatusOK, list)
}

// SaveChargePlan creates a yearly/monthly fee schedule
func (h *BalanceHandler) SaveChargePlan(c *gin.Context) {
	var req CreateChargePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid inputs", "details": err.Error()})
		return
	}

	startDate, err1 := time.Parse("2006-01-02", req.StartDate)
	endDate, err2 := time.Parse("2006-01-02", req.EndDate)
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format, must be YYYY-MM-DD"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	var planID int
	err = tx.QueryRow(`
		INSERT INTO charge_plans (name, amount, start_date, end_date, charge_day)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, req.Name, req.Amount, startDate, endDate, req.ChargeDay).Scan(&planID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to insert charge plan", "details": err.Error()})
		return
	}

	levels := req.Levels
	if len(levels) == 0 {
		levels = req.TargetLevels
	}
	classes := req.Classes
	if len(classes) == 0 {
		classes = req.TargetClasses
	}
	students := req.Students
	if len(students) == 0 {
		students = req.TargetStudents
	}

	for _, lvl := range levels {
		_, err = tx.Exec("INSERT INTO charge_plan_levels (charge_plan_id, level) VALUES ($1, $2)", planID, lvl)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to map level", "details": err.Error()})
			return
		}
	}

	for _, classID := range classes {
		_, err = tx.Exec("INSERT INTO charge_plan_classes (charge_plan_id, class_id) VALUES ($1, $2)", planID, classID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to map class", "details": err.Error()})
			return
		}
	}

	for _, studentID := range students {
		_, err = tx.Exec("INSERT INTO charge_plan_students (charge_plan_id, student_id) VALUES ($1, $2)", planID, studentID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to map student", "details": err.Error()})
			return
		}
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "charge_plans",
		RecordID:  strconv.Itoa(planID),
		NewValues: req,
	})

	err = tx.Commit()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Commit failure"})
		return
	}

	// Trigger execution sweep immediately in the background for this plan
	go h.processSinglePlanSweep(dbConn, planID)

	c.JSON(http.StatusCreated, gin.H{
		"message": "To'lov rejasi muvaffaqiyatli saqlandi",
		"id":      planID,
	})
}

// ListChargePlans returns all registered plans
func (h *BalanceHandler) ListChargePlans(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	rows, err := dbConn.Query("SELECT id, name, amount, start_date, end_date, charge_day, created_at FROM charge_plans ORDER BY created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch plans"})
		return
	}
	defer rows.Close()

	list := []models.ChargePlan{}
	for rows.Next() {
		var p models.ChargePlan
		err := rows.Scan(&p.ID, &p.Name, &p.Amount, &p.StartDate, &p.EndDate, &p.ChargeDay, &p.CreatedAt)
		if err != nil {
			continue
		}

		// Fetch levels
		lvlRows, _ := dbConn.Query("SELECT level FROM charge_plan_levels WHERE charge_plan_id = $1", p.ID)
		p.Levels = []int{}
		for lvlRows.Next() {
			var l int
			if err := lvlRows.Scan(&l); err == nil {
				p.Levels = append(p.Levels, l)
			}
		}
		lvlRows.Close()

		// Fetch classes
		clsRows, _ := dbConn.Query("SELECT class_id FROM charge_plan_classes WHERE charge_plan_id = $1", p.ID)
		p.Classes = []int{}
		for clsRows.Next() {
			var cl int
			if err := clsRows.Scan(&cl); err == nil {
				p.Classes = append(p.Classes, cl)
			}
		}
		clsRows.Close()

		// Fetch students
		stdRows, _ := dbConn.Query("SELECT student_id FROM charge_plan_students WHERE charge_plan_id = $1", p.ID)
		p.Students = []int{}
		for stdRows.Next() {
			var s int
			if err := stdRows.Scan(&s); err == nil {
				p.Students = append(p.Students, s)
			}
		}
		stdRows.Close()

		list = append(list, p)
	}

	c.JSON(http.StatusOK, list)
}

// DeleteChargePlan removes plan from registry
func (h *BalanceHandler) DeleteChargePlan(c *gin.Context) {
	planIDStr := c.Param("id")
	planID, err := strconv.Atoi(planIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid plan ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	var p models.ChargePlan
	err = dbConn.QueryRow("SELECT id, name FROM charge_plans WHERE id = $1", planID).Scan(&p.ID, &p.Name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plan not found"})
		return
	}

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction start failure"})
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM charge_plans WHERE id = $1", planID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete plan"})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "charge_plans",
		RecordID:  strconv.Itoa(planID),
		OldValues: p,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit deletion"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Plan deleted successfully"})
}

// UpdateChargePlan updates an existing charge plan and records its audit history snapshot
func (h *BalanceHandler) UpdateChargePlan(c *gin.Context) {
	planIDStr := c.Param("id")
	planID, err := strconv.Atoi(planIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid plan ID"})
		return
	}

	var req CreateChargePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid inputs", "details": err.Error()})
		return
	}

	_, err1 := time.Parse("2006-01-02", req.StartDate)
	endDate, err2 := time.Parse("2006-01-02", req.EndDate)
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format, must be YYYY-MM-DD"})
		return
	}

	// Validate minimum end date: End date cannot be before today
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if endDate.Before(today) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Tugash sanasi bugungi sanadan oldin bo'lishi mumkin emas"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	userIDVal, _ := c.Get("userID")
	userIDStr, _ := userIDVal.(string)
	currentUserID, _ := strconv.Atoi(userIDStr)

	// Fetch current user name for history log
	var editorName string
	_ = dbConn.QueryRow("SELECT CONCAT(first_name, ' ', last_name) FROM users WHERE id = $1", currentUserID).Scan(&editorName)
	if strings.TrimSpace(editorName) == "" {
		editorName = "Administrator"
	}

	// Fetch old plan state before edit
	var oldPlan models.ChargePlan
	err = dbConn.QueryRow("SELECT id, name, amount, start_date, end_date, charge_day FROM charge_plans WHERE id = $1", planID).
		Scan(&oldPlan.ID, &oldPlan.Name, &oldPlan.Amount, &oldPlan.StartDate, &oldPlan.EndDate, &oldPlan.ChargeDay)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Plan not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database query error", "details": err.Error()})
		}
		return
	}

	// Fetch old levels, classes, students
	lvlRows, _ := dbConn.Query("SELECT level FROM charge_plan_levels WHERE charge_plan_id = $1", planID)
	oldLevels := []int{}
	for lvlRows.Next() {
		var l int
		if err := lvlRows.Scan(&l); err == nil {
			oldLevels = append(oldLevels, l)
		}
	}
	lvlRows.Close()

	clsRows, _ := dbConn.Query("SELECT class_id FROM charge_plan_classes WHERE charge_plan_id = $1", planID)
	oldClasses := []int{}
	for clsRows.Next() {
		var cl int
		if err := clsRows.Scan(&cl); err == nil {
			oldClasses = append(oldClasses, cl)
		}
	}
	clsRows.Close()

	stdRows, _ := dbConn.Query("SELECT student_id FROM charge_plan_students WHERE charge_plan_id = $1", planID)
	oldStudents := []int{}
	for stdRows.Next() {
		var st int
		if err := stdRows.Scan(&st); err == nil {
			oldStudents = append(oldStudents, st)
		}
	}
	stdRows.Close()

	oldStateMap := map[string]interface{}{
		"name":       oldPlan.Name,
		"amount":     oldPlan.Amount,
		"start_date": oldPlan.StartDate.Format("2006-01-02"),
		"end_date":   oldPlan.EndDate.Format("2006-01-02"),
		"charge_day": oldPlan.ChargeDay,
		"levels":     oldLevels,
		"classes":    oldClasses,
		"students":   oldStudents,
	}

	// Enforce immutable fields: Amount and StartDate cannot be modified during edit
	finalAmount := oldPlan.Amount
	finalStartDate := oldPlan.StartDate

	newStateMap := map[string]interface{}{
		"name":       req.Name,
		"amount":     finalAmount,
		"start_date": finalStartDate.Format("2006-01-02"),
		"end_date":   endDate.Format("2006-01-02"),
		"charge_day": req.ChargeDay,
		"levels":     req.Levels,
		"classes":    req.Classes,
		"students":   req.Students,
	}

	oldJSON, _ := json.Marshal(oldStateMap)
	newJSON, _ := json.Marshal(newStateMap)

	tx, err := dbConn.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	// Update main plan (preserving amount and start_date)
	_, err = tx.Exec(`
		UPDATE charge_plans 
		SET name = $1, amount = $2, start_date = $3, end_date = $4, charge_day = $5, updated_at = NOW()
		WHERE id = $6`, req.Name, finalAmount, finalStartDate, endDate, req.ChargeDay, planID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update charge plan", "details": err.Error()})
		return
	}

	// Delete and re-map levels, classes, students
	_, _ = tx.Exec("DELETE FROM charge_plan_levels WHERE charge_plan_id = $1", planID)
	_, _ = tx.Exec("DELETE FROM charge_plan_classes WHERE charge_plan_id = $1", planID)
	_, _ = tx.Exec("DELETE FROM charge_plan_students WHERE charge_plan_id = $1", planID)

	updateLevels := req.Levels
	if len(updateLevels) == 0 {
		updateLevels = req.TargetLevels
	}
	updateClasses := req.Classes
	if len(updateClasses) == 0 {
		updateClasses = req.TargetClasses
	}
	updateStudents := req.Students
	if len(updateStudents) == 0 {
		updateStudents = req.TargetStudents
	}

	for _, lvl := range updateLevels {
		_, _ = tx.Exec("INSERT INTO charge_plan_levels (charge_plan_id, level) VALUES ($1, $2)", planID, lvl)
	}
	for _, classID := range updateClasses {
		_, _ = tx.Exec("INSERT INTO charge_plan_classes (charge_plan_id, class_id) VALUES ($1, $2)", planID, classID)
	}
	for _, studentID := range updateStudents {
		_, _ = tx.Exec("INSERT INTO charge_plan_students (charge_plan_id, student_id) VALUES ($1, $2)", planID, studentID)
	}

	// Record audit history snapshot
	summary := fmt.Sprintf("Reja tahrirlandi: %s tomonidan", editorName)
	_, err = tx.Exec(`
		INSERT INTO charge_plan_history (charge_plan_id, edited_by_user_id, edited_by_user_name, old_state, new_state, change_summary)
		VALUES ($1, $2, $3, $4, $5, $6)`, planID, currentUserID, editorName, oldJSON, newJSON, summary)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record plan history log", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "charge_plans",
		RecordID:  strconv.Itoa(planID),
		OldValues: oldStateMap,
		NewValues: newStateMap,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit changes"})
		return
	}

	// Trigger execution sweep immediately for this updated plan
	go h.processSinglePlanSweep(dbConn, planID)

	c.JSON(http.StatusOK, gin.H{"message": "To'lov rejasi muvaffaqiyatli yangilandi"})
}

// GetChargePlanHistory returns the audit history of edits for a plan
func (h *BalanceHandler) GetChargePlanHistory(c *gin.Context) {
	planIDStr := c.Param("id")
	planID, err := strconv.Atoi(planIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid plan ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	rows, err := dbConn.Query(`
		SELECT id, charge_plan_id, edited_by_user_id, COALESCE(edited_by_user_name, 'Administrator') as edited_by_user_name,
		       edited_at, old_state, new_state, COALESCE(change_summary, '') as change_summary
		FROM charge_plan_history
		WHERE charge_plan_id = $1
		ORDER BY edited_at DESC`, planID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query history", "details": err.Error()})
		return
	}
	defer rows.Close()

	list := []models.ChargePlanHistory{}
	for rows.Next() {
		var item models.ChargePlanHistory
		err := rows.Scan(&item.ID, &item.ChargePlanID, &item.EditedByUserID, &item.EditedUserName, &item.EditedAt, &item.OldState, &item.NewState, &item.ChangeSummary)
		if err != nil {
			continue
		}
		list = append(list, item)
	}

	c.JSON(http.StatusOK, list)
}

// TriggerChargesManual executes plans sweep manually
func (h *BalanceHandler) TriggerChargesManual(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	count := h.RunSchedulerSweep(dbConn)
	c.JSON(http.StatusOK, gin.H{
		"message":          "To'lovlar rejasi muvaffaqiyatli yakunlandi",
		"processed_charge_count": count,
	})
}

// RunSchedulerSweep iterates all registered plans to apply charges
func (h *BalanceHandler) RunSchedulerSweep(dbConn *sql.DB) int {
	rows, err := dbConn.Query("SELECT id, name, amount, start_date, end_date, charge_day FROM charge_plans")
	if err != nil {
		return 0
	}
	defer rows.Close()

	totalCharged := 0
	for rows.Next() {
		var p models.ChargePlan
		err := rows.Scan(&p.ID, &p.Name, &p.Amount, &p.StartDate, &p.EndDate, &p.ChargeDay)
		if err != nil {
			continue
		}
		totalCharged += h.executePlanSweep(dbConn, p)
	}
	return totalCharged
}

func (h *BalanceHandler) processSinglePlanSweep(dbConn *sql.DB, planID int) {
	var p models.ChargePlan
	err := dbConn.QueryRow("SELECT id, name, amount, start_date, end_date, charge_day FROM charge_plans WHERE id = $1", planID).
		Scan(&p.ID, &p.Name, &p.Amount, &p.StartDate, &p.EndDate, &p.ChargeDay)
	if err == nil {
		h.executePlanSweep(dbConn, p)
	}
}

func (h *BalanceHandler) executePlanSweep(dbConn *sql.DB, plan models.ChargePlan) int {
	// Fetch levels
	lvlRows, _ := dbConn.Query("SELECT level FROM charge_plan_levels WHERE charge_plan_id = $1", plan.ID)
	var levels []int
	for lvlRows.Next() {
		var l int
		if err := lvlRows.Scan(&l); err == nil {
			levels = append(levels, l)
		}
	}
	lvlRows.Close()

	// Fetch classes
	clsRows, _ := dbConn.Query("SELECT class_id FROM charge_plan_classes WHERE charge_plan_id = $1", plan.ID)
	var classes []int
	for clsRows.Next() {
		var cl int
		if err := clsRows.Scan(&cl); err == nil {
			classes = append(classes, cl)
		}
	}
	clsRows.Close()

	// Fetch students
	stdRows, _ := dbConn.Query("SELECT student_id FROM charge_plan_students WHERE charge_plan_id = $1", plan.ID)
	var directStudents []int
	for stdRows.Next() {
		var s int
		if err := stdRows.Scan(&s); err == nil {
			directStudents = append(directStudents, s)
		}
	}
	stdRows.Close()

	studentIDs := make(map[int]bool)
	for _, sid := range directStudents {
		studentIDs[sid] = true
	}

	// Fetch level matches
	if len(levels) > 0 {
		qMarks := make([]string, len(levels))
		args := make([]interface{}, len(levels))
		for i, l := range levels {
			qMarks[i] = fmt.Sprintf("$%d", i+1)
			args[i] = l
		}
		query := fmt.Sprintf(`
			SELECT s.id 
			FROM students s 
			JOIN classes c ON s.class_id = c.id 
			WHERE s.is_deleted = false AND c.level IN (%s)`, strings.Join(qMarks, ","))
		rows, err := dbConn.Query(query, args...)
		if err == nil {
			for rows.Next() {
				var sid int
				if err := rows.Scan(&sid); err == nil {
					studentIDs[sid] = true
				}
			}
			rows.Close()
		}
	}

	// Fetch class matches
	if len(classes) > 0 {
		qMarks := make([]string, len(classes))
		args := make([]interface{}, len(classes))
		for i, c := range classes {
			qMarks[i] = fmt.Sprintf("$%d", i+1)
			args[i] = c
		}
		query := fmt.Sprintf(`
			SELECT id 
			FROM students 
			WHERE is_deleted = false AND class_id IN (%s)`, strings.Join(qMarks, ","))
		rows, err := dbConn.Query(query, args...)
		if err == nil {
			for rows.Next() {
				var sid int
				if err := rows.Scan(&sid); err == nil {
					studentIDs[sid] = true
				}
			}
			rows.Close()
		}
	}

	chargedCount := 0
	now := time.Now()

	for sid := range studentIDs {
		// Traverse billing months from plan.StartDate to current month
		currentMonth := time.Date(plan.StartDate.Year(), plan.StartDate.Month(), 1, 0, 0, 0, 0, time.UTC)
		limitMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		if plan.EndDate.Before(now) {
			limitMonth = time.Date(plan.EndDate.Year(), plan.EndDate.Month(), 1, 0, 0, 0, 0, time.UTC)
		}

		for !currentMonth.After(limitMonth) {
			chargeDay := plan.ChargeDay
			lastDay := lastDayOfMonth(currentMonth.Year(), currentMonth.Month())
			if chargeDay > lastDay {
				chargeDay = lastDay
			}

			scheduledChargeDate := time.Date(currentMonth.Year(), currentMonth.Month(), chargeDay, 0, 0, 0, 0, time.UTC)

			// Only process if the scheduled charge day is reached, and fits within plan boundaries
			if !scheduledChargeDate.After(now) && !scheduledChargeDate.Before(plan.StartDate) && !scheduledChargeDate.After(plan.EndDate) {
				var exists bool
				err := dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM charge_logs WHERE charge_plan_id = $1 AND student_id = $2 AND billing_month = $3)", plan.ID, sid, currentMonth).Scan(&exists)
				if err == nil && !exists {
					if err := h.applyChargeToStudent(dbConn, plan, sid, currentMonth); err == nil {
						chargedCount++
					}
				}
			}
			currentMonth = currentMonth.AddDate(0, 1, 0)
		}
	}

	return chargedCount
}

func (h *BalanceHandler) applyChargeToStudent(dbConn *sql.DB, plan models.ChargePlan, studentID int, billingMonth time.Time) error {
	tx, err := dbConn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var currentBalance float64
	err = tx.QueryRow("SELECT balance FROM students WHERE id = $1 AND is_deleted = false FOR UPDATE", studentID).Scan(&currentBalance)
	if err != nil {
		return err
	}

	newBalance := currentBalance - plan.Amount
	description := fmt.Sprintf("%s - %s uchun to'lov", plan.Name, billingMonth.Format("2006-01"))

	var txID int
	err = tx.QueryRow(`
		INSERT INTO payment_transactions (student_id, amount, type, description)
		VALUES ($1, $2, 'CHARGE', $3)
		RETURNING id`, studentID, -plan.Amount, description).Scan(&txID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO charge_logs (charge_plan_id, student_id, billing_month, transaction_id)
		VALUES ($1, $2, $3, $4)`, plan.ID, studentID, billingMonth, txID)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE students SET balance = $1 WHERE id = $2", newBalance, studentID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetNextCharge determines upcoming charge estimation within active plans
func (h *BalanceHandler) GetNextCharge(c *gin.Context) {
	studentIDStr := c.Param("id")
	studentID, err := strconv.Atoi(studentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student ID"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	var classID int
	var classLevel int
	err = dbConn.QueryRow(`
		SELECT s.class_id, COALESCE(c.level, 1) 
		FROM students s 
		LEFT JOIN classes c ON s.class_id = c.id 
		WHERE s.id = $1 AND s.is_deleted = false`, studentID).Scan(&classID, &classLevel)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query class details"})
		}
		return
	}

	rows, err := dbConn.Query("SELECT id, name, amount, start_date, end_date, charge_day FROM charge_plans")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query charge plans"})
		return
	}
	defer rows.Close()

	type CandidateCharge struct {
		Date   time.Time `json:"date"`
		Amount float64   `json:"amount"`
		Name   string    `json:"name"`
	}
	var nextCharges []CandidateCharge

	now := time.Now()

	for rows.Next() {
		var p models.ChargePlan
		err := rows.Scan(&p.ID, &p.Name, &p.Amount, &p.StartDate, &p.EndDate, &p.ChargeDay)
		if err != nil {
			continue
		}

		var hasLevel bool
		dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM charge_plan_levels WHERE charge_plan_id = $1 AND level = $2)", p.ID, classLevel).Scan(&hasLevel)
		var hasClass bool
		dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM charge_plan_classes WHERE charge_plan_id = $1 AND class_id = $2)", p.ID, classID).Scan(&hasClass)
		var hasStudent bool
		dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM charge_plan_students WHERE charge_plan_id = $1 AND student_id = $2)", p.ID, studentID).Scan(&hasStudent)

		if !hasLevel && !hasClass && !hasStudent {
			continue
		}

		currentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		limitMonth := time.Date(p.EndDate.Year(), p.EndDate.Month(), 1, 0, 0, 0, 0, time.UTC)

		for !currentMonth.After(limitMonth) {
			chargeDay := p.ChargeDay
			lastDay := lastDayOfMonth(currentMonth.Year(), currentMonth.Month())
			if chargeDay > lastDay {
				chargeDay = lastDay
			}
			scheduledDate := time.Date(currentMonth.Year(), currentMonth.Month(), chargeDay, 0, 0, 0, 0, time.UTC)

			if !scheduledDate.Before(p.StartDate) && !scheduledDate.After(p.EndDate) {
				var alreadyCharged bool
				dbConn.QueryRow("SELECT EXISTS(SELECT 1 FROM charge_logs WHERE charge_plan_id = $1 AND student_id = $2 AND billing_month = $3)", p.ID, studentID, currentMonth).Scan(&alreadyCharged)

				if !alreadyCharged && scheduledDate.After(now) {
					nextCharges = append(nextCharges, CandidateCharge{
						Date:   scheduledDate,
						Amount: p.Amount,
						Name:   p.Name,
					})
					break
				}
			}
			currentMonth = currentMonth.AddDate(0, 1, 0)
		}
	}

	if len(nextCharges) == 0 {
		c.JSON(http.StatusOK, gin.H{"next_charge": nil})
		return
	}

	earliest := nextCharges[0]
	for _, nc := range nextCharges {
		if nc.Date.Before(earliest.Date) {
			earliest = nc
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"next_charge": gin.H{
			"date":   earliest.Date.Format("2006-01-02"),
			"amount": earliest.Amount,
			"name":   earliest.Name,
		},
	})
}

// ImportPayments handles spreadsheet uploads to record bulk student payments
func (h *BalanceHandler) ImportPayments(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Excel fayl talab etiladi"})
		return
	}

	openedFile, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Faylni o'qib bo'lmadi"})
		return
	}
	defer openedFile.Close()

	f, err := excelize.OpenReader(openedFile)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Noto'g'ri excel formati"})
		return
	}
	defer f.Close()

	sheetName := f.GetSheetName(0)
	rows, err := f.GetRows(sheetName)
	if err != nil || len(rows) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Fayl bo'sh yoki yetarli qatorlar mavjud emas"})
		return
	}

	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	headers := rows[0]
	colIndices := map[string]int{
		"o'quvchi id": -1,
		"telefon":     -1,
		"summa":       -1,
		"description": -1,
	}

	for i, hCell := range headers {
		cleanHeader := strings.ToLower(strings.TrimSpace(hCell))
		if cleanHeader == "id" || cleanHeader == "o'quvchi id" || cleanHeader == "student id" || cleanHeader == "uid" {
			colIndices["o'quvchi id"] = i
		} else if cleanHeader == "telefon" || cleanHeader == "phone" || cleanHeader == "nomer" {
			colIndices["telefon"] = i
		} else if cleanHeader == "summa" || cleanHeader == "amount" || cleanHeader == "to'lov" || cleanHeader == "tolov" {
			colIndices["summa"] = i
		} else if cleanHeader == "izoh" || cleanHeader == "description" || cleanHeader == "comment" {
			colIndices["description"] = i
		}
	}

	if colIndices["summa"] == -1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Faylda majburiy 'summa' ustuni topilmadi"})
		return
	}

	successCount := 0
	failedCount := 0
	type RowError struct {
		Row   int    `json:"row"`
		Error string `json:"error"`
	}
	var rowErrors []RowError

	for rowIdx := 1; rowIdx < len(rows); rowIdx++ {
		row := rows[rowIdx]
		if len(row) == 0 {
			continue
		}

		getCell := func(key string) string {
			idx := colIndices[key]
			if idx >= 0 && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
			return ""
		}

		idStr := getCell("o'quvchi id")
		phoneStr := getCell("telefon")
		summaStr := getCell("summa")
		descStr := getCell("description")

		if summaStr == "" {
			failedCount++
			rowErrors = append(rowErrors, RowError{Row: rowIdx + 1, Error: "Summa belgilanmagan"})
			continue
		}

		amount, err := strconv.ParseFloat(summaStr, 64)
		if err != nil || amount <= 0 {
			failedCount++
			rowErrors = append(rowErrors, RowError{Row: rowIdx + 1, Error: "Summa musbat son bo'lishi shart"})
			continue
		}

		var studentID int
		var currentBalance float64
		found := false

		if idStr != "" {
			idVal, err := strconv.Atoi(idStr)
			if err == nil {
				err = dbConn.QueryRow("SELECT id, balance FROM students WHERE id = $1 AND is_deleted = false", idVal).Scan(&studentID, &currentBalance)
				if err == nil {
					found = true
				}
			}
		}

		if !found && phoneStr != "" {
			err = dbConn.QueryRow(`
				SELECT s.id, s.balance 
				FROM students s 
				JOIN users u ON s.user_id = u.id 
				WHERE u.phone = $1 AND s.is_deleted = false`, phoneStr).Scan(&studentID, &currentBalance)
			if err == nil {
				found = true
			}

			if !found {
				err = dbConn.QueryRow(`
					SELECT s.id, s.balance 
					FROM students s 
					JOIN student_parents sp ON sp.student_id = s.id 
					JOIN users u ON sp.parent_id = u.id 
					WHERE u.phone = $1 AND s.is_deleted = false LIMIT 1`, phoneStr).Scan(&studentID, &currentBalance)
				if err == nil {
					found = true
				}
			}
		}

		if !found {
			failedCount++
			rowErrors = append(rowErrors, RowError{Row: rowIdx + 1, Error: "O'quvchi topilmadi (ID yoki Telefon noto'g'ri)"})
			continue
		}

		tx, err := dbConn.Begin()
		if err != nil {
			failedCount++
			rowErrors = append(rowErrors, RowError{Row: rowIdx + 1, Error: "Tranzaksiyani boshlashda xatolik"})
			continue
		}

		newBalance := currentBalance + amount
		if descStr == "" {
			descStr = "Excel import to'lovi"
		}

		var transactionID int
		err = tx.QueryRow(`
			INSERT INTO payment_transactions (student_id, amount, paid_amount, bonus_amount, type, description)
			VALUES ($1, $2, $2, 0.00, 'PAYMENT', $3)
			RETURNING id`, studentID, amount, descStr).Scan(&transactionID)
		if err != nil {
			tx.Rollback()
			failedCount++
			rowErrors = append(rowErrors, RowError{Row: rowIdx + 1, Error: "To'lovni yaratishda xatolik"})
			continue
		}

		_, err = tx.Exec("UPDATE students SET balance = $1 WHERE id = $2", newBalance, studentID)
		if err != nil {
			tx.Rollback()
			failedCount++
			rowErrors = append(rowErrors, RowError{Row: rowIdx + 1, Error: "Balansni yangilashda xatolik"})
			continue
		}

		audit.LogChange(c, tx, audit.LogData{
			Action:    "BALANCE_TRANSACTION",
			TableName: "students",
			RecordID:  strconv.Itoa(studentID),
			OldValues: map[string]interface{}{"balance": currentBalance},
			NewValues: map[string]interface{}{"balance": newBalance, "transaction_id": transactionID, "amount": amount, "type": "PAYMENT"},
		})

		err = tx.Commit()
		if err != nil {
			failedCount++
			rowErrors = append(rowErrors, RowError{Row: rowIdx + 1, Error: "Commit xatoligi"})
			continue
		}

		successCount++
	}

	c.JSON(http.StatusOK, gin.H{
		"success":        failedCount == 0,
		"imported_count": successCount,
		"failed_count":   failedCount,
		"errors":         rowErrors,
	})
}

// ExportPaymentTemplate generates an Excel template for bulk payment import
func (h *BalanceHandler) ExportPaymentTemplate(c *gin.Context) {
	f := excelize.NewFile()
	defer f.Close()

	sheetName := "Sheet1"
	f.SetSheetName("Sheet1", sheetName)

	headers := []string{"O'quvchi ID", "Telefon", "Summa", "Izoh"}
	for colIdx, text := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
		f.SetCellValue(sheetName, cell, text)
	}

	// Add sample rows for guidance
	f.SetCellValue(sheetName, "A2", "1")
	f.SetCellValue(sheetName, "B2", "+998901234567")
	f.SetCellValue(sheetName, "C2", "500000")
	f.SetCellValue(sheetName, "D2", "Iyul oyi ta'lim to'lovi")

	f.SetCellValue(sheetName, "A3", "2")
	f.SetCellValue(sheetName, "B3", "+998919876543")
	f.SetCellValue(sheetName, "C3", "650000")
	f.SetCellValue(sheetName, "D3", "Kassa orqali naqd to'lov")

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=tolovlar_import_shablon.xlsx")
	c.Header("File-Name", "tolovlar_import_shablon.xlsx")

	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Faylni yaratishda xatolik"})
	}
}

func lastDayOfMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
