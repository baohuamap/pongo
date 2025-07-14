package game

import (
	"context"
	"fmt"
	"sync"
	"time"

	gamev1 "github.com/baohuamap/pongo/proto/game/v1"
)

const (
	GameWidth    = 800
	GameHeight   = 400
	PaddleWidth  = 10
	PaddleHeight = 80
	BallSize     = 8
	PaddleSpeed  = 5
	BallSpeed    = 3
	MaxScore     = 5
	TickRate     = 60 // FPS
)

type GameEngine struct {
	rooms map[string]*Room
	mutex sync.RWMutex
}

type Room struct {
	ID       string
	Players  map[string]*Player
	Ball     *Ball
	Score    *Score
	Status   gamev1.GameStatus
	LastTick time.Time
	mutex    sync.RWMutex
	stopChan chan bool
}

type Player struct {
	ID           string
	Name         string
	PlayerNumber int32
	Paddle       *Paddle
	Ready        bool
	LastInput    time.Time
}

type Paddle struct {
	X, Y   float32
	Width  float32
	Height float32
	VelY   float32
}

type Ball struct {
	X, Y    float32
	VelX    float32
	VelY    float32
	Size    float32
	LastHit string
}

type Score struct {
	Player1 int32
	Player2 int32
}

func NewGameEngine() *GameEngine {
	return &GameEngine{
		rooms: make(map[string]*Room),
	}
}

func (ge *GameEngine) CreateRoom(ctx context.Context, req *gamev1.CreateRoomRequest) (*gamev1.CreateRoomResponse, error) {
	ge.mutex.Lock()
	defer ge.mutex.Unlock()

	roomID := generateRoomID()
	room := &Room{
		ID:       roomID,
		Players:  make(map[string]*Player),
		Ball:     ge.initializeBall(),
		Score:    &Score{},
		Status:   gamev1.GameStatus_GAME_STATUS_WAITING,
		stopChan: make(chan bool),
	}

	// Add first player
	player := &Player{
		ID:           req.PlayerId,
		Name:         req.PlayerName,
		PlayerNumber: 1,
		Paddle:       ge.initializePaddle(1),
		Ready:        false,
	}
	room.Players[req.PlayerId] = player

	ge.rooms[roomID] = room
	go ge.runGameLoop(room)

	return &gamev1.CreateRoomResponse{
		RoomId:  roomID,
		Success: true,
	}, nil
}

func (ge *GameEngine) JoinRoom(ctx context.Context, req *gamev1.JoinRoomRequest) (*gamev1.JoinRoomResponse, error) {
	ge.mutex.Lock()
	defer ge.mutex.Unlock()

	room, exists := ge.rooms[req.RoomId]
	if !exists {
		return &gamev1.JoinRoomResponse{
			Success: false,
			Error:   "Room not found",
		}, nil
	}

	room.mutex.Lock()
	defer room.mutex.Unlock()

	if len(room.Players) >= 2 {
		return &gamev1.JoinRoomResponse{
			Success: false,
			Error:   "Room is full",
		}, nil
	}

	player := &Player{
		ID:           req.PlayerId,
		Name:         req.PlayerName,
		PlayerNumber: 2,
		Paddle:       ge.initializePaddle(2),
		Ready:        false,
	}
	room.Players[req.PlayerId] = player

	gameState := ge.buildGameState(room)
	return &gamev1.JoinRoomResponse{
		Success:   true,
		GameState: gameState,
	}, nil
}

func (ge *GameEngine) UpdatePlayerInput(ctx context.Context, req *gamev1.UpdatePlayerInputRequest) (*gamev1.UpdatePlayerInputResponse, error) {
	ge.mutex.RLock()
	room, exists := ge.rooms[req.RoomId]
	ge.mutex.RUnlock()

	if !exists {
		return &gamev1.UpdatePlayerInputResponse{
			Success: false,
			Error:   "Room not found",
		}, nil
	}

	room.mutex.Lock()
	defer room.mutex.Unlock()

	player, exists := room.Players[req.PlayerId]
	if !exists {
		return &gamev1.UpdatePlayerInputResponse{
			Success: false,
			Error:   "Player not found",
		}, nil
	}

	switch req.Input.Type {
	case gamev1.InputType_INPUT_TYPE_PADDLE_MOVE:
		player.Paddle.VelY = req.Input.Value * PaddleSpeed
		player.LastInput = time.Now()
	case gamev1.InputType_INPUT_TYPE_READY:
		player.Ready = true
		ge.checkGameStart(room)
	}

	return &gamev1.UpdatePlayerInputResponse{Success: true}, nil
}

func (ge *GameEngine) runGameLoop(room *Room) {
	ticker := time.NewTicker(time.Second / TickRate)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ge.updateGame(room)
		case <-room.stopChan:
			return
		}
	}
}

