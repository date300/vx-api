package Inbox

import (
	"fmt"
	"time"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"vx-api/Config"
	
	"vx-api/Middleware"
	"vx-api/Realtime"
	"vx-api/Utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const defaultMessagePageSize = 50

func init() {
	Realtime.OnMessageRead = func(messageID uint, readerID uint) {
		var msg Message
		if err := Config.DB.First(&msg, messageID).Error; err != nil {
			return
		}

		// Only the receiver can mark a message as read
		if msg.ReceiverID != readerID || msg.IsRead {
			return
		}

		now := time.Now()
		Config.DB.Model(&msg).Updates(map[string]interface{}{
			"is_read": true,
			"read_at": &now,
		})

		// Broadcast back to the SENDER that their message was seen
		Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
			Type: "message_read",
			Payload: map[string]interface{}{
				"message_id":      msg.ID,
				"conversation_id": msg.ConversationID,
				"reader_id":       readerID,
				"read_at":         now,
			},
			Target: msg.SenderID,
		})
		// Broadcast to the READER too (for sync on other devices)
		Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
			Type: "message_read",
			Payload: map[string]interface{}{
				"message_id":      msg.ID,
				"conversation_id": msg.ConversationID,
				"reader_id":       readerID,
				"read_at":         now,
			},
			Target: readerID,
		})
	}
}

// RegisterRoutes handles all Inbox and Notification related endpoints
func RegisterRoutes(r *gin.RouterGroup) {
	inboxGroup := r.Group("/inbox", Middleware.AuthRequired())
	{
		inboxGroup.GET("/notifications", GetNotifications) // Fetch all notifications
		inboxGroup.PUT("/notifications/read", MarkAsRead)  // Mark all as read
		inboxGroup.GET("/unread-count", GetUnreadCount)    // Get total unread count
		inboxGroup.GET("/summary", GetNotificationSummary) // Get summary by category

		// Messaging
		inboxGroup.GET("/conversations", GetConversations)
		inboxGroup.GET("/messages/:target_id", GetMessages)
		inboxGroup.POST("/send", SendMessage)
		inboxGroup.POST("/send/voice", SendVoiceMessage)
		inboxGroup.POST("/send/image", SendImageMessage)
		inboxGroup.POST("/messages/:id/forward", ForwardMessage)
		inboxGroup.PATCH("/messages/:id", EditMessage)
		inboxGroup.DELETE("/messages/:id", DeleteMessage)
		inboxGroup.POST("/messages/:id/react", ReactMessage)
		inboxGroup.POST("/conversations/:id/pin", PinMessage)
		inboxGroup.DELETE("/conversations/:id/pin", UnpinMessage)
		inboxGroup.GET("/conversations/:id/pin", GetPinnedMessage)

		// Calls
		inboxGroup.GET("/calls/history", GetCallHistory)
		inboxGroup.POST("/calls/log", CreateCallLog)
	}
}

// GetConversations handles GET /api/v1/inbox/conversations
// প্রতিটা conversation-এ unread count ও "other user" যোগ করে দেয়, যাতে ফ্রন্টএন্ডে
// প্রতিটা কনভারসেশনের জন্য আলাদা করে গণনা করতে না হয়
func GetConversations(c *gin.Context) {
	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var conversations []Conversation
	if err := Config.DB.Preload("User1").Preload("User2").
		Where("user1_id = ? OR user2_id = ?", userID, userID).
		Order("updated_at desc").
		Find(&conversations).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to fetch conversations"})
		return
	}

	if len(conversations) == 0 {
		c.JSON(http.StatusOK, gin.H{"status": true, "data": []gin.H{}})
		return
	}

	convIDs := make([]uint, 0, len(conversations))
	for _, conv := range conversations {
		convIDs = append(convIDs, conv.ID)
	}

	// প্রতিটা conversation-এ আমার unread message সংখ্যা এক কোয়েরিতে বের করা
	type unreadRow struct {
		ConversationID uint
		Count          int64
	}
	var unreadRows []unreadRow
	Config.DB.Model(&Message{}).
		Select("conversation_id, count(*) as count").
		Where("conversation_id IN ? AND receiver_id = ? AND is_read = ?", convIDs, userID, false).
		Group("conversation_id").
		Scan(&unreadRows)

	unreadMap := make(map[uint]int64, len(unreadRows))
	for _, row := range unreadRows {
		unreadMap[row.ConversationID] = row.Count
	}

	data := make([]gin.H, 0, len(conversations))
	for _, conv := range conversations {
		otherUser := conv.User2
		if conv.User1ID != userID {
			otherUser = conv.User1
		}

		// Skip if user is missing
		if otherUser.ID == 0 {
			continue
		}

		data = append(data, gin.H{
			"id":            conv.ID,
			"other_user":    otherUser,
			"last_msg":      conv.LastMsg,
			"updated_at":    conv.UpdatedAt,
			"unread_count":  unreadMap[conv.ID],
			"is_online":     otherUser.IsOnline,
			"last_active":   formatLastActive(otherUser.LastSeen),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   data,
	})
}

