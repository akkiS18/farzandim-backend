package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/farzandim/backend/internal/audit"
	"github.com/farzandim/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

type BookHandler struct{}

func NewBookHandler() *BookHandler {
	return &BookHandler{}
}

// List returns all active books, optionally filtered by level or class_id
func (h *BookHandler) List(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	levelParam := c.Query("level")
	classParam := c.Query("class_id")

	query := `
		SELECT id, title, author, description, cover_url, file_url, file_size, target_levels, class_ids, is_deleted, created_at, updated_at
		FROM books
		WHERE is_deleted = false`

	var args []interface{}
	argCount := 1

	if levelParam != "" {
		if lvl, err := strconv.Atoi(levelParam); err == nil {
			query += fmt.Sprintf(" AND ($%d = ANY(target_levels) OR cardinality(target_levels) = 0)", argCount)
			args = append(args, lvl)
			argCount++
		}
	}

	if classParam != "" {
		if clsID, err := strconv.Atoi(classParam); err == nil {
			query += fmt.Sprintf(" AND ($%d = ANY(class_ids) OR cardinality(class_ids) = 0)", argCount)
			args = append(args, clsID)
			argCount++
		}
	}

	query += " ORDER BY created_at DESC"

	rows, err := dbConn.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch books", "details": err.Error()})
		return
	}
	defer rows.Close()

	books := []models.Book{}
	for rows.Next() {
		var item models.Book
		var tLevels, cIDs pq.Int64Array

		err := rows.Scan(
			&item.ID,
			&item.Title,
			&item.Author,
			&item.Description,
			&item.CoverURL,
			&item.FileURL,
			&item.FileSize,
			&tLevels,
			&cIDs,
			&item.IsDeleted,
			&item.CreatedAt,
			&item.UpdatedAt,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan book row", "details": err.Error()})
			return
		}

		item.TargetLevels = tLevels
		item.ClassIDs = cIDs
		books = append(books, item)
	}

	c.JSON(http.StatusOK, books)
}

// Create creates a new book record
func (h *BookHandler) Create(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	var input models.CreateBookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input", "details": err.Error()})
		return
	}

	tx, err := dbConn.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	query := `
		INSERT INTO books (title, author, description, cover_url, file_url, file_size, target_levels, class_ids, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		RETURNING id, title, author, description, cover_url, file_url, file_size, target_levels, class_ids, created_at, updated_at`

	var item models.Book
	var tLevels, cIDs pq.Int64Array

	err = tx.QueryRow(
		query,
		input.Title,
		input.Author,
		input.Description,
		input.CoverURL,
		input.FileURL,
		input.FileSize,
		pq.Int64Array(input.TargetLevels),
		pq.Int64Array(input.ClassIDs),
	).Scan(
		&item.ID,
		&item.Title,
		&item.Author,
		&item.Description,
		&item.CoverURL,
		&item.FileURL,
		&item.FileSize,
		&tLevels,
		&cIDs,
		&item.CreatedAt,
		&item.UpdatedAt,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create book", "details": err.Error()})
		return
	}

	item.TargetLevels = tLevels
	item.ClassIDs = cIDs

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE",
		TableName: "books",
		RecordID:  strconv.Itoa(item.ID),
		NewValues: item,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, item)
}

// Update updates an existing book record
func (h *BookHandler) Update(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	idParam := c.Param("id")
	bookID, err := strconv.Atoi(idParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID"})
		return
	}

	var input models.UpdateBookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input", "details": err.Error()})
		return
	}

	tx, err := dbConn.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	query := `
		UPDATE books
		SET title = $1, author = $2, description = $3, cover_url = $4, file_url = $5, file_size = $6, target_levels = $7, class_ids = $8, updated_at = NOW()
		WHERE id = $9 AND is_deleted = false
		RETURNING id, title, author, description, cover_url, file_url, file_size, target_levels, class_ids, created_at, updated_at`

	var item models.Book
	var tLevels, cIDs pq.Int64Array

	err = tx.QueryRow(
		query,
		input.Title,
		input.Author,
		input.Description,
		input.CoverURL,
		input.FileURL,
		input.FileSize,
		pq.Int64Array(input.TargetLevels),
		pq.Int64Array(input.ClassIDs),
		bookID,
	).Scan(
		&item.ID,
		&item.Title,
		&item.Author,
		&item.Description,
		&item.CoverURL,
		&item.FileURL,
		&item.FileSize,
		&tLevels,
		&cIDs,
		&item.CreatedAt,
		&item.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update book", "details": err.Error()})
		}
		return
	}

	item.TargetLevels = tLevels
	item.ClassIDs = cIDs

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE",
		TableName: "books",
		RecordID:  strconv.Itoa(bookID),
		NewValues: item,
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, item)
}

// Delete soft deletes a book
func (h *BookHandler) Delete(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	idParam := c.Param("id")
	bookID, err := strconv.Atoi(idParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID"})
		return
	}

	tx, err := dbConn.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	query := `UPDATE books SET is_deleted = true, deleted_at = NOW() WHERE id = $1 AND is_deleted = false`
	res, err := tx.Exec(query, bookID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete book", "details": err.Error()})
		return
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE",
		TableName: "books",
		RecordID:  strconv.Itoa(bookID),
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Book deleted successfully"})
}

// UploadFile handles direct file upload for books (PDF/EPUB) or cover images
func (h *BookHandler) UploadFile(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Fayl yuborilmadi", "details": err.Error()})
		return
	}

	uploadDir := "./uploads/books"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Fayl papkasini yaratib bo'lmadi", "details": err.Error()})
		return
	}

	filename := fmt.Sprintf("%d_%s", time.Now().UnixNano(), filepath.Base(file.Filename))
	dst := filepath.Join(uploadDir, filename)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Faylni saqlashda xatolik yuz berdi", "details": err.Error()})
		return
	}

	fileSizeStr := fmt.Sprintf("%.2f MB", float64(file.Size)/(1024*1024))
	if file.Size < 1024*1024 {
		fileSizeStr = fmt.Sprintf("%.1f KB", float64(file.Size)/1024)
	}

	urlPath := fmt.Sprintf("/uploads/books/%s", filename)

	c.JSON(http.StatusOK, gin.H{
		"url":       urlPath,
		"file_name": file.Filename,
		"file_size": fileSizeStr,
	})
}
