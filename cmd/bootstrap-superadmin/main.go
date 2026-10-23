// Bootstrap is an operator-only command, never an HTTP endpoint.
// Run from the repository root after applying central migrations.
package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"strings"
	"time"

	"github.com/farzandim/backend/internal/utils"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	_ = godotenv.Load()
	phone := utils.NormalizePhone(os.Getenv("BOOTSTRAP_ADMIN_PHONE"))
	first := strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_FIRST_NAME"))
	last := strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_LAST_NAME"))
	password := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if len(phone) < 9 || len(phone) > 15 || first == "" || last == "" {
		log.Fatal("Set BOOTSTRAP_ADMIN_PHONE, BOOTSTRAP_ADMIN_FIRST_NAME and BOOTSTRAP_ADMIN_LAST_NAME")
	}
	if err := utils.ValidateAdminPassword(password); err != nil { log.Fatal(err) }
	connection := os.Getenv("CENTRAL_DB_URL")
	if connection == "" { log.Fatal("CENTRAL_DB_URL is required") }
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil { log.Fatal("Could not hash password") }
	conn, err := sql.Open("postgres", connection)
	if err != nil { log.Fatal("Could not open central DB") }
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil { log.Fatal("Could not connect to central DB") }
	defer tx.Rollback()
	// Blocks concurrent bootstrap and registration inserts while checking emptiness.
	if _, err = tx.ExecContext(ctx, "LOCK TABLE super_admins IN EXCLUSIVE MODE"); err != nil { log.Fatal("Apply central migrations first") }
	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM super_admins)").Scan(&exists); err != nil { log.Fatal("Could not read super admins") }
	if exists { log.Fatal("Bootstrap refused: a super-admin record already exists (including deleted accounts)") }
	_, err = tx.ExecContext(ctx, "INSERT INTO super_admins (phone, password_hash, first_name, last_name, auth_version) VALUES ($1,$2,$3,$4,1)", phone, string(hash), first, last)
	if err != nil { log.Fatal("Could not create initial super admin; verify migrations and input") }
	if err = tx.Commit(); err != nil { log.Fatal("Could not commit bootstrap; inspect DB before retrying") }
	log.Println("Initial super admin created. Remove bootstrap secrets from the environment.")
}
