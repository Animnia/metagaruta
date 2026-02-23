<script setup lang="ts">
import { ref, onUnmounted, nextTick, watch, computed } from 'vue'

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
const chatHistoryRef = ref<HTMLElement | null>(null)


// 倒计时与状态文本控制
const audioStatusText = ref('🔊 等待开始...')
let playTimer: ReturnType<typeof setInterval> | null = null
let remainingTime = ref(0)
let totalPlayTime = 0

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

const sortedPlayers = computed(() => {
  return [...players.value].sort((a, b) => b.score - a.score)
})

// 初始状态下场上没有牌
const cards = ref<Card[]>([]) 
const gameState = ref('waiting') // 控制显示“开始按钮”还是“进行中”
const currentRound = ref(1)
const chatMessage = ref('')
const chatLogs = ref<string[]>(['系统: 欢迎来到歌牌房间！'])

let socket: WebSocket | null = null
const isConnected = ref(false)
const hasAnswered = ref(false)

// 控制弹窗和设置的变量
const showRules = ref(false)
const showSettings = ref(false)
const displayMode = ref('original')

let heartbeatInterval: ReturnType<typeof setInterval> | null = null // 心跳定时器

// 监听聊天记录变化，自动滚动到底部
watch(chatLogs, () => {
  nextTick(() => {
    if (chatHistoryRef.value) {
      chatHistoryRef.value.scrollTop = chatHistoryRef.value.scrollHeight
    }
  })
}, { deep: true })


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

    heartbeatInterval = setInterval(() => {
      if (socket && isConnected.value) {
        socket.send(JSON.stringify({ type: 'ping', payload: {} }))
      }
    }, 30000)
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
      hasAnswered.value = false // 新回合开始，恢复答题资格
      const startTime = data.payload.startTime
      totalPlayTime = data.payload.playDuration // 后端传来的实际播放时长
      audioStatusText.value = '⏳ 音频缓冲中...' // 更新状态文本
      chatLogs.value.push(`系统: 第 ${currentRound.value} 局音频缓冲中...`)
      
      // 核心防作弊与防缓存机制：带上当前时间戳 t=...，强迫浏览器重新请求
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

    else if (data.type === 'countdown_start') {
      audioStatusText.value = '⏳ 准备播放...'
      
      let countdown = 4
      chatLogs.value.push(`系统: ${countdown} 秒后自动播放...`)
      
      const cdTimer = setInterval(() => {
        countdown--
        if (countdown > 0) {
          chatLogs.value.push(`系统: ${countdown} 秒后自动播放...`)
        } else {
          clearInterval(cdTimer)
        }
      }, 1000)
    }
    
    // 收到裁判发令枪：所有人同时开始播放！
    else if (data.type === 'play_round') {
      gameState.value = 'playing'
      chatLogs.value.push(`系统: 播放开始！仔细听...`)

      remainingTime.value = totalPlayTime
      audioStatusText.value = '🔊 播放中...'
      
      if (playTimer) clearInterval(playTimer)
      playTimer = setInterval(() => {
        remainingTime.value--
        // 最后 15 秒显示倒计时
        if (remainingTime.value <= 15 && remainingTime.value > 0) {
          audioStatusText.value = `⏳ 倒计时: ${remainingTime.value} 秒`
        } else if (remainingTime.value <= 0) {
          audioStatusText.value = '⏳ 结算中...'
          clearInterval(playTimer!)
        } else {
          audioStatusText.value = '🔊 播放中...'
        }
      }, 1000)

      if (audioPlayer.value) {
        audioPlayer.value.play().catch(e => {
          console.error('音频播放失败，真实原因:', e) 
          chatLogs.value.push(`系统: 播放异常 (${e.name})`)
        })
      }
    }

    else if (data.type === 'wrong_answer') {
      hasAnswered.value = true // 答错了，剥夺本局继续点击的资格
      chatLogs.value.push('系统: ❌ 回答错误，扣除 5 分，本局无法继续操作！')
    }

    else if (data.type === 'round_end') {
      gameState.value = 'ended'
      hasAnswered.value = true
      cards.value = data.payload.cards // 刷新牌面，被答对的牌会自动消失

      if (playTimer) clearInterval(playTimer)
      audioStatusText.value = '⏹️ 回合结束'
      
      // 停止播放音乐
      if (audioPlayer.value) {
        audioPlayer.value.pause()
      }
      
      chatLogs.value.push(`🏆 ${data.payload.reason}`)
      // 只有当有人答对场上的歌牌时，才公布答案
      if (data.payload.showAnswer) {
        chatLogs.value.push(`🎵 正确答案是: ${data.payload.correctSong}`)
      }
    }

    else if (data.type === 'game_over') {
      gameState.value = 'ended'
      if (playTimer) clearInterval(playTimer)
      audioStatusText.value = '🎉 游戏结束！'
      if (audioPlayer.value) audioPlayer.value.pause()
      chatLogs.value.push('系统: 场上所有歌牌已被找齐，游戏结束！')
    }

    else if (data.type === 'error') {
      alert(data.payload.message)
      // 如果房间满了被拒绝，退回到首页
      currentView.value = 'home' 
      socket?.close()
    }
  }

  socket.onclose = () => {
    isConnected.value = false 
    //清理定时器
    if (heartbeatInterval) clearInterval(heartbeatInterval)
  }
}