// GetMessages handles GET /api/v1/inbox/messages/:target_id
// মেসেজ লোড করার সাথে সাথে ওই target-এর পাঠানো unread মেসেজগুলো read হিসেবে মার্ক করে দেয়
func GetMessages(c *gin.Context) {
	myIDRaw, _ := c.Get("userID")
	myID := myIDRaw.(uint)

	targetIDStr := c.Param("target_id")
	targetID64, err := strconv.ParseUint(targetIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid target ID"})
		return
	}
	targetID := uint(targetID64)

	// পেজিনেশন: ?before_id=123 দিয়ে পুরনো মেসেজ লোড করা যাবে (cursor-based)
	limit := defaultMessagePageSize
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	query := Config.DB.Preload("Sender").
		Where("(sender_id = ? AND receiver_id = ?) OR (sender_id = ? AND receiver_id = ?)", myID, targetID, targetID, myID)

	if beforeID, err := strconv.Atoi(c.Query("before_id")); err == nil && beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}

	// Mark messages from target as read BEFORE fetching to return updated state
	Config.DB.Model(&Message{}).
		Where("sender_id = ? AND receiver_id = ? AND is_read = ?", targetID, myID, false).
		Updates(map[string]interface{}{
			"is_read": true,
			"read_at": time.Now(),
		})

	// Broadcast to both parties that messages were read
	readPayload := map[string]interface{}{
		"reader_id": myID,
		"target_id": targetID,
	}
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:    "message_read",
		Payload: readPayload,
		Target:  targetID,
	})
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:    "message_read",
		Payload: readPayload,
		Target:  myID,
	})

	var messages []Message
	if err := query.Preload("Reactions").Preload("Parent").Preload("Parent.Sender").Order("created_at desc").Limit(limit).Find(&messages).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to fetch messages"})
		return
	}

	// সময়ানুক্রমে (পুরনো -> নতুন) সাজানোর জন্য রিভার্স
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   messages,
	})
}

// EditMessage handles PATCH /api/v1/inbox/messages/:id
func EditMessage(c *gin.Context) {
	myID, _ := c.Get("userID")
	msgIDStr := c.Param("id")
	msgID, _ := strconv.ParseUint(msgIDStr, 10, 64)

	var input struct {
		Text string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid input"})
		return
	}

	var msg Message
	if err := Config.DB.First(&msg, msgID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Message not found"})
		return
	}

	if msg.SenderID != myID.(uint) {
		c.JSON(http.StatusForbidden, gin.H{"status": false, "message": "Unauthorized"})
		return
	}

	if msg.IsDeleted {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Cannot edit deleted message"})
		return
	}

	now := time.Now()
	msg.Text = input.Text
	msg.IsEdited = true
	msg.EditedAt = &now

	if err := Config.DB.Save(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to update message"})
		return
	}

	// Broadcast
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_edit",
		Payload: map[string]interface{}{
			"message_id":      msg.ID,
			"conversation_id": msg.ConversationID,
			"text":            msg.Text,
			"is_edited":       true,
			"edited_at":       msg.EditedAt,
		},
		Target: msg.ReceiverID,
	})
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_edit",
		Payload: map[string]interface{}{
			"message_id":      msg.ID,
			"conversation_id": msg.ConversationID,
			"text":            msg.Text,
			"is_edited":       true,
			"edited_at":       msg.EditedAt,
		},
		Target: msg.SenderID,
	})

	c.JSON(http.StatusOK, gin.H{"status": true, "data": msg})
}

