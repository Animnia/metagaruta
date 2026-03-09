package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

type PlayerInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Score       int    `json:"score"`
	HasAnswered bool   `json:"hasAnswered"`
	GameReady   bool   `json:"gameReady"`
}

type RoomInfo struct {
	ID           string       `json:"id"`
	OwnerID      string       `json:"ownerId"`
	GameMode     string       `json:"gameMode"`
	State        string       `json:"state"`
	RoundState   string       `json:"roundState"`
	CurrentRound int          `json:"currentRound"`
	PlayerCount  int          `json:"playerCount"`
	Players      []PlayerInfo `json:"players"`
	BoardCards   int          `json:"boardCardsTotal"`
	MatchedCards int          `json:"matchedCards"`
	SongPoolSize int          `json:"songPoolSize"`
	CurrentSong  string       `json:"currentSong,omitempty"`
}

type StatusResponse struct {
	Timestamp     string     `json:"timestamp"`
	TotalRooms    int        `json:"totalRooms"`
	TotalPlayers  int        `json:"totalPlayers"`
	VocaloidSongs int        `json:"vocaloidSongs"`
	TouhouChars   int        `json:"touhouChars"`
	Rooms         []RoomInfo `json:"rooms"`
}

func main() {
	url := "http://127.0.0.1:3000/api/admin/status"

	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法连接到游戏服务: %v\n", err)
		fmt.Fprintln(os.Stderr, "   请确认游戏服务正在运行 (端口 3000)")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "请求失败: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		fmt.Fprintf(os.Stderr, "解析响应失败: %v\n", err)
		os.Exit(1)
	}

	printStatus(status)
}

func printStatus(s StatusResponse) {
	line := strings.Repeat("═", 56)
	thinLine := strings.Repeat("─", 56)

	fmt.Println(line)
	fmt.Println("  Metagaruta 游戏服务状态")
	fmt.Println(line)
	fmt.Printf("  时间        %s\n", s.Timestamp)
	fmt.Printf("  活跃房间    %d\n", s.TotalRooms)
	fmt.Printf("  在线玩家    %d\n", s.TotalPlayers)
	fmt.Printf("  Vocaloid 曲库  %d 首\n", s.VocaloidSongs)
	fmt.Printf("  东方角色库      %d 个\n", s.TouhouChars)
	fmt.Println(thinLine)

	if len(s.Rooms) == 0 {
		fmt.Println("  (当前没有活跃房间)")
		fmt.Println(line)
		return
	}

	for i, rm := range s.Rooms {
		modeStr := "Vocaloid"
		if rm.GameMode == "touhou" {
			modeStr = "东方"
		}
		stateStr := stateText(rm.State)
		roundStateStr := roundStateText(rm.RoundState)

		fmt.Printf("  房间 #%s  [%s]  %s\n", rm.ID, modeStr, stateStr)
		fmt.Printf("    回合: %d    回合状态: %s\n", rm.CurrentRound, roundStateStr)
		fmt.Printf("    牌面: %d/%d 已匹配    题库剩余: %d\n", rm.MatchedCards, rm.BoardCards, rm.SongPoolSize)
		if rm.CurrentSong != "" {
			fmt.Printf("    当前曲目: %s\n", rm.CurrentSong)
		}

		if len(rm.Players) == 0 {
			fmt.Println("    玩家: (无)")
		} else {
			fmt.Println("    ┌────────────────┬──────┬──────┬──────┐")
			fmt.Println("    │ 玩家名         │ 分数 │ 已答 │ 准备 │")
			fmt.Println("    ├────────────────┼──────┼──────┼──────┤")
			for _, p := range rm.Players {
				name := padRight(p.Name, 14)
				ownerMark := ""
				if p.ID == rm.OwnerID {
					ownerMark = "*"
				}
				answered := boolMark(p.HasAnswered)
				ready := boolMark(p.GameReady)
				fmt.Printf("    │ %s%s│ %4d │  %s   │  %s   │\n", name, ownerMark, p.Score, answered, ready)
			}
			fmt.Println("    └────────────────┴──────┴──────┴──────┘")
			fmt.Println("    (* = 房主)")
		}

		if i < len(s.Rooms)-1 {
			fmt.Println(thinLine)
		}
	}
	fmt.Println(line)
}

func stateText(s string) string {
	switch s {
	case "waiting":
		return "等待中"
	case "playing":
		return "游戏中"
	default:
		return s
	}
}

func roundStateText(s string) string {
	switch s {
	case "preparing":
		return "准备中"
	case "countdown":
		return "倒计时"
	case "playing":
		return "播放中"
	case "ended":
		return "已结束"
	case "":
		return "-"
	default:
		return s
	}
}

func boolMark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

// padRight 将字符串填充到指定显示宽度（简易处理，CJK 字符按 2 宽度算）
func padRight(s string, width int) string {
	w := 0
	for _, r := range s {
		if r > 0x7F {
			w += 2
		} else {
			w++
		}
	}
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}
