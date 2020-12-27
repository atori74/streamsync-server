package main

import (
	"encoding/json"
	"log"

	"github.com/rs/xid"
)

type Room struct {
	ID         xid.ID
	clients    map[*Client]bool
	host       *Client
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	open       chan *Client
	closed     chan bool
	mediaURL   string
}

func newRoom() *Room {
	return &Room{
		ID:         xid.New(),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		open:       make(chan *Client),
		closed:     make(chan bool),
		broadcast:  make(chan []byte),
		clients:    make(map[*Client]bool),
		host:       new(Client),
	}
}

func (r *Room) run() {
	for {
		select {
		case host := <-r.open:
			r.host = host
			log.Println("on open; room id = ", r.ID.String())
			f := Frame{
				Type: "roomInfo",
				From: "server",
				Data: map[string]interface{}{
					"roomID": r.ID.String(),
				},
			}
			j, err := json.Marshal(f)
			if err != nil {
				log.Println(err)
			}
			r.host.send <- j
		case <-r.closed:
			for client := range r.clients {
				delete(r.clients, client)
				close(client.send)
			}
		case client := <-r.register:
			r.clients[client] = true
			f := Frame{
				Type: "newClient",
				From: "server",
				Data: map[string]interface{}{
					"clientCount": len(r.clients),
				},
			}
			j, err := json.Marshal(f)
			if err != nil {
				log.Println(err)
			}

			r.host.send <- j
		case client := <-r.unregister:
			if _, ok := r.clients[client]; ok {
				delete(r.clients, client)
				close(client.send)
			}
		case message := <-r.broadcast:
			for client := range r.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(r.clients, client)
				}
			}
		}
	}
}
