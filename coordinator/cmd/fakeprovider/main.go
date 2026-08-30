// fakeprovider speaks the provider protocol but generates canned tokens.
// Use it to load-test the coordinator scheduler with dozens of simulated
// Macs without owning them (Darkbloom's e2e/testbed approach).
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"idlegrid/coordinator/internal/e2e"
	"idlegrid/protocol"
)

func main() {
	coordURL := flag.String("coordinator", "ws://localhost:8090/ws/provider", "coordinator WS URL")
	models := flag.String("models", "qwen2.5-0.5b-instruct", "comma-separated models to advertise")
	count := flag.Int("count", 8, "number of fake nodes to run (0 = 1)")
	tokens := flag.Int("tokens", 24, "tokens per fake completion")
	delayMS := flag.Int("delay-ms", 30, "simulated decode delay per token")
	code := flag.String("code", "", "provider join code (if coordinator requires one)")
	enroll := flag.String("enroll", "", "enrollment code to bind this node to your account")
	flag.Parse()

	if *count <= 0 {
		*count = 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	for i := 0; i < *count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runNode(ctx, fmt.Sprintf("fake-%02d", i), *coordURL, splitModels(*models), *tokens, *delayMS, *code, *enroll)
		}(i)
	}
	wg.Wait()
	log.Printf("[fakeprovider] all nodes stopped")
}

func splitModels(s string) []string {
	var out []string
	for _, m := range splitComma(s) {
		out = append(out, m)
	}
	return out
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

type fakeNode struct {
	mu      sync.Mutex
	queue   int
	nodeID  string
	cancels map[string]context.CancelFunc
	x25519  e2e.XKeyPair
	ed25519 e2e.EdKeyPair
}

func runNode(ctx context.Context, name, coordURL string, models []string, tokens, delayMS int, code, enroll string) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := runOnce(ctx, name, coordURL, models, tokens, delayMS, code, enroll); err != nil {
			log.Printf("[%s] disconnected: %v (retrying in %s)", name, err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func runOnce(ctx context.Context, name, coordURL string, models []string, tokens, delayMS int, code, enroll string) error {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, coordURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	node := &fakeNode{cancels: make(map[string]context.CancelFunc)}
	node.nodeID = fmt.Sprintf("%s-%04x", name, rand.Intn(0xffff))
	node.x25519 = e2e.GenerateX25519()
	node.ed25519 = e2e.GenerateEd25519()

	reg, _ := protocol.New(protocol.TypeRegister, protocol.Register{
		NodeID:         node.nodeID,
		Name:           name,
		Chip:           "Simulated M9 Ultra",
		MemoryGB:       256,
		Models:         models,
		Version:        "v0.4.0-fake",
		JoinCode:       code,
		EnrollmentCode: enroll,
		PublicKey:      e2e.PubKeyToB64(&node.x25519.Pub),
		SigningKey:     base64.StdEncoding.EncodeToString(node.ed25519.Pub),
	})
	if err := conn.WriteJSON(reg); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	// read register_ok (or register_denied = permanent)
	var env protocol.Envelope
	if err := conn.ReadJSON(&env); err != nil {
		return fmt.Errorf("register: %v", err)
	}
	if env.Type == protocol.TypeRegisterDenied {
		var denied protocol.RegisterDenied
		_ = env.Decode(&denied)
		return fmt.Errorf("REGISTRATION DENIED: %s (check -code flag)", denied.Reason)
	}
	if env.Type != protocol.TypeRegisterOK {
		return fmt.Errorf("expected register_ok, got %s", env.Type)
	}
	log.Printf("[%s] registered as %s (models=%v)", name, node.nodeID, models)

	// heartbeat loop
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				hb, _ := protocol.New(protocol.TypeHeartbeat, protocol.Heartbeat{
					NodeID: node.nodeID, FreeMemoryGB: 100, QueueDepth: node.snapshotQueue(),
				})
				if err := conn.WriteJSON(hb); err != nil {
					return
				}
			}
		}
	}()

	// read loop: serve inference forever
	for {
		var env protocol.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return fmt.Errorf("read: %w", err)
		}
		switch env.Type {
		case protocol.TypeInferenceRequest:
			var req protocol.InferenceRequest
			if err := env.Decode(&req); err != nil {
				continue
			}
			go node.serve(conn, req, tokens, delayMS)
		case protocol.TypeCancel:
			var c protocol.Cancel
			if err := env.Decode(&c); err == nil {
				node.mu.Lock()
				if cancel, ok := node.cancels[c.RequestID]; ok {
					cancel()
					delete(node.cancels, c.RequestID)
				}
				node.mu.Unlock()
			}
		}
	}
}

func (n *fakeNode) snapshotQueue() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.queue
}

