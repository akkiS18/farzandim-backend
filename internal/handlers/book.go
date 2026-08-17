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
	"github.com/farzandim/backend/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

type BookHandler struct{}

func NewBookHandler() *BookHandler {
	return &BookHandler{}
}

// ----------------------------------------------------
// BOOK CATEGORIES (GROUPS) HANDLERS
// ----------------------------------------------------

// ListCategories returns all active book categories
func (h *BookHandler) ListCategories(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	query := `
		SELECT id, name, description, created_by, created_at
		FROM book_categories
		WHERE is_deleted = false
		ORDER BY name ASC`

	rows, err := dbConn.Query(query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch book categories", "details": err.Error()})
		return
	}
	defer rows.Close()

	categories := []models.BookCategory{}
	for rows.Next() {
		var cat models.BookCategory
		var createdBy sql.NullInt64

		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Description, &createdBy, &cat.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan category", "details": err.Error()})
			return
		}
		if createdBy.Valid {
			cb := int(createdBy.Int64)
			cat.CreatedBy = &cb
		}
		categories = append(categories, cat)
	}

	c.JSON(http.StatusOK, categories)
}

// CreateCategory creates a new book category (Accessible to Admin & Teachers)
func (h *BookHandler) CreateCategory(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	userIDVal, _ := c.Get("userID")
	currentUserID, _ := strconv.Atoi(userIDVal.(string))

	var input models.CreateBookCategoryInput
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

	var cat models.BookCategory
	err = tx.QueryRow(`
		INSERT INTO book_categories (name, description, created_by, created_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING id, name, description, created_by, created_at`,
		input.Name, input.Description, currentUserID,
	).Scan(&cat.ID, &cat.Name, &cat.Description, &cat.CreatedBy, &cat.CreatedAt)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create book category", "details": err.Error()})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE_BOOK_CATEGORY",
		TableName: "book_categories",
		RecordID:  strconv.Itoa(cat.ID),
		NewValues: map[string]interface{}{"name": cat.Name},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction commit failed", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, cat)
}

// DeleteCategory soft deletes a book category
func (h *BookHandler) DeleteCategory(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn := tenantDBVal.(*sql.DB)

	catID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid category ID"})
		return
	}

	tx, err := dbConn.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec("UPDATE book_categories SET is_deleted = true, deleted_at = NOW() WHERE id = $1 AND is_deleted = false", catID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete category"})
		return
	}

	rowsAff, _ := res.RowsAffected()
	if rowsAff == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Category not found"})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE_BOOK_CATEGORY",
		TableName: "book_categories",
		RecordID:  strconv.Itoa(catID),
	})

	tx.Commit()
	c.JSON(http.StatusOK, gin.H{"message": "Category deleted successfully"})
}

// ----------------------------------------------------
// BOOKS HANDLERS
// ----------------------------------------------------

// List returns all active books with category details
func (h *BookHandler) List(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	categoryParam := c.Query("category_id")

	query := `
		SELECT b.id, b.title, b.author, b.description, b.cover_url, COALESCE(b.file_url, ''), COALESCE(b.file_size, ''),
		       b.category_id, COALESCE(bc.name, '') as category_name, COALESCE(b.download_link, ''), COALESCE(b.location_in_school, ''),
		       b.created_by, b.target_levels, b.class_ids, b.is_deleted, b.created_at, b.updated_at
		FROM books b
		LEFT JOIN book_categories bc ON b.category_id = bc.id AND bc.is_deleted = false
		WHERE b.is_deleted = false`

	var args []interface{}
	argCount := 1

	if categoryParam != "" {
		if catID, err := strconv.Atoi(categoryParam); err == nil {
			query += fmt.Sprintf(" AND b.category_id = $%d", argCount)
			args = append(args, catID)
			argCount++
		}
	}

	query += " ORDER BY b.created_at DESC"

	rows, err := dbConn.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch books", "details": err.Error()})
		return
	}
	defer rows.Close()

	books := []models.Book{}
	for rows.Next() {
		var item models.Book
		var catID, createdBy sql.NullInt64
		var tLevels, cIDs pq.Int64Array

		err := rows.Scan(
			&item.ID,
			&item.Title,
			&item.Author,
			&item.Description,
			&item.CoverURL,
			&item.FileURL,
			&item.FileSize,
			&catID,
			&item.CategoryName,
			&item.DownloadLink,
			&item.LocationInSchool,
			&createdBy,
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

		if catID.Valid {
			cid := int(catID.Int64)
			item.CategoryID = &cid
		}
		if createdBy.Valid {
			cb := int(createdBy.Int64)
			item.CreatedBy = &cb
		}
		item.TargetLevels = tLevels
		item.ClassIDs = cIDs
		books = append(books, item)
	}

	c.JSON(http.StatusOK, books)
}

