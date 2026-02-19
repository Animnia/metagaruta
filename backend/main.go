package main

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// 客户端池：用来存所有连接的玩家
// key是连接指针，value是一个布尔值（占位符）
var clients = make(map[*websocket.Conn]bool)
var broadcast = make(chan []byte) // 广播通道
var mutex = &sync.Mutex{}         // 互斥锁，防止并发读写崩溃

func main() {
	// 1. 静态文件由 Nginx 托管，这里只处理 /ws
	http.HandleFunc("/ws", handleConnections)

	// 启动一个协程专门处理消息广播
	go handleMessages()

	fmt.Println("---------------------------------------")
	fmt.Println("Go 广播服务器已启动，监听 :3000/ws, v0.1")
	fmt.Println("---------------------------------------")

	http.ListenAndServe(":3000", nil)
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close()

	// 新玩家加入，注册到池子里
	mutex.Lock()
	clients[conn] = true
	mutex.Unlock()

	fmt.Println("新玩家加入！当前在线:", len(clients))

	for {
		// 读取消息
		// 注意：这里我们不再区分事件名，简单处理所有文本消息
		_, msg, err := conn.ReadMessage()
		if err != nil {
			mutex.Lock()
			delete(clients, conn) // 玩家断开，移除
			mutex.Unlock()
			fmt.Println("玩家断开。当前在线:", len(clients))
			break
		}
		// 把收到的消息塞进广播通道
		broadcast <- msg
	}
}

func handleMessages() {
	for {
		// 从通道里拿消息
		msg := <-broadcast

		// 发送给所有连接的客户端
		mutex.Lock()
		for client := range clients {
			err := client.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				fmt.Println("发送错误:", err)
				client.Close()
				delete(clients, client)
			}
		}
		mutex.Unlock()
	}
}
