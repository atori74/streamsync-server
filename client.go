package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
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

type Client struct {
	room   *Room
	conn   *websocket.Conn
	send   chan []byte
	inSync bool
}

func (c *Client) reader() {
	defer func() {
		c.room.unregister <- c
		c.conn.Close()
	}()
	// readのmax sizeを設定
	c.conn.SetReadLimit(maxMessageSize)
	// 1分ごとのping応答がなければtimeoutする
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}

		var f Frame
		json.Unmarshal(message, &f)

		handleFrame(f, c)
	}
}

func (c *Client) writer() {
	// あとでpongハンドルをwriterに含めてみる
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// 後続のメッセージがある場合はchanから取り出してこのタイミングでまとめて送っちゃう
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			// ping送信
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) hostReader() {
	defer func() {
		c.room.closed <- true
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		var f Frame
		json.Unmarshal(message, &f)

		handleFrame(f, c)
	}
}

func handleFrame(f Frame, c *Client) {
	switch f.Type {
	case "playbackPosition":
		data := f.Data.(map[string]interface{})

		position, ok := data["position"].(float64)
		if !ok {
			log.Println("invalid frame")
			return
		}
		currentTime, ok := data["currentTime"].(string)
		if !ok {
			log.Println("invalid frame")
			return
		}
		log.Printf("roomID: %s, position: %b, at: %s\n", c.room.ID.String(), position, currentTime)

		url, ok := data["mediaURL"].(string)
		if !ok {
			log.Println("invalid frame")
			return
		}
		if c.room.mediaURL == "" || c.room.mediaURL != url {
			c.room.mediaURL = url
		}

		sFrame := f
		sFrame.From = "server"

		b, err := json.Marshal(sFrame)
		if err != nil {
			log.Println(err)
			return
		}

		c.room.broadcast <- b

	case "command":
		if cmd, ok := f.Data.(map[string]interface{})["command"].(string); ok {
			switch f.Data.(map[string]interface{})["command"].(string) {
			case "syncStart":
				if c.inSync {
					return
				}
				c.inSync = true

				sFrame := Frame{
					Type: "syncStopped",
					From: "server",
				}
			case "syncStop":
				if !c.inSync {
					return
				}
				c.inSync = false

				sFrame := Frame{
					Type: "syncStarted",
					From: "server",
				}
			}
		}
	}
}

func (c *Client) hostWriter() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
		c.room.closed <- true
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// 後続のメッセージがある場合はchanから取り出してこのタイミングでまとめて送っちゃう
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			// ping送信
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
