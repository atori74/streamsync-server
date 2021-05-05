package api

import (
	"stream_sync/client"
	"stream_sync/frame"

	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
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

const (
	roomsKey = "rooms"
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

func H(rdb *redis.Client, psc *pubsub.Client, fn func(http.ResponseWriter, *http.Request, *redis.Client, *pubsub.Client)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		fn(w, r, rdb, psc)
	}
}

func NewRoomHandler(w http.ResponseWriter, r *http.Request, rdb *redis.Client, psc *pubsub.Client) {
	if r.URL.Path != "/new" {
		// error response
		wsError(w, "bad url", http.StatusNotFound)
		return
	}

	// upgrade websocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		// error response
		wsError(w, "failed to upgrade websocket", http.StatusInternalServerError)
		return
	}

	ctx := context.TODO()

	// initialize Host
	host, err := client.HostRoom(rdb, psc, ctx)
	if err != nil {
		conn.Close() //?
		wsError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Println("initializing host is complete")

	// websocket writer
	go writer(conn, host)
	// websocket reader
	go reader(conn, host, rdb, psc, hostMessageHandler)

	f := frame.Frame{
		From: "server",
		Type: "roomInfo",
		Data: map[string]interface{}{
			"roomID": host.RoomID,
		},
	}
	j, err := json.Marshal(f)
	if err != nil {
		log.Println(err)
	}

	host.Send <- j
}

func JoinRoomHandler(w http.ResponseWriter, r *http.Request, rdb *redis.Client, psc *pubsub.Client) {
	log.Println("joinRoomHandler called")

	validPath := regexp.MustCompile(`^/join/[0-9a-zA-Z]+$`)
	m := validPath.FindStringSubmatch(r.URL.Path)
	if len(m) == 0 {
		wsError(w, "invalid url", http.StatusNotFound)
		return
	}

	rid, ok := mux.Vars(r)["room_id"]
	if !ok {
		wsError(w, "invalid room id", http.StatusBadRequest)
		return
	}

	ctx := context.TODO()

	// query room
	exists, err := rdb.SIsMember(ctx, roomsKey, rid).Result()
	if err != nil {
		wsError(w, "redis error", http.StatusInternalServerError)
		return
	}
	if !exists {
		wsError(w, "invalid room id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// error response
		wsError(w, "failed to upgrade websocket", http.StatusInternalServerError)
		return
	}

	client, err := client.JoinRoom(rdb, psc, ctx, rid)
	if err != nil {
		conn.Close()
		wsError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// websocket reader
	go reader(conn, client, rdb, psc, clientMessageHandler)
	// websocket writer
	go writer(conn, client)

	mediaURL, err := rdb.HGet(ctx, client.RoomID, "mediaURL").Result()
	if err != nil {
		log.Println(err)
	}

	f := frame.Frame{
		From: "server",
		Type: "joinSuccess",
		Data: map[string]interface{}{
			"roomID":   client.RoomID,
			"mediaURL": mediaURL,
		},
	}
	j, err := json.Marshal(f)
	if err != nil {
		log.Println(err)
	}

	client.Send <- j
}

func reader(conn *websocket.Conn, client *client.Client, rdb *redis.Client, psc *pubsub.Client, handler func(*frame.Frame, *redis.Client, *pubsub.Client, *client.Client)) {
	defer func() {
		conn.Close()
		client.Unsubscribe()
		ctx := context.TODO()
		if client.IsHost {
			if err := rdb.SRem(ctx, roomsKey, client.RoomID).Err(); err != nil {
				log.Println(err)
			}
			if err := rdb.Unlink(ctx, client.RoomID).Err(); err != nil {
				log.Println(err)
			}
			if err := publishMessage(ctx, psc, client.RoomID, "CLOSE"); err != nil {
				log.Println(err)
			}
			if err := psc.Topic(client.RoomID).Delete(ctx); err != nil {
				log.Println(err)
			}
		} else {
			if e, err := rdb.Exists(ctx, client.RoomID).Result(); err != nil {
				log.Println(err)
			} else if e > 0 {
				if err := rdb.HIncrBy(ctx, client.RoomID, "clients", -1).Err(); err != nil {
					log.Println(err)
				}
			}
		}
	}()
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		select {
		case <-client.Disconnect:
			return
		default:
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("error: %v", err)
				}
				return
			}

			var f frame.Frame
			json.Unmarshal(message, &f)

			handler(&f, rdb, psc, client)
		}
	}
}

