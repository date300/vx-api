package Inbox

import (
	"net/http"
	"time"

	"vx-api/Config"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CallHistory Model for audio/video call logs
type CallHistory struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	CallerID   uint           `gorm:"index;not null" json:"caller_id"`
	Caller     User           `gorm:"foreignKey:CallerID" json:"caller"`
	ReceiverID uint           `gorm:"index;not null" json:"receiver_id"`
	Receiver   User           `gorm:"foreignKey:ReceiverID" json:"receiver"`
	Type       string         `gorm:"type:varchar(20);not null" json:"type"`   // audio, video
	Status     string         `gorm:"type:varchar(20);not null" json:"status"` // missed, completed, rejected, cancelled
	Duration   int            `gorm:"default:0" json:"duration"`               // duration in seconds
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

// GetCallHistory handles GET /api/v1/inbox/calls/history
func GetCallHistory(c *gin.Context) {
	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var history []CallHistory
	if err := Config.DB.Preload("Caller").Preload("Receiver").
		Where("caller_id = ? OR receiver_id = ?", userID, userID).
		Order("created_at desc").
		Limit(50).
		Find(&history).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to fetch call history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   history,
	})
}

// CreateCallLog handles POST /api/v1/inbox/calls/log
func CreateCallLog(c *gin.Context) {
	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var input struct {
		ReceiverID uint   `json:"receiver_id" binding:"required"`
		Type       string `json:"type" binding:"required"`   // audio, video
		Status     string `json:"status" binding:"required"` // missed, completed, rejected
		Duration   int    `json:"duration"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid input"})
		return
	}

	log := CallHistory{
		CallerID:   userID,
		ReceiverID: input.ReceiverID,
		Type:       input.Type,
		Status:     input.Status,
		Duration:   input.Duration,
	}

	if err := Config.DB.Create(&log).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to create call log"})
		return
	}

	// Preload for the response
	Config.DB.Preload("Caller").Preload("Receiver").First(&log, log.ID)

	c.JSON(http.StatusCreated, gin.H{
		"status": true,
		"data":   log,
	})
}
