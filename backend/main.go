package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
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
	GameReady   bool            `json:"gameReady"`   // 游戏开始前的准备状态
	IsReady     bool            `json:"-"`
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
	OwnerID string
	Players map[string]*Player
	Mutex   sync.Mutex

	// --- 新增的游戏状态 ---
	State            string        `json:"state"` // "waiting"(等待中), "playing"(游戏中)
	CurrentRound     int           `json:"currentRound"`
	SongPool         []Song        `json:"-"` // 本局抽出的 25 首题库 (不需要发给前端，防作弊)
	BoardCards       []Card        `json:"-"` // 场上的 16 张歌牌
	CurrentSong      *Song         `json:"-"` // 当前正在播放的歌
	CurrentSongIndex int           `json:"-"` // 记住当前歌在题库里的位置，方便等会儿移除
	RoundState       string        `json:"-"` // 新增：记录回合状态 ("preparing" 或 "playing")
	TimerCancel      chan struct{} `json:"-"` // 新增：用于打断 5 秒强制开局的定时器
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
	loadSongs() // 载入题库
	http.HandleFunc("/ws", handleConnections)
	http.HandleFunc("/api/audio", handleAudioProxy) // 挂载音频接口
	fmt.Println("---------------------------------------")
	fmt.Println("歌牌游戏裁判服务器已启动 :3000/ws")
	fmt.Println("---------------------------------------")
	http.ListenAndServe(":3000", nil)
}

