package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/rs/xid"
)

var dsClient *Datastore
var psClient *pubsub.Client

func init() {
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		panic("no project-id in env variables")
	}

	ctx := context.Background()
	dsc, err := NewDatastore(projectID)
	if err != nil {
		panic("failed to initialize datastore client")
	}
	dsClient = dsc

	psc, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		panic("failed to initialize pubsub client")
	}
	psClient = psc
}

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

var ErrSubscriptionSkip = errors.New("Skip subscription message")
var ErrRoomClosed = errors.New("The room was closed")

type Frame struct {
	Type string      `json:"type"`
	From string      `json:"from"`
	Data interface{} `json:"data"`
}

type wsResult struct {
	err error
	msg Frame
}

type subResult struct {
	err error
	msg []byte
}

type room struct {
	clientCount int
	mediaURL    string
}

func (r *room) increment(i int) {
	r.clientCount += i
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

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), "host", struct{}{}))

	roomID := xid.New().String()

	dsClient.Lock()
	err = dsClient.Put(ctx, roomID, &Room{})
	dsClient.Unlock()
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}

	wsReadCh := readWebsocket(ctx, conn)
	wsWriteCh := make(chan []byte)
	writeWebsocket(ctx, wsWriteCh, conn)

	// TODO: Create topic

	var mu sync.Mutex
	subscribeCh := subscribe(ctx, roomID, &mu)
	publishCh := make(chan []byte)
	err = registerPublisher(ctx, publishCh, roomID, &mu)
	if err != nil {
		log.Println(err)
		cancel()
		close(wsWriteCh)
		close(publishCh)
		return
	}

	go func() {
		defer func() {
			log.Printf("%s is closed", roomID)
			dsClient.Lock()
			if err := dsClient.Delete(context.Background(), roomID); err != nil {
				log.Println("failed to delete room in datastore")
			}
			dsClient.Unlock()
			close(wsWriteCh)
			close(publishCh)
			// TODO: Delete topic
		}()

		for {
			select {
			case res := <-wsReadCh:
				if res.err != nil {
					cancel()
					return
				}

				b, err := hostWSHandler(&res.msg, roomID)
				if err != nil {
					log.Println(err)
					continue
				}
				publishCh <- b
			case res := <-subscribeCh:
				if res.err != nil {
					cancel()
					return
				}

				b, err := hostSubHandler(res.msg)
				if err == ErrSubscriptionSkip {
					continue
				} else if err != nil {
					log.Println(err)
					continue
				}
				wsWriteCh <- b
			}
		}
	}()

	f := Frame{
		From: "server",
		Type: "roomInfo",
		Data: map[string]interface{}{
			"roomID": roomID,
		},
	}
	b, err := json.Marshal(f)
	if err != nil {
		log.Println(err)
	}
	wsWriteCh <- b
}

