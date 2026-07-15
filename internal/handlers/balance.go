package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
)

type BalanceHandler struct{}

func NewBalanceHandler() *BalanceHandler {
	return &BalanceHandler{}
}

type CreateTransactionRequest struct {
	Amount      float64 `json:"amount" binding:"required,gt=0"`
	Type        string  `json:"type" binding:"required"` // 'PAYMENT' or 'CHARGE'
	Description string  `json:"description"`
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

	// 1. Verify student exists and get current balance
	var currentBalance float64
	var classID int
	err = tx.QueryRow("SELECT class_id, balance FROM students WHERE id = $1 AND is_deleted = false", studentID).Scan(&classID, &currentBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "O'quvchi topilmadi yoki o'chirilgan"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify student details", "details": err.Error()})
		}
		return
	}

	// 2. Calculate new balance
	// PAYMENT: Parent pays school → balance increases (credit increases)
	// CHARGE: School bills parent → balance decreases (debt increases)
	newBalance := currentBalance
	signedAmount := req.Amount
	if req.Type == "PAYMENT" {
		newBalance += req.Amount
	} else {
		newBalance -= req.Amount
		signedAmount = -req.Amount
	}

	// 3. Insert transaction log
	var transactionID int
	insertTxQuery := `
		INSERT INTO payment_transactions (student_id, amount, type, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id`
	err = tx.QueryRow(insertTxQuery, studentID, signedAmount, req.Type, req.Description).Scan(&transactionID)
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
		NewValues: map[string]interface{}{"balance": newBalance, "transaction_id": transactionID, "amount": signedAmount, "type": req.Type},
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

	// Authorization Check:
	// - ADMIN: Allow all
	// - MAIN_TEACHER: Only if class teacher of student's class
	// - STUDENT: Only if it's their own profile
	// - PARENT: Only if linked to student
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
		SELECT id, student_id, amount, type, COALESCE(description, '') as description, created_at 
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
		err := rows.Scan(&pt.ID, &pt.StudentID, &pt.Amount, &pt.Type, &desc, &pt.CreatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan transaction record", "details": err.Error()})
			return
		}
		pt.Description = &desc
		list = append(list, pt)
	}

	c.JSON(http.StatusOK, list)
}