// 处理音频请求 (防 F12 作弊接口)
func handleAudioProxy(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("roomId")

	globalMutex.Lock()
	room, exists := rooms[roomID]
	globalMutex.Unlock()

	// 如果房间不存在，或者当前回合还没有选定歌曲，拒绝请求
	if !exists || room.CurrentSong == nil {
		http.Error(w, "找不到歌曲或游戏未开始", http.StatusNotFound)
		return
	}

	// 构造本地音频文件路径
	audioPath := filepath.Join("audio", room.CurrentSong.ID+".m4a")

	if _, err := os.Stat(audioPath); os.IsNotExist(err) {
		fmt.Printf("严重错误: 找不到音频文件: %s\n", audioPath)
		http.Error(w, "音频文件不存在", http.StatusNotFound)
		return
	}

	fmt.Printf("正在发送音频文件: %s\n", audioPath)

	// 设置 Header，严禁浏览器缓存这首歌！防止玩家通过缓存提前知道答案
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Content-Type", "audio/mp4")

	// 将 MP3 文件流直接返回给前端
	http.ServeFile(w, r, audioPath)
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

// 生成唯一的 4 位数字房间号 (调用前必须持有 globalMutex)
func generateRoomID() string {
	for {
		id := fmt.Sprintf("%04d", rand.Intn(10000))
		if _, exists := rooms[id]; !exists {
			return id
		}
	}
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

// 阶段一：开始新一回合，发送“准备”指令
func startRound(room *Room) {
	// 在这里统一加上锁
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	if len(room.Players) == 0 {
		return
	}

	room.RoundState = "preparing"

	// 1. 重置所有玩家的答题和准备状态
	for _, p := range room.Players {
		p.HasAnswered = false
		p.IsReady = false
	}

	// 检查场上是否还有未消除的牌。如果全消除了，游戏结束！
	matchedCount := 0
	for _, c := range room.BoardCards {
		if c.IsMatched {
			matchedCount++
		}
	}
	if matchedCount >= 16 {
		fmt.Printf("房间 [%s] 游戏结束，所有歌牌已清空！\n", room.ID)
		overMsg := WsMessage{Type: "game_over", Payload: map[string]interface{}{}}
		broadcastToRoom(room, overMsg) // 通知所有人结束
		room.RoundState = "ended"
		return
	}

	if len(room.SongPool) == 0 {
		return // 理论上不会空，加个安全底线
	}

	// 每一轮都从剩余的题库中【随机】抽一首
	room.CurrentSongIndex = rand.Intn(len(room.SongPool))
	targetSong := room.SongPool[room.CurrentSongIndex]
	room.CurrentSong = &targetSong

	maxStart := targetSong.Duration * 3 / 4
	if maxStart <= 0 {
		maxStart = 1
	}
	startTime := rand.Intn(maxStart)

	// 计算本回合的实际播放时长 (最多90秒，或者剩余不足90秒时取真实值)
	playDuration := targetSong.Duration - startTime
	if playDuration > 90 {
		playDuration = 90
	}

	fmt.Printf("房间 [%s] 第 %d 局，播放时长: %d 秒\n", room.ID, room.CurrentRound, playDuration)

	// 发送 prepare_round 指令 (带上计算好的时长给前端)
	prepMsg := WsMessage{
		Type: "prepare_round",
		Payload: map[string]interface{}{
			"round":        room.CurrentRound,
			"startTime":    startTime,
			"playDuration": playDuration, // 发给前端用于倒计时
		},
	}

	// 因为当前已经在锁内部，绝对不能调用 broadcastToRoom（会再次造成死锁）
	// 我们像 startCountdownAndPlay 那样，手动遍历发送
	msgBytes, _ := json.Marshal(prepMsg)
	for _, p := range room.Players {
		p.Conn.WriteMessage(websocket.TextMessage, msgBytes)
	}

	// 5. 开启 5 秒防卡死倒计时。
	room.TimerCancel = make(chan struct{})
	go func(r *Room, roundNum int, cancelCh chan struct{}) {
		select {
		case <-time.After(5 * time.Second): // 5秒超时
			startCountdownAndPlay(r, roundNum)
		case <-cancelCh: // 所有人都提前准备好了
			return
		}
	}(room, room.CurrentRound, room.TimerCancel)
}

// 阶段二：开始倒计时，然后正式播放
func startCountdownAndPlay(room *Room, roundNum int) {
	room.Mutex.Lock()
	if room.RoundState != "preparing" || room.CurrentRound != roundNum {
		room.Mutex.Unlock()
		return
	}
	room.RoundState = "countdown" // 🌟 进入新的倒计时状态

	// 告诉前端：可以开始打印 4-3-2-1 了
	countdownMsg := WsMessage{Type: "countdown_start", Payload: map[string]interface{}{}}
	cdBytes, _ := json.Marshal(countdownMsg)
	for _, p := range room.Players {
		p.Conn.WriteMessage(websocket.TextMessage, cdBytes)
	}
	room.Mutex.Unlock() // 必须先解锁，因为我们要睡 4 秒！

	// 服务端严格等待 4 秒
	time.Sleep(4 * time.Second)

	// 4 秒后，正式下达播放指令
	room.Mutex.Lock()
	if room.RoundState != "countdown" || room.CurrentRound != roundNum {
		room.Mutex.Unlock()
		return
	}
	room.RoundState = "playing"

	fmt.Printf("房间 [%s] 第 %d 局正式播放！\n", room.ID, room.CurrentRound)

	playMsg := WsMessage{Type: "play_round", Payload: map[string]interface{}{}}
	msgBytes, _ := json.Marshal(playMsg)
	for _, p := range room.Players {
		p.Conn.WriteMessage(websocket.TextMessage, msgBytes)
	}

	// 开启 90 秒回合倒计时
	room.TimerCancel = make(chan struct{})
	go func(r *Room, roundNum int, cancelCh chan struct{}) {
		select {
		case <-time.After(90 * time.Second):
			r.Mutex.Lock()
			defer r.Mutex.Unlock()
			if r.RoundState == "playing" && r.CurrentRound == roundNum {
				// 🌟 超时无人答对，不展示答案
				endRound(r, "时间到！无人答对。", !isSongOnBoard(r), false)
			}
		case <-cancelCh:
			return
		}
	}(room, room.CurrentRound, room.TimerCancel)
	room.Mutex.Unlock()
}

// 辅助函数：检查当前歌曲是否真的在场上的 16 张牌中
func isSongOnBoard(room *Room) bool {
	for _, c := range room.BoardCards {
		if c.ID == room.CurrentSong.ID && !c.IsMatched {
			return true
		}
	}
	return false
}

// 辅助函数：检查是否房间里所有人都已经答过题了
func isAllAnswered(room *Room) bool {
	for _, p := range room.Players {
		if !p.HasAnswered {
			return false
		}
	}
	return true
}

// 结束本回合，等待几秒后自动开启下一回合
// 注意：调用此函数时，必须已经加了 room.Mutex.Lock()！
func endRound(room *Room, reason string, removeSong bool, showAnswer bool) {
	room.RoundState = "ended"

	// 1. 打断 90 秒倒计时
	if room.TimerCancel != nil {
		close(room.TimerCancel)
		room.TimerCancel = nil
	}

	if removeSong {
		idx := room.CurrentSongIndex
		if idx >= 0 && idx < len(room.SongPool) {
			// Go 语言中删除切片元素的经典写法
			room.SongPool = append(room.SongPool[:idx], room.SongPool[idx+1:]...)
			fmt.Printf("🎵 歌曲已被移出题库，剩余 %d 首\n", len(room.SongPool))
		}
	}

	// 检查场上 16 张牌是否已经全部被消除
	matchedCount := 0
	for _, c := range room.BoardCards {
		if c.IsMatched {
			matchedCount++
		}
	}
	isAllMatched := (matchedCount >= 16)

	fmt.Printf("房间 [%s] 第 %d 局结束。原因: %s\n", room.ID, room.CurrentRound, reason)

	// 2. 告诉所有人本局结束，公布正确答案
	endMsg := WsMessage{
		Type: "round_end",
		Payload: map[string]interface{}{
			"reason":      reason,
			"correctSong": room.CurrentSong.TitleOriginal,
			"cards":       room.BoardCards, // 发送最新的卡牌状态（包含被消除的牌）
			"showAnswer":  showAnswer,      // 传给前端，决定是否打印答案
		},
	}
	msgBytes, _ := json.Marshal(endMsg)
	for _, p := range room.Players {
		p.Conn.WriteMessage(websocket.TextMessage, msgBytes)
	}

	// 3. 广播最新分数
	// 注意：因为这里在锁里，不能直接调用 broadcastRoomState(room)
	var playerList []Player
	for _, p := range room.Players {
		playerList = append(playerList, *p)
	}
	stateMsg := WsMessage{
		Type:    "room_state_update",
		Payload: map[string]interface{}{"players": playerList, "ownerId": room.OwnerID},
	}
	stateBytes, _ := json.Marshal(stateMsg)
	for _, p := range room.Players {
		p.Conn.WriteMessage(websocket.TextMessage, stateBytes)
	}

	// 4. 开启一个独立的协程，3 秒后开启下一局（留出展示结算画面的时间）
	go func(r *Room, isGameOver bool) {
		time.Sleep(3 * time.Second)
		if isGameOver {
			overMsg := WsMessage{Type: "game_over", Payload: map[string]interface{}{}}
			broadcastToRoom(r, overMsg)
		} else {
			r.Mutex.Lock()
			r.CurrentRound++
			r.Mutex.Unlock()
			startRound(r)
		}
	}(room, isAllMatched)
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("WebSocket 升级失败:", err)
		return
	}

	var currentPlayer *Player
	var currentRoom *Room

	defer func() {
		if currentRoom != nil && currentPlayer != nil {
			currentRoom.Mutex.Lock()
			delete(currentRoom.Players, currentPlayer.ID)
			isEmpty := len(currentRoom.Players) == 0 // 检查房间是否空了
			// 如果离开的是房主且房间还有人，转移房主身份
			if !isEmpty && currentRoom.OwnerID == currentPlayer.ID {
				for _, p := range currentRoom.Players {
					currentRoom.OwnerID = p.ID
					break
				}
			}
			currentRoom.Mutex.Unlock()

			fmt.Printf("玩家 [%s] 离开了房间 [%s]\n", currentPlayer.Name, currentRoom.ID)

			if isEmpty {
				// 如果房间空无一人，销毁该房间，防止“幽灵循环”
				globalMutex.Lock()
				delete(rooms, currentRoom.ID)
				globalMutex.Unlock()

				currentRoom.Mutex.Lock()
				currentRoom.RoundState = "ended" // 强行把状态设为结束
				if currentRoom.TimerCancel != nil {
					close(currentRoom.TimerCancel) // 打断可能正在进行的 5 秒或 90 秒倒计时
					currentRoom.TimerCancel = nil
				}
				currentRoom.Mutex.Unlock()
				fmt.Printf("房间 [%s] 已空，销毁房间并释放资源\n", currentRoom.ID)
			} else {
				// 还有人在，只广播最新列表
				broadcastRoomState(currentRoom)
			}
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

		case "create_room":
			playerName := msg.Payload["playerName"].(string)
			playerID := msg.Payload["playerId"].(string)

			globalMutex.Lock()
			roomID := generateRoomID()
			room := &Room{
				ID:      roomID,
				OwnerID: playerID,
				Players: make(map[string]*Player),
				State:   "waiting",
			}
			rooms[roomID] = room
			globalMutex.Unlock()

			room.Mutex.Lock()
			newPlayer := &Player{ID: playerID, Name: playerName, Score: 0, Conn: conn}
			room.Players[playerID] = newPlayer
			currentPlayer = newPlayer
			currentRoom = room
			room.Mutex.Unlock()

			createdMsg := WsMessage{
				Type:    "room_created",
				Payload: map[string]interface{}{"roomId": roomID},
			}
			cBytes, _ := json.Marshal(createdMsg)
			conn.WriteMessage(websocket.TextMessage, cBytes)

			fmt.Printf("玩家 [%s] 创建了房间 [%s]\n", playerName, roomID)
			broadcastRoomState(room)

		case "join_room":
			roomID := msg.Payload["roomId"].(string)
			playerName := msg.Payload["playerName"].(string)
			playerID := msg.Payload["playerId"].(string)

			globalMutex.Lock()
			room, exists := rooms[roomID]
			globalMutex.Unlock()

			if !exists {
				errMsg := WsMessage{
					Type:    "error",
					Payload: map[string]interface{}{"message": "房间不存在！请检查房间号。"},
				}
				eBytes, _ := json.Marshal(errMsg)
				conn.WriteMessage(websocket.TextMessage, eBytes)
				continue
			}

			room.Mutex.Lock()
			if len(room.Players) >= 4 {
				room.Mutex.Unlock()
				errMsg := WsMessage{
					Type:    "error",
					Payload: map[string]interface{}{"message": "房间人数已满 (最多4人)"},
				}
				msgBytes, _ := json.Marshal(errMsg)
				conn.WriteMessage(websocket.TextMessage, msgBytes)
				continue
			}
			nameConflict := false
			for _, p := range room.Players {
				if p.Name == playerName {
					nameConflict = true
					break
				}
			}
			if nameConflict {
				room.Mutex.Unlock()
				errMsg := WsMessage{
					Type:    "error",
					Payload: map[string]interface{}{"message": "该房间已有同名玩家，请更换名称！"},
				}
				eBytes, _ := json.Marshal(errMsg)
				conn.WriteMessage(websocket.TextMessage, eBytes)
				continue
			}

			newPlayer := &Player{ID: playerID, Name: playerName, Score: 0, Conn: conn}
			room.Players[playerID] = newPlayer
			currentPlayer = newPlayer
			currentRoom = room
			room.Mutex.Unlock()

			fmt.Printf("玩家 [%s] 加入了房间 [%s]\n", playerName, roomID)
			broadcastRoomState(room)
			if room.State == "playing" {
				syncMsg := WsMessage{
					Type: "game_started",
					Payload: map[string]interface{}{
						"cards": room.BoardCards,
						"round": room.CurrentRound,
					},
				}
				msgBytes, _ := json.Marshal(syncMsg)
				conn.WriteMessage(websocket.TextMessage, msgBytes)
			}

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

		case "toggle_ready":
			if currentRoom != nil && currentPlayer != nil {
				currentRoom.Mutex.Lock()
				if currentRoom.OwnerID != currentPlayer.ID && currentRoom.State == "waiting" {
					currentPlayer.GameReady = !currentPlayer.GameReady
				}
				currentRoom.Mutex.Unlock()
				broadcastRoomState(currentRoom)
			}

		case "start_game":
			// 只有房主在等待状态下才能开始
			if currentRoom != nil && currentPlayer != nil && currentRoom.State == "waiting" {
				currentRoom.Mutex.Lock()
				if currentRoom.OwnerID != currentPlayer.ID {
					currentRoom.Mutex.Unlock()
					continue
				}
				allPlayersReady := true
				for _, p := range currentRoom.Players {
					if p.ID != currentRoom.OwnerID && !p.GameReady {
						allPlayersReady = false
						break
					}
				}
				if !allPlayersReady {
					currentRoom.Mutex.Unlock()
					continue
				}
				currentRoom.Mutex.Unlock()

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

				// 🌟 发牌完毕后，服务器主动发起第一回合的“准备播放”
				startRound(currentRoom)
			}

		case "client_ready": // 🌟 新增：接收前端缓冲完毕的信号
			if currentRoom != nil && currentRoom.RoundState == "preparing" {
				currentRoom.Mutex.Lock()
				currentPlayer.IsReady = true

				// 检查房间里是不是所有人都 IsReady 了
				allReady := true
				for _, p := range currentRoom.Players {
					if !p.IsReady {
						allReady = false
						break
					}
				}

				// 如果都准备好了，立刻打断定时器并播放
				if allReady {
					if currentRoom.TimerCancel != nil {
						close(currentRoom.TimerCancel)
						currentRoom.TimerCancel = nil
					}
					currentRoom.Mutex.Unlock() // 先解锁，再调用 startCountdownAndPlay
					startCountdownAndPlay(currentRoom, currentRoom.CurrentRound)
				} else {
					currentRoom.Mutex.Unlock()
				}
			}

		case "buzz":
			if currentRoom != nil {
				currentRoom.Mutex.Lock() // 抢答锁：保证绝对公平，谁的网速快谁先进锁

				// 只有在游戏中且玩家没答过题才能抢答
				if currentRoom.RoundState == "playing" && !currentPlayer.HasAnswered {
					cardID := msg.Payload["cardId"].(string)
					currentPlayer.HasAnswered = true

					// 判定对错
					if cardID == currentRoom.CurrentSong.ID {
						// 答对了！
						currentPlayer.Score += 10
						// 消除这张卡牌
						for i, c := range currentRoom.BoardCards {
							if c.ID == cardID {
								currentRoom.BoardCards[i].IsMatched = true
								break
							}
						}
						endRound(currentRoom, fmt.Sprintf("玩家 [%s] 抢答正确！(+10分)", currentPlayer.Name), true, true)
					} else {
						// 答错了！
						currentPlayer.Score -= 5
						// 告诉这个玩家他答错了（其他玩家继续）
						wrongMsg := WsMessage{Type: "wrong_answer", Payload: map[string]interface{}{}}
						msgBytes, _ := json.Marshal(wrongMsg)
						currentPlayer.Conn.WriteMessage(websocket.TextMessage, msgBytes)

						// 如果所有人都答错了，回合结束
						if isAllAnswered(currentRoom) {
							endRound(currentRoom, "全军覆没！无人答对。", !isSongOnBoard(currentRoom), false)
						}
					}
				}
				currentRoom.Mutex.Unlock()
			}

		case "no_song":
			if currentRoom != nil {
				currentRoom.Mutex.Lock()

				if currentRoom.RoundState == "playing" && !currentPlayer.HasAnswered {
					currentPlayer.HasAnswered = true

					// 判断场上是不是真的没有这首歌
					songOnBoard := isSongOnBoard(currentRoom)

					if !songOnBoard {
						// 真的没有这首歌，判断正确！
						currentPlayer.Score += 5 // 发现没有这首歌奖励 5 分

						if isAllAnswered(currentRoom) {
							endRound(currentRoom, "本轮幽灵歌曲，全员鉴定完毕！", true, false)
						}
					} else {
						// 场上明明有这首歌，判断错误！
						currentPlayer.Score -= 5
						wrongMsg := WsMessage{Type: "wrong_answer", Payload: map[string]interface{}{}}
						msgBytes, _ := json.Marshal(wrongMsg)
						currentPlayer.Conn.WriteMessage(websocket.TextMessage, msgBytes)

						if isAllAnswered(currentRoom) {
							endRound(currentRoom, "全军覆没！这首歌其实在场上。", false, false)
						}
					}
				}
				currentRoom.Mutex.Unlock()
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
	ownerID := room.OwnerID
	room.Mutex.Unlock()

	stateMsg := WsMessage{
		Type: "room_state_update",
		Payload: map[string]interface{}{
			"players": playerList,
			"ownerId": ownerID,
		},
	}
	broadcastToRoom(room, stateMsg)
}
