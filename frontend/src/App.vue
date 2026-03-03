<script setup lang="ts">
import { ref, onUnmounted, nextTick, watch, computed } from 'vue'

interface Player { 
  id: string, 
  name: string, 
  score: number,
  gameReady: boolean
}
interface Card { 
  id: string, 
  titleOriginal: string, 
  titleTranslation: string, 
  isMatched: boolean,
  characterId?: number,
  characterName?: string,
  pictureUrl?: string
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
const selectedGameMode = ref<'vocaloid' | 'touhou'>('vocaloid') // 创建房间时选择的游戏模式

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
const chatLogs = ref<string[]>(['系统: 欢迎来到metagaruta！'])

let socket: WebSocket | null = null
const isConnected = ref(false)
const hasAnswered = ref(false)

// 控制弹窗和设置的变量
const showRules = ref(false)
const showSettings = ref(false)
const showContact = ref(false)
const displayMode = ref('original')
const showCharacterName = ref(false) // touhou 模式：是否显示角色名称，默认不显示
const roomGameMode = ref('vocaloid') // 当前房间的实际游戏模式 (从服务器获取)

// 房主与准备状态
const ownerId = ref('')
const isOwner = computed(() => myPlayerId === ownerId.value)
const myReadyState = computed(() => {
  const me = players.value.find(p => p.id === myPlayerId)
  return me?.gameReady ?? false
})
const allNonOwnersReady = computed(() => {
  const nonOwners = players.value.filter(p => p.id !== ownerId.value)
  return nonOwners.length === 0 || nonOwners.every(p => p.gameReady)
})

// 结算屏幕状态
const showResult = ref(false)
const finalPlayers = ref<Player[]>([])
const topThree = computed(() => {
  return [...finalPlayers.value].sort((a, b) => b.score - a.score).slice(0, 3)
})
const myRank = computed(() => {
  const sorted = [...finalPlayers.value].sort((a, b) => b.score - a.score)
  const idx = sorted.findIndex(p => p.id === myPlayerId)
  return idx >= 0 ? idx + 1 : -1
})
const myFinalScore = computed(() => {
  const me = finalPlayers.value.find(p => p.id === myPlayerId)
  return me?.score ?? 0
})

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
// 3. 核心方法：WebSocket 与房间管理
// ==========================================
const handleWsMessage = (event: MessageEvent) => {
  const data = JSON.parse(event.data)
  if (data.type === 'room_created') {
    inputRoomId.value = data.payload.roomId
    if (data.payload.gameMode) {
      roomGameMode.value = data.payload.gameMode
    }
  }
  else if (data.type === 'room_state_update') {
    players.value = data.payload.players
    if (data.payload.ownerId) {
      ownerId.value = data.payload.ownerId
    }
    if (data.payload.gameMode) {
      roomGameMode.value = data.payload.gameMode
    }
  } 
  else if (data.type === 'chat_receive') {
    chatLogs.value.push(`${data.payload.sender}: ${data.payload.text}`)
  }
  else if (data.type === 'game_started') {
    // 后端发牌了！
    cards.value = data.payload.cards
    currentRound.value = data.payload.round
    gameState.value = 'playing'
    chatLogs.value.push('系统: 游戏开始！仔细聆听音乐片段，寻找对应的歌牌！')
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
      
      // 监听浏览器"可以流畅播放"事件
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
    chatLogs.value.push(`系统: 播放开始...`)

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
    chatLogs.value.push('系统: 游戏结束！')
    // 展示结算弹窗
    if (data.payload.players) {
      finalPlayers.value = data.payload.players
    } else {
      finalPlayers.value = [...players.value]
    }
    showResult.value = true
  }

  else if (data.type === 'game_reset') {
    // 房间重置回等待状态
    showResult.value = false
    gameState.value = 'waiting'
    cards.value = []
    currentRound.value = 1
    hasAnswered.value = false
    audioStatusText.value = '🔊 等待开始...'
    chatLogs.value.push('系统: 房间已重置，等待开始新一局！')
  }

  else if (data.type === 'error') {
    alert(data.payload.message)
    // 如果房间满了被拒绝，退回到首页
    currentView.value = 'home' 
    socket?.close()
  }
}

const connectWebSocket = (openMessage: object) => {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const wsUrl = `${protocol}//${window.location.host}/ws`
  socket = new WebSocket(wsUrl)

  socket.onopen = () => {
    isConnected.value = true
    socket?.send(JSON.stringify(openMessage))

    heartbeatInterval = setInterval(() => {
      if (socket && isConnected.value) {
        socket.send(JSON.stringify({ type: 'ping', payload: {} }))
      }
    }, 30000)
  }

  socket.onmessage = handleWsMessage

  socket.onclose = () => {
    isConnected.value = false 
    if (heartbeatInterval) clearInterval(heartbeatInterval)
  }
}

const joinGame = () => {
  if (!inputName.value.trim()) return alert('请输入玩家名称！')
  if (!inputRoomId.value.trim()) return alert('请输入房间号！')
  currentView.value = 'game'
  connectWebSocket({
    type: 'join_room',
    payload: {
      roomId: inputRoomId.value.trim(),
      playerName: inputName.value.trim(),
      playerId: myPlayerId
    }
  })
}

const createGame = () => {
  if (!inputName.value.trim()) return alert('请输入玩家名称！')
  currentView.value = 'game'
  connectWebSocket({
    type: 'create_room',
    payload: {
      playerName: inputName.value.trim(),
      playerId: myPlayerId,
      gameMode: selectedGameMode.value
    }
  })
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

const toggleReady = () => {
  if (socket && isConnected.value) {
    // 当从"未准备"切换到"准备"时，解锁浏览器音频权限
    if (audioPlayer.value && !myReadyState.value) {
      audioPlayer.value.volume = 0
      audioPlayer.value.play().then(() => {
        audioPlayer.value!.pause()
        audioPlayer.value!.volume = 1
        console.log("✅ 浏览器音频权限解锁成功！")
      }).catch(e => {
        console.warn("⚠️ 音频预解锁失败:", e)
      })
    }
    socket.send(JSON.stringify({ type: 'toggle_ready', payload: {} }))
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

const playAgain = () => {
  if (socket && isConnected.value) {
    socket.send(JSON.stringify({ type: 'restart_game', payload: {} }))
  }
}

const leaveRoom = () => {
  showResult.value = false
  if (socket) socket.close()
  socket = null
  // 重置所有状态
  players.value = []
  cards.value = []
  gameState.value = 'waiting'
  currentRound.value = 1
  chatLogs.value = ['系统: 欢迎来到metagaruta！']
  hasAnswered.value = false
  ownerId.value = ''
  roomGameMode.value = 'vocaloid'
  audioStatusText.value = '🔊 等待开始...'
  inputRoomId.value = ''
  currentView.value = 'home'
}
</script>

<template>
  <audio ref="audioPlayer" preload="auto"></audio>
  <div v-if="currentView === 'home'" class="home-wrapper">
    <div class="login-box">
      <h1 class="game-title">Metagaruta Online</h1>
      <p class="subtitle">在线歌曲抢答</p>
      
      <div class="form-group">
        <label>玩家名称</label>
        <input v-model="inputName" type="text" placeholder="输入你的昵称" @keyup.enter="joinGame" />
      </div>

      <div class="form-group">
        <label>房间号</label>
        <input v-model="inputRoomId" type="text" placeholder="例如: 8848" @keyup.enter="joinGame" />
      </div>

      <div class="form-group">
        <label>游戏模式 (创建房间时生效)</label>
        <div class="mode-selector">
          <button class="mode-btn" :class="{ active: selectedGameMode === 'vocaloid' }" @click="selectedGameMode = 'vocaloid'">
            🎵 Vocaloid
          </button>
          <button class="mode-btn" :class="{ active: selectedGameMode === 'touhou' }" @click="selectedGameMode = 'touhou'">
            🌸 东方 Project
          </button>
        </div>
      </div>

      <div class="btn-group">
        <button class="btn-primary" @click="joinGame">加入房间</button>
        <button class="btn-secondary" @click="createGame">创建房间</button>
      </div>
    </div>

    <!-- 首页角落按钮 -->
    <div class="home-footer-links">
      <a class="footer-link" href="https://github.com/Animnia/metagaruta" target="_blank" rel="noopener" title="GitHub">
        <svg viewBox="0 0 16 16" width="20" height="20" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
        GitHub
      </a>
      <button class="footer-link" @click="showContact = true" title="联系开发者">
        <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
        联系开发者
      </button>
    </div>

    <!-- 联系开发者弹窗 -->
    <div v-if="showContact" class="modal-overlay" @click.self="showContact = false">
      <div class="modal-box">
        <h2>📬 联系开发者</h2>
        <p style="color:#8a857a; line-height:1.8; text-align:left;">
          如果你有任何问题、建议或想法，欢迎通过以下方式联系：
        </p>
        <div class="contact-list">
          <div class="contact-item">
            <span class="contact-label">GitHub</span>
            <a href="https://github.com/Animnia/metagaruta" target="_blank" rel="noopener" class="contact-value">Animnia/metagaruta</a>
          </div>
          <div class="contact-item">
            <span class="contact-label">Email</span>
            <span class="contact-value">animnia@metagaruta.com</span>
          </div>
        </div>
        <button class="btn-primary" @click="showContact = false" style="width:100%; margin-top:15px;">关闭</button>
      </div>
    </div>
  </div>

  <div v-else class="game-wrapper">
    <div class="game-layout">
      <aside class="sidebar">
        <div class="player-list">
          <div v-for="player in sortedPlayers" :key="player.id" class="player-item">
            <span class="p-name">{{ player.name }}<span v-if="player.id === ownerId" class="owner-tag">(房主)</span></span>
            <template v-if="gameState === 'waiting'">
              <span v-if="player.id !== ownerId" class="p-ready" :class="{ 'is-ready': player.gameReady }">{{ player.gameReady ? '已准备' : '未准备' }}</span>
            </template>
            <template v-else>
              <span class="p-score" :class="{ 'negative': player.score < 0 }">{{ player.score }} 分</span>
            </template>
          </div>
        </div>
        <div class="sidebar-bottom">
          <button class="no-song-btn" :class="{ 'disabled': hasAnswered || gameState !== 'playing' }" @click="handleNoSongClick">没有这首歌</button>
          <div class="room-info">房间号: <strong>{{ inputRoomId }}</strong></div>
          <div class="room-mode-tag" :class="roomGameMode">{{ roomGameMode === 'touhou' ? '🌸 东方' : '🎵 Vocaloid' }}</div>
        </div>
      </aside>

      <main class="main-area">
        <header class="top-bar">
          <div class="audio-status">{{ audioStatusText }}</div>
          <div class="round-display">第 {{ currentRound }} 局</div>
          <div class="actions">
            <template v-if="gameState === 'waiting'">
              <button v-if="isOwner" class="start-btn" :disabled="!allNonOwnersReady" @click="startGame">
                🚀 开始游戏
              </button>
              <button v-else class="ready-btn" :class="{ 'is-ready': myReadyState }" @click="toggleReady">
                {{ myReadyState ? '✅ 已准备' : '🎯 准备' }}
              </button>
            </template>
            <button class="icon-btn" @click="showRules = true">ℹ️</button>
            <button class="icon-btn" @click="showSettings = true">⚙️</button>
          </div>
        </header>

        <div class="karuta-board" :class="{ 'touhou-board': roomGameMode === 'touhou' }">
          <div v-for="card in cards" :key="card.id" class="karuta-card" :class="{ 'card-hidden': card.isMatched, 'touhou-card': roomGameMode === 'touhou', 'card-frozen': hasAnswered && gameState === 'playing' && !card.isMatched }" @click="handleCardClick(card)">
            <!-- Touhou 模式: 显示角色图片 -->
            <template v-if="roomGameMode === 'touhou'">
              <img :src="card.pictureUrl" class="card-picture" alt="" />
              <span v-if="showCharacterName" class="card-name-overlay">{{ card.characterName }}</span>
            </template>
            <!-- Vocaloid 模式: 显示歌曲名称 -->
            <template v-else>
              <span class="card-text">{{ displayMode === 'original' ? card.titleOriginal : card.titleTranslation }}</span>
            </template>
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
          <p>3. 如果点错将扣分(-5)且本局无法再进行操作。</p>
          <p>4. 歌曲可能不在场上！此时点击“没有这首歌”得分(+5)。</p>
          <p>5. 每局至多播放音频90秒。</p>
          <button class="btn-primary" @click="showRules = false" style="width:100%; margin-top:15px;">明白</button>
        </div>
      </div>

      <div v-if="showSettings" class="modal-overlay" @click.self="showSettings = false">
        <div class="modal-box">
          <h2>⚙️ 玩家设置</h2>
          <!-- Vocaloid 模式设置 -->
          <div v-if="roomGameMode === 'vocaloid'" class="form-group">
            <label>歌牌显示语言：</label>
            <select v-model="displayMode" style="width:100%; padding:10px; border:2px solid #000; outline:none; font-size:1rem;">
              <option value="original">原文 (Original)</option>
              <option value="translation">译文 (Translation)</option>
            </select>
          </div>
          <!-- Touhou 模式设置 -->
          <div v-if="roomGameMode === 'touhou'" class="form-group">
            <label>歌牌显示人物名称：</label>
            <div class="mode-selector">
              <button class="mode-btn" :class="{ active: !showCharacterName }" @click="showCharacterName = false">不显示</button>
              <button class="mode-btn" :class="{ active: showCharacterName }" @click="showCharacterName = true">显示</button>
            </div>
          </div>
          <button class="btn-primary" @click="showSettings = false" style="width:100%; margin-top:15px;">关闭</button>
        </div>
      </div>
  </div>

  <!-- 游戏结算弹窗 -->
  <div v-if="showResult" class="modal-overlay">
    <div class="modal-box result-box">
      <h2>🎉 游戏结算</h2>
      <div class="result-podium">
        <div v-for="(p, idx) in topThree" :key="p.id" class="podium-item">
          <span class="podium-rank">{{ ['🥇', '🥈', '🥉'][idx] }}</span>
          <span class="podium-name">{{ p.name }}</span>
          <span class="podium-score">{{ p.score }} 分</span>
        </div>
      </div>
      <div class="result-self">
        <span>你的排名：第 <strong>{{ myRank }}</strong> 名</span>
        <span>得分：<strong>{{ myFinalScore }}</strong> 分</span>
      </div>
      <div class="result-actions">
        <button class="btn-primary" @click="playAgain">🔁 再来一局</button>
        <button class="btn-secondary" @click="leaveRoom">🚪 退出房间</button>
      </div>
    </div>
  </div>
</template>

<style>
/* 全局重置 */
@import url('https://fonts.googleapis.com/css2?family=Zen+Maru+Gothic:wght@400;700&family=Share+Tech+Mono&family=Noto+Serif+JP:wght@400;700&display=swap');
body, html { margin: 0; padding: 0; width: 100%; height: 100%; overflow: hidden; background-color: #f3f0e8; }
* { box-sizing: border-box; }
</style>

<style scoped>
/* ==========================================
   色板 (和風サイバー — 日式古典×赛博meta)
   --bg-deep:    #f3f0e8  和紙白 (warm washi paper)
   --bg-panel:   #faf8f2  生成色 (aged paper white)
   --bg-sidebar: #f0ede4  鳥の子 (warm ivory)
   --bg-card:    #2c3044  鉄紺 (iron navy)
   --accent:     #c05550  朱色 (vermillion)
   --accent-dim: #a84a42  茜色 (madder red)
   --gold:       #b89040  金茶 (gold brown)
   --teal:       #5d8a8a  青磁色 (celadon teal)
   --text:       #3a3530  墨色 (sumi ink)
   --text-dim:   #8a857a  利休鼠 (Rikyu gray)
   --border:     #d6d1c6  灰白 (warm ash)
   ========================================== */

/* ==========================================
   首页
   ========================================== */
.home-wrapper {
  width: 100vw;
  height: 100dvh;
  display: flex;
  justify-content: center;
  align-items: center;
  background: radial-gradient(ellipse at 50% 30%, #edeae2 0%, #f3f0e8 70%);
  font-family: 'Zen Maru Gothic', 'Noto Sans JP', sans-serif;
  position: relative;
  overflow: hidden;
}
.home-wrapper::before {
  content: '';
  position: absolute;
  inset: 0;
  background:
    repeating-linear-gradient(0deg, transparent, transparent 50px, rgba(93,138,138,0.035) 50px, rgba(93,138,138,0.035) 51px),
    repeating-linear-gradient(90deg, transparent, transparent 50px, rgba(93,138,138,0.035) 50px, rgba(93,138,138,0.035) 51px);
  pointer-events: none;
}

.login-box {
  background: #faf8f2;
  border: 2px solid #5d8a8a;
  padding: 40px;
  width: 90%;
  max-width: 420px;
  box-shadow: 0 4px 30px rgba(93,138,138,0.10), 0 1px 0 rgba(93,138,138,0.08);
  text-align: center;
  border-radius: 8px;
  position: relative;
}
.login-box::after {
  content: '';
  position: absolute;
  top: -1px; left: 10%; right: 10%; height: 2px;
  background: linear-gradient(90deg, transparent, #5d8a8a 30%, #b89040 50%, #5d8a8a 70%, transparent);
}

.game-title {
  margin: 0; font-size: 2rem; letter-spacing: 4px;
  color: #3a3530;
  font-family: 'Noto Serif JP', 'Zen Maru Gothic', serif;
  text-shadow: 0 0 10px rgba(93,138,138,0.15);
}
.subtitle {
  margin-top: 5px; margin-bottom: 30px;
  font-weight: 400;
  color: #5d8a8a;
  letter-spacing: 2px;
  font-family: 'Share Tech Mono', monospace;
  font-size: 0.85rem;
}

.form-group { margin-bottom: 20px; text-align: left; }
.form-group label {
  display: block; font-weight: 700; margin-bottom: 8px;
  color: #8a857a; font-size: 0.85rem; letter-spacing: 1px; text-transform: uppercase;
}
.form-group input {
  width: 100%; padding: 12px;
  border: 1px solid #d6d1c6; font-size: 1rem; outline: none;
  transition: border-color 0.3s, box-shadow 0.3s;
  background: #f5f2ea; color: #3a3530; border-radius: 6px;
  font-family: 'Zen Maru Gothic', sans-serif;
}
.form-group input:focus {
  border-color: #5d8a8a;
  box-shadow: 0 0 0 3px rgba(93,138,138,0.12);
}
.form-group input::placeholder { color: #b0ab9e; }

/* 游戏模式选择器 */
.mode-selector {
  display: flex; gap: 10px;
}
.mode-btn {
  flex: 1; padding: 10px 8px; font-size: 0.95rem; font-weight: bold;
  border: 2px solid #d6d1c6; background: #f5f2ea; color: #8a857a;
  cursor: pointer; border-radius: 6px; transition: all 0.2s;
  font-family: 'Zen Maru Gothic', sans-serif;
}
.mode-btn:hover { border-color: #5d8a8a; color: #5d8a8a; background: rgba(93,138,138,0.04); }
.mode-btn.active {
  border-color: #5d8a8a; color: #5d8a8a; background: rgba(93,138,138,0.08);
  box-shadow: 0 0 0 3px rgba(93,138,138,0.12);
}

.btn-group { display: flex; flex-direction: column; gap: 15px; margin-top: 30px; }

/* 首页角落链接 */
.home-footer-links {
  position: absolute; bottom: 24px; right: 28px;
  display: flex; gap: 16px; z-index: 10;
}
.footer-link {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 8px 14px; border-radius: 20px;
  font-size: 0.85rem; font-weight: 600;
  background: rgba(255,255,255,0.85); color: #8a857a;
  border: 1px solid #d6d1c6; cursor: pointer;
  text-decoration: none;
  font-family: 'Zen Maru Gothic', sans-serif; letter-spacing: 0.5px;
  transition: all 0.2s; backdrop-filter: blur(6px);
}
.footer-link:hover {
  color: #5d8a8a; border-color: #5d8a8a;
  background: rgba(255,255,255,0.95);
  box-shadow: 0 2px 12px rgba(93,138,138,0.12);
  transform: translateY(-1px);
}
.footer-link svg { flex-shrink: 0; }

/* 联系开发者弹窗 */
.contact-list { margin: 15px 0 5px; }
.contact-item {
  display: flex; justify-content: space-between; align-items: center;
  padding: 10px 12px; border-bottom: 1px dashed #e2ded4;
}
.contact-item:last-child { border-bottom: none; }
.contact-label {
  font-weight: 700; color: #8a857a; font-size: 0.85rem;
  letter-spacing: 1px; text-transform: uppercase;
}
.contact-value {
  color: #5d8a8a; font-weight: 600; font-size: 0.95rem;
  text-decoration: none;
}
.contact-value:hover { text-decoration: underline; }
.btn-primary, .btn-secondary {
  padding: 12px; font-size: 1.05rem; font-weight: bold;
  border: 1px solid; cursor: pointer; transition: all 0.2s;
  border-radius: 6px; font-family: 'Zen Maru Gothic', sans-serif; letter-spacing: 1px;
}
.btn-primary {
  background: linear-gradient(135deg, #c05550, #a84a42);
  color: #fff; border-color: #c05550;
  box-shadow: 0 2px 10px rgba(192,85,80,0.22);
}
.btn-primary:hover {
  background: linear-gradient(135deg, #cf6058, #c05550);
  box-shadow: 0 4px 18px rgba(192,85,80,0.32);
  transform: translateY(-1px);
}
.btn-primary:active { transform: translateY(0) scale(0.98); box-shadow: 0 1px 5px rgba(192,85,80,0.18); }
.btn-primary:disabled {
  background: #ccc8bc; border-color: #ccc8bc; color: #a09c90;
  cursor: not-allowed; box-shadow: none; transform: none;
}
.btn-secondary {
  background: transparent; color: #5d8a8a; border-color: #5d8a8a;
  box-shadow: 0 1px 6px rgba(93,138,138,0.08);
}
.btn-secondary:hover {
  background: rgba(93,138,138,0.06);
  box-shadow: 0 2px 12px rgba(93,138,138,0.16);
  transform: translateY(-1px);
}
.btn-secondary:active { transform: translateY(0) scale(0.98); }
.btn-secondary:disabled {
  color: #ccc8bc; border-color: #ccc8bc;
  cursor: not-allowed; box-shadow: none; transform: none;
}

/* 弹窗 */
.modal-overlay {
  position: absolute; top: 0; left: 0; width: 100%; height: 100%;
  background: rgba(58,53,48,0.40); display: flex; justify-content: center; align-items: center;
  z-index: 100; backdrop-filter: blur(6px);
}
.modal-box {
  background: #faf8f2; border: 1px solid #5d8a8a;
  padding: 25px; width: 90%; max-width: 420px;
  box-shadow: 0 8px 40px rgba(93,138,138,0.10);
  border-radius: 8px; color: #3a3530;
}
.modal-box h2 { margin-top: 0; border-bottom: 1px solid #e2ded4; padding-bottom: 10px; color: #5d8a8a; }
.modal-box p { line-height: 1.7; font-weight: 400; font-size: 0.95rem; color: #5a5650; }
.modal-box select {
  width: 100%; padding: 10px; border: 1px solid #d6d1c6 !important; outline: none; font-size: 1rem;
  background: #f5f2ea !important; color: #3a3530 !important; border-radius: 6px;
}

/* ==========================================
   游戏房间
   ========================================== */
.game-wrapper {
  width: 100vw; height: 100dvh; padding: 10px;
  display: flex; justify-content: center; align-items: center;
  background: #f3f0e8;
  font-family: 'Zen Maru Gothic', 'Noto Sans JP', sans-serif;
}
.game-layout {
  display: flex; width: 100%; max-width: 1200px; height: 100%;
  border: 1px solid #d6d1c6;
  background-color: #faf8f2;
  box-shadow: 0 4px 30px rgba(0,0,0,0.06);
  border-radius: 8px; overflow: hidden;
}

/* 侧边栏 */
.sidebar { width: 220px; border-right: 1px solid #e2ded4; display: flex; flex-direction: column; background: #f0ede4; }
.player-list { flex: 1; overflow-y: auto; }
.player-item {
  border-bottom: 1px solid #e6e2d8; padding: 12px 10px;
  display: flex; justify-content: space-between; align-items: center;
  font-weight: bold; font-size: 0.95rem; color: #4a4640;
  transition: background 0.2s;
}
.player-item:hover { background: rgba(93,138,138,0.04); }
.p-name { color: #3a3530; }
.p-score { color: #5d8a8a; font-family: 'Share Tech Mono', monospace; }
.p-score.negative { color: #c05550; }
.owner-tag { color: #b89040; font-size: 0.75em; margin-left: 4px; }
.p-ready { font-size: 0.8rem; color: #b0ab9e; font-family: 'Share Tech Mono', monospace; }
.p-ready.is-ready { color: #5d8a8a; font-weight: bold; }

.sidebar-bottom { border-top: 1px solid #e2ded4; display: flex; flex-direction: column; background: #ece9e0; }
.no-song-btn {
  margin: 12px; padding: 10px;
  border: 1px solid #c05550; background: rgba(192,85,80,0.07);
  color: #c05550; font-weight: bold; font-size: 0.95rem;
  cursor: pointer; border-radius: 6px;
  transition: all 0.2s;
  box-shadow: 0 1px 6px rgba(192,85,80,0.06);
}
.no-song-btn:hover {
  background: rgba(192,85,80,0.14);
  box-shadow: 0 2px 12px rgba(192,85,80,0.14);
  transform: translateY(-1px);
}
.no-song-btn:active { transform: translateY(0) scale(0.97); }
.no-song-btn.disabled {
  background: #ebe8df; border-color: #ccc8bc; color: #b0ab9e;
  cursor: not-allowed; transform: none; box-shadow: none;
}
.room-info {
  border-top: 1px dashed #ddd9ce; padding: 10px; text-align: center;
  font-weight: bold; color: #8a857a; background: #ebe8df;
  font-family: 'Share Tech Mono', monospace; font-size: 0.85rem;
}
.room-info strong { color: #b89040; }

/* 房间模式标签 */
.room-mode-tag {
  padding: 4px 0; text-align: center; font-size: 0.8rem; font-weight: bold;
  font-family: 'Share Tech Mono', monospace; letter-spacing: 1px;
}
.room-mode-tag.vocaloid { color: #5d8a8a; background: rgba(93,138,138,0.06); }
.room-mode-tag.touhou { color: #c05550; background: rgba(192,85,80,0.06); }

/* 主区域 */
.main-area { flex: 1; display: flex; flex-direction: column; min-width: 0; background: #faf8f2; }
.top-bar {
  display: flex; justify-content: space-between; align-items: center;
  padding: 12px 20px; border-bottom: 1px solid #e2ded4;
  font-weight: bold; font-size: 1rem; color: #4a4640;
  background: #f5f2ea;
}
.audio-status { font-family: 'Share Tech Mono', monospace; color: #5d8a8a; font-size: 0.9rem; }
.round-display { font-family: 'Share Tech Mono', monospace; color: #b89040; }
.actions { display: flex; gap: 10px; align-items: center; }
.start-btn {
  background: linear-gradient(135deg, #c05550, #a84a42);
  color: white; border: 1px solid #c05550; padding: 5px 12px;
  font-weight: bold; cursor: pointer; border-radius: 6px;
  box-shadow: 0 2px 10px rgba(192,85,80,0.18);
  transition: all 0.2s; font-family: 'Zen Maru Gothic', sans-serif;
}
.start-btn:hover {
  background: linear-gradient(135deg, #cf6058, #c05550);
  box-shadow: 0 4px 16px rgba(192,85,80,0.28);
  transform: translateY(-1px);
}
.start-btn:active { transform: translateY(0) scale(0.97); }
.start-btn:disabled {
  background: #ccc8bc; border-color: #ccc8bc; color: #a09c90;
  cursor: not-allowed; box-shadow: none; transform: none;
}
.ready-btn {
  background: transparent; color: #b89040; border: 1px solid #b89040;
  padding: 5px 12px; font-weight: bold; cursor: pointer;
  border-radius: 6px; transition: all 0.2s;
  box-shadow: 0 1px 6px rgba(184,144,64,0.08);
  font-family: 'Zen Maru Gothic', sans-serif;
}
.ready-btn:hover {
  background: rgba(184,144,64,0.08);
  box-shadow: 0 2px 12px rgba(184,144,64,0.16);
  transform: translateY(-1px);
}
.ready-btn:active { transform: translateY(0) scale(0.97); }
.ready-btn.is-ready {
  background: rgba(93,138,138,0.08); color: #5d8a8a; border-color: #5d8a8a;
  box-shadow: 0 2px 10px rgba(93,138,138,0.10);
}
.ready-btn.is-ready:hover {
  background: rgba(93,138,138,0.14);
  box-shadow: 0 4px 16px rgba(93,138,138,0.18);
}
.icon-btn {
  background: none; border: none; font-size: 1.4rem; cursor: pointer;
  filter: none; transition: transform 0.15s, filter 0.15s;
}
.icon-btn:hover { transform: scale(1.15); filter: drop-shadow(0 0 4px rgba(93,138,138,0.25)); }
.icon-btn:active { transform: scale(0.95); }

/* 歌牌棋盘 */
.karuta-board {
  flex: 1; min-height: 0;
  display: grid; grid-template-columns: repeat(4, auto); grid-template-rows: repeat(4, minmax(0, 1fr));
  justify-content: center; gap: 12px 24px; padding: 15px;
  background:
    repeating-linear-gradient(45deg, transparent, transparent 20px, rgba(93,138,138,0.02) 20px, rgba(93,138,138,0.02) 21px),
    repeating-linear-gradient(-45deg, transparent, transparent 20px, rgba(93,138,138,0.02) 20px, rgba(93,138,138,0.02) 21px),
    radial-gradient(ellipse at center, #e8e5dc 0%, #e2dfd6 100%);
}
.karuta-card {
  aspect-ratio: 2 / 3; height: 100%;
  border: 1.5px solid #c0baa8;
  background: linear-gradient(170deg, #3a4058, #2c3044);
  border-radius: 6px;
  display: flex; justify-content: center; align-items: center;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(44,48,68,0.18), inset 0 1px 0 rgba(255,255,255,0.05);
  transition: transform 0.15s, box-shadow 0.15s, border-color 0.15s, opacity 0.15s;
  overflow: hidden;
  position: relative;
}
.karuta-card::before {
  content: '';
  position: absolute; top: 0; left: 0; right: 0; height: 1px;
  background: linear-gradient(90deg, transparent, rgba(93,138,138,0.25), transparent);
}
.karuta-card:hover {
  border-color: #5d8a8a;
  box-shadow: 0 4px 18px rgba(93,138,138,0.20), inset 0 1px 0 rgba(255,255,255,0.08);
  transform: translateY(-3px);
}
.karuta-card:active {
  transform: translateY(0) scale(0.96);
  box-shadow: 0 1px 4px rgba(44,48,68,0.2);
  border-color: #c05550;
}
.karuta-card.card-hidden { visibility: hidden; }
.karuta-card.card-frozen {
  opacity: 0.45;
  filter: grayscale(0.6);
  cursor: not-allowed;
  pointer-events: none;
  transform: none !important;
  box-shadow: 0 1px 4px rgba(44,48,68,0.10) !important;
  border-color: #c0baa8 !important;
}
.card-text {
  writing-mode: vertical-rl; text-orientation: upright;
  letter-spacing: 2px; font-size: clamp(0.9rem, 1.5vh, 1.2rem);
  font-weight: 600; text-align: center; padding: 5px;
  color: #ddd9ce;
  text-shadow: 0 0 6px rgba(93,138,138,0.10);
}

/* ==========================================
   Touhou 模式歌牌
   ========================================== */
.touhou-board {
  grid-template-columns: repeat(4, 1fr);
  grid-template-rows: repeat(4, minmax(0, 1fr));
  justify-items: center;
}
.touhou-card {
  background: #2c3044 !important;
  overflow: hidden;
  position: relative;
  width: auto;
  max-width: 100%;
}
.touhou-card .card-picture {
  width: 100%; height: 100%;
  object-fit: cover;
  position: absolute; top: 0; left: 0;
  transition: transform 0.2s;
}
.touhou-card:hover .card-picture {
  transform: scale(1.05);
}
.touhou-card .card-name-overlay {
  position: absolute; bottom: 0; left: 0; right: 0;
  background: linear-gradient(transparent, rgba(0,0,0,0.75));
  color: #fff; text-align: center;
  padding: 6px 4px 4px;
  font-size: clamp(0.7rem, 1.2vh, 0.9rem); font-weight: 700;
  letter-spacing: 1px; z-index: 1;
  writing-mode: horizontal-tb;
  text-shadow: 0 1px 3px rgba(0,0,0,0.6);
}

/* 聊天区 */
.chat-area { height: 120px; border-top: 1px solid #e2ded4; display: flex; flex-direction: column; background: #f5f2ea; }
.chat-history { flex: 1; padding: 8px 15px; overflow-y: auto; font-size: 0.83rem; color: #8a857a; }
.chat-line { margin-bottom: 4px; }
.chat-input-box { display: flex; border-top: 1px solid #e6e2d8; }
.chat-input-box input {
  flex: 1; border: none; padding: 10px 15px; font-size: 0.95rem; outline: none;
  background: #faf8f2; color: #3a3530;
  font-family: 'Zen Maru Gothic', sans-serif;
}
.chat-input-box input::placeholder { color: #b0ab9e; }
.chat-input-box input:focus { background: #f5f2ea; }

/* 结算弹窗 */
.result-box { max-width: 450px; text-align: center; }
.result-box h2 { font-size: 1.6rem; color: #b89040; }
.result-podium { margin: 20px 0; }
.podium-item {
  display: flex; justify-content: space-between; align-items: center;
  padding: 10px 15px; border-bottom: 1px dashed #e2ded4;
  font-size: 1.05rem; font-weight: bold; color: #4a4640;
}
.podium-item:last-child { border-bottom: none; }
.podium-rank { font-size: 1.4rem; min-width: 36px; }
.podium-name { flex: 1; text-align: left; margin-left: 8px; }
.podium-score { color: #5d8a8a; min-width: 70px; text-align: right; font-family: 'Share Tech Mono', monospace; }
.result-self {
  border: 1px solid #e2ded4; padding: 12px 15px; margin: 15px 0;
  display: flex; justify-content: space-between; font-weight: bold; font-size: 1rem;
  background: #f5f2ea; color: #4a4640; border-radius: 6px;
}
.result-actions { display: flex; gap: 12px; margin-top: 15px; }
.result-actions .btn-primary, .result-actions .btn-secondary { flex: 1; padding: 12px; font-size: 1rem; }

/* ==========================================
   移动端响应式
   ========================================== */
@media (max-width: 768px) {
  .game-wrapper { padding: 0; }
  .game-layout { border: none; flex-direction: column; border-radius: 0; }
  .sidebar { width: 100%; border-right: none; border-bottom: 1px solid #e2ded4; flex-direction: row; justify-content: space-between; align-items: stretch; }
  .player-list { display: flex; overflow-x: auto; flex: 1; border-right: 1px dashed #e2ded4; }
  .player-item { border-bottom: none; border-right: 1px solid #e6e2d8; padding: 10px; flex-direction: column; justify-content: center; align-items: center; min-width: 70px; }
  .p-name { font-size: 0.8rem; }
  .p-score { font-size: 0.9rem; }
  .sidebar-bottom { border-top: none; flex-direction: column; justify-content: center; min-width: 100px; }
  .no-song-btn { margin: 5px; padding: 6px; font-size: 0.85rem; }
  .room-info { border-top: none; padding: 2px; font-size: 0.8rem; }
  .top-bar { padding: 8px 10px; font-size: 0.9rem; }
  .karuta-board { gap: 6px; padding: 6px; }
  .touhou-board { gap: 4px 6px; }
  .touhou-card { max-width: 100%; }
  .karuta-card { box-shadow: 0 1px 4px rgba(44,48,68,0.15); }
  .card-text { letter-spacing: 0px; }
  .chat-area { height: 100px; }
}
</style>