const createGame = () => {
  alert('测试阶段：请直接输入房间号加入已有房间！')
}

const startGame = () => {
  if (socket && isConnected.value) {
    // 利用真实的点击事件，强行拿到浏览器的播放授权
    if (audioPlayer.value) {
      audioPlayer.value.volume = 0; // 设为静音
      audioPlayer.value.play().then(() => {
        audioPlayer.value!.pause(); // 拿到权限后立刻暂停
        audioPlayer.value!.volume = 1; // 恢复正常音量
        console.log("✅ 浏览器音频权限解锁成功！");
      }).catch(e => {
        console.warn("⚠️ 音频预解锁失败:", e);
      });
    }
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
  // 如果牌没了、游戏没在进行、或者自己已经答过题了，就不准点
  if (card.isMatched || gameState.value !== 'playing' || hasAnswered.value) return
  
  if (socket && isConnected.value) {
    socket.send(JSON.stringify({
      type: 'buzz',
      payload: { cardId: card.id }
    }))
  }
}

const handleNoSongClick = () => {
  if (gameState.value !== 'playing' || hasAnswered.value) return

  // 点击后立刻将自己的状态锁定，使按钮变灰
  hasAnswered.value = true
  chatLogs.value.push('系统: 已选择“没有这首歌”，等待其他玩家操作...')

  if (socket && isConnected.value) {
    socket.send(JSON.stringify({
      type: 'no_song',
      payload: {}
    }))
  }
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
          <div v-for="player in sortedPlayers" :key="player.id" class="player-item">
            <span class="p-name">{{ player.name }}</span>
            <span class="p-score" :class="{ 'negative': player.score < 0 }">{{ player.score }} 分</span>
          </div>
        </div>
        <div class="sidebar-bottom">
          <button class="no-song-btn" :class="{ 'disabled': hasAnswered || gameState !== 'playing' }" @click="handleNoSongClick">没有这首歌</button>
          <div class="room-info">房间号: <strong>{{ inputRoomId }}</strong></div>
        </div>
      </aside>

      <main class="main-area">
        <header class="top-bar">
          <div class="audio-status">{{ audioStatusText }}</div>
          <div class="round-display">第 {{ currentRound }} 局</div>
          <div class="actions">
            <button v-if="gameState === 'waiting'" class="start-btn" @click="startGame">
              🚀 开始游戏
            </button>
            <button class="icon-btn" @click="showRules = true">ℹ️</button>
            <button class="icon-btn" @click="showSettings = true">⚙️</button>
          </div>
        </header>

        <div class="karuta-board">
          <div v-for="card in cards" :key="card.id" class="karuta-card" :class="{ 'card-hidden': card.isMatched }" @click="handleCardClick(card)">
            <span class="card-text">{{ displayMode === 'original' ? card.titleOriginal : card.titleTranslation }}</span>
          </div>
        </div>

        <footer class="chat-area">
          <div class="chat-history" ref="chatHistoryRef">
            <div v-for="(log, idx) in chatLogs" :key="idx" class="chat-line">{{ log }}</div>
          </div>
          <div class="chat-input-box">
            <input v-model="chatMessage" @keyup.enter="sendChat" placeholder="局内聊天框..." type="text" />
          </div>
        </footer>
      </main>
    </div>
    <div v-if="showRules" class="modal-overlay" @click.self="showRules = false">
        <div class="modal-box">
          <h2>ℹ️ 游戏玩法</h2>
          <p>1. 仔细聆听播放的音乐片段。</p>
          <p>2. 在 16 张歌牌中寻找对应的歌曲，最先点击正确的玩家得分(+10)。</p>
          <p>3. 如果点错将扣分(-5)且本局锁定。</p>
          <p>4. 歌曲可能不在场上！此时点击“没有这首歌”得分(+5)。</p>
          <button class="btn-primary" @click="showRules = false" style="width:100%; margin-top:15px;">明白</button>
        </div>
      </div>

      <div v-if="showSettings" class="modal-overlay" @click.self="showSettings = false">
        <div class="modal-box">
          <h2>⚙️ 玩家设置</h2>
          <div class="form-group">
            <label>歌牌显示语言：</label>
            <select v-model="displayMode" style="width:100%; padding:10px; border:2px solid #000; outline:none; font-size:1rem;">
              <option value="original">原文 (Original)</option>
              <option value="translation">译文 (Translation)</option>
            </select>
          </div>
          <button class="btn-primary" @click="showSettings = false" style="width:100%; margin-top:15px;">关闭</button>
        </div>
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
.modal-overlay {
  position: absolute; top: 0; left: 0; width: 100%; height: 100%;
  background: rgba(0,0,0,0.6); display: flex; justify-content: center; align-items: center;
  z-index: 100;
}
.modal-box {
  background: #fff; border: 4px solid #000; padding: 25px; width: 90%; max-width: 400px;
  box-shadow: 8px 8px 0px #000;
}
.modal-box h2 { margin-top: 0; border-bottom: 2px solid #000; padding-bottom: 10px; }
.modal-box p { line-height: 1.6; font-weight: bold; font-size: 0.95rem; }

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
.no-song-btn.disabled { background: #ccc; cursor: not-allowed; transform: none; box-shadow: none; }
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