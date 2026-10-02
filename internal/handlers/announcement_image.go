package handlers

import (
	"database/sql"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/farzandim/backend/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// UploadImage accepts only actual JPEG/PNG images that Telegram can display as photos.
func (h *AnnouncementHandler) UploadImage(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 11<<20)
	file, err := c.FormFile("file")
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	if err != nil || file.Size > 10<<20 {
		c.JSON(400, gin.H{"error": "JPEG/PNG rasm hajmi 10 MB dan oshmasin"})
		return
	}
	src, err := file.Open()
	if err != nil {
		c.JSON(400, gin.H{"error": "Rasm ochilmadi"})
		return
	}
	config, format, err := image.DecodeConfig(src)
	src.Close()
	if err != nil || (format != "jpeg" && format != "png") || config.Width+config.Height > 10000 || config.Width == 0 || config.Height == 0 || config.Width > 20*config.Height || config.Height > 20*config.Width {
		c.JSON(400, gin.H{"error": "JPEG/PNG rasm o‘lchamlari yaroqsiz"})
		return
	}
	ext := ".jpg"
	if format == "png" {
		ext = ".png"
	}
	file.Filename = uuid.NewString() + ext
	folder := "announcements/" + c.GetString("currentSchoolID")
	var url string
	if storage.IsR2Enabled() {
		url, err = storage.UploadToR2(c.Request.Context(), file, folder)
	} else {
		dir := filepath.Join("uploads", folder)
		err = os.MkdirAll(dir, 0755)
		if err == nil {
			err = c.SaveUploadedFile(file, filepath.Join(dir, file.Filename))
		}
		url = "/uploads/" + folder + "/" + file.Filename
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "Rasmni saqlab bo‘lmadi"})
		return
	}
	dbConn := c.MustGet("tenantDB").(*sql.DB)
	userID, _ := strconv.Atoi(c.GetString("userID"))
	if _, err := dbConn.Exec("INSERT INTO announcement_uploads(url, uploaded_by) VALUES($1,$2)", url, userID); err != nil {
		c.JSON(500, gin.H{"error": "Rasmni ro‘yxatga olib bo‘lmadi"})
		return
	}
	c.JSON(201, gin.H{"url": url})
}
