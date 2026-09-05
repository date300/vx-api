package Settings

import (
	"net/http"
	"vx-api/Config"
	
	"vx-api/Middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes handles all User Settings related endpoints
func RegisterRoutes(r *gin.RouterGroup) {
	settingsGroup := r.Group("/settings", Middleware.AuthRequired())
	{
		settingsGroup.GET("/", GetSettings)       // Fetch user settings
		settingsGroup.PUT("/", UpdateSettings)    // Update user settings
	}
}

// GetSettings handles GET /api/v1/settings
func GetSettings(c *gin.Context) {
	userID, _ := c.Get("userID")

	var settings UserSettings
	// Find or Create if doesn't exist
	if err := Config.DB.FirstOrCreate(&settings, UserSettings{UserID: userID.(uint)}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load settings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   settings,
	})
}

// UpdateSettings handles PUT /api/v1/settings
func UpdateSettings(c *gin.Context) {
	userID, _ := c.Get("userID")

	var input struct {
		PrivateAccount     *bool   `json:"private_account"`
		AllowComments      *bool   `json:"allow_comments"`
		ShowLikes          *bool   `json:"show_likes"`
		EmailNotifications *bool   `json:"email_notifications"`
		PushNotifications  *bool   `json:"push_notifications"`
		Autoplay           *bool   `json:"autoplay"`
		VideoQuality       *string `json:"video_quality"`
		DataSaver          *bool   `json:"data_saver"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid input"})
		return
	}

	var settings UserSettings
	Config.DB.Where("user_id = ?", userID).First(&settings)

	// Update fields if provided in JSON
	if input.PrivateAccount != nil {
		settings.PrivateAccount = *input.PrivateAccount
	}
	if input.AllowComments != nil {
		settings.AllowComments = *input.AllowComments
	}
	if input.ShowLikes != nil {
		settings.ShowLikes = *input.ShowLikes
	}
	if input.EmailNotifications != nil {
		settings.EmailNotifications = *input.EmailNotifications
	}
	if input.PushNotifications != nil {
		settings.PushNotifications = *input.PushNotifications
	}
	if input.Autoplay != nil {
		settings.Autoplay = *input.Autoplay
	}
	if input.VideoQuality != nil {
		settings.VideoQuality = *input.VideoQuality
	}
	if input.DataSaver != nil {
		settings.DataSaver = *input.DataSaver
	}

	if err := Config.DB.Save(&settings).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to update settings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  true,
		"message": "Settings updated successfully",
		"data":    settings,
	})
}

// UserSettings Model
type UserSettings struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	UserID             uint   `gorm:"uniqueIndex;not null" json:"user_id"`
	PrivateAccount     bool   `gorm:"default:false" json:"private_account"`
	AllowComments      bool   `gorm:"default:true" json:"allow_comments"`
	ShowLikes          bool   `gorm:"default:true" json:"show_likes"`
	EmailNotifications bool   `gorm:"default:true" json:"email_notifications"`
	PushNotifications  bool   `gorm:"default:true" json:"push_notifications"`
	Autoplay           bool   `gorm:"default:true" json:"autoplay"`
	VideoQuality       string `gorm:"default:'auto'" json:"video_quality"`
	DataSaver          bool   `gorm:"default:false" json:"data_saver"`
}