func (n *fakeNode) serve(conn *websocket.Conn, req protocol.InferenceRequest, tokens, delayMS int) {
	genCtx, cancel := context.WithCancel(context.Background())
	n.mu.Lock()
	n.cancels[req.RequestID] = cancel
	n.queue++
	n.mu.Unlock()
	defer func() {
		cancel()
		n.mu.Lock()
		delete(n.cancels, req.RequestID)
		n.queue--
		n.mu.Unlock()
	}()

	accepted, _ := protocol.New(protocol.TypeInferenceAccepted, protocol.InferenceAccepted{RequestID: req.RequestID})
	if err := conn.WriteJSON(accepted); err != nil {
		return
	}

	// E2E mode: decrypt the body, seal every response payload to the
	// gateway's response key, sign the usage report.
	e2eMode := req.Encrypted != nil && req.ResponseKey != ""
	var respKey e2e.XKeyPair
	var bodyText string
	if e2eMode {
		var ephPub [32]byte
		if raw, err := base64.StdEncoding.DecodeString(req.Encrypted.EphPub); err == nil && len(raw) == 32 {
			copy(ephPub[:], raw)
		}
		inner, err := e2e.Open(req.Encrypted.EphPub, req.Encrypted.Nonce, req.Encrypted.Ciphertext, &n.x25519.Priv)
		if err != nil {
			e2eSend(conn, req.RequestID, req.ResponseKey, &respKey, map[string]string{"error": "decrypt failed: " + err.Error()}, protocol.TypeInferenceError)
			return
		}
		bodyText = string(inner)
		respKey = e2e.GenerateX25519() // response leg ephemeral
	}
	_ = bodyText

	promptTokens := 0
	if req.Body != nil {
		promptTokens = len(req.Body) / 4
	} else if e2eMode {
		promptTokens = len(bodyText) / 4
	}
	for i := 0; i < tokens; i++ {
		select {
		case <-genCtx.Done():
			if e2eMode {
				e2eSend(conn, req.RequestID, req.ResponseKey, &respKey, map[string]string{"error": "cancelled"}, protocol.TypeInferenceError)
			} else {
				errEnv, _ := protocol.New(protocol.TypeInferenceError, protocol.InferenceError{
					RequestID: req.RequestID, Error: "cancelled",
				})
				_ = conn.WriteJSON(errEnv)
			}
			return
		case <-time.After(time.Duration(delayMS) * time.Millisecond):
		}
		delta := fmt.Sprintf("tok%d ", i)
		if e2eMode {
			if err := e2eSend(conn, req.RequestID, req.ResponseKey, &respKey, map[string]string{"delta": delta}, protocol.TypeInferenceChunk); err != nil {
				return
			}
			continue
		}
		chunk, _ := protocol.New(protocol.TypeInferenceChunk, protocol.InferenceChunk{
			RequestID: req.RequestID,
			Delta:     delta,
		})
		if err := conn.WriteJSON(chunk); err != nil {
			return
		}
	}
	if e2eMode {
		// inner matches the Go InferenceComplete shape: {"usage":{...}}
		inner, _ := json.Marshal(map[string]any{
			"usage": map[string]int{"prompt_tokens": promptTokens, "completion_tokens": tokens},
		})
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(n.ed25519.Priv, inner))
		if err := e2eSignedSend(conn, req.RequestID, req.ResponseKey, &respKey, inner, sig, nil, protocol.TypeInferenceComplete); err != nil {
			return
		}
		return
	}
	complete, _ := protocol.New(protocol.TypeInferenceComplete, protocol.InferenceComplete{
		RequestID: req.RequestID,
		Usage:     protocol.Usage{PromptTokens: promptTokens, CompletionTokens: tokens},
	})
	_ = conn.WriteJSON(complete)
}

// e2eSend seals an inner JSON payload to the gateway response key and
// sends it as an encrypted envelope on the provider connection.
func e2eSend(conn *websocket.Conn, requestID, responseKeyB64 string, respKey *e2e.XKeyPair, inner any, msgType string) error {
	return e2eSignedSend(conn, requestID, responseKeyB64, respKey, nil, "", inner, msgType)
}

func e2eSignedSend(conn *websocket.Conn, requestID, responseKeyB64 string, respKey *e2e.XKeyPair, signedInner []byte, sig string, inner any, msgType string) error {
	pub, err := base64.StdEncoding.DecodeString(responseKeyB64)
	if err != nil || len(pub) != 32 {
		return fmt.Errorf("bad response key")
	}
	var recipient [32]byte
	copy(recipient[:], pub)
	payload := inner
	if signedInner != nil {
		payload = json.RawMessage(signedInner)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ephPub, nonce, ct, err := e2e.Seal(data, &recipient)
	if err != nil {
		return err
	}
	env, err := protocol.New(msgType, protocol.InferenceChunk{
		RequestID: requestID,
		Encrypted: &protocol.SealedPayload{EphPub: ephPub, Nonce: nonce, Ciphertext: ct},
	})
	if err != nil {
		return err
	}
	return conn.WriteJSON(env)
}
