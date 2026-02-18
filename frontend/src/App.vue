<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { io, type Socket } from 'socket.io-client'

// 1. 定义状态
const message = ref('')           // 输入框的内容
const messages = ref<string[]>([]) // 聊天记录列表
let socket: Socket | null = null

// 2. 连接服务器
onMounted(() => {
  // 这里的地址填你的域名，注意是 wss (安全 WebSocket)
  // 加上 /ws 是为了配合 Nginx 的转发路径
  socket = io('https://metagaruta.com', {
    path: '/ws', // 关键：告诉 Socket.io 走这个路径
    transports: ['websocket'] // 强制使用 WebSocket，不轮询
  })

  // 监听连接成功
  socket.on('connect', () => {
    messages.value.push('已连接到服务器！')
  })

  // 监听收到消息 (后端广播回来的)
  socket.on('message', (msg: string) => {
    messages.value.push(`收到: ${msg}`)
  })

  // 监听错误
  socket.on('connect_error', (err) => {
    console.error('连接错误:', err)
    messages.value.push('连接失败，请检查控制台')
  })
})

// 3. 发送消息函数
const sendMessage = () => {
  if (message.value.trim() && socket) {
    // 发送给服务器
    // 注意：这里我们约定发送的事件名叫 'chat_message'
    socket.emit('chat_message', message.value)
    message.value = '' // 清空输入框
  }
}
</script>

<template>
  <div class="container">
    <h1>Chat</h1>
    
    <div class="chat-box">
      <div v-for="(msg, index) in messages" :key="index" class="message-item">
        {{ msg }}
      </div>
    </div>

    <div class="input-area">
      <input 
        v-model="message" 
        @keyup.enter="sendMessage" 
        placeholder="请输入文本..." 
        type="text"
      />
      <button @click="sendMessage">发送</button>
    </div>
  </div>
</template>

<style scoped>
/* 简单写点样式让它不那么丑 */
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
</style>