package Inbox

import (
	"net/http"
	"strconv"
	"time"

	"vx-api/Config"
	"vx-api/Realtime"
	"vx-api/Utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Notification Model
type Notification struct {
	ID          uint               `gorm:"primaryKey" json:"id"`
	SenderID    *uint              `gorm:"index" json:"sender_id"`
	ReceiverID  uint               `gorm:"index;not null" json:"receiver_id"`
	Type        string             `gorm:"type:varchar(50);not null" json:"type"` // like, comment, follow, system
	ReferenceID *uint              `json:"reference_id"`
	Title       string             `gorm:"type:varchar(255)" json:"title"`
	Body        string             `gorm:"type:text" json:"body"`
	UserID      uint               `gorm:"index;not null" json:"user_id"`
	ActorID     *uint              `gorm:"index" json:"actor_id"`
	Actor       User               `gorm:"foreignKey:ActorID" json:"actor"`
	Message     string             `gorm:"type:text" json:"message"`
	Content     string             `gorm:"type:text" json:"content"` // For comment text etc
	VideoID     *uint              `gorm:"index" json:"video_id"`
	Video       *NotificationVideo `gorm:"foreignKey:VideoID" json:"video"`
	IsRead      bool               `gorm:"default:false" json:"is_read"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	DeletedAt   gorm.DeletedAt     `gorm:"index" json:"-"`
}

type NotificationVideo struct {
	ID           uint   `json:"id"`
	ThumbnailURL string `json:"thumbnail_url"`
}

func (NotificationVideo) TableName() string {
	return "videos"
}

// GetNotifications handles GET /api/v1/inbox/notifications
func GetNotifications(c *gin.Context) {
	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var notifications []Notification
	if err := Config.DB.Preload("Actor").Preload("Video").
		Where("user_id = ?", userID).
		Order("created_at desc").
		Find(&notifications).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to fetch notifications"})
		return
	}

	// Enrich actors with follow status
	actorIDs := make([]uint, 0)
	for _, n := range notifications {
		if n.ActorID != nil {
			actorIDs = append(actorIDs, *n.ActorID)
		}
	}

	followingMap := make(map[uint]bool)
	if len(actorIDs) > 0 {
		var follows []struct{ FollowingID uint }
		Config.DB.Table("follows").Where("follower_id = ? AND following_id IN ?", userID, actorIDs).Find(&follows)
		for _, f := range follows {
			followingMap[f.FollowingID] = true
		}
	}

	// Create response data with is_following flag
	var data []gin.H
	for _, n := range notifications {
		isFollowing := false
		if n.ActorID != nil {
			isFollowing = followingMap[*n.ActorID]
		}

		data = append(data, gin.H{
			"id":             n.ID,
			"user_id":        n.UserID,
			"receiver_id":    n.ReceiverID,
			"actor_id":       n.ActorID,
			"actor":          n.Actor,
			"type":           n.Type,
			"video_id":       n.VideoID,
			"video":          n.Video,
			"reference_id":   n.ReferenceID,
			"message":        n.Message,
			"content":        n.Content,
			"is_read":        n.IsRead,
			"is_following":   isFollowing,
			"created_at":     n.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   data,
	})
}

// CreateNotification creates a new notification and broadcasts it via WebSocket
func CreateNotification(receiverID uint, actorID *uint, nType string, videoID *uint, refID *uint, message string) {
	var content string
	if nType == "comment" && refID != nil {
		// Fetch comment text
		var comment struct{ Text string }
		Config.DB.Table("comments").Select("text").Where("id = ?", *refID).First(&comment)
		content = comment.Text
	}

	notification := Notification{
		ReceiverID:  receiverID,
		UserID:      receiverID,
		ActorID:     actorID,
		Type:        nType,
		VideoID:     videoID,
		ReferenceID: refID,
		Message:     message,
		Content:     content,
		IsRead:      false,
	}

	if err := Config.DB.Create(&notification).Error; err != nil {
		return
	}

	// Preload Actor and Video for the broadcast
	Config.DB.Preload("Actor").Preload("Video").First(&notification, notification.ID)

	// Broadast data
	payload := map[string]interface{}{
		"id":             notification.ID,
		"user_id":        notification.UserID,
		"receiver_id":    notification.ReceiverID,
		"actor_id":       notification.ActorID,
		"actor_name":     notification.Actor.Nickname,
		"actor_username": notification.Actor.Username,
		"actor_avatar":   notification.Actor.AvatarURL,
		"type":           notification.Type,
		"video_id":       notification.VideoID,
		"video":          notification.Video,
		"reference_id":   notification.ReferenceID,
		"message":        notification.Message,
		"content":        notification.Content,
		"is_read":        notification.IsRead,
		"created_at":     notification.CreatedAt,
	}

	// Broadcast via WebSocket
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:    "notification",
		Payload: payload,
		Target:  receiverID,
	})

	// Send FCM Push Notification
	var receiver User
	if err := Config.DB.Select("fcm_token").First(&receiver, receiverID).Error; err == nil && receiver.FCMToken != "" {
		title := "New Notification"
		if notification.Actor.Nickname != "" {
			title = notification.Actor.Nickname
		}
		data := map[string]string{
			"type": notification.Type,
		}
		if notification.VideoID != nil {
			data["video_id"] = strconv.FormatUint(uint64(*notification.VideoID), 10)
		}
		Utils.SendFCMNotification(receiver.FCMToken, title, message, data)
	}
}

// MarkAsRead handles PUT /api/v1/inbox/notifications/read
func MarkAsRead(c *gin.Context) {
	userID, _ := c.Get("userID")

	if err := Config.DB.Model(&Notification{}).
		Where("user_id = ? AND is_read = ?", userID, false).
		Update("is_read", true).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to update notifications"})
		return
	}

	// Broadcast to the user that notifications were marked as read to update counts in real-time
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:   "refresh_notifications",
		Target: userID.(uint),
	})

	c.JSON(http.StatusOK, gin.H{
		"status":  true,
		"message": "All notifications marked as read",
	})
}

// GetUnreadCount handles GET /api/v1/inbox/unread-count
func GetUnreadCount(c *gin.Context) {
	userID, _ := c.Get("userID")

	var notifCount int64
	Config.DB.Model(&Notification{}).
		Where("user_id = ? AND is_read = ?", userID, false).
		Count(&notifCount)

	var msgCount int64
	Config.DB.Model(&Message{}).
		Where("receiver_id = ? AND is_read = ?", userID, false).
		Count(&msgCount)

	c.JSON(http.StatusOK, gin.H{
		"status":            true,
		"unread_count":      notifCount + msgCount,
		"notifications":     notifCount,
		"messages":          msgCount,
	})
}

// GetNotificationSummary handles GET /api/v1/inbox/summary
func GetNotificationSummary(c *gin.Context) {
	userID, _ := c.Get("userID")

	var likes int64
	Config.DB.Model(&Notification{}).Where("user_id = ? AND type = ? AND is_read = ?", userID, "like", false).Count(&likes)

	var comments int64
	Config.DB.Model(&Notification{}).Where("user_id = ? AND type = ? AND is_read = ?", userID, "comment", false).Count(&comments)

	var followers int64
	Config.DB.Model(&Notification{}).Where("user_id = ? AND type = ? AND is_read = ?", userID, "follow", false).Count(&followers)

	var messages int64
	Config.DB.Model(&Message{}).Where("receiver_id = ? AND is_read = ?", userID, false).Count(&messages)

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data": gin.H{
			"likes":     likes,
			"comments":  comments,
			"followers": followers,
			"messages":  messages,
			"shares":    0,
		},
	})
}
