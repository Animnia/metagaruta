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

type Player struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Score       int             `json:"score"`
	HasAnswered bool            `json:"hasAnswered"`
	GameReady   bool            `json:"gameReady"`
	IsReady     bool            `json:"-"`
	Conn        *websocket.Conn `json:"-"`
}

type Song struct {
	ID               string `json:"id"`
	TitleOriginal    string `json:"title_original"`
	TitleTranslation string `json:"title_translation"`
	Duration         int    `json:"duration"`
	CharacterID      int    `json:"-"` // touhou
	CharacterName    string `json:"-"` // touhou
}

type TouhouCharacter struct {
	ID         int            `json:"id"`
	Character  string         `json:"character"`
	MusicCount int            `json:"music_count"`
	Data       map[string]int `json:"data"` // songId -> duration
}

type Card struct {
	ID               string `json:"id"`
	TitleOriginal    string `json:"titleOriginal"`
	TitleTranslation string `json:"titleTranslation"`
	IsMatched        bool   `json:"isMatched"`
	CharacterID      int    `json:"characterId,omitempty"`   // touhou
	CharacterName    string `json:"characterName,omitempty"` // touhou
	PictureUrl       string `json:"pictureUrl,omitempty"`    // touhou
}

type Room struct {
	ID       string
	OwnerID  string
	GameMode string
	Players  map[string]*Player
	Mutex    sync.Mutex

	State            string        `json:"state"`
	CurrentRound     int           `json:"currentRound"`
	SongPool         []Song        `json:"-"`
	BoardCards       []Card        `json:"-"`
	CurrentSong      *Song         `json:"-"`
	CurrentSongIndex int           `json:"-"`
	RoundState       string        `json:"-"`
	TimerCancel      chan struct{} `json:"-"`
	NoSongCorrect    bool          `json:"-"`
}

// 统一 JSON 格式
type WsMessage struct {
	Type    string                 `json:"type"`
	Payload map[string]interface{} `json:"payload"`
}

// 全局题库
var globalSongs []Song
var globalTouhouChars []TouhouCharacter

var (
	rooms = make(map[string]*Room)
	// globalMutex 保护对 rooms map 的并发读写
	globalMutex = sync.Mutex{}

	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

func main() {
	loadSongs()
	loadTouhouChars()
	http.HandleFunc("/ws", handleConnections)
	http.HandleFunc("/api/audio", handleAudioProxy)
	http.HandleFunc("/api/picture", handlePictureProxy)
	fmt.Println("---------------------------------------")
	fmt.Println("歌牌游戏裁判服务器已启动 :3000/ws")
	fmt.Println("---------------------------------------")
	http.ListenAndServe(":3000", nil)
}

// 处理音频请求
func handleAudioProxy(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("roomId")

	globalMutex.Lock()
	room, exists := rooms[roomID]
	globalMutex.Unlock()

	if !exists || room.CurrentSong == nil {
		http.Error(w, "找不到歌曲或游戏未开始", http.StatusNotFound)
		return
	}

	var audioPath string
	var contentType string
	if room.GameMode == "touhou" {
		// touhou: touhou/audio/{characterId}/{songId}.ogg
		audioPath = filepath.Join("touhou", "audio",
			fmt.Sprintf("%d", room.CurrentSong.CharacterID),
			room.CurrentSong.ID+".ogg")
		contentType = "audio/ogg"
	} else {
		// vocaloid: vocaloid/audio/{songId}.m4a
		audioPath = filepath.Join("vocaloid", "audio", room.CurrentSong.ID+".m4a")
		contentType = "audio/mp4"
	}

	if _, err := os.Stat(audioPath); os.IsNotExist(err) {
		fmt.Printf("严重错误: 找不到音频文件: %s\n", audioPath)
		http.Error(w, "音频文件不存在", http.StatusNotFound)
		return
	}

	fmt.Printf("正在发送音频文件: %s\n", audioPath)

	// 设置 Header，禁浏览器缓存
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Content-Type", contentType)

	// 音频文件流返回给前端
	http.ServeFile(w, r, audioPath)
}

func handlePictureProxy(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "缺少 id 参数", http.StatusBadRequest)
		return
	}
	picPath := filepath.Join("touhou", "picture", id+".jpg")
	if _, err := os.Stat(picPath); os.IsNotExist(err) {
		http.Error(w, "图片不存在", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, picPath)
}

