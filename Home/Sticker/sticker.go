package Sticker

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"vx-api/Config"
	"vx-api/Middleware"
	"vx-api/Utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Sticker struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	UserID    uint           `gorm:"index" json:"user_id"`
	URL       string         `gorm:"type:text;not null" json:"url"`
	UsageCount int           `gorm:"default:0" json:"usage_count"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type SavedSticker struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"not null;uniqueIndex:idx_user_sticker_save" json:"user_id"`
	StickerID uint      `gorm:"not null;uniqueIndex:idx_user_sticker_save" json:"sticker_id"`
	Sticker   Sticker   `gorm:"foreignKey:StickerID" json:"sticker"`
	CreatedAt time.Time `json:"created_at"`
}

func (Sticker) TableName() string {
	return "stickers"
}

func (SavedSticker) TableName() string {
	return "saved_stickers"
}

func RegisterRoutes(r *gin.RouterGroup) {
	stickerGroup := r.Group("/sticker")
	{
		stickerGroup.GET("/list", Middleware.OptionalAuth(), GetStickers)
		stickerGroup.GET("/saved", Middleware.AuthRequired(), GetSavedStickers)
		stickerGroup.POST("/save/:id", Middleware.AuthRequired(), SaveSticker)
		stickerGroup.DELETE("/save/:id", Middleware.AuthRequired(), UnsaveSticker)
		stickerGroup.POST("/create", Middleware.AuthRequired(), CreateSticker)
	}
}

func GetStickers(c *gin.Context) {
	var stickers []Sticker
	category := c.Query("category") // trending, new

	query := Config.DB.Order("created_at desc")
	if category == "trending" {
		query = Config.DB.Order("usage_count desc")
	}

	if err := query.Limit(50).Find(&stickers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load stickers"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   true,
		"stickers": stickers,
	})
}

func GetSavedStickers(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid := userID.(uint)

	var saved []SavedSticker
	if err := Config.DB.Preload("Sticker").Where("user_id = ?", uid).Order("created_at desc").Find(&saved).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load saved stickers"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   true,
		"stickers": saved,
	})
}

func SaveSticker(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid := userID.(uint)
	stickerIDStr := c.Param("id")
	var stickerID uint
	fmt.Sscanf(stickerIDStr, "%d", &stickerID)

	var existing SavedSticker
	if res := Config.DB.Where("user_id = ? AND sticker_id = ?", uid, stickerID).Limit(1).Find(&existing); res.RowsAffected > 0 {
		c.JSON(http.StatusOK, gin.H{"status": true, "message": "Already saved"})
		return
	}

	if err := Config.DB.Create(&SavedSticker{UserID: uid, StickerID: stickerID}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to save sticker"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Sticker saved"})
}

func UnsaveSticker(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid := userID.(uint)
	stickerIDStr := c.Param("id")
	var stickerID uint
	fmt.Sscanf(stickerIDStr, "%d", &stickerID)

	if err := Config.DB.Where("user_id = ? AND sticker_id = ?", uid, stickerID).Delete(&SavedSticker{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to unsave sticker"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Sticker unsaved"})
}

func CreateSticker(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid := userID.(uint)

	// Ensure directory exists BEFORE parsing form to avoid permission issues mid-stream
	stickerDir := filepath.Join("public", "uploads", "stickers")
	if err := os.MkdirAll(stickerDir, 0755); err != nil {
		log.Printf("ERROR: Failed to create sticker directory: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Server storage error"})
		return
	}

	// Increase upload limit for stickers
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 50<<20) // 50MB max
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		log.Printf("ERROR: Sticker parse form failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "File too large or bad request"})
		return
	}

	file, header, err := c.Request.FormFile("video")
	if err != nil {
		log.Printf("ERROR: Sticker FormFile failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "No video file provided"})
		return
	}
	defer file.Close()

	log.Printf("DEBUG: Creating sticker from %s (Size: %d bytes)", header.Filename, header.Size)

	startTime := c.DefaultPostForm("start", "00:00:00")
	duration := c.DefaultPostForm("duration", "3")

	stickerUUID := uuid.New().String()
	tempVideoPath := filepath.Join(os.TempDir(), stickerUUID+filepath.Ext(header.Filename))

	// Save temp video
	dst, err := os.Create(tempVideoPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Server error"})
		return
	}
	defer os.Remove(tempVideoPath)
	io.Copy(dst, file)
	dst.Close()

	outputFileName := stickerUUID + ".webp"
	outputPath := filepath.Join(stickerDir, outputFileName)

	// FFmpeg conversion to animated WebP
	cmd := exec.Command("ffmpeg",
		"-ss", startTime,
		"-t", duration,
		"-i", tempVideoPath,
		"-vf", "scale=256:256:force_original_aspect_ratio=decrease,pad=256:256:(ow-iw)/2:(oh-ih)/2:color=black@0,fps=12",
		"-vcodec", "libwebp",
		"-lossless", "0",
		"-compression_level", "6",
		"-q:v", "50",
		"-loop", "0",
		"-an", // No audio
		outputPath,
	)

	if err := cmd.Run(); err != nil {
		log.Printf("FFmpeg sticker error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Sticker creation failed"})
		return
	}

	publicURL := fmt.Sprintf("/uploads/stickers/%s", outputFileName)
	cldFolder := fmt.Sprintf("vx-app/users/%d/stickers", uid)
	// Upload to Cloudinary
	if cldURL, err := Utils.UploadToCloudinary(outputPath, "image", cldFolder); err == nil && cldURL != "" {
		publicURL = cldURL
		// Delete local file after successful Cloudinary upload
		os.Remove(outputPath)
	}

	sticker := Sticker{
		UserID: uid,
		URL:    publicURL,
	}

	if err := Config.DB.Create(&sticker).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Database error"})
		return
	}

	// Auto-save created sticker to user's collection
	Config.DB.Create(&SavedSticker{UserID: uid, StickerID: sticker.ID})

	c.JSON(http.StatusCreated, gin.H{
		"status":  true,
		"message": "Sticker created successfully",
		"sticker": sticker,
	})
}
