<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const message = ref('')
const messages = ref<string[]>([])
let socket: WebSocket | null = null
const isConnected = ref(false)

onMounted(() => {
  // 1. 计算 WebSocket URL
  // 如果是 https 访问，就用 wss://；否则用 ws://
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const wsUrl = `${protocol}//${window.location.host}/ws`
  
  // 2. 建立原生 WebSocket 连接
  console.log('正在连接到:', wsUrl)
  socket = new WebSocket(wsUrl)

  // 3. 监听连接开启
  socket.onopen = () => {
    console.log('WebSocket 连接成功')
    messages.value.push('已连接到服务器！')
    isConnected.value = true
  }

  // 4. 监听收到消息 (Go 后端发来的)
  socket.onmessage = (event) => {
    // 后端发来的是 Blob (二进制) 还是 Text (文本)？
    // 通常 Gorilla 发送的是 Text，直接用 event.data
    console.log('收到消息:', event.data)
    messages.value.push(`收到: ${event.data}`)
  }

  // 5. 监听关闭
  socket.onclose = (event) => {
    console.log('连接关闭', event.code, event.reason)
    messages.value.push('连接已断开')
    isConnected.value = false
  }

  // 6. 监听错误
  socket.onerror = (error) => {
    console.error('WebSocket 错误:', error)
    messages.value.push('发生错误，请检查控制台')
  }
})

onUnmounted(() => {
  if (socket) {
    socket.close()
  }
})

const sendMessage = () => {
  if (message.value.trim() && socket && isConnected.value) {
    // 原生 WebSocket 直接发送字符串
    socket.send(message.value)
    // 我们的后端是“广播”模式，自己发的消息也会被广播回来
    // 所以这里其实不需要手动 push 到 messages 数组，
    // 等 onmessage 收到回音再显示会更准确（确认服务器收到了）
    message.value = ''
  }
}
</script>

<template>
  <div class="container">
    <h1>Chat v0.1</h1>
    
    <div class="status">
      状态: <span :class="{ 'online': isConnected, 'offline': !isConnected }">
        {{ isConnected ? '在线' : '离线' }}
      </span>
    </div>

    <div class="chat-box">
      <div v-for="(msg, index) in messages" :key="index" class="message-item">
        {{ msg }}
      </div>
    </div>

    <div class="input-area">
      <input 
        v-model="message" 
        @keyup.enter="sendMessage" 
        placeholder="输入内容..." 
        type="text"
        :disabled="!isConnected"
      />
      <button @click="sendMessage" :disabled="!isConnected">发送</button>
    </div>
  </div>
</template>

<style scoped>
.container { max-width: 600px; margin: 0 auto; padding: 20px; font-family: sans-serif; }
.chat-box { 
  border: 1px solid #ccc; 
  height: 300px; 
  overflow-y: auto; 
  padding: 10px; 
  margin-bottom: 10px;
  background: #f9f9f9;
}
.message-item { margin-bottom: 5px; border-bottom: 1px solid #eee; }
.input-area { display: flex; gap: 10px; }
input { flex: 1; padding: 8px; }
button { padding: 8px 16px; cursor: pointer; background: #42b883; color: white; border: none; }
button:disabled { background: #ccc; }
.status { margin-bottom: 10px; font-weight: bold; }
.online { color: green; }
.offline { color: red; }
</style>