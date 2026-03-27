package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"p2p-terminal-messenger/internal"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"go.uber.org/zap"
)

type Signal struct {
	// common fields
	To   string `json:"to"`
	Data string `json:"data"`
	Type string `json:"type"`
	From string `json:"from"`
}

type Peer struct {
	connection        *webrtc.PeerConnection
	candidateMu       sync.Mutex
	pendingCandidates []webrtc.ICECandidateInit
	remoteDescSet     bool
	dataChanel        *webrtc.DataChannel
}

var (
	peerConnectionMap map[string]*Peer
	logger            *zap.SugaredLogger
)

func sendWsMessage(ws *websocket.Conn, v any) error {
	var wsMu sync.Mutex
	wsMu.Lock()
	defer wsMu.Unlock()
	return ws.WriteJSON(v)
}

func handlingWsMessages(ws *websocket.Conn, id string) error {
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
				_, _, offerBytes, err := createDataChanel(ws, id, peer)
				if err != nil {
					fmt.Errorf("Error while creating chat data channel with peers")
					continue
				}

				fmt.Printf("Sending offer to peer %v \n", peer)
				// send offer to peers to connect
				if err := sendWsMessage(ws, Signal{From: id, To: peer, Data: string(offerBytes)}); err != nil {
					fmt.Errorf("Error while sending offer to the signaling server")
					continue
				}
			}
		}

		var desc webrtc.SessionDescription
		if err := json.Unmarshal([]byte(s.Data), &desc); err == nil && desc.SDP != "" {
			drainCandidateWrapper := func() {
				peerM, ok := peerConnectionMap[s.From]
				if !ok {
					fmt.Printf("Peer %s does not exist!\n", s.From)
					return
				}
				peerM.candidateMu.Lock()
				peerM.remoteDescSet = true
				drainPeerCandidates(s.From)
				peerM.candidateMu.Unlock()
			}
			if desc.Type == webrtc.SDPTypeOffer {
				pc, err := createPeerConnection(ws, id, s.From)
				if err != nil {
					fmt.Println("create peer connection error:", err)
					continue
				}

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
				if err := sendWsMessage(ws, Signal{From: id, To: s.From, Data: string(ansBytes)}); err != nil {
					fmt.Println("Error sending answer:", err)
				}

				drainCandidateWrapper()
			} else if desc.Type == webrtc.SDPTypeAnswer {
				peerM, ok := peerConnectionMap[s.From]
				if !ok {
					fmt.Printf("Peer %s does not exist!\n", s.From)
					continue
				}
				if err := peerM.connection.SetRemoteDescription(desc); err != nil {
					fmt.Println("SetRemoteDescription error:", err)
					continue
				}
				drainCandidateWrapper()
				continue
			} else {
				fmt.Errorf("This type is not supported, only accepting the offer at the moment")
				continue
			}

		}

		// parser 3
		var cand webrtc.ICECandidateInit
		if err := json.Unmarshal([]byte(s.Data), &cand); err == nil {
			peerM, ok := peerConnectionMap[s.From]
			if !ok {
				fmt.Printf("Peer %s does not exist!\n", s.From)
				continue
			}
			peerM.candidateMu.Lock()
			if peerM.remoteDescSet {
				if err := peerM.connection.AddICECandidate(cand); err != nil {
					fmt.Println("AddICECandidate error:", err)
				}
			} else {
				fmt.Println("Append more candidates to the pending list")
				peerM.pendingCandidates = append(peerM.pendingCandidates, cand)
			}
			peerM.candidateMu.Unlock()
		}
	}

	return nil
}

func createPeerConnection(ws *websocket.Conn, id string, peer string) (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Fatalf("failed to create peer connection: %v", err)
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		data, err := json.Marshal(c.ToJSON())
		if err != nil {
			fmt.Println("Error marshalling ICE candidate:", err)
			return
		}
		if err := sendWsMessage(ws, Signal{From: id, To: peer, Data: string(data)}); err != nil {
			fmt.Println("Error sending ICE candidate:", err)
		}
	})

	// set the pc to the connection map
	peerConnectionMap[peer] = &Peer{
		connection:        pc,
		pendingCandidates: []webrtc.ICECandidateInit{},
		remoteDescSet:     false,
	}

	pc.OnDataChannel(func(remote *webrtc.DataChannel) {
		remote.OnMessage(func(msg webrtc.DataChannelMessage) {
			fmt.Println("peer:", string(msg.Data))
		})
		remote.OnOpen(func() {
			fmt.Println("--- Connection Established! You can now type messages ---")
		})
		if remote.ReadyState() == webrtc.DataChannelStateOpen {
			fmt.Println("--- Connection Established! You can now type messages ---")
		}
		peerConnectionMap[peer].dataChanel = remote
	})

	return pc, err
}

func drainPeerCandidates(peer string) {
	peerM, ok := peerConnectionMap[peer]
	if !ok {
		fmt.Printf("Peer %s does not exist!\n", peer)
		return
	}

	for _, cand := range peerM.pendingCandidates {
		fmt.Println("Drainnnn candidatessssss ", cand)
		if err := peerM.connection.AddICECandidate(cand); err != nil {
			fmt.Println("AddICECandidate error:", err)
		}
	}
	peerM.pendingCandidates = nil
}

func createDataChanel(ws *websocket.Conn, id string, peer string) (*webrtc.PeerConnection, *webrtc.DataChannel, []byte, error) {
	pc, err := createPeerConnection(ws, id, peer)
	if err != nil {
		log.Fatalf("failed to create peer connection: %v", err)
	}

	ch, err := pc.CreateDataChannel("chat", nil)
	if err != nil {
		logger.Errorf("failed to create data channel: %v", err)
		return nil, nil, nil, err
	}

	// events for data channels
	ch.OnMessage(func(msg webrtc.DataChannelMessage) {
		logger.Infof("peer: ", string(msg.Data))
	})
	ch.OnOpen(func() {
		logger.Infof("connection with peer %s is established! You can send some messages from now", peer)
	})

	peerConnectionMap[peer].dataChanel = ch

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		logger.Errorf("failed to create data channel: %v", err)
		return nil, nil, nil, err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		logger.Errorf("failed to set the local description for the peer connection: %v", err)
		return nil, nil, nil, err
	}
	offerBytes, err := json.Marshal(offer)
	if err != nil {
		logger.Errorf("failed to marshall offer data: %v", err)
		return nil, nil, nil, err
	}
	return pc, ch, offerBytes, err
}

func init() {
	internal.Init()

	ctx := context.Background()
	logger = internal.LoggerFromContext(ctx)
}

func main() {
	id := os.Args[1]
	server := os.Args[2]

	u := url.URL{Scheme: "ws", Host: server, Path: "/ws"}
	q := u.Query()
	q.Set("id", id)
	u.RawQuery = q.Encode()

	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		logger.Fatalf("Error while dialing to the Signaling websocket server")
	}

	peerConnectionMap = make(map[string]*Peer)
	go handlingWsMessages(ws, id)

	// Answerer: dc will be set when OnDataChannel fires.
	// Now sending data via p2p connection using webRTC
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()
		for peerName, peer := range peerConnectionMap {
			readyDc := peer.dataChanel
			if readyDc == nil {
				logger.Infof("connection (peer %s) is not ready yet!", peerName)
				continue
			}
			if err := readyDc.SendText(text); err != nil {
				logger.Errorf("Error while sending your message! %v", err)
			}
		}
	}
}