func writer(conn *websocket.Conn, client *client.Client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		conn.Close()
	}()
	for {
		select {
		case m, ok := <-client.Send:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(m)

			n := len(client.Send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-client.Send)
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
}

// TODO HOST websocket message handling
// redisにpublishしてから個々のgoroutineでフィルタリングするのはリソースの無駄なので
// 基本的にこのタイミングで前処理は終わらせる
func hostMessageHandler(f *frame.Frame, rdb *redis.Client, psc *pubsub.Client, c *client.Client) {
	ctx := context.TODO()
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
		log.Printf("roomID: %s, position: %f, at: %s\n", c.RoomID, position, currentTime)

		url, ok := data["mediaURL"].(string)
		if !ok {
			log.Println("invalid frame")
			return
		}
		mURL, err := rdb.HGet(ctx, c.RoomID, "mediaURL").Result()
		if err != nil && err != redis.Nil {
			log.Println(err)
		} else if err == redis.Nil || mURL != url {
			err := rdb.HSet(ctx, c.RoomID, "mediaURL", url)
			if err != nil {
				log.Println(err)
			}
		}

		sFrame := f
		sFrame.From = "server"

		b, err := json.Marshal(sFrame)
		if err != nil {
			log.Println(err)
			return
		}

		// broadcastする代わりにredisにpublishする
		// redisにpublishするかわりにcloud pub/subのRoomIDトピックにpublishする
		// err = rdb.Publish(ctx, c.RoomID, string(b)).Err()
		err = publishMessage(ctx, psc, c.RoomID, string(b))
		if err != nil {
			log.Println(err)
			return
		}
	case "command":
		if cmd, ok := f.Data.(map[string]interface{})["command"].(string); ok {
			switch cmd {
			case "pause":
				data := f.Data.(map[string]interface{})

				_, ok := data["position"].(float64)
				if !ok {
					log.Println("invalid frame")
					return
				}
				_, ok = data["mediaURL"].(string)
				if !ok {
					log.Println("invalid frame")
					return
				}

				sFrame := f

				b, err := json.Marshal(sFrame)
				if err != nil {
					log.Println(err)
					return
				}

				// broadcastする代わりにredisにpublishする
				// err = rdb.Publish(ctx, c.RoomID, string(b)).Err()
				err = publishMessage(ctx, psc, c.RoomID, string(b))
				if err != nil {
					log.Println(err)
					return
				}
			case "play":
				sFrame := f

				b, err := json.Marshal(sFrame)
				if err != nil {
					log.Println(err)
					return
				}

				// broadcastする代わりにredisにpublishする
				ctx := context.Background()
				// err = rdb.Publish(ctx, c.RoomID, string(b)).Err()
				err = publishMessage(ctx, psc, c.RoomID, string(b))
				if err != nil {
					log.Println(err)
					return
				}
				// case "syncStart":
				// 	if c.inSync {
				// 		return
				// 	}
				// 	c.inSync = true

				// 	sFrame := Frame{
				// 		Type: "syncStarted",
				// 		From: "server",
				// 	}

				// 	b, err := json.Marshal(sFrame)
				// 	if err != nil {
				// 		log.Println(err)
				// 		return
				// 	}

				// 	c.send <- b
				// case "syncStop":
				// 	if !c.inSync {
				// 		return
				// 	}
				// 	c.inSync = false

				// 	sFrame := Frame{
				// 		Type: "syncStopped",
				// 		From: "server",
				// 	}

				// 	b, err := json.Marshal(sFrame)
				// 	if err != nil {
				// 		log.Println(err)
				// 		return
				// 	}

				// 	c.send <- b
			}
		}
	case "message":
		data, ok := f.Data.(map[string]interface{})
		if !ok {
			log.Println("invalid frame")
			return
		}

		content, ok := data["content"].(string)
		if !ok {
			log.Println("invalid frame")
			return
		}

		sFrame := frame.Frame{
			Type: "message",
			From: "host",
			Data: content,
		}

		b, err := json.Marshal(sFrame)
		if err != nil {
			log.Println(err)
			return
		}

		// broadcastする代わりにredisにpublishする
		// err = rdb.Publish(ctx, c.RoomID, string(b)).Err()
		err = publishMessage(ctx, psc, c.RoomID, string(b))
		if err != nil {
			log.Println(err)
			return
		}
	}
}

// TODO HOST websocket message handling
func clientMessageHandler(f *frame.Frame, rdb *redis.Client, psc *pubsub.Client, c *client.Client) {
	ctx := context.TODO()
	switch f.Type {
	// case "command":
	// 	if cmd, ok := f.Data.(map[string]interface{})["command"].(string); ok {
	// 		switch cmd {
	// 		case "syncStart":
	// 			if c.inSync {
	// 				return
	// 			}
	// 			c.inSync = true

	// 			sFrame := Frame{
	// 				Type: "syncStarted",
	// 				From: "server",
	// 			}

	// 			b, err := json.Marshal(sFrame)
	// 			if err != nil {
	// 				log.Println(err)
	// 				return
	// 			}

	// 			c.send <- b
	// 		case "syncStop":
	// 			if !c.inSync {
	// 				return
	// 			}
	// 			c.inSync = false

	// 			sFrame := Frame{
	// 				Type: "syncStopped",
	// 				From: "server",
	// 			}

	// 			b, err := json.Marshal(sFrame)
	// 			if err != nil {
	// 				log.Println(err)
	// 				return
	// 			}

	// 			c.send <- b
	// 		}
	// 	}
	case "message":
		data, ok := f.Data.(map[string]interface{})
		if !ok {
			log.Println("invalid frame")
			return
		}

		content, ok := data["content"].(string)
		if !ok {
			log.Println("invalid frame")
			return
		}

		sFrame := frame.Frame{
			Type: "message",
			From: "client",
			Data: content,
		}

		b, err := json.Marshal(sFrame)
		if err != nil {
			log.Println(err)
			return
		}

		// broadcastする代わりにredisにpublishする
		// err = rdb.Publish(ctx, c.RoomID, string(b)).Err()
		err = publishMessage(ctx, psc, c.RoomID, string(b))
		if err != nil {
			log.Println(err)
			return
		}
	}
}

func publishMessage(ctx context.Context, client *pubsub.Client, topicID, msg string) error {
	t := client.Topic(topicID)
	result := t.Publish(ctx, &pubsub.Message{
		Data: []byte(msg),
	})
	_, err := result.Get(ctx)
	if err != nil {
		return fmt.Errorf("Get: %v", err)
	}
	return nil
}

func wsError(w http.ResponseWriter, msg string, code int) {
	log.Println("wsError: " + msg)
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	fmt.Fprint(w, msg)
}