// DeleteMessage handles DELETE /api/v1/inbox/messages/:id
func DeleteMessage(c *gin.Context) {
	myID, _ := c.Get("userID")
	msgIDStr := c.Param("id")
	msgID, _ := strconv.ParseUint(msgIDStr, 10, 64)

	var msg Message
	if err := Config.DB.First(&msg, msgID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Message not found"})
		return
	}

	if msg.SenderID != myID.(uint) {
		c.JSON(http.StatusForbidden, gin.H{"status": false, "message": "Unauthorized"})
		return
	}

	now := time.Now()
	msg.IsDeleted = true
	msg.DeletedAt = &now
	msg.Text = "This message was deleted" // Optional placeholder in DB

	if err := Config.DB.Save(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to delete message"})
		return
	}

	// Broadcast
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_delete",
		Payload: map[string]interface{}{
			"message_id":      msg.ID,
			"conversation_id": msg.ConversationID,
			"is_deleted":      true,
			"deleted_at":      msg.DeletedAt,
		},
		Target: msg.ReceiverID,
	})
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_delete",
		Payload: map[string]interface{}{
			"message_id":      msg.ID,
			"conversation_id": msg.ConversationID,
			"is_deleted":      true,
			"deleted_at":      msg.DeletedAt,
		},
		Target: msg.SenderID,
	})

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Message deleted"})
}