func JoinRoomHandler(w http.ResponseWriter, r *http.Request) {
	validPath := regexp.MustCompile(`^/join/[0-9a-zA-Z]+$`)
	m := validPath.FindStringSubmatch(r.URL.Path)
	if len(m) == 0 {
		http.Error(w, "invalid url", http.StatusNotFound)
		return
	}

	roomID, ok := mux.Vars(r)["room_id"]
	if !ok {
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	// find the room in datastore
	dsClient.RLock()
	room, err := dsClient.Get(ctx, roomID)
	dsClient.RUnlock()
	if err != nil {
		log.Println(err)
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "failed to upgrade websocket", http.StatusInternalServerError)
		return
	}

	// increment client count of the room in datastore
	room.ClientCount += 1
	dsClient.Lock()
	err = dsClient.Mutate(ctx, roomID, room)
	dsClient.Unlock()
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}

	wsReadCh := readWebsocket(ctx, conn)
	wsWriteCh := make(chan []byte)
	writeWebsocket(ctx, wsWriteCh, conn)

	var mu sync.Mutex
	subscribeCh := subscribe(ctx, roomID, &mu)
	publishCh := make(chan []byte)
	err = registerPublisher(ctx, publishCh, roomID, &mu)
	if err != nil {
		log.Println(err)
		cancel()
		close(wsWriteCh)
		close(publishCh)
		return
	}

	go func() {
		defer func() {
			dsClient.Lock()
			room, err := dsClient.Get(context.Background(), roomID)
			if err == nil {
				room.ClientCount -= 1
				dsClient.Mutate(context.Background(), roomID, room)
			}
			dsClient.Unlock()
			close(wsWriteCh)
			close(publishCh)
		}()

		for {
			select {
			case res := <-wsReadCh:
				if res.err != nil {
					cancel()
					return
				}

				b, err := clientWSHandler(&res.msg)
				if err != nil {
					log.Println(err)
					continue
				}
				publishCh <- b
			case res := <-subscribeCh:
				if res.err != nil {
					cancel()
					return
				}

				b, err := clientSubHandler(res.msg)
				if err == ErrRoomClosed {
					cancel()
					return
				} else if err != nil {
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

func subscribe(ctx context.Context, roomID string, mu *sync.Mutex) <-chan subResult {
	subCh := make(chan subResult)
	go func() {
		defer close(subCh)
		mu.Lock()
		topic, err := getTopic(ctx, roomID)
		mu.Unlock()
		if err != nil {
			subCh <- subResult{err: err, msg: nil}
			return
		}
		sub, err := psClient.CreateSubscription(ctx, xid.New().String(), pubsub.SubscriptionConfig{
			Topic: topic,
		})
		if err != nil {
			subCh <- subResult{err: err, msg: nil}
			return
		}

		err = sub.Receive(ctx, func(ctx context.Context, m *pubsub.Message) {
			subCh <- subResult{err: nil, msg: m.Data}
			m.Ack()
		})
		if err != nil {
			subCh <- subResult{err: err, msg: nil}
		}
	}()
	return subCh
}

type pubRegister struct {
	ctx context.Context
	ch  <-chan []byte
}

var publishers map[string]chan pubRegister = make(map[string]chan pubRegister)

func registerPublisher(ctx context.Context, ch <-chan []byte, roomID string, mu *sync.Mutex) error {
	if rch, ok := publishers[roomID]; ok {
		fmt.Println("multiplex new ch in publisher")
		rch <- pubRegister{ctx: ctx, ch: ch}
		return nil
	}

	fmt.Println("new publisher")

	// pretty long blocking
	mu.Lock()
	topic, err := getTopic(ctx, roomID)
	mu.Unlock()
	if err != nil {
		return err
	}

	publishCh := make(chan []byte)
	registerCh := make(chan pubRegister)
	publishers[roomID] = registerCh

	var wg sync.WaitGroup
	multiplex := func(ctx context.Context, c <-chan []byte) {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				if ctx.Value("host") != nil {
					publishCh <- []byte("CLOSE")
				}
				return
			case i := <-c:
				publishCh <- i
			}
		}
	}

	// initial ch
	wg.Add(1)
	go multiplex(ctx, ch)

	// main publisher
	go func() {
		for msg := range publishCh {
			topic.Publish(context.Background(), &pubsub.Message{Data: msg})
		}
	}()

	// add ch for publishing
	go func() {
		for r := range registerCh {
			wg.Add(1)
			go multiplex(r.ctx, r.ch)
		}
	}()

	// wait until all channels for publishing  get closed
	go func() {
		wg.Wait()
		log.Println("this publisher is garbage now")
		delete(publishers, roomID)
		close(registerCh)
		close(publishCh)
	}()

	return nil
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

func getTopic(ctx context.Context, topicID string) (*pubsub.Topic, error) {
	topic := psClient.Topic(topicID)
	ok, err := topic.Exists(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		topic, err = psClient.CreateTopic(context.Background(), topicID)
		if err != nil {
			return nil, err
		}
	}
	return topic, nil
}

func hostWSHandler(frame *Frame, roomID string) ([]byte, error) {
	ctx := context.Background()
	switch frame.Type {
	case "playbackPosition":
		data := frame.Data.(map[string]interface{})
		if _, ok := data["position"].(float64); !ok {
			return nil, errors.New("invalid frame")
		}
		if _, ok := data["currentTime"].(string); !ok {
			return nil, errors.New("invalid frame")
		}
		mediaURL, ok := data["mediaURL"].(string)
		if !ok {
			return nil, errors.New("invalid frame")
		}

		dsClient.Lock()
		if room, err := dsClient.Get(ctx, roomID); err == nil {
			if room.MediaURL == "" || room.MediaURL != mediaURL {
				room.MediaURL = mediaURL
				dsClient.Mutate(ctx, roomID, room)
			}
		} else {
			log.Println("failed to update mediaURL: ", roomID)
		}
		dsClient.Unlock()

		frame.From = "server"
		b, err := json.Marshal(frame)
		if err != nil {
			return nil, err
		}
		return b, nil
	case "command":
		cmd, ok := frame.Data.(map[string]interface{})["command"].(string)
		if !ok {
			return nil, errors.New("invalid command frame")
		}
		switch cmd {
		case "play":
			b, err := json.Marshal(frame)
			if err != nil {
				return nil, err
			}
			return b, nil
		case "pause":
			data := frame.Data.(map[string]interface{})
			if _, ok := data["position"].(float64); !ok {
				return nil, errors.New("invalid pause frame")
			}
			if _, ok := data["mediaURL"].(string); !ok {
				return nil, errors.New("invalid pause frame")
			}

			b, err := json.Marshal(frame)
			if err != nil {
				return nil, err
			}
			return b, nil
		}
	case "message":
		msg, ok := frame.Data.(map[string]interface{})["content"].(string)
		if !ok {
			return nil, errors.New("invalid message frame")
		}

		send := Frame{
			Type: "message",
			From: "host",
			Data: msg,
		}

		b, err := json.Marshal(send)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, errors.New("invalid frame type")
}

func hostSubHandler(msg []byte) ([]byte, error) {
	return nil, ErrSubscriptionSkip
}

func clientWSHandler(frame *Frame) ([]byte, error) {
	switch frame.Type {
	case "message":
		msg, ok := frame.Data.(map[string]interface{})["content"].(string)
		if !ok {
			return nil, errors.New("invalid message frame")
		}

		send := Frame{
			Type: "message",
			From: "client",
			Data: msg,
		}

		b, err := json.Marshal(send)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, errors.New("invalid frame type")
}

func clientSubHandler(msg []byte) ([]byte, error) {
	if string(msg) == "CLOSE" {
		return nil, ErrRoomClosed
	}
	return msg, nil
}
