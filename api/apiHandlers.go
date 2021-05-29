package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/xid"
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 256
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// CheckOriginメソッドでchrome拡張のoriginを許可する必要あり
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header["Origin"]
		if len(origin) == 0 {
			return true
		}

		mode := os.Getenv("APP_ENV")
		var allowAll bool
		if mode == "develop" {
			allowAll = true
		} else {
			allowAll = false
		}

		u, err := url.Parse(origin[0])
		if err != nil {
			return false
		}
		switch u.Host {
		case r.Host:
			return true
		case "mlkcalakglmhbbogogidckljebnaeipb": // Test environment
			return true
		case "honmbceijbf.com/go/pubsuboniffckiolgkgaieikenk": // Test environment
			return true
		default:
			return allowAll
		}
	},
}

type wsResult struct {
	err error
	msg Frame
}

func NewRoomHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/new" {
		// error response
		http.Error(w, "bad url", http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "failed to upgrade websocket", http.StatusInternalServerError)
		return
	}

	roomID := xid.New().String()

	ctx, cancel := context.WithCancel(context.Background())
	wsReadCh := readWebsocket(ctx, conn)
	wsWriteCh := make(chan []byte)
	writeWebsocket(ctx, wsWriteCh, conn)

	go func() {
		defer func() {
			log.Printf("%s is closed", roomID)
			close(wsWriteCh)
		}()

		for {
			select {
			case res := <-wsReadCh:
				if res.err != nil {
					cancel()
					return
				}

				b, err := json.Marshal(res.msg)
				if err != nil {
					log.Println(err)
					continue
				}
				wsWriteCh <- b
			}
		}
	}()

	f := Frame{
		From: "server",
		Type: "joinSuccess",
		Data: map[string]interface{}{
			"roomID":   roomID,
			"mediaURL": "",
		},
	}
	b, err := json.Marshal(f)
	if err != nil {
		log.Println(err)
	}
	wsWriteCh <- b
}

func JoinRoomHandler(w http.ResponseWriter, r *http.Request) {
	// hoge
}

type Frame struct {
	Type string      `json:"type"`
	From string      `json:"from"`
	Data interface{} `json:"data"`
}

func readWebsocket(ctx context.Context, conn *websocket.Conn) <-chan wsResult {
	ch := make(chan wsResult)
	go func() {
		defer conn.Close()
		conn.SetReadLimit(maxMessageSize)
		conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
		for {
			_, message, err := conn.ReadMessage()
			var f Frame
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("error: %v", err)
				}
			} else {
				json.Unmarshal(message, &f)
			}

			result := wsResult{err: err, msg: f}
			select {
			case <-ctx.Done():
				return
			case ch <- result:
			}
		}
	}()
	return ch
}

func writeWebsocket(ctx context.Context, ch chan []byte, conn *websocket.Conn) {
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer func() {
			ticker.Stop()
			conn.Close()
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				if !ok {
					conn.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}

				w, err := conn.NextWriter(websocket.TextMessage)
				if err != nil {
					return
				}
				w.Write(msg)

				n := len(ch)
				for i := 0; i < n; i++ {
					w.Write(newline)
					w.Write(<-ch)
				}

				if err := w.Close(); err != nil {
					return
				}
			case <-ticker.C:
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()
}
