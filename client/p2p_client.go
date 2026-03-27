package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

type Signal struct {
	// common fields
	To   string `json:"to"`
	Data string `json:"data"`
	Type string `json:"type"`
	From string `json:"from"`

	// specific fields
	RandomId int `json:"random_id"`
}

func sendWsMessage(ws *websocket.Conn, v any) error {
	var wsMu sync.Mutex
	wsMu.Lock()
	defer wsMu.Unlock()
	return ws.WriteJSON(v)
}

func handlingWsMessages(ws *websocket.Conn) error {
	for {
		var s Signal
		if err := ws.ReadJSON(&s); err != nil {
			fmt.Println("WebSocket read error:", err)
			break
		}

		// create a list of data channel to the list
		if s.Type != "" && s.Type == "new_peer" {
			fmt.Printf("New peer %v is just joined \n ", s.From)
			continue
		}

		// getting this message in the first time of the connection creation
		if s.Type != "" && s.Type == "online_list" {
			currentPeers := strings.Split(s.Data, ";")
			for _, peer := range currentPeers {
				// TODO: data channel here
				_, _, offerBytes, err := createDataChanel()
				if err != nil {
					fmt.Errorf("Error while creating chat data channel with peers")
					continue
				}

				fmt.Printf("Sending offer to peer %v \n", peer)
				// send offer to peers to connect
				if err := sendWsMessage(ws, Signal{To: peer, Data: string(offerBytes)}); err != nil {
					fmt.Errorf("Error while sending offer to the signaling server")
					continue
				}
			}
		}
	}

	return nil
}

func createDataChanel() (*webrtc.PeerConnection, *webrtc.DataChannel, []byte, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Fatalf("failed to create peer connection: %v", err)
	}

	ch, err := pc.CreateDataChannel("chat", nil)
	if err != nil {
		panic(fmt.Sprintf("failed to create data channel: %v", err))
	}

	// events for data channels
	ch.OnMessage(func(msg webrtc.DataChannelMessage) {
		fmt.Println("peer:", string(msg.Data))
	})
	ch.OnOpen(func() {
		fmt.Println("--- Connection Established! You can now type messages ---")
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		panic(fmt.Sprintf("CreateOffer error: %v", err))
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		panic(fmt.Sprintf("SetLocalDescription error: %v", err))
	}
	offerBytes, err := json.Marshal(offer)
	if err != nil {
		panic(fmt.Sprintf("Marshal offer error: %v", err))
	}
	return pc, ch, offerBytes, err
}

// func initPeerDataChannel() {
// 	log.Printf("The client %v is the offerer to establish the data connection", clientInitialId)
// 	// This client is the offerer: create the data channel and send the offer.
// 	ch, err := pc.CreateDataChannel("chat", nil)
// 	if err != nil {
// 		panic(fmt.Sprintf("failed to create data channel: %v", err))
// 	}
// 	attachDataChannel(ch) // assigns into dc under dcMu

// 	offer, err := pc.CreateOffer(nil)
// 	if err != nil {
// 		panic(fmt.Sprintf("CreateOffer error: %v", err))
// 	}
// 	if err := pc.SetLocalDescription(offer); err != nil {
// 		panic(fmt.Sprintf("SetLocalDescription error: %v", err))
// 	}
// 	offerBytes, err := json.Marshal(offer)
// 	if err != nil {
// 		panic(fmt.Sprintf("Marshal offer error: %v", err))
// 	}
// }

