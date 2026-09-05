package Realtime

import (
	"encoding/json"
	"net/http"
	"time"
	"vx-api/Middleware"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // In production, check origin properly
	},
}

func HandleWS(c *gin.Context) {
	// Auth check (if needed, userID should be in context via Middleware)
	userIDInterface, _ := c.Get("userID")
	userID, _ := userIDInterface.(uint)

	token := c.Query("token")
	println("DEBUG: WS Connection attempt - UserID:", userID, "Token length:", len(token))

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		println("DEBUG: WS Upgrade Error:", err.Error())
		return
	}
	println("DEBUG: WS Upgrade Successful")

	client := &Client{
		UserID:        userID,
		Conn:          conn,
		Send:          make(chan []byte, 256),
		LastHeartbeat: time.Now(),
	}

	MainHub.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		MainHub.unregister <- c
		c.Conn.Close()
	}()
	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}

		var msg BroadcastMessage
		if err := json.Unmarshal(message, &msg); err == nil {
			// Update heartbeat timestamp
			c.LastHeartbeat = time.Now()

			// If it's a typing event, ensure SenderID is correct and broadcast
			if msg.Type == "typing" {
				payload, ok := msg.Payload.(map[string]interface{})
				if ok {
					payload["sender_id"] = c.UserID
					msg.Payload = payload
					MainHub.Broadcast(msg)
				}
			} else if msg.Type == "heartbeat" {
				// Heartbeat handled, no further action needed
				continue
			} else if msg.Type == "message_read" {
				payload, ok := msg.Payload.(map[string]interface{})
				if ok {
					msgIDFloat, _ := payload["message_id"].(float64)
					msgID := uint(msgIDFloat)
					if msgID > 0 && OnMessageRead != nil {
						OnMessageRead(msgID, c.UserID)
					}
				}
			} else if msg.Type == "call_invite" || msg.Type == "call_accept" || msg.Type == "call_reject" || msg.Type == "call_hangup" {
				// Signal message - add SenderID and broadcast to target
				payload, ok := msg.Payload.(map[string]interface{})
				if ok {
					payload["sender_id"] = c.UserID
					msg.Payload = payload
					MainHub.Broadcast(msg)
				}
			}
		}
	}
}

func (c *Client) writePump() {
	defer func() {
		c.Conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.Send:
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.Conn.WriteMessage(websocket.TextMessage, message)
		}
	}
}

func RegisterRoutes(r *gin.RouterGroup) {
	// OptionalAuth ensures connection succeeds even if token is missing/expired
	r.GET("/ws", Middleware.OptionalAuth(), HandleWS)
}