func (ge *GameEngine) updateGame(room *Room) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	if room.Status != gamev1.GameStatus_GAME_STATUS_PLAYING {
		return
	}

	deltaTime := float32(1.0 / TickRate)

	// Update paddles
	for _, player := range room.Players {
		ge.updatePaddle(player.Paddle, deltaTime)
	}

	// Update ball
	ge.updateBall(room, deltaTime)

	// Check win condition
	if room.Score.Player1 >= MaxScore || room.Score.Player2 >= MaxScore {
		room.Status = gamev1.GameStatus_GAME_STATUS_FINISHED
	}
}

func (ge *GameEngine) updatePaddle(paddle *Paddle, deltaTime float32) {
	paddle.Y += paddle.VelY * deltaTime

	// Clamp to screen bounds
	if paddle.Y < 0 {
		paddle.Y = 0
	}
	if paddle.Y > GameHeight-paddle.Height {
		paddle.Y = GameHeight - paddle.Height
	}
}

func (ge *GameEngine) updateBall(room *Room, deltaTime float32) {
	ball := room.Ball
	ball.X += ball.VelX * deltaTime
	ball.Y += ball.VelY * deltaTime

	// Bounce off top and bottom walls
	if ball.Y <= 0 || ball.Y >= GameHeight-ball.Size {
		ball.VelY = -ball.VelY
	}

	// Check paddle collisions
	for _, player := range room.Players {
		if ge.checkBallPaddleCollision(ball, player.Paddle) {
			ball.VelX = -ball.VelX
			ball.LastHit = player.ID
			// Add some spin based on where ball hit paddle
			hitPos := (ball.Y - player.Paddle.Y) / player.Paddle.Height
			ball.VelY += (hitPos - 0.5) * 2
		}
	}

	// Check scoring
	if ball.X <= 0 {
		room.Score.Player2++
		ge.resetBall(ball)
	} else if ball.X >= GameWidth-ball.Size {
		room.Score.Player1++
		ge.resetBall(ball)
	}
}

func (ge *GameEngine) checkBallPaddleCollision(ball *Ball, paddle *Paddle) bool {
	return ball.X < paddle.X+paddle.Width &&
		ball.X+ball.Size > paddle.X &&
		ball.Y < paddle.Y+paddle.Height &&
		ball.Y+ball.Size > paddle.Y
}

func (ge *GameEngine) resetBall(ball *Ball) {
	ball.X = GameWidth / 2
	ball.Y = GameHeight / 2
	ball.VelX = BallSpeed
	if ball.VelX > 0 {
		ball.VelX = -BallSpeed
	}
	ball.VelY = 0
}

func (ge *GameEngine) initializeBall() *Ball {
	return &Ball{
		X:    GameWidth / 2,
		Y:    GameHeight / 2,
		VelX: BallSpeed,
		VelY: 0,
		Size: BallSize,
	}
}

func (ge *GameEngine) initializePaddle(playerNum int32) *Paddle {
	var x float32
	if playerNum == 1 {
		x = 20
	} else {
		x = GameWidth - 30
	}

	return &Paddle{
		X:      x,
		Y:      GameHeight/2 - PaddleHeight/2,
		Width:  PaddleWidth,
		Height: PaddleHeight,
		VelY:   0,
	}
}

func (ge *GameEngine) checkGameStart(room *Room) {
	if len(room.Players) == 2 {
		allReady := true
		for _, player := range room.Players {
			if !player.Ready {
				allReady = false
				break
			}
		}
		if allReady {
			room.Status = gamev1.GameStatus_GAME_STATUS_PLAYING
		}
	}
}

func (ge *GameEngine) buildGameState(room *Room) *gamev1.GameState {
	players := make([]*gamev1.Player, 0, len(room.Players))
	for _, player := range room.Players {
		players = append(players, &gamev1.Player{
			Id:           player.ID,
			Name:         player.Name,
			PlayerNumber: player.PlayerNumber,
			Paddle: &gamev1.Position{
				X: player.Paddle.X,
				Y: player.Paddle.Y,
			},
			Ready: player.Ready,
		})
	}

	return &gamev1.GameState{
		RoomId:  room.ID,
		Players: players,
		Ball: &gamev1.Ball{
			Position: &gamev1.Position{
				X: room.Ball.X,
				Y: room.Ball.Y,
			},
			Velocity: &gamev1.Velocity{
				X: room.Ball.VelX,
				Y: room.Ball.VelY,
			},
		},
		Score: &gamev1.Score{
			Player1: room.Score.Player1,
			Player2: room.Score.Player2,
		},
		Status:    room.Status,
		Timestamp: time.Now().UnixMilli(),
	}
}

func generateRoomID() string {
	return fmt.Sprintf("room_%d", time.Now().UnixNano())
}
