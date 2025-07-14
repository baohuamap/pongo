package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	gamev1 "github.com/baohuamap/pongo/proto/game/v1"
)

type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	rooms      map[string]map[*Client]bool
	mutex      sync.RWMutex
}

type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	playerID string
	roomID   string
}

type Message struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type GameStateMessage struct {
	Type      string            `json:"type"`
	GameState *gamev1.GameState `json:"gameState"`
}

type PlayerInputMessage struct {
	Type   string              `json:"type"`
	RoomID string              `json:"roomId"`
	Input  *gamev1.PlayerInput `json:"input"`
}

type JoinRoomMessage struct {
	Type     string `json:"type"`
	RoomID   string `json:"roomId"`
	PlayerID string `json:"playerId"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins in development
	},
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		rooms:      make(map[string]map[*Client]bool),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mutex.Lock()
			h.clients[client] = true
			if h.rooms[client.roomID] == nil {
				h.rooms[client.roomID] = make(map[*Client]bool)
			}
			h.rooms[client.roomID][client] = true
			h.mutex.Unlock()

			log.Printf("Client %s joined room %s", client.playerID, client.roomID)

			// Send welcome message
			welcome := Message{
				Type: "welcome",
				Data: map[string]string{
					"message": "Connected to game server",
					"roomId":  client.roomID,
				},
			}
			if data, err := json.Marshal(welcome); err == nil {
				select {
				case client.send <- data:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}

		case client := <-h.unregister:
			h.mutex.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				if roomClients, exists := h.rooms[client.roomID]; exists {
					delete(roomClients, client)
					if len(roomClients) == 0 {
						delete(h.rooms, client.roomID)
					}
				}
				close(client.send)
				log.Printf("Client %s left room %s", client.playerID, client.roomID)
			}
			h.mutex.Unlock()

		case message := <-h.broadcast:
			h.mutex.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mutex.RUnlock()
		}
	}
}

func (h *Hub) BroadcastToRoom(roomID string, message []byte) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	if roomClients, exists := h.rooms[roomID]; exists {
		for client := range roomClients {
			select {
			case client.send <- message:
			default:
				close(client.send)
				delete(h.clients, client)
				delete(roomClients, client)
			}
		}
	}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, playerID, roomID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	client := &Client{
		hub:      h,
		conn:     conn,
		send:     make(chan []byte, 256),
		playerID: playerID,
		roomID:   roomID,
	}

	client.hub.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}

		// Handle incoming messages
		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Error unmarshaling message: %v", err)
			continue
		}

		switch msg.Type {
		case "player_input":
			// Handle player input - this would typically call your game engine
			log.Printf("Received player input from %s: %v", c.playerID, msg.Data)
			// You would process this input through your game engine here

		case "ping":
			// Respond to ping
			pong := Message{Type: "pong", Data: map[string]interface{}{"timestamp": time.Now().UnixMilli()}}
			if data, err := json.Marshal(pong); err == nil {
				select {
				case c.send <- data:
				default:
					return
				}
			}
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current websocket message
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// GameStateUpdater handles broadcasting game state updates
type GameStateUpdater struct {
	hub *Hub
}

func NewGameStateUpdater(hub *Hub) *GameStateUpdater {
	return &GameStateUpdater{hub: hub}
}

func (gsu *GameStateUpdater) BroadcastGameState(roomID string, gameState *gamev1.GameState) {
	message := GameStateMessage{
		Type:      "game_state",
		GameState: gameState,
	}

	data, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshaling game state: %v", err)
		return
	}

	gsu.hub.BroadcastToRoom(roomID, data)
}

func (gsu *GameStateUpdater) BroadcastPlayerJoined(roomID string, playerName string) {
	message := Message{
		Type: "player_joined",
		Data: map[string]string{
			"playerName": playerName,
			"message":    playerName + " joined the game",
		},
	}

	data, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshaling player joined message: %v", err)
		return
	}

	gsu.hub.BroadcastToRoom(roomID, data)
}

func (gsu *GameStateUpdater) BroadcastPlayerLeft(roomID string, playerName string) {
	message := Message{
		Type: "player_left",
		Data: map[string]string{
			"playerName": playerName,
			"message":    playerName + " left the game",
		},
	}

	data, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshaling player left message: %v", err)
		return
	}

	gsu.hub.BroadcastToRoom(roomID, data)
}
