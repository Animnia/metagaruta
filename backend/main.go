package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ==========================================
// 1. 数据结构定义
// ==========================================

// Player 代表一个玩家
type Player struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Score       int             `json:"score"`
	HasAnswered bool            `json:"hasAnswered"` // 本局是否已点过牌
	Conn        *websocket.Conn `json:"-"`
}

type Song struct {
	ID               string `json:"id"`
	TitleOriginal    string `json:"title_original"`
	TitleTranslation string `json:"title_translation"`
	Duration         int    `json:"duration"`
}

type Card struct {
	ID               string `json:"id"`
	TitleOriginal    string `json:"titleOriginal"` // 转成驼峰命名给前端 Vue 用
	TitleTranslation string `json:"titleTranslation"`
	IsMatched        bool   `json:"isMatched"`
}

// Room 代表一个游戏房间
type Room struct {
	ID      string
	Players map[string]*Player
	Mutex   sync.Mutex

	// --- 新增的游戏状态 ---
	State        string `json:"state"` // "waiting"(等待中), "playing"(游戏中)
	CurrentRound int    `json:"currentRound"`
	SongPool     []Song `json:"-"` // 本局抽出的 25 首题库 (不需要发给前端，防作弊)
	BoardCards   []Card `json:"-"` // 场上的 16 张歌牌
	CurrentSong  *Song  `json:"-"` // 当前正在播放的歌
}

// WsMessage 是前后端通信的统一 JSON 格式
type WsMessage struct {
	Type    string                 `json:"type"`
	Payload map[string]interface{} `json:"payload"`
}

// ==========================================
// 2. 全局状态
// ==========================================

// 全局题库
var globalSongs []Song