// ReactMessage handles POST /api/v1/inbox/messages/:id/react
func ReactMessage(c *gin.Context) {
	myID, _ := c.Get("userID")
	msgIDStr := c.Param("id")
	msgID, _ := strconv.ParseUint(msgIDStr, 10, 64)

	var input struct {
		Reaction string `json:"reaction" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid input"})
		return
	}

	var msg Message
	if err := Config.DB.First(&msg, msgID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Message not found"})
		return
	}

	var reaction MessageReaction
	err := Config.DB.Where("message_id = ? AND user_id = ?", uint(msgID), myID.(uint)).First(&reaction).Error
	if err == nil {
		if reaction.Reaction == input.Reaction {
			// Remove reaction if same
			Config.DB.Delete(&reaction)
		} else {
			// Update reaction
			reaction.Reaction = input.Reaction
			Config.DB.Save(&reaction)
		}
	} else {
		// New reaction
		reaction = MessageReaction{
			MessageID: uint(msgID),
			UserID:    myID.(uint),
			Reaction:  input.Reaction,
		}
		Config.DB.Create(&reaction)
	}

	// Fetch all reactions for this message to broadcast
	var reactions []MessageReaction
	Config.DB.Where("message_id = ?", uint(msgID)).Find(&reactions)

	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_reaction",
		Payload: map[string]interface{}{
			"message_id":      msg.ID,
			"conversation_id": msg.ConversationID,
			"reactions":       reactions,
		},
		Target: msg.ReceiverID,
	})
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_reaction",
		Payload: map[string]interface{}{
			"message_id":      msg.ID,
			"conversation_id": msg.ConversationID,
			"reactions":       reactions,
		},
		Target: msg.SenderID,
	})

	c.JSON(http.StatusOK, gin.H{"status": true, "reactions": reactions})
}

// PinMessage handles POST /api/v1/inbox/conversations/:id/pin
func PinMessage(c *gin.Context) {
	myID, _ := c.Get("userID")
	convIDStr := c.Param("id")
	convID, _ := strconv.ParseUint(convIDStr, 10, 64)

	var input struct {
		MessageID uint `json:"message_id" binding:"required"`
		Hours     int  `json:"hours"` // 0 for permanent
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid input"})
		return
	}

	// Verify conversation access
	var conv Conversation
	if err := Config.DB.First(&conv, convID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Conversation not found"})
		return
	}
	if conv.User1ID != myID.(uint) && conv.User2ID != myID.(uint) {
		c.JSON(http.StatusForbidden, gin.H{"status": false, "message": "Unauthorized"})
		return
	}

	var expiresAt *time.Time
	if input.Hours > 0 {
		t := time.Now().Add(time.Duration(input.Hours) * time.Hour)
		expiresAt = &t
	}

	pinned := PinnedMessage{
		ConversationID: uint(convID),
		MessageID:      input.MessageID,
		UserID:         myID.(uint),
		ExpiresAt:      expiresAt,
	}

	// Replace existing pin for conversation
	Config.DB.Where("conversation_id = ?", uint(convID)).Delete(&PinnedMessage{})
	if err := Config.DB.Create(&pinned).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to pin message"})
		return
	}

	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_pin",
		Payload: map[string]interface{}{
			"conversation_id": conv.ID,
			"message_id":      pinned.MessageID,
			"expires_at":      pinned.ExpiresAt,
			"is_pinned":       true,
		},
		Target: conv.User1ID,
	})
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_pin",
		Payload: map[string]interface{}{
			"conversation_id": conv.ID,
			"message_id":      pinned.MessageID,
			"expires_at":      pinned.ExpiresAt,
			"is_pinned":       true,
		},
		Target: conv.User2ID,
	})

	c.JSON(http.StatusOK, gin.H{"status": true, "data": pinned})
}

// UnpinMessage handles DELETE /api/v1/inbox/conversations/:id/pin
func UnpinMessage(c *gin.Context) {
	myID, _ := c.Get("userID")
	convIDStr := c.Param("id")
	convID, _ := strconv.ParseUint(convIDStr, 10, 64)

	var conv Conversation
	if err := Config.DB.First(&conv, convID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Conversation not found"})
		return
	}
	if conv.User1ID != myID.(uint) && conv.User2ID != myID.(uint) {
		c.JSON(http.StatusForbidden, gin.H{"status": false, "message": "Unauthorized"})
		return
	}

	Config.DB.Where("conversation_id = ?", uint(convID)).Delete(&PinnedMessage{})

	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_pin",
		Payload: map[string]interface{}{
			"conversation_id": conv.ID,
			"is_pinned":       false,
		},
		Target: conv.User1ID,
	})
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "message_pin",
		Payload: map[string]interface{}{
			"conversation_id": conv.ID,
			"is_pinned":       false,
		},
		Target: conv.User2ID,
	})

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Unpinned"})
}

// GetPinnedMessage handles GET /api/v1/inbox/conversations/:id/pin
func GetPinnedMessage(c *gin.Context) {
	myID, _ := c.Get("userID")
	convIDStr := c.Param("id")
	convID, _ := strconv.ParseUint(convIDStr, 10, 64)

	var conv Conversation
	if err := Config.DB.First(&conv, convID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Conversation not found"})
		return
	}
	if conv.User1ID != myID.(uint) && conv.User2ID != myID.(uint) {
		c.JSON(http.StatusForbidden, gin.H{"status": false, "message": "Unauthorized"})
		return
	}

	var pinned PinnedMessage
	if err := Config.DB.Where("conversation_id = ?", uint(convID)).First(&pinned).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"status": true, "data": nil})
		return
	}

	// Check if expired
	if pinned.ExpiresAt != nil && pinned.ExpiresAt.Before(time.Now()) {
		Config.DB.Delete(&pinned)
		c.JSON(http.StatusOK, gin.H{"status": true, "data": nil})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "data": pinned})
}

// SendMessage handles POST /api/v1/inbox/send
func SendMessage(c *gin.Context) {
	myIDRaw, _ := c.Get("userID")
	myIDUint := myIDRaw.(uint)

	var input struct {
		ReceiverID uint    `json:"receiver_id" binding:"required"`
		Text       string  `json:"text"`
		StickerURL *string `json:"sticker_url"`
		StickerID  *uint   `json:"sticker_id"`
		ParentID   *uint   `json:"parent_id"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid input"})
		return
	}

	if input.Text == "" && input.StickerURL == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Message or sticker required"})
		return
	}

	if input.ReceiverID == myIDUint {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "নিজেকে মেসেজ পাঠানো যাবে না"})
		return
	}

	// receiver আসলেই আছে কিনা যাচাই
	var receiverExists int64
	Config.DB.Model(&User{}).Where("id = ?", input.ReceiverID).Count(&receiverExists)
	if receiverExists == 0 {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "প্রাপক খুঁজে পাওয়া যায়নি"})
		return
	}

	u1, u2 := myIDUint, input.ReceiverID
	if u1 > u2 {
		u1, u2 = u2, u1
	}

	var msg Message
	var conversation Conversation

	// Find-or-create conversation + create message — একটা transaction-এ, যাতে দুইজন
	// একসাথে প্রথম মেসেজ পাঠালে duplicate conversation তৈরি না হয়।
	// এটা পুরোপুরি race-proof করতে হলে (user1_id, user2_id)-এর উপর DB-লেভেল
	// unique constraint লাগবে — সেটা না থাকলে extreme concurrency-তে এখনও ছোট
	// একটা window থাকতে পারে। থাকলে সবচেয়ে ভালো, নিচের কোড দুটোই handle করে।
	err := Config.DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("user1_id = ? AND user2_id = ?", u1, u2).First(&conversation).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			conversation = Conversation{User1ID: u1, User2ID: u2}
			if createErr := tx.Create(&conversation).Error; createErr != nil {
				// আরেকটা রিকোয়েস্ট একই সময়ে conversation বানিয়ে ফেলেছে হতে পারে
				// (unique constraint violation) -> আবার fetch করা
				if fetchErr := tx.Where("user1_id = ? AND user2_id = ?", u1, u2).First(&conversation).Error; fetchErr != nil {
					return createErr
				}
			}
		}

		msg = Message{
			ConversationID: conversation.ID,
			SenderID:       myIDUint,
			ReceiverID:     input.ReceiverID,
			Text:           input.Text,
			StickerURL:     input.StickerURL,
			StickerID:      input.StickerID,
			ParentID:       input.ParentID,
			IsRead:         false,
		}
		if err := tx.Create(&msg).Error; err != nil {
			return err
		}

		if input.ParentID != nil {
			tx.Preload("Sender").First(&msg.Parent, *input.ParentID)
		}

		if msg.StickerID != nil {
			tx.Table("stickers").Where("id = ?", *msg.StickerID).UpdateColumn("usage_count", gorm.Expr("usage_count + 1"))
		}

		lastText := input.Text
		if input.StickerURL != nil && lastText == "" {
			lastText = "[Sticker]"
		}

		return tx.Model(&conversation).Updates(map[string]interface{}{
			"last_msg":   lastText,
			"updated_at": msg.CreatedAt,
		}).Error
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to send message"})
		return
	}

	// Broadcast via WebSocket (Realtime)
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:    "chat_message",
		Payload: msg,
		Target:  input.ReceiverID,
	})

	// Send FCM Push Notification for Message
	var sender User
	Config.DB.Select("nickname").First(&sender, myIDUint)
	var receiver User
	if err := Config.DB.Select("fcm_token").First(&receiver, input.ReceiverID).Error; err == nil && receiver.FCMToken != "" {
		body := input.Text
		if body == "" && input.StickerURL != nil {
			body = "[Sticker]"
		}
		Utils.SendFCMNotification(receiver.FCMToken, sender.Nickname, body, map[string]string{
			"type":            "chat_message",
			"sender_id":       strconv.FormatUint(uint64(myIDUint), 10),
			"conversation_id": strconv.FormatUint(uint64(msg.ConversationID), 10),
		})
	}

	c.JSON(http.StatusCreated, gin.H{
		"status": true,
		"data":   msg,
	})
}

