package audit

import (
	"database/sql"
	"encoding/json"
	"log"
	"strconv"

	"github.com/gin-gonic/gin"
)

type LogData struct {
	Action    string      // "CREATE", "UPDATE", "SOFT_DELETE"
	TableName string      // e.g., "classes", "users"
	RecordID  string      // Primary key value of the mutated record
	OldValues interface{} // Previous state before change
	NewValues interface{} // Future state after change
}

// LogChange inserts an audit record inside the active transaction (tx).
// If an error occurs, it is logged, but does not abort the transaction to keep it resilient.
func LogChange(c *gin.Context, tx *sql.Tx, data LogData) {
	var userID *int

	// 1. Resolve tenant user ID (Super Admins do not exist in tenant DB users list)
	roleVal, roleExists := c.Get("role")
	uidVal, uidExists := c.Get("userID")

	if roleExists && uidExists {
		roleStr, ok1 := roleVal.(string)
		uidStr, ok2 := uidVal.(string)
		if ok1 && ok2 && roleStr != "SUPER_ADMIN" {
			if id, err := strconv.Atoi(uidStr); err == nil {
				userID = &id
			}
		}
	}

	// 2. Resolve client environment details
	ipAddress := c.ClientIP()
	userAgent := c.Request.UserAgent()

	// 3. Serialize data payloads to JSON
	// 3. Serialize data payloads to JSON as strings (enabling Postgres text-to-jsonb auto-cast)
	var oldVal, newVal interface{}
	var err error

	if data.OldValues != nil {
		oldJSON, err := json.Marshal(data.OldValues)
		if err == nil {
			oldVal = string(oldJSON)
		} else {
			log.Printf("[AUDIT ERROR] failed to marshal old values: %v", err)
		}
	}

	if data.NewValues != nil {
		newJSON, err := json.Marshal(data.NewValues)
		if err == nil {
			newVal = string(newJSON)
		} else {
			log.Printf("[AUDIT ERROR] failed to marshal new values: %v", err)
		}
	}

	// 4. Exec insert statement inside transaction block
	query := `
		INSERT INTO audit_logs (user_id, action, table_name, record_id, old_values, new_values, ip_address, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err = tx.Exec(query, userID, data.Action, data.TableName, data.RecordID, oldVal, newVal, ipAddress, userAgent)
	if err != nil {
		log.Printf("[AUDIT ERROR] failed to insert audit log into database: %v", err)
	}
}
