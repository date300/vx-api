package Home

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"vx-api/Config"
	"vx-api/Studio"
	"vx-api/Realtime"
	"vx-api/Utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// DeleteVideo handles DELETE /api/v1/interaction/video/:id
func DeleteVideo(c *gin.Context) {
	videoID := c.Param("id")
	userIDRaw, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "message": "Unauthorized: User ID not found"})
		return
	}
	userID := userIDRaw.(uint)

	var video Video
	// Using Find instead of First to be consistent with silent lookups
	if err := Config.DB.Where("id = ?", videoID).Limit(1).Find(&video).Error; err != nil || video.ID == 0 {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Video not found in database"})
		return
	}

	// Ownership check
	if video.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"status": false, "message": "You can only delete your own videos"})
		return
	}

	// Delete physical files
	if video.URL != "" && strings.HasPrefix(video.URL, "/uploads") {
		filePath := filepath.Join("public", strings.TrimPrefix(video.URL, "/"))
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			fmt.Printf("Warning: failed to delete video file %s: %v\n", filePath, err)
		}
	}

	if video.ThumbnailURL != "" && strings.HasPrefix(video.ThumbnailURL, "/uploads") {
		thumbPath := filepath.Join("public", strings.TrimPrefix(video.ThumbnailURL, "/"))
		if err := os.Remove(thumbPath); err != nil && !os.IsNotExist(err) {
			fmt.Printf("Warning: failed to delete thumbnail file %s: %v\n", thumbPath, err)
		}
	}

	// Delete from DB
	if err := Config.DB.Delete(&video).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to delete video from database"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Video deleted successfully"})
}

// ToggleLike handles POST /api/v1/interaction/like
func ToggleLike(c *gin.Context) {
	var input struct {
		VideoID uint `json:"video_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Video ID is required"})
		return
	}

	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var existing Like
	res := Config.DB.Where("user_id = ? AND video_id = ?", userID, input.VideoID).Limit(1).Find(&existing)

	isLiked := false
	err2 := Config.DB.Transaction(func(tx *gorm.DB) error {
		if res.RowsAffected > 0 {
			if err := tx.Delete(&existing).Error; err != nil {
				return err
			}
			if err := tx.Model(&Video{}).Where("id = ? AND likes > 0", input.VideoID).
				UpdateColumn("likes", gorm.Expr("likes - 1")).Error; err != nil {
				return err
			}
			isLiked = false
		} else {
			newLike := Like{UserID: userID, VideoID: input.VideoID}
			if err := tx.Create(&newLike).Error; err != nil {
				return err
			}
			if err := tx.Model(&Video{}).Where("id = ?", input.VideoID).
				UpdateColumn("likes", gorm.Expr("likes + 1")).Error; err != nil {
				return err
			}

			var v Video
			tx.Select("user_id").Where("id = ?", input.VideoID).Limit(1).Find(&v)
			if v.UserID != 0 {
				Studio.IncrementDailyStat(v.UserID, "likes", 1)
				// Notify the video owner
				if v.UserID != userID {
					msg := "liked your video"
					go createNotificationHelper(v.UserID, &userID, "like", &input.VideoID, nil, msg)
				}
			}

			isLiked = true
		}
		return nil
	})

	if err2 != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to update like"})
		return
	}

	var video Video
	Config.DB.Select("likes").Where("id = ?", input.VideoID).Limit(1).Find(&video)

	c.JSON(http.StatusOK, gin.H{
		"status":   true,
		"is_liked": isLiked,
		"likes":    video.Likes,
		"video_id": input.VideoID,
	})

	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "like_update",
		Payload: gin.H{
			"video_id":    input.VideoID,
			"likes_count": video.Likes,
		},
	})
}

// ToggleSave handles POST /api/v1/interaction/save
func ToggleSave(c *gin.Context) {
	var input struct {
		VideoID uint `json:"video_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Video ID is required"})
		return
	}

	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var existing Save
	res := Config.DB.Where("user_id = ? AND video_id = ?", userID, input.VideoID).Limit(1).Find(&existing)

	isSaved := false
	if res.RowsAffected > 0 {
		Config.DB.Delete(&existing)
		isSaved = false
	} else {
		Config.DB.Create(&Save{UserID: userID, VideoID: input.VideoID})
		isSaved = true
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   true,
		"is_saved": isSaved,
		"video_id": input.VideoID,
	})
}