func main() {
	id := os.Args[1]
	peer := os.Args[2]
	server := os.Args[3]
	clientInitialId := rand.Int()

	u := url.URL{Scheme: "ws", Host: server, Path: "/ws"}
	q := u.Query()
	q.Set("id", id)
	u.RawQuery = q.Encode()

	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		panic(err)
	}

	var wsMu sync.Mutex
	wsWriteJSON := func(v any) error {
		wsMu.Lock()
		defer wsMu.Unlock()
		return ws.WriteJSON(v)
	}

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Fatalf("failed to create peer connection: %v", err)
	}

	// dc is written from two goroutines (OnDataChannel callback, offerer setup)
	// and read from the main goroutine — guard with a mutex.
	var (
		dc   *webrtc.DataChannel
		dcMu sync.Mutex
	)

	// to set the data channel
	attachDataChannel := func(ch *webrtc.DataChannel) {
		dcMu.Lock()
		dc = ch
		dcMu.Unlock()
		ch.OnMessage(func(msg webrtc.DataChannelMessage) {
			fmt.Println("peer:", string(msg.Data))
		})
		ch.OnOpen(func() {
			fmt.Println("--- Connection Established! You can now type messages ---")
		})
		if ch.ReadyState() == webrtc.DataChannelStateOpen {
			fmt.Println("--- Connection Established! You can now type messages ---")
		}
	}

	// for clients as answerer
	pc.OnDataChannel(func(remote *webrtc.DataChannel) {
		attachDataChannel(remote)
	})

	// peer connection event OnICECandidate: find the local candidate and send to the signaling channel
	// for others to try to connect
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		data, err := json.Marshal(c.ToJSON())
		if err != nil {
			fmt.Println("Error marshalling ICE candidate:", err)
			return
		}
		if err := wsWriteJSON(Signal{To: peer, Data: string(data)}); err != nil {
			fmt.Println("Error sending ICE candidate:", err)
		}
	})

	var (
		pendingCandidates []webrtc.ICECandidateInit
		candidateMu       sync.Mutex
		remoteDescSet     bool
	)

	drainCandidates := func() {
		for _, cand := range pendingCandidates {
			if err := pc.AddICECandidate(cand); err != nil {
				fmt.Println("AddICECandidate error:", err)
			}
		}
		pendingCandidates = nil
	}

	go handlingWsMessages(ws)
	// handling the websocket for the signaling server
	go func() {
		for {
			var s Signal
			if err := ws.ReadJSON(&s); err != nil {
				fmt.Println("WebSocket read error:", err)
				break
			}

			if s.Type != "" && s.Type == "choosing_offerer" {
				fmt.Println("TEST for choosing offerer!!!")
				if clientInitialId < s.RandomId {
					log.Printf("The client %v is the offerer to establish the data connection", clientInitialId)
					// This client is the offerer: create the data channel and send the offer.
					ch, err := pc.CreateDataChannel("chat", nil)
					if err != nil {
						panic(fmt.Sprintf("failed to create data channel: %v", err))
					}
					attachDataChannel(ch) // assigns into dc under dcMu

					offer, err := pc.CreateOffer(nil)
					if err != nil {
						panic(fmt.Sprintf("CreateOffer error: %v", err))
					}
					if err := pc.SetLocalDescription(offer); err != nil {
						panic(fmt.Sprintf("SetLocalDescription error: %v", err))
					}
					offerBytes, err := json.Marshal(offer)
					if err != nil {
						panic(fmt.Sprintf("Marshal offer error: %v", err))
					}
					if err := wsWriteJSON(Signal{To: peer, Data: string(offerBytes)}); err != nil {
						panic(fmt.Sprintf("Error sending offer: %v", err))
					}
				}

			}

			var desc webrtc.SessionDescription
			if err := json.Unmarshal([]byte(s.Data), &desc); err == nil && desc.SDP != "" {
				if desc.Type == webrtc.SDPTypeOffer {
					if err := pc.SetRemoteDescription(desc); err != nil {
						fmt.Println("SetRemoteDescription error:", err)
						continue
					}
					answer, err := pc.CreateAnswer(nil)
					if err != nil {
						fmt.Println("CreateAnswer error:", err)
						continue
					}
					if err := pc.SetLocalDescription(answer); err != nil {
						fmt.Println("SetLocalDescription error:", err)
						continue
					}
					ansBytes, err := json.Marshal(answer)
					if err != nil {
						fmt.Println("Marshal answer error:", err)
						continue
					}
					if err := wsWriteJSON(Signal{To: peer, Data: string(ansBytes)}); err != nil {
						fmt.Println("Error sending answer:", err)
					}
				} else {
					if err := pc.SetRemoteDescription(desc); err != nil {
						fmt.Println("SetRemoteDescription error:", err)
						continue
					}
				}

				candidateMu.Lock()
				remoteDescSet = true
				drainCandidates()
				candidateMu.Unlock()
				continue
			}

			var cand webrtc.ICECandidateInit
			if err := json.Unmarshal([]byte(s.Data), &cand); err == nil {
				candidateMu.Lock()
				if remoteDescSet {
					if err := pc.AddICECandidate(cand); err != nil {
						fmt.Println("AddICECandidate error:", err)
					}
				} else {
					pendingCandidates = append(pendingCandidates, cand)
				}
				candidateMu.Unlock()
			}
		}
	}()

	// after create web socket connection with the signaling server, we need to send a message to the signal server
	if err := wsWriteJSON(Signal{To: peer, RandomId: clientInitialId, Type: "choosing_offerer", Data: ""}); err != nil {
		panic(fmt.Sprintf("Error while sending signal to choose the offerer: %v", err))
	}

	// Answerer: dc will be set when OnDataChannel fires.
	// Now sending data via p2p connection using webRTC
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		dcMu.Lock()
		ready := dc
		dcMu.Unlock()
		if ready == nil {
			fmt.Println("(connection not ready yet)")
			continue
		}
		if err := ready.SendText(scanner.Text()); err != nil {
			fmt.Printf("Error sending text: %v\n", err)
		}
	}
}
