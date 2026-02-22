<script setup lang="ts">
import { ref, onUnmounted } from 'vue'

interface Player { 
  id: string, 
  name: string, 
  score: number

}
interface Card { 
  id: string, 
  titleOriginal: string, 
  titleTranslation: string, 
  isMatched: boolean 
}

const audioPlayer = ref<HTMLAudioElement | null>(null)

// ==========================================
// 1. 页面路由与表单状态
// ==========================================
// 控制当前显示哪个页面：'home' 是开始页面, 'game' 是游戏房间
const currentView = ref('home') 

// 用户在输入框里填的数据
const inputName = ref('')
const inputRoomId = ref('')

// 玩家的内部唯一 ID (保持随机生成即可)
const myPlayerId = 'user_' + Math.floor(Math.random() * 10000)

// ==========================================
// 2. 游戏内状态
// ==========================================
const players = ref<Player[]>([])
// 初始状态下场上没有牌
const cards = ref<Card[]>([]) 
const gameState = ref('waiting') // 控制显示“开始按钮”还是“进行中”
const currentRound = ref(1)
const chatMessage = ref('')
const chatLogs = ref<string[]>(['系统: 欢迎来到歌牌房间！'])

let socket: WebSocket | null = null
const isConnected = ref(false)

// ==========================================
// 3. 核心方法：加入房间
// ==========================================
const joinGame = () => {
  // 简单的表单验证
  if (!inputName.value.trim()) return alert('请输入玩家名称！')
  if (!inputRoomId.value.trim()) return alert('请输入房间号！')

  // 切换页面到游戏房间
  currentView.value = 'game'

  // 开始连接 WebSocket (以前这部分在 onMounted 里)
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const wsUrl = `${protocol}//${window.location.host}/ws`
  socket = new WebSocket(wsUrl)

  socket.onopen = () => {
    isConnected.value = true
    // 发送加入房间请求，使用用户刚才输入的名字和房间号
    socket?.send(JSON.stringify({
      type: 'join_room',
      payload: {
        roomId: inputRoomId.value.trim(),
        playerName: inputName.value.trim(),
        playerId: myPlayerId
      }
    }))
  }

  socket.onmessage = (event) => {
    const data = JSON.parse(event.data)
    if (data.type === 'room_state_update') {
      players.value = data.payload.players
    } 
    else if (data.type === 'chat_receive') {
      chatLogs.value.push(`${data.payload.sender}: ${data.payload.text}`)
    }
    else if (data.type === 'game_started') {
      // 后端发牌了！
      cards.value = data.payload.cards
      currentRound.value = data.payload.round
      gameState.value = 'playing'
      chatLogs.value.push('系统: 游戏开始！生成了 16 张歌牌。')
    }
    // 收到裁判指令：静音加载音频，设置进度，但不准播放
    else if (data.type === 'prepare_round') {
      currentRound.value = data.payload.round
      const startTime = data.payload.startTime
      chatLogs.value.push(`系统: 第 ${currentRound.value} 局音频缓冲中...`)
      
      // 🌟 核心防作弊与防缓存机制：带上当前时间戳 t=...，强迫浏览器重新请求
      const audioUrl = `/api/audio?roomId=${inputRoomId.value}&t=${new Date().getTime()}`
      
      if (audioPlayer.value) {
        audioPlayer.value.src = audioUrl
        
        // 监听浏览器“可以流畅播放”事件
        audioPlayer.value.oncanplay = () => {
          // 清空事件，防止因为网络波动重复触发
          audioPlayer.value!.oncanplay = null 
          
          // 跳转到随机生成的裁切时间
          audioPlayer.value!.currentTime = startTime
          
          // 举手告诉裁判：我缓冲完毕了！
          socket?.send(JSON.stringify({ type: 'client_ready', payload: {} }))
        }
      }
    }
    
    // 收到裁判发令枪：所有人同时开始播放！
    else if (data.type === 'play_round') {
      chatLogs.value.push(`系统: 播放开始！仔细听...`)
      if (audioPlayer.value) {
        audioPlayer.value.play().catch(e => {
          console.error('自动播放被浏览器拦截:', e)
          chatLogs.value.push('系统: 浏览器限制自动播放，请点击网页任意处恢复。')
        })
      }
    }
    else if (data.type === 'error') {
      alert(data.payload.message)
      // 如果房间满了被拒绝，退回到首页
      currentView.value = 'home' 
      socket?.close()
    }
  }

  socket.onclose = () => { isConnected.value = false }
}

const createGame = () => {
  alert('测试阶段：请直接输入房间号加入已有房间！')
}

const startGame = () => {
  if (socket && isConnected.value) {
    socket.send(JSON.stringify({ type: 'start_game', payload: {} }))
  }
}

// ==========================================
// 4. 游戏内交互方法
// ==========================================
onUnmounted(() => {
  if (socket) socket.close()
})

