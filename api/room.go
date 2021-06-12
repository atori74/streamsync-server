package api

import (
	"context"
	"sync"

	"cloud.google.com/go/datastore"
)

type Room struct {
	Key         *datastore.Key `datastore:"__key__"`
	ClientCount int            `datastore:"clients"`
	MediaURL    string         `datastore:"mediaURL"`
}

type Datastore struct {
	sync.RWMutex
	c *datastore.Client
}

type DB interface {
	Put(ctx context.Context, room *Room) error
	Get(ctx context.Context, id string) (*Room, error)
	Mutate(ctx context.Context, room *Room) error
}

func NewDatastore(projectID string) (*Datastore, error) {
	c, err := datastore.NewClient(context.Background(), projectID)
	if err != nil {
		return nil, err
	}
	return &Datastore{c: c}, nil
}

func (db *Datastore) Get(ctx context.Context, k string) (*Room, error) {
	key := datastore.NameKey("Room", k, nil)
	room := &Room{}
	if err := db.c.Get(ctx, key, room); err != nil {
		return nil, err
	}

	return room, nil
}

func (db *Datastore) Put(ctx context.Context, k string, room *Room) error {
	key := datastore.NameKey("Room", k, nil)
	if _, err := db.c.Put(ctx, key, room); err != nil {
		return err
	}
	return nil
}

func (db *Datastore) Mutate(ctx context.Context, k string, room *Room) error {
	key := datastore.NameKey("Room", k, nil)
	_, err := db.c.Mutate(ctx, datastore.NewUpdate(key, room))
	if err != nil {
		return err
	}
	return nil
}

func (db *Datastore) Delete(ctx context.Context, k string) error {
	key := datastore.NameKey("Room", k, nil)
	if err := db.c.Delete(ctx, key); err != nil {
		return err
	}
	return nil
}