// SendVoiceMessage handles POST /api/v1/inbox/send/voice
func SendVoiceMessage(c *gin.Context) {
	myIDRaw, _ := c.Get("userID")
	myIDUint := myIDRaw.(uint)

	receiverIDStr := c.PostForm("receiver_id")
	receiverID, _ := strconv.ParseUint(receiverIDStr, 10, 32)

	parentIDStr := c.PostForm("parent_id")
	var parentID *uint
	if parentIDStr != "" {
		pid, _ := strconv.ParseUint(parentIDStr, 10, 32)
		p := uint(pid)
		parentID = &p
	}

	durationStr := c.PostForm("duration")
	duration, _ := strconv.Atoi(durationStr)

	file, err := c.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Audio file required"})
		return
	}

	voiceDir := "public/uploads/audios"
	if err := os.MkdirAll(voiceDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to create directory"})
		return
	}

	ext := filepath.Ext(file.Filename)
	if ext == "" {
		ext = ".m4a"
	}
	filename := fmt.Sprintf("chat_voice_%d_%d%s", myIDUint, time.Now().UnixNano(), ext)
	fullPath := filepath.Join(voiceDir, filename)

	if err := c.SaveUploadedFile(file, fullPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to save audio"})
		return
	}

	voiceURL := fmt.Sprintf("/uploads/audios/%s", filename)
	cldFolder := fmt.Sprintf("vx-app/users/%d/messages", myIDUint)
	// Upload to Cloudinary
	if cldURL, err := Utils.UploadToCloudinary(fullPath, "video", cldFolder); err == nil && cldURL != "" {
		voiceURL = cldURL
		// Delete local file after successful Cloudinary upload
		os.Remove(fullPath)
	}

	u1, u2 := myIDUint, uint(receiverID)
	if u1 > u2 {
		u1, u2 = u2, u1
	}

	var msg Message
	var conversation Conversation

	err = Config.DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("user1_id = ? AND user2_id = ?", u1, u2).First(&conversation).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			conversation = Conversation{User1ID: u1, User2ID: u2}
			tx.Create(&conversation)
		}

		msg = Message{
			ConversationID: conversation.ID,
			SenderID:       myIDUint,
			ReceiverID:     uint(receiverID),
			Text:           "Voice Message",
			IsVoice:       true,
			VoiceURL:      &voiceURL,
			VoiceDuration: duration,
			ParentID:      parentID,
			IsRead:         false,
		}
		if err := tx.Create(&msg).Error; err != nil {
			return err
		}

		return tx.Model(&conversation).Updates(map[string]interface{}{
			"last_msg":   "[Voice Message]",
			"updated_at": msg.CreatedAt,
		}).Error
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to send voice message"})
		return
	}

	// Broadcast via WebSocket
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:    "chat_message",
		Payload: msg,
		Target:  uint(receiverID),
	})

	c.JSON(http.StatusCreated, gin.H{"status": true, "data": msg})
}

