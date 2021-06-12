package main

import (
	"log"
	"net/http"
	"os"

	"streamsync-server/api"

	"github.com/gorilla/mux"
)

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("[Request]", r.RequestURI)
		next.ServeHTTP(w, r)
	})
}

func startWebserver() error {
	r := mux.NewRouter().StrictSlash(true)

	r.HandleFunc("/new", api.NewRoomHandler)
	r.HandleFunc("/join/{room_id}", api.JoinRoomHandler)
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
