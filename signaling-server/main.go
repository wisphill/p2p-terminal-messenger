package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

type Message struct {
	Type     string `json:"type"`
	From     string `json:"from"`
	To       string `json:"to"`
	RandomId int    `json:"random_id"`
	Data     string `json:"data"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var peers = make(map[string]*websocket.Conn)
var mu sync.Mutex

func wsHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", 400)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	mu.Lock()
	peers[id] = conn
	mu.Unlock()

	log.Println("peer connected:", id)

	defer func() {
		mu.Lock()
		delete(peers, id)
		mu.Unlock()
		conn.Close()
		log.Println("peer disconnected:", id)
	}()

	for {
		var msg Message

		err := conn.ReadJSON(&msg)
		if err != nil {
			// Check if the error is a normal or abnormal closure
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Connection error for %s: %v", id, err)
			} else {
				log.Printf("Peer %s disconnected normally %v %v", id, msg, err)
			}

			return
		}

		mu.Lock()
		target := peers[msg.To]
		mu.Unlock()

		if target != nil {
			target.WriteJSON(msg)
		}
	}
}

func main() {
	http.HandleFunc("/ws", wsHandler)
	log.Println("signaling server running on :8080")
	http.ListenAndServe(":8080", nil)
}
