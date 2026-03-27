package main

import (
	"log"
	"net/http"
	"strings"
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

// TODO: handle disconnected conn
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

	// send current peers list
	var onlinePeers []string
	for peerID := range peers {
		onlinePeers = append(onlinePeers, peerID)
	}
	listMsg := map[string]interface{}{
		"type": "online_list",
		"data": strings.Join(onlinePeers, ";"), // Gửi mảng string IDs
	}
	conn.WriteJSON(listMsg)

	peers[id] = conn
	// notify the newcomer to the old peers
	joinMsg := Message{
		Type: "new_peer",
		From: id,
	}
	for key, peerConn := range peers {
		if key != id {
			peerConn.WriteJSON(joinMsg)
		}
	}
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
