package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// ==========================================
// 1. 数据结构定义
// ==========================================

// Player 代表一个玩家
type Player struct {
	ID    string          `json:"id"`    // 强制转为小写
	Name  string          `json:"name"`  // 强制转为小写
	Score int             `json:"score"` // 强制转为小写
	Conn  *websocket.Conn `json:"-"`
}

// Room 代表一个游戏房间
type Room struct {
	ID      string
	Players map[string]*Player // 房间里的玩家，key 是玩家 ID
	Mutex   sync.Mutex         // 互斥锁！防止几个人同时抢答导致数据错乱
	// 我们后续会在这里加上：Cards (场上的牌), CurrentSong (当前播放的歌) 等状态
}

// WsMessage 是前后端通信的统一 JSON 格式
type WsMessage struct {
	Type    string                 `json:"type"`
	Payload map[string]interface{} `json:"payload"`
}

// ==========================================
// 2. 全局状态
// ==========================================

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
	http.HandleFunc("/ws", handleConnections)
	fmt.Println("---------------------------------------")
	fmt.Println("歌牌游戏裁判服务器已启动 :3000/ws")
	fmt.Println("---------------------------------------")
	http.ListenAndServe(":3000", nil)
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
