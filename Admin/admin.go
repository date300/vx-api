package Admin

import (
	"net/http"
	"strconv"
	"vx-api/Config"
	"vx-api/Middleware"
	"vx-api/Profile"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes registers all admin routes
func RegisterRoutes(r *gin.RouterGroup) {
	admin := r.Group("/admin", Middleware.AuthRequired(), Middleware.AdminRequired())
	{
		admin.GET("/stats", GetStats)
		admin.GET("/users", GetUsers)
		admin.POST("/user/:id/toggle-admin", ToggleAdmin)
		admin.POST("/user/:id/toggle-ban", ToggleBan)
		admin.GET("/videos", GetVideos)
		admin.DELETE("/video/:id", DeleteVideo)
	}
}

// GetStats handles GET /api/v1/admin/stats
func GetStats(c *gin.Context) {
	var userCount, videoCount, likeCount, commentCount int64

	Config.DB.Table("users").Count(&userCount)
	Config.DB.Table("videos").Count(&videoCount)
	Config.DB.Table("likes").Count(&likeCount)
	Config.DB.Table("comments").Count(&commentCount)

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data": gin.H{
			"total_users":    userCount,
			"total_videos":   videoCount,
			"total_likes":    likeCount,
			"total_comments": commentCount,
		},
	})
}

// GetUsers handles GET /api/v1/admin/users
func GetUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit

	var users []Profile.User
	var total int64

	Config.DB.Model(&Profile.User{}).Count(&total)
	Config.DB.Limit(limit).Offset(offset).Order("created_at desc").Find(&users)

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   users,
		"total":  total,
	})
}

// ToggleAdmin handles POST /api/v1/admin/user/:id/toggle-admin
func ToggleAdmin(c *gin.Context) {
	userID := c.Param("id")
	var user Profile.User
	if err := Config.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}

	user.IsAdmin = !user.IsAdmin
	Config.DB.Save(&user)

	c.JSON(http.StatusOK, gin.H{
		"status":   true,
		"is_admin": user.IsAdmin,
		"message":  "Admin status updated",
	})
}

// ToggleBan handles POST /api/v1/admin/user/:id/toggle-ban
func ToggleBan(c *gin.Context) {
	userID := c.Param("id")
	var user Profile.User
	if err := Config.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}

	user.IsBanned = !user.IsBanned
	Config.DB.Save(&user)

	c.JSON(http.StatusOK, gin.H{
		"status":    true,
		"is_banned": user.IsBanned,
		"message":   "User ban status updated",
	})
}

// GetVideos handles GET /api/v1/admin/videos
func GetVideos(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit

	var videos []Profile.Video
	var total int64

	Config.DB.Model(&Profile.Video{}).Count(&total)
	Config.DB.Preload("User").Limit(limit).Offset(offset).Order("created_at desc").Find(&videos)

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   videos,
		"total":  total,
	})
}

// DeleteVideo handles DELETE /api/v1/admin/video/:id
func DeleteVideo(c *gin.Context) {
	videoID := c.Param("id")
	if err := Config.DB.Delete(&Profile.Video{}, videoID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to delete video"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Video deleted by admin"})
}
