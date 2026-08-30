package server

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"idlegrid/coordinator/internal/registry"
	"idlegrid/protocol"
)

type envelope = protocol.Envelope

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true }, // providers dial from any NAT
}

// Hub owns provider WebSocket connections: registration, heartbeats,
// outbound sends, and failure propagation.
type Hub struct {
	Reg    *registry.Registry
	Router *Router

	// JoinCode, when non-empty, must be presented by every provider at
	// registration (gates who can join the network).
	JoinCode string

	mu       sync.Mutex
	conns    map[string]chan envelope   // nodeID -> outbound queue
	inflight map[string]map[string]bool // nodeID -> requestID set
}

func NewHub(reg *registry.Registry, router *Router, joinCode string) *Hub {
	return &Hub{
		Reg:      reg,
		Router:   router,
		JoinCode: joinCode,
		conns:    make(map[string]chan envelope),
		inflight: make(map[string]map[string]bool),
	}
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// HandleWS is the http handler for GET /ws/provider.
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// First frame must be a register.
	var env envelope
	if err := conn.ReadJSON(&env); err != nil || env.Type != protocol.TypeRegister {
		conn.Close()
		return
	}
	var reg protocol.Register
	if err := env.Decode(&reg); err != nil {
		conn.Close()
		return
	}
	if h.JoinCode != "" && reg.JoinCode != h.JoinCode {
		denied, _ := protocol.New(protocol.TypeRegisterDenied, protocol.RegisterDenied{
			Reason: "invalid or missing join code",
		})
		_ = conn.WriteJSON(denied)
		log.Printf("[hub] registration denied (bad join code) from %s", conn.RemoteAddr())
		conn.Close()
		return
	}
	if reg.NodeID == "" {
		reg.NodeID = newID()
	}
	nodeID := reg.NodeID

	h.Reg.Register(nodeID, reg.Name, reg.Chip, reg.Version, reg.MemoryGB, reg.Models)
	log.Printf("[hub] provider registered: id=%s name=%q chip=%s mem=%dGB models=%v",
		nodeID, reg.Name, reg.Chip, reg.MemoryGB, reg.Models)

	ok, _ := protocol.New(protocol.TypeRegisterOK, protocol.RegisterOK{HeartbeatIntervalSecs: 5})
	_ = conn.WriteJSON(ok)

	out := make(chan envelope, 256)
	h.mu.Lock()
	h.conns[nodeID] = out
	h.inflight[nodeID] = make(map[string]bool)
	h.mu.Unlock()
	defer h.disconnect(nodeID)

	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-r.Context().Done():
			// client vanished without a WS close frame
			_ = conn.Close()
		}
	}()
	defer close(done)

	go h.writePump(conn, out)
	h.readLoop(conn, nodeID)
}

func (h *Hub) writePump(conn *websocket.Conn, out chan envelope) {
	for env := range out {
		if err := conn.WriteJSON(env); err != nil {
			log.Printf("[hub] writePump error: %v", err)
			_ = conn.Close()
			return
		}
	}
}

func (h *Hub) readLoop(conn *websocket.Conn, nodeID string) {
	for {
		var env envelope
		if err := conn.ReadJSON(&env); err != nil {
			log.Printf("[hub] provider %s disconnected: %v", nodeID, err)
			return
		}
		switch env.Type {
		case protocol.TypeHeartbeat:
			var hb protocol.Heartbeat
			if err := env.Decode(&hb); err == nil {
				h.Reg.Heartbeat(nodeID, hb.FreeMemoryGB, hb.QueueDepth)
			}
		case protocol.TypeInferenceAccepted, protocol.TypeInferenceChunk:
			var req protocol.InferenceAccepted // same shape: {request_id}
			if err := env.Decode(&req); err == nil {
				h.Router.Deliver(req.RequestID, env)
			}
		case protocol.TypeInferenceComplete, protocol.TypeInferenceError:
			var done protocol.InferenceComplete // superset shape
			if err := env.Decode(&done); err == nil {
				h.clearInflight(nodeID, done.RequestID)
				h.Router.Deliver(done.RequestID, env)
			}
		default:
			// unknown types are ignored for forward compatibility
		}
	}
}

func (h *Hub) disconnect(nodeID string) {
	h.Reg.Unregister(nodeID)
	log.Printf("[hub] provider removed: id=%s", nodeID)

	h.mu.Lock()
	out, ok := h.conns[nodeID]
	delete(h.conns, nodeID)
	var orphans []string
	for reqID := range h.inflight[nodeID] {
		orphans = append(orphans, reqID)
	}
	delete(h.inflight, nodeID)
	h.mu.Unlock()

	if ok {
		close(out) // writePump exits and closes the conn
	}
	// Fail every request this node was working on so gateway callers get an
	// error instead of hanging.
	for _, reqID := range orphans {
		errEnv, _ := protocol.New(protocol.TypeInferenceError, protocol.InferenceError{
			RequestID: reqID, Error: "provider disconnected mid-request",
		})
		h.Router.Deliver(reqID, errEnv)
	}
}

// Send queues an envelope to a node. Returns false if the node has no live
// connection. Tracks in-flight request IDs for failure propagation.
func (h *Hub) Send(nodeID string, env envelope) bool {
	h.mu.Lock()
	out, ok := h.conns[nodeID]
	if ok && env.Type == protocol.TypeInferenceRequest {
		var req protocol.InferenceRequest
		if err := env.Decode(&req); err == nil {
			h.inflight[nodeID][req.RequestID] = true
		}
	}
	h.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case out <- env:
		return true
	default:
		return false // outbound queue full: treat as down
	}
}

func (h *Hub) clearInflight(nodeID, reqID string) {
	h.mu.Lock()
	delete(h.inflight[nodeID], reqID)
	h.mu.Unlock()
}

// CloseNode force-drops a node's connection (used by the stale sweep).
func (h *Hub) CloseNode(nodeID string) {
	h.mu.Lock()
	out, ok := h.conns[nodeID]
	h.mu.Unlock()
	if ok {
		close(out)
	}
}

// StartSweeper drops heartbeat-stale nodes every interval.
func (h *Hub) StartSweeper(interval time.Duration) chan struct{} {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				for _, id := range h.Reg.Sweep() {
					log.Printf("[hub] sweeping stale provider: id=%s", id)
					h.CloseNode(id)
				}
			}
		}
	}()
	return stop
}
