package main

import (
	"context"
	"errors"
	"stream_sync/api"

	"log"
	"net/http"
	"os"

	"cloud.google.com/go/pubsub"
	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
)

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("[Request]", r.RequestURI)
		next.ServeHTTP(w, r)
	})
}

func startWebServer() error {
	redisAddr := os.Getenv("REDIS")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
		log.Printf("defaulting redis address to %s", redisAddr)
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		return errors.New("No GCP_PROJECT_ID is set in environment variables")
	}
	ctx := context.TODO()
	psc, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return err
	}

	r := mux.NewRouter().StrictSlash(true)

	r.HandleFunc("/new", api.H(rdb, psc, api.NewRoomHandler))
	r.HandleFunc("/join/{room_id}", api.H(rdb, psc, api.JoinRoomHandler))
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
