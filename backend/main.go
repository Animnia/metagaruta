package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

// 配置 WebSocket 升级器（允许跨域）
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	// 1. 处理主页请求 (HTTP)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Go 游戏服务器运行正常！(Go Game Server is Running!)")
	})

	// 2. 处理 WebSocket 连接
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// 将 HTTP 升级为 WebSocket
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Println("升级失败:", err)
			return
		}
		defer conn.Close()

		fmt.Println("新玩家连接:", conn.RemoteAddr())

		// 循环读取消息
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				fmt.Println("玩家断开:", err)
				break
			}
			fmt.Printf("收到消息: %s\n", message)

			// 把消息原样发回去 (Echo)
			if err := conn.WriteMessage(messageType, message); err != nil {
				fmt.Println("发送失败:", err)
				break
			}
		}
	})

	// 3. 启动服务器
	fmt.Println("---------------------------------------")
	fmt.Println("Go 游戏服务器已启动，正在监听 :3000")
	fmt.Println("---------------------------------------")
	err := http.ListenAndServe(":3000", nil)
	if err != nil {
		fmt.Println("启动失败:", err)
	}
}