func loadSongs() {
	file, err := os.ReadFile("vocaloid/data/songs.json")
	if err != nil {
		fmt.Println("警告: 无法读取 vocaloid/data/songs.json，请检查路径！", err)
		return
	}
	json.Unmarshal(file, &globalSongs)
	fmt.Printf("成功加载 %d 首 Vocaloid 歌曲到全局题库\n", len(globalSongs))
}

func loadTouhouChars() {
	file, err := os.ReadFile("touhou/data/data.json")
	if err != nil {
		fmt.Println("警告: 无法读取 touhou/data/data.json，请检查路径！", err)
		return
	}
	// 去除 UTF-8 BOM (0xEF 0xBB 0xBF)，防止 json.Unmarshal 解析失败
	if len(file) >= 3 && file[0] == 0xEF && file[1] == 0xBB && file[2] == 0xBF {
		file = file[3:]
	}
	json.Unmarshal(file, &globalTouhouChars)
	fmt.Printf("成功加载 %d 个东方角色到全局题库\n", len(globalTouhouChars))
}

// 持有 globalMutex
func generateRoomID() string {
	for {
		id := fmt.Sprintf("%04d", rand.Intn(10000))
		if _, exists := rooms[id]; !exists {
			return id
		}
	}
}

func initGame(room *Room) {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	room.State = "playing"
	room.CurrentRound = 1

	if room.GameMode == "touhou" {
		initTouhouGame(room)
	} else {
		initVocaloidGame(room)
	}
}

func initVocaloidGame(room *Room) {
	rand.Seed(time.Now().UnixNano())
	shuffledAll := make([]Song, len(globalSongs))
	copy(shuffledAll, globalSongs)
	rand.Shuffle(len(shuffledAll), func(i, j int) {
		shuffledAll[i], shuffledAll[j] = shuffledAll[j], shuffledAll[i]
	})

	poolSize := 25
	if len(shuffledAll) < 25 {
		poolSize = len(shuffledAll)
	}
	room.SongPool = shuffledAll[:poolSize]

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

	rand.Shuffle(len(room.BoardCards), func(i, j int) {
		room.BoardCards[i], room.BoardCards[j] = room.BoardCards[j], room.BoardCards[i]
	})

	fmt.Printf("房间 [%s] Vocaloid 游戏初始化完成，生成 %d 张牌\n", room.ID, cardSize)
}

func initTouhouGame(room *Room) {
	rand.Seed(time.Now().UnixNano())

	shuffledChars := make([]TouhouCharacter, len(globalTouhouChars))
	copy(shuffledChars, globalTouhouChars)
	rand.Shuffle(len(shuffledChars), func(i, j int) {
		shuffledChars[i], shuffledChars[j] = shuffledChars[j], shuffledChars[i]
	})

	poolSize := 25
	if len(shuffledChars) < 25 {
		poolSize = len(shuffledChars)
	}
	selectedChars := shuffledChars[:poolSize]

	room.SongPool = make([]Song, poolSize)
	for i, char := range selectedChars {
		keys := make([]string, 0, len(char.Data))
		for k := range char.Data {
			keys = append(keys, k)
		}
		songID := keys[rand.Intn(len(keys))]
		duration := char.Data[songID]

		room.SongPool[i] = Song{
			ID:               songID,
			TitleOriginal:    char.Character,
			TitleTranslation: char.Character,
			Duration:         duration,
			CharacterID:      char.ID,
			CharacterName:    char.Character,
		}
	}

	cardSize := 16
	if poolSize < 16 {
		cardSize = poolSize
	}

	room.BoardCards = make([]Card, cardSize)
	for i := 0; i < cardSize; i++ {
		room.BoardCards[i] = Card{
			ID:            room.SongPool[i].ID,
			CharacterID:   room.SongPool[i].CharacterID,
			CharacterName: room.SongPool[i].CharacterName,
			PictureUrl:    fmt.Sprintf("/api/picture?id=%d", room.SongPool[i].CharacterID),
			IsMatched:     false,
		}
	}

	rand.Shuffle(len(room.BoardCards), func(i, j int) {
		room.BoardCards[i], room.BoardCards[j] = room.BoardCards[j], room.BoardCards[i]
	})

	fmt.Printf("房间 [%s] Touhou 游戏初始化完成，生成 %d 张牌\n", room.ID, cardSize)
}

