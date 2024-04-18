package chat_room

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"log/slog"
	"sync"
	chat_db "web_server/db"
	chat_type "web_server/type"
)

const maxHistoryCount = 100

type Room struct {
	ctx  context.Context
	stop context.CancelFunc
	init sync.Once
	// Registered clients.
	clients map[*Client]bool

	// Inbound messages from the clients.
	broadcast chan chat_type.Message

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	// Current room
	*chat_type.ChatRoom
}

func newRoom(room *chat_type.ChatRoom) *Room {
	ctx, cancel := context.WithCancel(context.Background())
	return &Room{
		ctx:        ctx,
		stop:       cancel,
		broadcast:  make(chan chat_type.Message),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
		ChatRoom:   room,
	}
}

// BroadCast 全房间广播消息
func (h *Room) BroadCast(m chat_type.Message) {
	h.broadcast <- m
}

// UserJoin 将用户加入房间
func (h *Room) UserJoin(conn *websocket.Conn, user *chat_type.User) {
	ctx, cancel := context.WithCancel(h.ctx)
	client := &Client{
		ctx:  ctx,
		stop: cancel,
		User: user,
		conn: conn,
		onMessage: func(u *chat_type.User, m chat_type.Message) error {
			h.BroadCast(m)
			return nil
		},
		send: make(chan []byte),
		onClientLeave: func(c *Client) {
			slog.Info("user leave", "id", c.UserID, "userName", c.UserName, "roomName", h.RoomName)
			h.unregister <- c
			h.broadRoomUserCountMessage()
		},
	}
	client.Serve()
	slog.Info("new user join", "id", user.UserID, "userName", user.UserName, "roomName", h.RoomName)
	h.register <- client
	h.broadRoomUserCountMessage()
}

// sendHistory 发送历史消息
func (h *Room) sendHistory(c *Client) {
	var messages []chat_type.Message
	if len(h.Messages) > maxHistoryCount {
		// 保留最新100条
		messages = h.Messages[len(h.Messages)-maxHistoryCount:]
	} else {
		messages = h.Messages
	}

	for _, message := range messages {
		_ = c.Send(message)
	}
	_ = c.Send(chat_type.Message{Type: "over", RoomName: h.RoomName})
}

func (h *Room) sendRoomList(c *Client) {
	_ = c.Send(chat_type.Message{Type: "roomList", ChatRoomList: ListChatRoom()})
}

func (h *Room) broadRoomUserCountMessage() {
	slog.Info("broadcast room user count", "roomName", h.RoomName)
	// 筛选出房间内所有用户，注册的和没注册的分开
	userCount := len(h.clients)
	type RoomCount struct {
		UserCount int
		RoomName  string
	}
	roomCount := RoomCount{
		UserCount: userCount,
		RoomName:  h.RoomName,
	}
	// 转换成json
	jsonData, err := json.Marshal(roomCount)
	if err != nil {
		slog.Error("json marshal error", "error", err)
		return
	}
	slog.Info("broadcast room user count end", "roomName", h.RoomName, "userCount", userCount)
	if userCount <= 0 {
		return
	}
	h.BroadCast(chat_type.Message{Type: "userCount", Data: jsonData})
}

func (h *Room) serve() {
	for {
		select {
		case <-h.ctx.Done():
			close(h.register)
			close(h.unregister)
			close(h.broadcast)
			return
		case client := <-h.register:
			h.clients[client] = true
			slog.Info("new user register", "id", client.UserID, "userName", client.UserName, "roomName", h.RoomName)
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Stop()
			}
			//if len(h.clients) == 0 {
			//	slog.Warn("room is empty", "roomName", h.RoomName)
			//	RemoveChatRoom(h.RoomName)
			//}
		case message := <-h.broadcast:
			switch message.Type {
			case "text", "image", "file":
				h.Messages = append(h.Messages, message)
				_ = chat_db.WriteChatInfoToLocalFile(h.ChatRoom)
			}
			for client := range h.clients {
				_ = client.Send(message)
			}
		}
	}
}

func (h *Room) Serve() {
	h.init.Do(func() {
		slog.Info("room serve", "roomName", h.RoomName)
		go h.serve()
	})
}

func (h *Room) UserCount() int {
	return len(h.clients)
}

func (h *Room) Stop() {
	h.stop()
}
