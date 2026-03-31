package handlers

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512
	sendBufSize    = 16
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Hub maintains all active WebSocket connections and broadcasts messages.
type Hub struct {
	clients    map[*wsClient]bool
	broadcast  chan []byte
	register   chan *wsClient
	unregister chan *wsClient
}

// NewHub creates a Hub ready to be started with Run.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan []byte, 32),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
	}
}

// Run processes hub events. Call in a dedicated goroutine.
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					delete(h.clients, c)
					close(c.send)
				}
			}
		}
	}
}

// Broadcast sends a JSON event message to all connected clients.
func (h *Hub) Broadcast(eventType string) {
	msg := []byte(`{"type":"` + eventType + `"}`)
	select {
	case h.broadcast <- msg:
	default:
		log.Println("ws: broadcast channel full, dropping event:", eventType)
	}
}

// ServeWs returns an HTTP handler that upgrades the connection.
func ServeWs(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("ws upgrade:", err)
			return
		}
		c := &wsClient{hub: hub, conn: conn, send: make(chan []byte, sendBufSize)}
		hub.register <- c
		go c.writePump()
		go c.readPump()
	}
}

type wsClient struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

func (c *wsClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break
		}
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