// SendImageMessage handles POST /api/v1/inbox/send/image
func SendImageMessage(c *gin.Context) {
	myIDRaw, _ := c.Get("userID")
	myIDUint := myIDRaw.(uint)

	receiverIDStr := c.PostForm("receiver_id")
	receiverID, _ := strconv.ParseUint(receiverIDStr, 10, 32)

	parentIDStr := c.PostForm("parent_id")
	var parentID *uint
	if parentIDStr != "" {
		pid, _ := strconv.ParseUint(parentIDStr, 10, 32)
		p := uint(pid)
		parentID = &p
	}

	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Image file required"})
		return
	}

	imageDir := "public/uploads/chat_images"
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to create directory"})
		return
	}

	ext := filepath.Ext(file.Filename)
	if ext == "" {
		ext = ".jpg"
	}
	filename := fmt.Sprintf("chat_img_%d_%d%s", myIDUint, time.Now().UnixNano(), ext)
	fullPath := filepath.Join(imageDir, filename)

	if err := c.SaveUploadedFile(file, fullPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to save image"})
		return
	}

	imageURL := fmt.Sprintf("/uploads/chat_images/%s", filename)
	cldFolder := fmt.Sprintf("vx-app/users/%d/messages", myIDUint)
	// Upload to Cloudinary
	if cldURL, err := Utils.UploadToCloudinary(fullPath, "image", cldFolder); err == nil && cldURL != "" {
		imageURL = cldURL
		os.Remove(fullPath)
	}

	u1, u2 := myIDUint, uint(receiverID)
	if u1 > u2 {
		u1, u2 = u2, u1
	}

	var msg Message
	var conversation Conversation

	err = Config.DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("user1_id = ? AND user2_id = ?", u1, u2).First(&conversation).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			conversation = Conversation{User1ID: u1, User2ID: u2}
			tx.Create(&conversation)
		}

		msg = Message{
			ConversationID: conversation.ID,
			SenderID:       myIDUint,
			ReceiverID:     uint(receiverID),
			Text:           "Image Message",
			IsImage:       true,
			ImageURL:      &imageURL,
			ParentID:      parentID,
			IsRead:         false,
		}
		if err := tx.Create(&msg).Error; err != nil {
			return err
		}

		return tx.Model(&conversation).Updates(map[string]interface{}{
			"last_msg":   "[Image Message]",
			"updated_at": msg.CreatedAt,
		}).Error
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to send image message"})
		return
	}

	// Broadcast via WebSocket
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:    "chat_message",
		Payload: msg,
		Target:  uint(receiverID),
	})

	c.JSON(http.StatusCreated, gin.H{"status": true, "data": msg})
}