const handleCardClick = (card: Card) => {
  if (card.isMatched) return
  console.log(`你点击了歌牌: ${card.titleOriginal}`)
}

const handleNoSongClick = () => {
  console.log('你点击了: 没有这首歌')
}

const sendChat = () => {
  if (chatMessage.value.trim() && socket && isConnected.value) {
    socket.send(JSON.stringify({
      type: 'chat',
      payload: { text: chatMessage.value }
    }))
    chatMessage.value = ''
  }
}
</script>

<template>
  <audio ref="audioPlayer" preload="auto"></audio>
  <div v-if="currentView === 'home'" class="home-wrapper">
    <div class="login-box">
      <h1 class="game-title">🧠 智力竞技歌牌</h1>
      <p class="subtitle">Metagaruta Online</p>
      
      <div class="form-group">
        <label>玩家名称</label>
        <input v-model="inputName" type="text" placeholder="输入你的昵称" @keyup.enter="joinGame" />
      </div>

      <div class="form-group">
        <label>房间号</label>
        <input v-model="inputRoomId" type="text" placeholder="例如: 8848" @keyup.enter="joinGame" />
      </div>

      <div class="btn-group">
        <button class="btn-primary" @click="joinGame">加入房间</button>
        <button class="btn-secondary" @click="createGame">创建房间</button>
      </div>
    </div>
  </div>

  <div v-else class="game-wrapper">
    <div class="game-layout">
      <aside class="sidebar">
        <div class="player-list">
          <div v-for="player in players" :key="player.id" class="player-item">
            <span class="p-name">{{ player.name }}</span>
            <span class="p-score" :class="{ 'negative': player.score < 0 }">{{ player.score }} 分</span>
          </div>
        </div>
        <div class="sidebar-bottom">
          <button class="no-song-btn" @click="handleNoSongClick">没有这首歌</button>
          <div class="room-info">房间号: <strong>{{ inputRoomId }}</strong></div>
        </div>
      </aside>

      <main class="main-area">
        <header class="top-bar">
          <div class="audio-status">🔊 等待开始...</div>
          <div class="round-display">第 {{ currentRound }} 局</div>
          <div class="actions">
            <button v-if="gameState === 'waiting'" class="start-btn" @click="startGame">
              🚀 开始游戏
            </button>
            <button class="icon-btn">ℹ️</button>
            <button class="icon-btn">⚙️</button>
          </div>
        </header>

        <div class="karuta-board">
          <div v-for="card in cards" :key="card.id" class="karuta-card" :class="{ 'card-hidden': card.isMatched }" @click="handleCardClick(card)">
            <span class="card-text">{{ card.titleOriginal }}</span>
          </div>
        </div>

        <footer class="chat-area">
          <div class="chat-history">
            <div v-for="(log, idx) in chatLogs" :key="idx" class="chat-line">{{ log }}</div>
          </div>
          <div class="chat-input-box">
            <input v-model="chatMessage" @keyup.enter="sendChat" placeholder="局内聊天框..." type="text" />
          </div>
        </footer>
      </main>
    </div>
  </div>
</template>

