package events

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"trex/backend/internal/model"
)

type Hub struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	clients  map[*websocket.Conn]struct{}
	upgrader websocket.Upgrader
}

func New() *Hub {
	return &Hub{
		clients: map[*websocket.Conn]struct{}{},
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.clients[conn] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, conn)
		h.mu.Unlock()
		_ = conn.Close()
	}()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *Hub) Publish(event model.Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	payload, _ := json.Marshal(event)
	h.mu.RLock()
	clients := make([]*websocket.Conn, 0, len(h.clients))
	for conn := range h.clients {
		clients = append(clients, conn)
	}
	h.mu.RUnlock()
	for _, conn := range clients {
		h.writeMu.Lock()
		_ = conn.WriteMessage(websocket.TextMessage, payload)
		h.writeMu.Unlock()
	}
}
