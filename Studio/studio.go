package Studio

import (
	"net/http"
	"time"
	"vx-api/Config"
	"vx-api/Middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// DailyStats holds metrics for a specific user on a specific day
type DailyStats struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	Date      time.Time `gorm:"type:date;index;not null" json:"date"`
	Views     int64     `gorm:"default:0" json:"views"`
	Likes     int64     `gorm:"default:0" json:"likes"`
	Followers int64     `gorm:"default:0" json:"followers"`
	ProfileVisits int64 `gorm:"default:0" json:"profile_visits"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func RegisterRoutes(r *gin.RouterGroup) {
	studioGroup := r.Group("/studio", Middleware.AuthRequired())
	{
		studioGroup.GET("/analytics", GetAnalytics)
	}
}

func GetAnalytics(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid := userID.(uint)

	// Get stats for the last 7 days
	now := time.Now()
	sevenDaysAgo := now.AddDate(0, 0, -7)

	var stats []DailyStats
	Config.DB.Where("user_id = ? AND date >= ?", uid, sevenDaysAgo.Format("2006-01-02")).
		Order("date asc").
		Find(&stats)

	// Aggregate totals
	var totalViews, totalLikes, totalFollowers, totalProfileVisits int64
	for _, s := range stats {
		totalViews += s.Views
		totalLikes += s.Likes
		totalFollowers += s.Followers
		totalProfileVisits += s.ProfileVisits
	}

	// Calculate trends (comparing with previous 7 days)
	// For simplicity, we return current totals and dummy trends for now
	// Real trend calculation would involve fetching stats from 14-7 days ago

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data": gin.H{
			"overview": gin.H{
				"views": gin.H{
					"value": totalViews,
					"trend": "+12%", // Placeholder
				},
				"likes": gin.H{
					"value": totalLikes,
					"trend": "+5%", // Placeholder
				},
				"followers": gin.H{
					"value": totalFollowers,
					"trend": "+8%", // Placeholder
				},
				"profile_visits": gin.H{
					"value": totalProfileVisits,
					"trend": "+3%", // Placeholder
				},
			},
			"daily_stats": stats,
		},
	})
}

// IncrementDailyStat utility function to update stats
func IncrementDailyStat(userID uint, field string, amount int64) {
	date := time.Now().Format("2006-01-02")

	var stat DailyStats
	res := Config.DB.Where("user_id = ? AND date = ?", userID, date).Limit(1).Find(&stat)

	if res.RowsAffected == 0 {
		newStat := DailyStats{
			UserID: userID,
			Date:   time.Now(),
		}
		// Set initial value
		switch field {
		case "views":
			newStat.Views = amount
		case "likes":
			newStat.Likes = amount
		case "followers":
			newStat.Followers = amount
		case "profile_visits":
			newStat.ProfileVisits = amount
		}
		Config.DB.Create(&newStat)
		return
	}

	Config.DB.Model(&stat).UpdateColumn(field, gorm.Expr(field+" + ?", amount))
}
