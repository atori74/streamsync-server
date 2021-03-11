package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

func startWebServer() error {
	r := mux.NewRouter().StrictSlash(true)

	r.HandleFunc("/new", newRoomHandler)
	r.HandleFunc("/join/{room_id}", joinRoomHandler)
	r.HandleFunc("/top", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/top.html")
	})
	r.Use(loggingMiddleware)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("defaulting to port %s", port)
	}

	log.Printf("listening on port %s", port)
	return http.ListenAndServe(":"+port, r)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("[Request]", r.RequestURI)
		next.ServeHTTP(w, r)
	})
}

var openRooms = make(map[string]*Room)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// CheckOriginメソッドでchrome拡張のoriginを許可する必要あり
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header["Origin"]
		if len(origin) == 0 {
			return true
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
		case "honmbceijbfoniffckiolgkgaieikenk": // Test environment
			return true
		default:
			return false
		}
	},
}

func newRoomHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("newRoomHandler called")
	if r.URL.Path != "/new" {
		// error response
		wsError(w, "bad url", http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		// error response
		wsError(w, "failed to upgrade websocket", http.StatusInternalServerError)
		return
	}

	room := newRoom()
	openRooms[room.ID.String()] = room
	go room.run()
	host := &Client{room: room, conn: conn, send: make(chan []byte), inSync: true}
	room.open <- host

	go host.hostReader()
	go host.hostWriter()
}

func joinRoomHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("joinRoomHandler called")
	validPath := regexp.MustCompile(`^/join/[0-9a-zA-Z]+$`)
	m := validPath.FindStringSubmatch(r.URL.Path)
	if len(m) == 0 {
		wsError(w, "bad url", http.StatusNotFound)
		return
	}

	// get room id
	rid, ok := mux.Vars(r)["room_id"]
	if !ok {
		wsError(w, "invalid room id", http.StatusBadRequest)
		return
	}
	// query room
	room, ok := openRooms[rid]
	if !ok {
		wsError(w, "invalid room id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// error response
		wsError(w, "failed to upgrade websocket", http.StatusInternalServerError)
		return
	}

	client := &Client{room: room, conn: conn, send: make(chan []byte), inSync: true}
	room.register <- client

	go client.reader()
	go client.writer()

	f := Frame{
		From: "server",
		Type: "joinSuccess",
		Data: map[string]interface{}{
			"roomID":   room.ID.String(),
			"mediaURL": room.mediaURL,
		},
	}
	j, err := json.Marshal(f)
	if err != nil {
		log.Println(err)
	}

	client.send <- j
}

func wsError(w http.ResponseWriter, msg string, code int) {
	log.Println("wsError: " + msg)
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	fmt.Fprint(w, msg)
}
