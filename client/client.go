package client

import (
	"stream_sync/frame"

	"context"
	"encoding/json"
	"log"

	"github.com/go-redis/redis/v8"
	"github.com/rs/xid"
)

const roomsKey = "rooms"

type Client struct {
	pubsub *redis.PubSub
	// listening    bool
	// stopListener chan struct{}
	Send   chan []byte
	RoomID string
	IsHost bool
	// disconnect client's websocket
	Disconnect chan struct{}
}

func HostRoom(rdb *redis.Client, ctx context.Context) (*Client, error) {
	roomID := xid.New().String()

	host := &Client{
		// stopListener: make(chan struct{}),
		Send:   make(chan []byte),
		RoomID: roomID,
		IsHost: true,
	}

	if err := rdb.SAdd(ctx, roomsKey, roomID).Err(); err != nil {
		return nil, err
	}

	pubsub := rdb.Subscribe(ctx, roomID)
	host.pubsub = pubsub

	// PubSub listener
	go pubSubListener(host, hostPubSubHandler)

	return host, nil
}

func JoinRoom(rdb *redis.Client, ctx context.Context, roomID string) (*Client, error) {
	client := &Client{
		// stopListener: make(chan struct{}),
		Send:   make(chan []byte),
		RoomID: roomID,
		IsHost: false,
	}

	if numClients, err := rdb.HIncrBy(ctx, roomID, "clients", 1).Result(); err != nil {
		log.Println(err)
	} else {
		f := frame.Frame{
			Type: "newClient",
			From: "server",
			Data: map[string]interface{}{"clientCount": numClients},
		}
		if b, err := json.Marshal(f); err != nil {
			log.Println(err)
		} else {
			if err := rdb.Publish(ctx, roomID, string(b)).Err(); err != nil {
				log.Println(err)
			}
		}
	}

	pubsub := rdb.Subscribe(ctx, roomID)
	client.pubsub = pubsub

	// PubSub listener
	go pubSubListener(client, clientPubSubHandler)

	return client, nil
}

func (c *Client) Unsubscribe() error {
	ctx := context.TODO()
	if c.pubsub != nil {
		if err := c.pubsub.Unsubscribe(ctx); err != nil {
			return err
		}
		if err := c.pubsub.Close(); err != nil {
			return err
		}
	}
	// if c.listening {
	// 	c.stopListener <- struct{}{}
	// }

	close(c.Send)

	return nil
}

func pubSubListener(client *Client, handler func(*redis.Message, *Client)) {
	// client.listening = true
	for {
		select {
		case m, ok := <-client.pubsub.Channel():
			// m: redis.Message
			if !ok {
				return
			}
			handler(m, client) // return []byte
			// case <-client.stopListener:
			// 	return
		}
	}
}

func hostPubSubHandler(msg *redis.Message, client *Client) {
	// HostはClientからメッセージを受け取る必要はないため、空にしておく
	var f frame.Frame
	if err := json.Unmarshal([]byte(msg.Payload), &f); err != nil {
		log.Println(err)
		return
	}

	if f.Type == "newClient" {
		client.Send <- []byte(msg.Payload)
	}
}

func clientPubSubHandler(msg *redis.Message, client *Client) {
	// 一旦channelにpublishされたメッセージをそのままbyte列にしてwebsocketで配信する
	if msg.Payload == "CLOSE" {
		client.Unsubscribe()
		client.Disconnect <- struct{}{}
	}
	client.Send <- []byte(msg.Payload)
}