// Create creates a new book record (Accessible to Admin & Teachers)
func (h *BookHandler) Create(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	userIDVal, _ := c.Get("userID")
	currentUserID, _ := strconv.Atoi(userIDVal.(string))

	var input models.CreateBookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input", "details": err.Error()})
		return
	}

	if input.TargetLevels == nil {
		input.TargetLevels = []int64{}
	}
	if input.ClassIDs == nil {
		input.ClassIDs = []int64{}
	}

	tx, err := dbConn.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction", "details": err.Error()})
		return
	}
	defer tx.Rollback()

	query := `
		INSERT INTO books (title, author, description, cover_url, file_url, file_size, category_id, download_link, location_in_school, created_by, target_levels, class_ids, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
		RETURNING id, title, author, description, cover_url, file_url, file_size, category_id, download_link, location_in_school, created_by, target_levels, class_ids, created_at, updated_at`

	var item models.Book
	var catID, createdBy sql.NullInt64
	var tLevels, cIDs pq.Int64Array

	err = tx.QueryRow(
		query,
		input.Title,
		input.Author,
		input.Description,
		input.CoverURL,
		input.FileURL,
		input.FileSize,
		input.CategoryID,
		input.DownloadLink,
		input.LocationInSchool,
		currentUserID,
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
		&catID,
		&item.DownloadLink,
		&item.LocationInSchool,
		&createdBy,
		&tLevels,
		&cIDs,
		&item.CreatedAt,
		&item.UpdatedAt,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create book", "details": err.Error()})
		return
	}

	if catID.Valid {
		cid := int(catID.Int64)
		item.CategoryID = &cid
	}
	if createdBy.Valid {
		cb := int(createdBy.Int64)
		item.CreatedBy = &cb
	}
	item.TargetLevels = tLevels
	item.ClassIDs = cIDs

	audit.LogChange(c, tx, audit.LogData{
		Action:    "CREATE_BOOK",
		TableName: "books",
		RecordID:  strconv.Itoa(item.ID),
		NewValues: map[string]interface{}{"title": item.Title, "author": item.Author},
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

	bookIDStr := c.Param("id")
	bookID, err := strconv.Atoi(bookIDStr)
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
		SET title = COALESCE(NULLIF($1, ''), title),
		    author = COALESCE(NULLIF($2, ''), author),
		    description = COALESCE(NULLIF($3, ''), description),
		    cover_url = COALESCE(NULLIF($4, ''), cover_url),
		    file_url = COALESCE(NULLIF($5, ''), file_url),
		    file_size = COALESCE(NULLIF($6, ''), file_size),
		    category_id = COALESCE($7, category_id),
		    download_link = COALESCE(NULLIF($8, ''), download_link),
		    location_in_school = COALESCE(NULLIF($9, ''), location_in_school),
		    target_levels = COALESCE($10, target_levels),
		    class_ids = COALESCE($11, class_ids),
		    updated_at = NOW()
		WHERE id = $12 AND is_deleted = false
		RETURNING id, title, author, description, cover_url, file_url, file_size, category_id, download_link, location_in_school, created_by, target_levels, class_ids, created_at, updated_at`

	var item models.Book
	var catID, createdBy sql.NullInt64
	var tLevels, cIDs pq.Int64Array

	var targetLevelsArg interface{} = nil
	if input.TargetLevels != nil {
		targetLevelsArg = pq.Int64Array(input.TargetLevels)
	}

	var classIDsArg interface{} = nil
	if input.ClassIDs != nil {
		classIDsArg = pq.Int64Array(input.ClassIDs)
	}

	err = tx.QueryRow(
		query,
		input.Title,
		input.Author,
		input.Description,
		input.CoverURL,
		input.FileURL,
		input.FileSize,
		input.CategoryID,
		input.DownloadLink,
		input.LocationInSchool,
		targetLevelsArg,
		classIDsArg,
		bookID,
	).Scan(
		&item.ID,
		&item.Title,
		&item.Author,
		&item.Description,
		&item.CoverURL,
		&item.FileURL,
		&item.FileSize,
		&catID,
		&item.DownloadLink,
		&item.LocationInSchool,
		&createdBy,
		&tLevels,
		&cIDs,
		&item.CreatedAt,
		&item.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update book", "details": err.Error()})
		return
	}

	if catID.Valid {
		cid := int(catID.Int64)
		item.CategoryID = &cid
	}
	if createdBy.Valid {
		cb := int(createdBy.Int64)
		item.CreatedBy = &cb
	}
	item.TargetLevels = tLevels
	item.ClassIDs = cIDs

	audit.LogChange(c, tx, audit.LogData{
		Action:    "UPDATE_BOOK",
		TableName: "books",
		RecordID:  strconv.Itoa(item.ID),
		NewValues: map[string]interface{}{"title": item.Title, "author": item.Author},
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, item)
}

// Delete soft deletes a book record
func (h *BookHandler) Delete(c *gin.Context) {
	tenantDBVal, _ := c.Get("tenantDB")
	dbConn, ok := tenantDBVal.(*sql.DB)
	if !ok || dbConn == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Tenant database connection missing"})
		return
	}

	bookIDStr := c.Param("id")
	bookID, err := strconv.Atoi(bookIDStr)
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

	rowsAff, _ := res.RowsAffected()
	if rowsAff == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found or already deleted"})
		return
	}

	audit.LogChange(c, tx, audit.LogData{
		Action:    "DELETE_BOOK",
		TableName: "books",
		RecordID:  strconv.Itoa(bookID),
	})

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Book deleted successfully"})
}

// UploadFile handles direct file upload for covers
func (h *BookHandler) UploadFile(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded", "details": err.Error()})
		return
	}

	if storage.IsR2Enabled() {
		urlPath, err := storage.UploadToR2(c.Request.Context(), file, "books")
		if err == nil {
			c.JSON(http.StatusOK, gin.H{
				"file_url":  urlPath,
				"file_name": file.Filename,
				"file_size": fmt.Sprintf("%.2f MB", float64(file.Size)/(1024*1024)),
			})
			return
		}
	}

	uploadDir := "./uploads/books"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload directory"})
		return
	}

	ext := filepath.Ext(file.Filename)
	filename := fmt.Sprintf("%d_%s%s", time.Now().UnixNano(), filepath.Base(file.Filename[:len(file.Filename)-len(ext)]), ext)
	dst := filepath.Join(uploadDir, filename)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	urlPath := fmt.Sprintf("/uploads/books/%s", filename)
	c.JSON(http.StatusOK, gin.H{
		"file_url":  urlPath,
		"file_name": file.Filename,
		"file_size": fmt.Sprintf("%.2f MB", float64(file.Size)/(1024*1024)),
	})
}
