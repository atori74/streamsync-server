package main

import (
	"log"
	"net/http"
)

func topHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, "top.html")
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Println(startWebServer())

	// room := newRoom()
	// go room.run()
	// http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
	// 	serveWs(room, w, r)
	// })
	// http.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
	// 	startHost(room, w, r)
	// })
	// http.HandleFunc("/", topHandler)
	// err := http.ListenAndServe(":8889", nil)
	// if err != nil {
	// 	log.Fatalln("ListenAndServe: ", err)
	// }
}
