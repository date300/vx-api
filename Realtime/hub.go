package Realtime

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
	"vx-api/Config"

	"github.com/gorilla/websocket"
)

// Client represents a single connected user
type Client struct {
	UserID        uint
	Conn          *websocket.Conn
	Send          chan []byte
	LastHeartbeat time.Time
}

// Hub manages all connected clients and message broadcasting
type Hub struct {
	clients    map[uint][]*Client
	register   chan *Client
	unregister chan *Client
	broadcast  chan BroadcastMessage
	mu         sync.RWMutex
}

var OnMessageRead func(messageID uint, readerID uint)

type BroadcastMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
	Target  uint        `json:"target,omitempty"` // 0 means broadcast to all
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[uint][]*Client),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan BroadcastMessage, 1024), // Buffer to prevent deadlocks and handle bursts
	}
}

func (h *Hub) Run() {
	// Start Redis subscriber if Redis is available
	if Config.RedisClient != nil {
		go h.listenRedis()
	}

	// Ticker for cleaning up stale connections
	cleanupTicker := time.NewTicker(30 * time.Second)
	defer cleanupTicker.Stop()

	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client.UserID] = append(h.clients[client.UserID], client)
			h.mu.Unlock()

			if client.UserID > 0 {
				h.updatePresence(client.UserID, true)
			}

		case client := <-h.unregister:
			h.handleUnregister(client)

		case message := <-h.broadcast:
			h.broadcastToLocal(message)

		case <-cleanupTicker.C:
			h.cleanupStaleConnections()
		}
	}
}

func (h *Hub) handleUnregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	clients, ok := h.clients[client.UserID]
	if !ok {
		return
	}

	for i, c := range clients {
		if c == client {
			h.clients[client.UserID] = append(clients[:i], clients[i+1:]...)
			break
		}
	}

	if len(h.clients[client.UserID]) == 0 {
		delete(h.clients, client.UserID)
	}

	if client.UserID > 0 {
		h.updatePresence(client.UserID, false)
	}
	close(client.Send)
}

func (h *Hub) updatePresence(userID uint, isConnecting bool) {
	if Config.RedisClient == nil {
		// Fallback to DB only
		h.updateDBStatus(userID, isConnecting)
		return
	}

	key := fmt.Sprintf("vx:presence:count:%d", userID)
	var newCount int64
	var err error

	if isConnecting {
		newCount, err = Config.RedisClient.Incr(Config.Ctx, key).Result()
	} else {
		newCount, err = Config.RedisClient.Decr(Config.Ctx, key).Result()
		if newCount < 0 {
			Config.RedisClient.Set(Config.Ctx, key, 0, 0)
			newCount = 0
		}
	}

	if err == nil {
		if (isConnecting && newCount == 1) || (!isConnecting && newCount == 0) {
			h.updateDBStatus(userID, isConnecting)
			// Broadcast status change
			h.Broadcast(BroadcastMessage{
				Type: "user_status",
				Payload: map[string]interface{}{
					"user_id":   userID,
					"is_online": isConnecting,
					"last_seen": time.Now(),
				},
			})
		}
	}
}

func (h *Hub) updateDBStatus(userID uint, isOnline bool) {
	Config.DB.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{
		"is_online": isOnline,
		"last_seen": time.Now(),
	})
}

func (h *Hub) cleanupStaleConnections() {
	h.mu.Lock()
	var toUnregister []*Client

	now := time.Now()
	timeout := 90 * time.Second // If no heartbeat for 90s, connection is stale

	for _, clients := range h.clients {
		for _, client := range clients {
			if now.Sub(client.LastHeartbeat) > timeout {
				toUnregister = append(toUnregister, client)
			}
		}
	}
	h.mu.Unlock()

	for _, client := range toUnregister {
		println("DEBUG: Force closing stale connection for user:", client.UserID)
		client.Conn.Close() // This will trigger readPump error and eventually handleUnregister
	}
}

func (h *Hub) broadcastToLocal(message BroadcastMessage) {
	msgBytes, _ := json.Marshal(message)
	h.mu.RLock()
	defer h.mu.RUnlock()

	if message.Target > 0 {
		// Send to specific user
		if clients, ok := h.clients[message.Target]; ok {
			for _, client := range clients {
				select {
				case client.Send <- msgBytes:
				default:
				}
			}
		}
	} else {
		// Broadcast to all
		for _, clients := range h.clients {
			for _, client := range clients {
				select {
				case client.Send <- msgBytes:
				default:
				}
			}
		}
	}
}

func (h *Hub) listenRedis() {
	pubsub := Config.RedisClient.Subscribe(Config.Ctx, "vx_realtime")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for msg := range ch {
		var broadcastMsg BroadcastMessage
		if err := json.Unmarshal([]byte(msg.Payload), &broadcastMsg); err == nil {
			h.broadcast <- broadcastMsg
		}
	}
}

func (h *Hub) Broadcast(msg BroadcastMessage) {
	if Config.RedisClient != nil {
		msgBytes, _ := json.Marshal(msg)
		Config.RedisClient.Publish(Config.Ctx, "vx_realtime", msgBytes)
	} else {
		// Fallback to local only if Redis is not available
		h.broadcast <- msg
	}
}

var MainHub = NewHub()
