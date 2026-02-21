<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

interface Player { id: string, name: string, score: number }
interface Card { id: string, songName: string, isMatched: boolean }

// 模拟玩家数据
const myPlayerId = 'user_' + Math.floor(Math.random() * 1000)
const myPlayerName = '玩家' + Math.floor(Math.random() * 10)
const roomId = ref('8848')
const currentRound = ref(1)

const players = ref<Player[]>([]) // 初始为空，等后端发过来

const cards = ref<Card[]>(
  Array.from({ length: 16 }, (_, i) => ({
    id: `song_${i}`,
    songName: `测试歌曲名称 ${i + 1}`,
    isMatched: false
  }))
)

const chatMessage = ref('')
const chatLogs = ref<string[]>(['系统: 欢迎来到歌牌房间！'])
let socket: WebSocket | null = null
const isConnected = ref(false)

onMounted(() => {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const wsUrl = `${protocol}//${window.location.host}/ws`
  socket = new WebSocket(wsUrl)

  socket.onopen = () => {
    isConnected.value = true
    
    // 🌟 1. 连接成功后，第一件事是发 JSON 请求加入房间！
    if (socket) {
      socket.send(JSON.stringify({
        type: 'join_room',
        payload: {
          roomId: roomId.value,
          playerName: myPlayerName,
          playerId: myPlayerId
        }
      }))
    }
  }

  socket.onmessage = (event) => {
    // 🌟 2. 接收后端的 JSON 数据
    const data = JSON.parse(event.data)

    if (data.type === 'room_state_update') {
      // 后端发来了最新的房间玩家列表！
      players.value = data.payload.players
      console.log('房间状态更新:', players.value)
    } 
    else if (data.type === 'chat_receive') {
      // 收到聊天消息
      chatLogs.value.push(`${data.payload.sender}: ${data.payload.text}`)
    }
    else if (data.type === 'error') {
      alert(data.payload.message) // 弹窗提示房间已满
      chatLogs.value.push(`系统: ${data.payload.message}`)
    }
  }

  socket.onclose = () => { isConnected.value = false }
})

const handleCardClick = (card: Card) => {
  if (card.isMatched) return
  console.log(`你点击了歌牌: ${card.songName}`)
}

const handleNoSongClick = () => {
  console.log('你点击了: 没有这首歌')
}

const sendChat = () => {
  if (chatMessage.value.trim() && socket && isConnected.value) {
    // 3. 发送 JSON 格式的聊天
    socket.send(JSON.stringify({
      type: 'chat',
      payload: { text: chatMessage.value }
    }))
    chatMessage.value = ''
  }
}
</script>

<template>
  <div class="game-wrapper">
    <div class="game-layout">
      
      <aside class="sidebar">
        <div class="player-list">
          <div v-for="player in players" :key="player.id" class="player-item">
            <span class="p-name">{{ player.name }}</span>
            <span class="p-score" :class="{ 'negative': player.score < 0 }">
              {{ player.score }} 分
            </span>
          </div>
        </div>
        
        <div class="sidebar-bottom">
          <button class="no-song-btn" @click="handleNoSongClick">没有这首歌</button>
          <div class="room-info">房间号: <strong>{{ roomId }}</strong></div>
        </div>
      </aside>

      <main class="main-area">
        
        <header class="top-bar">
          <div class="audio-status">🔊 播放中...</div>
          <div class="round-display">第 {{ currentRound }} 局</div>
          <div class="actions">
            <button class="icon-btn" title="游戏规则">ℹ️</button>
            <button class="icon-btn" title="设置">⚙️</button>
          </div>
        </header>

        <div class="karuta-board">
          <div 
            v-for="card in cards" 
            :key="card.id" 
            class="karuta-card"
            :class="{ 'card-hidden': card.isMatched }"
            @click="handleCardClick(card)"
          >
            <span class="card-text">{{ card.songName }}</span>
          </div>
        </div>

        <footer class="chat-area">
          <div class="chat-history">
            <div v-for="(log, idx) in chatLogs" :key="idx" class="chat-line">{{ log }}</div>
          </div>
          <div class="chat-input-box">
            <input 
              v-model="chatMessage" 
              @keyup.enter="sendChat" 
              placeholder="局内聊天框..." 
              type="text"
            />
          </div>
        </footer>

      </main>
    </div>
  </div>
</template>

<style>
/* 全局重置，防止浏览器默认的 margin 导致出现滚动条 */
body, html {
  margin: 0;
  padding: 0;
  width: 100%;
  height: 100%;
  overflow: hidden; /* 绝对禁止页面出现滚动条 */
}
* {
  box-sizing: border-box; /* 让边框计算在宽高内，防止撑破容器 */
}
</style>

<style scoped>
/* 最外层安全区，处理全屏边缘 */
.game-wrapper {
  width: 100vw;
  height: 100dvh; /* dvh 完美适配手机浏览器上下工具栏 */
  padding: 10px;
  background-color: #eee;
  display: flex;
  justify-content: center;
  align-items: center;
}

/* 游戏主容器 */
.game-layout {
  display: flex;
  width: 100%;
  max-width: 1200px;
  height: 100%;
  border: 4px solid #000;
  background-color: #fcfcfc;
  font-family: 'Noto Sans JP', sans-serif;
  box-shadow: 4px 4px 0px rgba(0,0,0,0.2);
}