// ToggleFollow handles POST /api/v1/interaction/follow
func ToggleFollow(c *gin.Context) {
	var input struct {
		UserID uint `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "User ID is required"})
		return
	}

	followerIDRaw, _ := c.Get("userID")
	followerID := followerIDRaw.(uint)

	if followerID == input.UserID {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "You cannot follow yourself"})
		return
	}

	var existing Follow
	res := Config.DB.Where("follower_id = ? AND following_id = ?", followerID, input.UserID).Limit(1).Find(&existing)

	isFollowing := false
	if res.RowsAffected > 0 {
		Config.DB.Delete(&existing)
		isFollowing = false
	} else {
		Config.DB.Create(&Follow{FollowerID: followerID, FollowingID: input.UserID})
		isFollowing = true
		// Notify the user being followed
		msg := "started following you"
		go createNotificationHelper(input.UserID, &followerID, "follow", nil, nil, msg)
	}

	c.JSON(http.StatusOK, gin.H{
		"status":       true,
		"is_following": isFollowing,
	})

	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "follow_update",
		Payload: gin.H{
			"user_id":      input.UserID,
			"is_following": isFollowing,
			"follower_id":  followerID,
		},
	})
}

func createNotificationHelper(receiverID uint, actorID *uint, nType string, videoID *uint, refID *uint, message string) {
	// Directly use Config.DB to create notification to avoid import cycle with Inbox package
	notification := map[string]interface{}{
		"user_id":      receiverID,
		"receiver_id":  receiverID,
		"actor_id":     actorID,
		"type":         nType,
		"video_id":     videoID,
		"reference_id": refID,
		"message":      message,
		"is_read":      false,
		"created_at":   time.Now(),
		"updated_at":   time.Now(),
	}

	if err := Config.DB.Table("notifications").Create(&notification).Error; err != nil {
		return
	}

	// Fetch full notification to broadcast with Actor info
	var fullNotification map[string]interface{}
	Config.DB.Table("notifications").
		Select("notifications.*, users.nickname as actor_name, users.avatar_url as actor_avatar, users.username as actor_username, videos.thumbnail_url as video_thumbnail").
		Joins("left join users on users.id = notifications.actor_id").
		Joins("left join videos on videos.id = notifications.video_id").
		Where("notifications.id = ?", notification["id"]).
		First(&fullNotification)

	// Broadcast via WebSocket
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:    "notification",
		Payload: fullNotification,
		Target:  receiverID,
	})

	// Send FCM Push Notification
	var receiver User
	if err := Config.DB.Select("fcm_token").First(&receiver, receiverID).Error; err == nil && receiver.FCMToken != "" {
		title := "New Notification"
		if actorName, ok := fullNotification["actor_name"].(string); ok && actorName != "" {
			title = actorName
		}
		data := map[string]string{
			"type": nType,
		}
		if videoID != nil {
			data["video_id"] = fmt.Sprintf("%d", *videoID)
		}
		Utils.SendFCMNotification(receiver.FCMToken, title, message, data)
	}
}

// IncrementViews handles POST /api/v1/video/:id/view
func IncrementViews(c *gin.Context) {
	videoID := c.Param("id")
	if err := Config.DB.Model(&Video{}).Where("id = ?", videoID).UpdateColumn("views", gorm.Expr("views + 1")).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to increment view"})
		return
	}

	var v Video
	if err := Config.DB.Select("user_id").Where("id = ?", videoID).Limit(1).Find(&v).Error; err == nil && v.ID != 0 {
		Studio.IncrementDailyStat(v.UserID, "views", 1)
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "View incremented"})
}