// 阶段一：开始新一回合，发送“准备”指令
func startRound(room *Room) {
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
	room.NoSongCorrect = false

	matchedCount := 0
	for _, c := range room.BoardCards {
		if c.IsMatched {
			matchedCount++
		}
	}
	if matchedCount >= 16 {
		fmt.Printf("房间 [%s] 游戏结束，所有歌牌已清空！\n", room.ID)
		var pList []Player
		for _, p := range room.Players {
			pList = append(pList, *p)
		}
		overMsg := WsMessage{Type: "game_over", Payload: map[string]interface{}{"players": pList}}
		broadcastToRoom(room, overMsg)
		room.RoundState = "ended"
		return
	}

	if len(room.SongPool) == 0 {
		return
	}

	room.CurrentSongIndex = rand.Intn(len(room.SongPool))
	targetSong := room.SongPool[room.CurrentSongIndex]
	room.CurrentSong = &targetSong

	maxStart := targetSong.Duration * 3 / 4
	if maxStart <= 0 {
		maxStart = 1
	}
	startTime := rand.Intn(maxStart)

	playDuration := targetSong.Duration - startTime
	if playDuration > 45 {
		playDuration = 45
	}

	fmt.Printf("房间 [%s] 第 %d 局，播放时长: %d 秒\n", room.ID, room.CurrentRound, playDuration)

	// 发送 prepare_round 指令 (带上计算好的时长给前端)
	prepMsg := WsMessage{
		Type: "prepare_round",
		Payload: map[string]interface{}{
			"round":        room.CurrentRound,
			"startTime":    startTime,
			"playDuration": playDuration,
		},
	}

	msgBytes, _ := json.Marshal(prepMsg)
	for _, p := range room.Players {
		p.Conn.WriteMessage(websocket.TextMessage, msgBytes)
	}

	room.TimerCancel = make(chan struct{})
	go func(r *Room, roundNum int, cancelCh chan struct{}) {
		select {
		case <-time.After(5 * time.Second):
			startCountdownAndPlay(r, roundNum)
		case <-cancelCh:
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
	room.RoundState = "countdown"

	countdownMsg := WsMessage{Type: "countdown_start", Payload: map[string]interface{}{}}
	cdBytes, _ := json.Marshal(countdownMsg)
	for _, p := range room.Players {
		p.Conn.WriteMessage(websocket.TextMessage, cdBytes)
	}
	room.Mutex.Unlock() // 必须先解锁，因为我们要睡 4 秒

	time.Sleep(4 * time.Second)

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

	room.TimerCancel = make(chan struct{})
	go func(r *Room, roundNum int, cancelCh chan struct{}) {
		select {
		case <-time.After(45 * time.Second):
			r.Mutex.Lock()
			defer r.Mutex.Unlock()
			if r.RoundState == "playing" && r.CurrentRound == roundNum {
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
// 注意：调用此函数时，必须已经加了 room.Mutex.Lock()
func endRound(room *Room, reason string, removeSong bool, showAnswer bool) {
	room.RoundState = "ended"

	if room.TimerCancel != nil {
		close(room.TimerCancel)
		room.TimerCancel = nil
	}

	if removeSong {
		idx := room.CurrentSongIndex
		if idx >= 0 && idx < len(room.SongPool) {
			room.SongPool = append(room.SongPool[:idx], room.SongPool[idx+1:]...)
			fmt.Printf("歌曲已被移出题库，剩余 %d 首\n", len(room.SongPool))
		}
	}

	matchedCount := 0
	for _, c := range room.BoardCards {
		if c.IsMatched {
			matchedCount++
		}
	}
	isAllMatched := (matchedCount >= 16)

	fmt.Printf("房间 [%s] 第 %d 局结束。原因: %s\n", room.ID, room.CurrentRound, reason)

	endMsg := WsMessage{
		Type: "round_end",
		Payload: map[string]interface{}{
			"reason":      reason,
			"correctSong": room.CurrentSong.TitleOriginal,
			"cards":       room.BoardCards,
			"showAnswer":  showAnswer,
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
		Payload: map[string]interface{}{"players": playerList, "ownerId": room.OwnerID, "gameMode": room.GameMode},
	}
	stateBytes, _ := json.Marshal(stateMsg)
	for _, p := range room.Players {
		p.Conn.WriteMessage(websocket.TextMessage, stateBytes)
	}

	// 4. 开启一个独立的协程，3 秒后开启下一局（留出展示结算画面的时间）
	go func(r *Room, isGameOver bool) {
		time.Sleep(3 * time.Second)
		if isGameOver {
			r.Mutex.Lock()
			var pList []Player
			for _, p := range r.Players {
				pList = append(pList, *p)
			}
			r.Mutex.Unlock()
			overMsg := WsMessage{Type: "game_over", Payload: map[string]interface{}{"players": pList}}
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
			isEmpty := len(currentRoom.Players) == 0
			// 转移房主身份
			if !isEmpty && currentRoom.OwnerID == currentPlayer.ID {
				for _, p := range currentRoom.Players {
					currentRoom.OwnerID = p.ID
					break
				}
			}
			currentRoom.Mutex.Unlock()

			fmt.Printf("玩家 [%s] 离开了房间 [%s]\n", currentPlayer.Name, currentRoom.ID)

			if isEmpty {
				globalMutex.Lock()
				delete(rooms, currentRoom.ID)
				globalMutex.Unlock()

				currentRoom.Mutex.Lock()
				currentRoom.RoundState = "ended"
				if currentRoom.TimerCancel != nil {
					close(currentRoom.TimerCancel)
					currentRoom.TimerCancel = nil
				}
				currentRoom.Mutex.Unlock()
				fmt.Printf("房间 [%s] 已空，销毁房间并释放资源\n", currentRoom.ID)
			} else {
				broadcastRoomState(currentRoom)
			}
		}
		conn.Close()
	}()

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("玩家断开连接/网络异常")
			break
		}

		var msg WsMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		switch msg.Type {

		case "create_room":
			playerName := msg.Payload["playerName"].(string)
			playerID := msg.Payload["playerId"].(string)
			gameMode := "vocaloid"
			if gm, ok := msg.Payload["gameMode"].(string); ok && gm != "" {
				gameMode = gm
			}

			globalMutex.Lock()
			if len(rooms) >= 10 {
				globalMutex.Unlock()
				errMsg := WsMessage{
					Type:    "error",
					Payload: map[string]interface{}{"message": "当前房间数已达上限 (最多10个)，请稍后再试。"},
				}
				eBytes, _ := json.Marshal(errMsg)
				conn.WriteMessage(websocket.TextMessage, eBytes)
				continue
			}
			roomID := generateRoomID()
			room := &Room{
				ID:       roomID,
				OwnerID:  playerID,
				GameMode: gameMode,
				Players:  make(map[string]*Player),
				State:    "waiting",
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
				Payload: map[string]interface{}{"roomId": roomID, "gameMode": gameMode},
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
			if len(room.Players) >= 8 {
				room.Mutex.Unlock()
				errMsg := WsMessage{
					Type:    "error",
					Payload: map[string]interface{}{"message": "房间人数已满 (最多8人)"},
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
			room.Mutex.Lock()
			roomState := room.State
			var syncCards []Card
			var syncRound int
			var syncMode string
			if roomState == "playing" {
				syncCards = make([]Card, len(room.BoardCards))
				copy(syncCards, room.BoardCards)
				syncRound = room.CurrentRound
				syncMode = room.GameMode
			}
			room.Mutex.Unlock()
			if roomState == "playing" {
				syncMsg := WsMessage{
					Type: "game_started",
					Payload: map[string]interface{}{
						"cards":    syncCards,
						"round":    syncRound,
						"gameMode": syncMode,
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

				startMsg := WsMessage{
					Type: "game_started",
					Payload: map[string]interface{}{
						"cards":    currentRoom.BoardCards,
						"round":    currentRoom.CurrentRound,
						"gameMode": currentRoom.GameMode,
					},
				}
				broadcastToRoom(currentRoom, startMsg)

				startRound(currentRoom)
			}

		case "restart_game":
			if currentRoom != nil && currentPlayer != nil {
				currentRoom.Mutex.Lock()
				if currentRoom.State != "waiting" {
					currentRoom.State = "waiting"
					currentRoom.CurrentRound = 1
					currentRoom.RoundState = ""
					currentRoom.BoardCards = nil
					currentRoom.SongPool = nil
					currentRoom.CurrentSong = nil
					currentRoom.CurrentSongIndex = 0
					// 关闭可能残留的定时器协程
					if currentRoom.TimerCancel != nil {
						close(currentRoom.TimerCancel)
						currentRoom.TimerCancel = nil
					}
				}
				// 重置所有玩家状态
				for _, p := range currentRoom.Players {
					p.Score = 0
					p.HasAnswered = false
					p.IsReady = false
					p.GameReady = false
				}
				currentRoom.Mutex.Unlock()

				// 只给发送者回复 game_reset，不影响其他人的结算界面
				resetMsg := WsMessage{
					Type:    "game_reset",
					Payload: map[string]interface{}{},
				}
				rBytes, _ := json.Marshal(resetMsg)
				conn.WriteMessage(websocket.TextMessage, rBytes)
				broadcastRoomState(currentRoom)
			}

		case "client_ready":
			if currentRoom != nil && currentRoom.RoundState == "preparing" {
				currentRoom.Mutex.Lock()
				currentPlayer.IsReady = true

				allReady := true
				for _, p := range currentRoom.Players {
					if !p.IsReady {
						allReady = false
						break
					}
				}

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
				currentRoom.Mutex.Lock() // 抢答锁

				if currentRoom.RoundState == "playing" && !currentPlayer.HasAnswered {
					cardID := msg.Payload["cardId"].(string)
					currentPlayer.HasAnswered = true

					if cardID == currentRoom.CurrentSong.ID {
						currentPlayer.Score += 10
						for i, c := range currentRoom.BoardCards {
							if c.ID == cardID {
								currentRoom.BoardCards[i].IsMatched = true
								break
							}
						}
						endRound(currentRoom, fmt.Sprintf("玩家 [%s] 抢答正确！(+10分)", currentPlayer.Name), true, true)
					} else {
						currentPlayer.Score -= 5
						wrongMsg := WsMessage{Type: "wrong_answer", Payload: map[string]interface{}{}}
						msgBytes, _ := json.Marshal(wrongMsg)
						currentPlayer.Conn.WriteMessage(websocket.TextMessage, msgBytes)

						if isAllAnswered(currentRoom) {
							if currentRoom.NoSongCorrect {
								endRound(currentRoom, "本轮歌曲不在场上，所有玩家鉴定完毕！", true, false)
							} else {
								endRound(currentRoom, "本轮无人答对。", !isSongOnBoard(currentRoom), false)
							}
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

					songOnBoard := isSongOnBoard(currentRoom)

					if !songOnBoard {
						currentPlayer.Score += 5
						currentRoom.NoSongCorrect = true

						if isAllAnswered(currentRoom) {
							endRound(currentRoom, "本轮歌曲不在场上，所有玩家鉴定完毕！", true, false)
						}
					} else {
						currentPlayer.Score -= 5
						wrongMsg := WsMessage{Type: "wrong_answer", Payload: map[string]interface{}{}}
						msgBytes, _ := json.Marshal(wrongMsg)
						currentPlayer.Conn.WriteMessage(websocket.TextMessage, msgBytes)

						if isAllAnswered(currentRoom) {
							endRound(currentRoom, "所有玩家选择错误，这首歌其实在场上。", false, false)
						}
					}
				}
				currentRoom.Mutex.Unlock()
			}
		}
	}
}

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
			"players":  playerList,
			"ownerId":  ownerID,
			"gameMode": room.GameMode,
		},
	}
	broadcastToRoom(room, stateMsg)
}