/* --- 左侧边栏 --- */
.sidebar {
  width: 220px;
  border-right: 4px solid #000;
  display: flex;
  flex-direction: column;
  background-color: #fff;
}
.player-list {
  flex: 1;
  overflow-y: auto;
}
.player-item {
  border-bottom: 2px solid #000;
  padding: 12px 10px;
  display: flex;
  justify-content: space-between;
  font-weight: bold;
  font-size: 0.95rem;
}
.p-score.negative { color: red; }

/* 左下角操作区 */
.sidebar-bottom {
  border-top: 4px solid #000;
  display: flex;
  flex-direction: column;
  background-color: #f9f9f9;
}
.no-song-btn {
  margin: 15px;
  padding: 12px;
  border: 2px solid #000;
  background: #ff5252;
  color: white;
  font-weight: bold;
  font-size: 1rem;
  cursor: pointer;
  border-radius: 4px;
  box-shadow: 2px 2px 0px #000;
  transition: all 0.1s;
}
.no-song-btn:active {
  transform: translate(2px, 2px);
  box-shadow: 0px 0px 0px #000;
}
.room-info {
  border-top: 2px dashed #000;
  padding: 10px;
  text-align: center;
  font-weight: bold;
  background: #fff;
}

/* --- 右侧主区域 --- */
.main-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0; /* 防止内容过长撑破 Flex 容器 */
}

/* 顶部栏 */
.top-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 15px 20px;
  border-bottom: 2px solid #000;
  font-weight: bold;
  font-size: 1.1rem;
}
.actions { display: flex; gap: 10px; }
.icon-btn { background: none; border: none; font-size: 1.5rem; cursor: pointer; }

/* --- 歌牌 4x4 网格 (核心) --- */
.karuta-board {
  flex: 1;
  min-height: 0; 
  display: grid;
  /* 🌟 将列宽从 1fr 改为 auto，不强制拉伸宽度 */
  grid-template-columns: repeat(4, auto);
  /* 行高依然平分剩余的可用高度 */
  grid-template-rows: repeat(4, minmax(0, 1fr));
  
  justify-content: center; /* 🌟 让整个 4x4 网格在区域内水平居中 */
  gap: 15px 30px; /* 🌟 增大间距：上下 15px，左右 30px (你可以根据喜好微调) */
  padding: 15px;
  background-color: #f4f4f4;
}

.karuta-card {
  aspect-ratio: 2 / 3; /* 🌟 核心魔法：强制卡牌比例为传统长方形 (宽2 高3) */
  height: 100%; /* 高度自动占满网格分配给它的那 1/4 空间 */
  
  border: 3px solid #000;
  background-color: #fff;
  border-radius: 4px;
  display: flex;
  justify-content: center;
  align-items: center;
  cursor: pointer;
  box-shadow: 2px 2px 0px #000;
  transition: transform 0.1s, background-color 0.1s;
  overflow: hidden; 
}
.karuta-card:active {
  transform: translate(2px, 2px);
  box-shadow: 0px 0px 0px #000;
}
.karuta-card.card-hidden {
  visibility: hidden;
}

.card-text {
  writing-mode: vertical-rl;
  text-orientation: upright;
  letter-spacing: 2px;
  font-size: clamp(0.9rem, 1.5vh, 1.2rem); /* 文字大小随屏幕高度自动缩放 */
  font-weight: 600;
  text-align: center;
  padding: 5px;
}

/* --- 底部聊天区 --- */
.chat-area {
  height: 120px; /* 固定高度，不被网格挤压 */
  border-top: 4px solid #000;
  display: flex;
  flex-direction: column;
  background: #fff;
}
.chat-history {
  flex: 1;
  padding: 8px 15px;
  overflow-y: auto;
  font-size: 0.85rem;
  color: #333;
}
.chat-line { margin-bottom: 4px; }
.chat-input-box {
  display: flex;
  border-top: 1px solid #ddd;
}
.chat-input-box input {
  flex: 1;
  border: none;
  padding: 10px 15px;
  font-size: 0.95rem;
  outline: none;
}

/* ==========================================
   响应式设计：适配移动端 (宽度小于 768px 时)
   ========================================== */
@media (max-width: 768px) {
  .game-wrapper { padding: 0; } /* 手机端去掉外留白，完全铺满 */
  .game-layout { border: none; flex-direction: column; }
  
  /* 左侧栏变成顶部栏 */
  .sidebar { width: 100%; border-right: none; border-bottom: 3px solid #000; flex-direction: row; justify-content: space-between; align-items: stretch; }
  .player-list { display: flex; overflow-x: auto; flex: 1; border-right: 2px dashed #000; }
  .player-item { border-bottom: none; border-right: 1px solid #ccc; padding: 10px; flex-direction: column; justify-content: center; align-items: center; min-width: 70px; }
  .p-name { font-size: 0.8rem; }
  .p-score { font-size: 0.9rem; }
  
  /* 左下角移到右上角 */
  .sidebar-bottom { border-top: none; flex-direction: column; justify-content: center; min-width: 100px; }
  .no-song-btn { margin: 5px; padding: 6px; font-size: 0.85rem; }
  .room-info { border-top: none; padding: 2px; font-size: 0.8rem; }
  
  /* 游戏区微调 */
  .top-bar { padding: 8px 10px; font-size: 0.9rem; }
  .karuta-board { gap: 6px; padding: 6px; }
  .karuta-card { border-width: 2px; box-shadow: 1px 1px 0px #000; }
  .card-text { letter-spacing: 0px; }
  .chat-area { height: 100px; }
}
</style>