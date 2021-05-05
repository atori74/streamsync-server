package client

import (
	"os"
	"stream_sync/frame"
	"time"

	"context"
	"encoding/json"
	"log"

	"cloud.google.com/go/pubsub"
	"github.com/go-redis/redis/v8"
	"github.com/rs/xid"
)

const roomsKey = "rooms"

var projectID string

func init() {
	projectID = os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		panic("No GCP_PROJECT_ID is set in environment variables")
	}
}

type Client struct {
	pubsub *pubsub.Subscription
	// listening    bool
	// stopListener chan struct{}
	Send   chan []byte
	RoomID string
	IsHost bool
	// disconnect client's websocket
	Disconnect chan struct{}
}

func HostRoom(rdb *redis.Client, psc *pubsub.Client, ctx context.Context) (*Client, error) {
	roomID := xid.New().String()

	host := &Client{
		// stopListener: make(chan struct{}),
		Send:   make(chan []byte),
		RoomID: roomID,
		IsHost: true,
	}

	if err := rdb.SAdd(ctx, roomsKey, roomID).Err(); err != nil {
		log.Println("redis add error")
		return nil, err
	}

	// pubsub := rdb.Subscribe(ctx, roomID)
	// host.pubsub = pubsub
	topic, err := psc.CreateTopic(ctx, roomID)
	if err != nil {
		log.Println("failed: create topic")
		return nil, err
	}

	sub, err := psc.CreateSubscription(ctx, xid.New().String(), pubsub.SubscriptionConfig{
		Topic:            topic,
		AckDeadline:      10 * time.Second,
		ExpirationPolicy: 24 * time.Hour,
	})
	if err != nil {
		log.Println("failed: create subscription")
		return nil, err
	}
	host.pubsub = sub

	// PubSub listener
	go pubSubListener(host, hostCloudPubSubHandler)

	return host, nil
}

func JoinRoom(rdb *redis.Client, psc *pubsub.Client, ctx context.Context, roomID string) (*Client, error) {
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

	// pubsub := rdb.Subscribe(ctx, roomID)
	// client.pubsub = pubsub
	sub, err := psc.CreateSubscription(ctx, xid.New().String(), pubsub.SubscriptionConfig{
		Topic:            psc.Topic(roomID),
		AckDeadline:      10 * time.Second,
		ExpirationPolicy: 24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	client.pubsub = sub

	// PubSub listener
	go pubSubListener(client, clientCloudPubSubHandler)

	return client, nil
}

func (c *Client) Unsubscribe() error {
	ctx := context.TODO()
	if c.pubsub != nil {
		if err := c.pubsub.Delete(ctx); err != nil {
			return err
		}
	}
	// if c.listening {
	// 	c.stopListener <- struct{}{}
	// }

	close(c.Send)

	return nil
}

// func pubSubListener(client *Client, handler func(*redis.Message, *Client)) {
// 	// client.listening = true
// 	for {
// 		select {
// 		case m, ok := <-client.pubsub.Channel():
// 			// m: redis.Message
// 			if !ok {
// 				return
// 			}
// 			handler(m, client) // return []byte
// 			// case <-client.stopListener:
// 			// 	return
// 		}
// 	}
// }

func pubSubListener(client *Client, handler func(context.CancelFunc, *pubsub.Message, *Client)) {
	ctx := context.Background()
	cctx, cancel := context.WithCancel(ctx)
	err := client.pubsub.Receive(cctx, func(ctx context.Context, msg *pubsub.Message) {
		handler(cancel, msg, client)
	})
	if err != nil {
		return
	}
}

func hostCloudPubSubHandler(cancel context.CancelFunc, msg *pubsub.Message, client *Client) {
	if string(msg.Data) == "CLOSE" {
		cancel()
		client.Unsubscribe()
		//TODO: delete topic
		msg.Ack()
		return
	}
	var f frame.Frame
	if err := json.Unmarshal(msg.Data, &f); err != nil {
		log.Println(err)
		return
	}

	if f.Type == "newClient" {
		client.Send <- msg.Data
	}
	msg.Ack()
}

func clientCloudPubSubHandler(cancel context.CancelFunc, msg *pubsub.Message, client *Client) {
	// 一旦channelにpublishされたメッセージをそのままbyte列にしてwebsocketで配信する
	if string(msg.Data) == "CLOSE" {
		cancel()
		client.Unsubscribe()
		client.Disconnect <- struct{}{}
		msg.Ack()
		return
	}
	client.Send <- msg.Data
	msg.Ack()
}

// func hostPubSubHandler(msg *redis.Message, client *Client) {
// 	var f frame.Frame
// 	if err := json.Unmarshal([]byte(msg.Payload), &f); err != nil {
// 		log.Println(err)
// 		return
// 	}
//
// 	if f.Type == "newClient" {
// 		client.Send <- []byte(msg.Payload)
// 	}
// }
//
// func clientPubSubHandler(msg *redis.Message, client *Client) {
// 	// 一旦channelにpublishされたメッセージをそのままbyte列にしてwebsocketで配信する
// 	if msg.Payload == "CLOSE" {
// 		client.Unsubscribe()
// 		client.Disconnect <- struct{}{}
// 	}
// 	client.Send <- []byte(msg.Payload)
// }