var (
	// rooms 存放所有的房间，key 是房间号
	rooms = make(map[string]*Room)
	// globalMutex 保护对 rooms map 的并发读写
	globalMutex = sync.Mutex{}

	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

// ==========================================
// 3. 核心逻辑
// ==========================================

func main() {
	loadSongs() // 👈 新增这行，载入题库
	http.HandleFunc("/ws", handleConnections)
	fmt.Println("---------------------------------------")
	fmt.Println("歌牌游戏裁判服务器已启动 :3000/ws")
	fmt.Println("---------------------------------------")
	http.ListenAndServe(":3000", nil)
}

// 启动时加载题库
func loadSongs() {
	file, err := os.ReadFile("data/songs.json") // 确保你的文件放在这个相对路径
	if err != nil {
		fmt.Println("警告: 无法读取 data/songs.json，请检查路径！", err)
		return
	}
	json.Unmarshal(file, &globalSongs)
	fmt.Printf("成功加载 %d 首歌曲到全局题库\n", len(globalSongs))
}

// 洗牌并生成 16 张歌牌
func initGame(room *Room) {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	room.State = "playing"
	room.CurrentRound = 1

	// 1. 打乱全局题库，抽取 25 首作为本房间的题库
	rand.Seed(time.Now().UnixNano())
	shuffledAll := make([]Song, len(globalSongs))
	copy(shuffledAll, globalSongs)
	rand.Shuffle(len(shuffledAll), func(i, j int) {
		shuffledAll[i], shuffledAll[j] = shuffledAll[j], shuffledAll[i]
	})

	// 如果你的题库不够 25 首，这里要做个保护，否则会越界崩溃
	poolSize := 25
	if len(shuffledAll) < 25 {
		poolSize = len(shuffledAll)
	}
	room.SongPool = shuffledAll[:poolSize]

	// 2. 从这 25 首歌里，再抽取前 16 首生成“歌牌”
	cardSize := 16
	if poolSize < 16 {
		cardSize = poolSize
	}

	room.BoardCards = make([]Card, cardSize)
	for i := 0; i < cardSize; i++ {
		room.BoardCards[i] = Card{
			ID:               room.SongPool[i].ID,
			TitleOriginal:    room.SongPool[i].TitleOriginal,
			TitleTranslation: room.SongPool[i].TitleTranslation,
			IsMatched:        false,
		}
	}

	// 3. 将 16 张牌再次乱序（防止场上的牌按题库顺序排列）
	rand.Shuffle(len(room.BoardCards), func(i, j int) {
		room.BoardCards[i], room.BoardCards[j] = room.BoardCards[j], room.BoardCards[i]
	})

	fmt.Printf("房间 [%s] 游戏初始化完成，生成 %d 张牌\n", room.ID, cardSize)
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("WebSocket 升级失败:", err)
		return
	}

	var currentPlayer *Player
	var currentRoom *Room

	// 🌟 核心修复 1：利用 defer 确保无论什么情况断开，都把玩家移出房间
	defer func() {
		if currentRoom != nil && currentPlayer != nil {
			// 加锁，安全地从 map 中删除自己
			currentRoom.Mutex.Lock()
			delete(currentRoom.Players, currentPlayer.ID)
			currentRoom.Mutex.Unlock()

			fmt.Printf("玩家 [%s] 离开了房间\n", currentPlayer.Name)
			// 通知房间里剩下的人，更新列表
			broadcastRoomState(currentRoom)
		}
		conn.Close()
	}()

	// 不断读取前端发来的消息
	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("玩家断开连接/网络异常")
			break // 退出循环，自动触发上面的 defer 清理逻辑
		}

		// 解析 JSON
		var msg WsMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		switch msg.Type {

		case "join_room":
			roomID := msg.Payload["roomId"].(string)
			playerName := msg.Payload["playerName"].(string)
			playerID := msg.Payload["playerId"].(string)

			globalMutex.Lock()
			room, exists := rooms[roomID]
			if !exists {
				room = &Room{
					ID:      roomID,
					Players: make(map[string]*Player),
				}
				rooms[roomID] = room
			}
			globalMutex.Unlock()

			room.Mutex.Lock()
			if len(room.Players) >= 4 {
				room.Mutex.Unlock() // 记得解锁
				// 🌟 核心修复 2：房间满了，给前端发个报错提示，而不是默默无视
				errMsg := WsMessage{
					Type: "error",
					Payload: map[string]interface{}{
						"message": "房间人数已满 (最多4人)",
					},
				}
				msgBytes, _ := json.Marshal(errMsg)
				conn.WriteMessage(websocket.TextMessage, msgBytes)
				continue
			}

			newPlayer := &Player{ID: playerID, Name: playerName, Score: 0, Conn: conn}
			room.Players[playerID] = newPlayer
			currentPlayer = newPlayer
			currentRoom = room
			room.Mutex.Unlock()

			fmt.Printf("玩家 [%s] 加入了房间 [%s]\n", playerName, roomID)
			broadcastRoomState(room)

		case "chat":
			if currentRoom != nil && currentPlayer != nil {
				text := msg.Payload["text"].(string)
				chatMsg := WsMessage{
					Type: "chat_receive",
					Payload: map[string]interface{}{
						"sender": currentPlayer.Name,
						"text":   text,
					},
				}
				broadcastToRoom(currentRoom, chatMsg)
			}

		case "start_game":
			// 只有等待中的房间才能开始
			if currentRoom != nil && currentRoom.State == "waiting" {
				initGame(currentRoom)

				// 告诉房间里所有人：游戏开始了！发牌！
				startMsg := WsMessage{
					Type: "game_started",
					Payload: map[string]interface{}{
						"cards": currentRoom.BoardCards,
						"round": currentRoom.CurrentRound,
					},
				}
				broadcastToRoom(currentRoom, startMsg)
			}
		}
	}
}

// ==========================================
// 4. 辅助函数
// ==========================================

// 将消息广播给房间里的所有人
func broadcastToRoom(room *Room, msg WsMessage) {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	msgBytes, _ := json.Marshal(msg)
	for _, p := range room.Players {
		p.Conn.WriteMessage(websocket.TextMessage, msgBytes)
	}
}

// 广播当前房间的玩家状态
func broadcastRoomState(room *Room) {
	// 把 map 转成 slice 方便前端渲染
	var playerList []Player
	room.Mutex.Lock()
	for _, p := range room.Players {
		playerList = append(playerList, *p)
	}
	room.Mutex.Unlock()

	stateMsg := WsMessage{
		Type: "room_state_update",
		Payload: map[string]interface{}{
			"players": playerList,
		},
	}
	broadcastToRoom(room, stateMsg)
}