<style>
/* 全局重置 */
body, html { margin: 0; padding: 0; width: 100%; height: 100%; overflow: hidden; background-color: #eee; }
* { box-sizing: border-box; }
</style>

<style scoped>
/* ==========================================
   首页专属样式 (硬核黑白日系风)
   ========================================== */
.home-wrapper {
  width: 100vw;
  height: 100dvh;
  display: flex;
  justify-content: center;
  align-items: center;
  background-color: #f4f4f4;
  font-family: 'Noto Sans JP', sans-serif;
}

.login-box {
  background: #fff;
  border: 4px solid #000;
  padding: 40px;
  width: 90%;
  max-width: 400px;
  box-shadow: 8px 8px 0px #000; /* 硬核阴影 */
  text-align: center;
}

.game-title { margin: 0; font-size: 2rem; letter-spacing: 2px; }
.subtitle { margin-top: 5px; margin-bottom: 30px; font-weight: bold; color: #555; letter-spacing: 1px; }

.form-group {
  margin-bottom: 20px;
  text-align: left;
}
.form-group label {
  display: block;
  font-weight: bold;
  margin-bottom: 8px;
}
.form-group input {
  width: 100%;
  padding: 12px;
  border: 2px solid #000;
  font-size: 1rem;
  outline: none;
  transition: box-shadow 0.2s;
}
.form-group input:focus {
  box-shadow: 4px 4px 0px rgba(0,0,0,0.2);
}

.btn-group {
  display: flex;
  flex-direction: column;
  gap: 15px;
  margin-top: 30px;
}
.btn-primary, .btn-secondary {
  padding: 12px;
  font-size: 1.1rem;
  font-weight: bold;
  border: 2px solid #000;
  cursor: pointer;
  transition: transform 0.1s, box-shadow 0.1s;
}
.btn-primary {
  background-color: #000;
  color: #fff;
  box-shadow: 4px 4px 0px #ccc;
}
.btn-secondary {
  background-color: #fff;
  color: #000;
  box-shadow: 4px 4px 0px #000;
}
.btn-primary:active, .btn-secondary:active {
  transform: translate(2px, 2px);
  box-shadow: 0px 0px 0px #000;
}

/* ==========================================
   游戏房间样式 (保持原样)
   ========================================== */
.game-wrapper { width: 100vw; height: 100dvh; padding: 10px; display: flex; justify-content: center; align-items: center; }
.game-layout { display: flex; width: 100%; max-width: 1200px; height: 100%; border: 4px solid #000; background-color: #fcfcfc; font-family: 'Noto Sans JP', sans-serif; box-shadow: 4px 4px 0px rgba(0,0,0,0.2); }
.sidebar { width: 220px; border-right: 4px solid #000; display: flex; flex-direction: column; background-color: #fff; }
.player-list { flex: 1; overflow-y: auto; }
.player-item { border-bottom: 2px solid #000; padding: 12px 10px; display: flex; justify-content: space-between; font-weight: bold; font-size: 0.95rem; }
.p-score.negative { color: red; }
.sidebar-bottom { border-top: 4px solid #000; display: flex; flex-direction: column; background-color: #f9f9f9; }
.no-song-btn { margin: 15px; padding: 12px; border: 2px solid #000; background: #ff5252; color: white; font-weight: bold; font-size: 1rem; cursor: pointer; border-radius: 4px; box-shadow: 2px 2px 0px #000; transition: all 0.1s; }
.no-song-btn:active { transform: translate(2px, 2px); box-shadow: 0px 0px 0px #000; }
.room-info { border-top: 2px dashed #000; padding: 10px; text-align: center; font-weight: bold; background: #fff; }
.main-area { flex: 1; display: flex; flex-direction: column; min-width: 0; }
.top-bar { display: flex; justify-content: space-between; align-items: center; padding: 15px 20px; border-bottom: 2px solid #000; font-weight: bold; font-size: 1.1rem; }
.actions { display: flex; gap: 10px; }
.start-btn { background: #42b883; color: white; border: 2px solid #000; padding: 5px 10px; font-weight: bold; cursor: pointer;}
.icon-btn { background: none; border: none; font-size: 1.5rem; cursor: pointer; }
.karuta-board { flex: 1; min-height: 0; display: grid; grid-template-columns: repeat(4, auto); grid-template-rows: repeat(4, minmax(0, 1fr)); justify-content: center; gap: 15px 30px; padding: 15px; background-color: #f4f4f4; }
.karuta-card { aspect-ratio: 2 / 3; height: 100%; border: 3px solid #000; background-color: #fff; border-radius: 4px; display: flex; justify-content: center; align-items: center; cursor: pointer; box-shadow: 2px 2px 0px #000; transition: transform 0.1s, background-color 0.1s; overflow: hidden; }
.karuta-card:active { transform: translate(2px, 2px); box-shadow: 0px 0px 0px #000; }
.karuta-card.card-hidden { visibility: hidden; }
.card-text { writing-mode: vertical-rl; text-orientation: upright; letter-spacing: 2px; font-size: clamp(0.9rem, 1.5vh, 1.2rem); font-weight: 600; text-align: center; padding: 5px; }
.chat-area { height: 120px; border-top: 4px solid #000; display: flex; flex-direction: column; background: #fff; }
.chat-history { flex: 1; padding: 8px 15px; overflow-y: auto; font-size: 0.85rem; color: #333; }
.chat-line { margin-bottom: 4px; }
.chat-input-box { display: flex; border-top: 1px solid #ddd; }
.chat-input-box input { flex: 1; border: none; padding: 10px 15px; font-size: 0.95rem; outline: none; }

@media (max-width: 768px) {
  .game-wrapper { padding: 0; }
  .game-layout { border: none; flex-direction: column; }
  .sidebar { width: 100%; border-right: none; border-bottom: 3px solid #000; flex-direction: row; justify-content: space-between; align-items: stretch; }
  .player-list { display: flex; overflow-x: auto; flex: 1; border-right: 2px dashed #000; }
  .player-item { border-bottom: none; border-right: 1px solid #ccc; padding: 10px; flex-direction: column; justify-content: center; align-items: center; min-width: 70px; }
  .p-name { font-size: 0.8rem; }
  .p-score { font-size: 0.9rem; }
  .sidebar-bottom { border-top: none; flex-direction: column; justify-content: center; min-width: 100px; }
  .no-song-btn { margin: 5px; padding: 6px; font-size: 0.85rem; }
  .room-info { border-top: none; padding: 2px; font-size: 0.8rem; }
  .top-bar { padding: 8px 10px; font-size: 0.9rem; }
  .karuta-board { gap: 6px; padding: 6px; }
  .karuta-card { border-width: 2px; box-shadow: 1px 1px 0px #000; }
  .card-text { letter-spacing: 0px; }
  .chat-area { height: 100px; }
}
</style>