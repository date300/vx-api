package Explore

import (
	"net/http"
	"strings"
	"time"

	"vx-api/Config"
	"vx-api/Middleware"

	"github.com/gin-gonic/gin"
)

// Sound Model
type Sound struct {
	ID              uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Title           string    `gorm:"type:varchar(200);not null" json:"title"`
	AuthorName      string    `gorm:"type:varchar(100)" json:"author_name"`
	AuthorAvatar    string    `gorm:"type:text" json:"author_avatar"`
	AudioURL        string    `gorm:"type:text;not null" json:"audio_url"`
	OriginalVideoID *uint     `gorm:"index" json:"original_video_id"`
	TotalVideos     int       `gorm:"column:total_videos;default:0" json:"total_videos"`
	CreatedAt       time.Time `json:"created_at"`
}

// GetSoundDetails handles GET /api/v1/explore/sound/:id
func GetSoundDetails(c *gin.Context) {
	soundID := c.Param("id")
	userID := getUserIDOrZero(c)
	limit, offset := parsePagination(c)

	var sound Sound
	if err := Config.DB.First(&sound, soundID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Sound not found"})
		return
	}

	var originalVideo *gin.H
	if sound.OriginalVideoID != nil {
		var v Video
		if err := Config.DB.Preload("User").First(&v, *sound.OriginalVideoID).Error; err == nil {
			ev := enrichVideos(userID, []Video{v})
			if len(ev) > 0 {
				originalVideo = &ev[0]
			}
		}
	}

	var total int64
	Config.DB.Model(&Video{}).Where("sound_id = ?", soundID).Count(&total)

	var videos []Video
	// ওই সাউন্ড ব্যবহার করা ভিডিওগুলো লোড করা (Pagination সহ)
	if err := Config.DB.Preload("User").
		Where("sound_id = ?", soundID).
		Order("views desc").
		Limit(limit).Offset(offset).
		Find(&videos).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load videos for this sound"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":         true,
		"sound":          sound,
		"original_video": originalVideo,
		"total":          total,
		"limit":          limit,
		"offset":         offset,
		"data":           enrichVideos(userID, videos),
		"videos":         enrichVideos(userID, videos),
	})
}

// SearchSounds handles GET /api/v1/explore/sounds/search?q=query
func SearchSounds(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	limit, offset := parsePagination(c)

	var sounds []Sound
	var total int64

	dbQuery := Config.DB.Model(&Sound{})

	if query != "" {
		searchTerm := "%" + escapeLike(strings.ToLower(query)) + "%"
		dbQuery = dbQuery.Where("LOWER(title) LIKE ? ESCAPE '\\' OR LOWER(author_name) LIKE ? ESCAPE '\\'", searchTerm, searchTerm)
	}

	dbQuery.Count(&total)

	if err := dbQuery.Order("total_videos desc").Limit(limit).Offset(offset).Find(&sounds).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Sound fetch failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"query":  query,
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"data":   sounds,
	})
}

// GetTrendingSounds handles GET /api/v1/explore/sounds/trending
func GetTrendingSounds(c *gin.Context) {
	var sounds []Sound
	if err := Config.DB.Order("total_videos desc").Limit(20).Find(&sounds).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load trending sounds"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   sounds,
	})
}

// UpdateSoundRoutes registers sound-related routes
func UpdateSoundRoutes(group *gin.RouterGroup) {
	group.GET("/sound/:id", Middleware.OptionalAuth(), GetSoundDetails)
	group.GET("/sounds/search", Middleware.OptionalAuth(), SearchSounds)
	group.GET("/sounds/trending", Middleware.OptionalAuth(), GetTrendingSounds)
}