// ForwardMessage handles POST /api/v1/inbox/messages/:id/forward
func ForwardMessage(c *gin.Context) {
	myID, _ := c.Get("userID")
	msgIDStr := c.Param("id")
	msgID, _ := strconv.ParseUint(msgIDStr, 10, 64)

	var input struct {
		TargetUserID uint `json:"target_user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid input"})
		return
	}

	var originalMsg Message
	if err := Config.DB.First(&originalMsg, msgID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Message not found"})
		return
	}

	if originalMsg.IsDeleted {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Cannot forward deleted message"})
		return
	}

	// Create a new message that is a forward of the original
	u1, u2 := myID.(uint), input.TargetUserID
	if u1 > u2 {
		u1, u2 = u2, u1
	}

	var newMsg Message
	var conversation Conversation

	err := Config.DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("user1_id = ? AND user2_id = ?", u1, u2).First(&conversation).Error
		if err != nil {
			conversation = Conversation{User1ID: u1, User2ID: u2}
			if err := tx.Create(&conversation).Error; err != nil {
				return err
			}
		}

		newMsg = Message{
			ConversationID: conversation.ID,
			SenderID:       myID.(uint),
			ReceiverID:     input.TargetUserID,
			Text:           originalMsg.Text,
			StickerURL:     originalMsg.StickerURL,
			StickerID:      originalMsg.StickerID,
			IsForwarded:    true,
			IsRead:         false,
		}
		if err := tx.Create(&newMsg).Error; err != nil {
			return err
		}

		lastText := newMsg.Text
		if lastText == "" && newMsg.StickerURL != nil {
			lastText = "[Sticker]"
		}

		return tx.Model(&conversation).Updates(map[string]interface{}{
			"last_msg":   lastText,
			"updated_at": newMsg.CreatedAt,
		}).Error
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to forward message"})
		return
	}

	// Broadcast
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:    "chat_message",
		Payload: newMsg,
		Target:  input.TargetUserID,
	})

	c.JSON(http.StatusCreated, gin.H{"status": true, "data": newMsg})
}

func formatLastActive(t time.Time) string {
	if t.IsZero() {
		return "long ago"
	}
	diff := time.Since(t)
	if diff.Minutes() < 1 {
		return "Just now"
	}
	if diff.Minutes() < 60 {
		return fmt.Sprintf("%d min ago", int(diff.Minutes()))
	}
	if diff.Hours() < 24 {
		return fmt.Sprintf("%d hours ago", int(diff.Hours()))
	}
	return t.Format("02 Jan")
}

// Category Model
type Category struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Name      string         `gorm:"type:varchar(50);unique;not null" json:"name"`
	Slug      string         `gorm:"type:varchar(50);unique;not null" json:"slug"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// User Model
type User struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	Email        string         `gorm:"type:varchar(100);unique;not null" json:"email"`
	Provider     string         `gorm:"type:varchar(20);not null" json:"provider"`
	Nickname     string         `gorm:"type:varchar(100)" json:"nickname"`
	Username     *string        `gorm:"type:varchar(50);unique" json:"username"`
	IsOnboarded  bool           `gorm:"default:false" json:"is_onboarded"`
	Bio          string         `gorm:"type:text" json:"bio"`
	AvatarURL    string         `gorm:"type:text" json:"avatar_url"`
	CoverURL     string         `gorm:"type:text" json:"cover_url"`
	Following    int            `gorm:"default:0" json:"following"`
	Followers    int            `gorm:"default:0" json:"followers"`
	Likes        int            `gorm:"default:0" json:"likes"`
	InstagramURL string         `gorm:"type:text" json:"instagram_url"`
	YoutubeURL   string         `gorm:"type:text" json:"youtube_url"`
	FacebookURL  string         `gorm:"type:text" json:"facebook_url"`
	FCMToken     string         `gorm:"type:text" json:"fcm_token"`
	IsVerified   bool           `gorm:"default:false" json:"is_verified"`
	IsAdmin      bool           `gorm:"default:false" json:"is_admin"`
	IsBanned     bool           `gorm:"default:false" json:"is_banned"`
	IsOnline     bool           `gorm:"default:false" json:"is_online"`
	LastSeen     time.Time      `json:"last_seen"`
	RefreshToken string         `gorm:"type:text" json:"-"`
	OTPCode      string         `gorm:"type:varchar(6)" json:"-"`
	OTPExpiresAt *time.Time     `gorm:"index" json:"-"`
	Interests    []Category     `gorm:"many2many:user_interests;" json:"interests"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// Conversation Model
type Conversation struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	User1ID   uint           `gorm:"index;not null" json:"user1_id"`
	User1     User           `gorm:"foreignKey:User1ID" json:"user1"`
	User2ID   uint           `gorm:"index;not null" json:"user2_id"`
	User2     User           `gorm:"foreignKey:User2ID" json:"user2"`
	LastMsg   string         `gorm:"type:text" json:"last_msg"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Message Model
type Message struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	ConversationID uint       `gorm:"index;not null" json:"conversation_id"`
	SenderID       uint       `gorm:"index;not null" json:"sender_id"`
	Sender         User       `gorm:"foreignKey:SenderID" json:"sender"`
	ReceiverID     uint       `gorm:"index;not null" json:"receiver_id"`
	Text           string     `gorm:"type:text;not null" json:"text"`
	StickerURL     *string    `gorm:"type:text" json:"sticker_url"`
	StickerID      *uint      `gorm:"index" json:"sticker_id"`
	IsVoice       bool           `gorm:"default:false" json:"is_voice"`
	VoiceURL      *string        `gorm:"type:text" json:"voice_url"`
	VoiceDuration int            `gorm:"default:0" json:"voice_duration"`
	IsImage       bool           `gorm:"default:false" json:"is_image"`
	ImageURL      *string        `gorm:"type:text" json:"image_url"`
	IsRead         bool       `gorm:"default:false" json:"is_read"`
	ReadAt         *time.Time `json:"read_at"`
	IsEdited       bool       `gorm:"default:false" json:"is_edited"`
	EditedAt       *time.Time `json:"edited_at"`
	IsDeleted      bool               `gorm:"default:false" json:"is_deleted"`
	DeletedAt      *time.Time         `json:"deleted_at"`
	IsForwarded    bool               `gorm:"default:false" json:"is_forwarded"`
	ParentID       *uint              `gorm:"index" json:"parent_id"`
	Parent         *Message           `gorm:"foreignKey:ParentID" json:"parent"`
	CreatedAt      time.Time          `json:"created_at"`
	Reactions      []MessageReaction  `gorm:"foreignKey:MessageID" json:"reactions"`
}

type MessageReaction struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	MessageID uint      `gorm:"index;not null" json:"message_id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	Reaction  string    `gorm:"type:varchar(20);not null" json:"reaction"`
	CreatedAt time.Time `json:"created_at"`
}

type PinnedMessage struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	ConversationID uint       `gorm:"index;unique;not null" json:"conversation_id"`
	MessageID      uint       `gorm:"not null" json:"message_id"`
	UserID         uint       `gorm:"not null" json:"user_id"`
	ExpiresAt      *time.Time `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
}
