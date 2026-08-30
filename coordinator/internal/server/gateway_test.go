package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"idlegrid/coordinator/internal/registry"
	"idlegrid/protocol"
)

// fakeWSProvider connects like a real provider and serves N tokens per
// request. Returns the raw WS conn so tests can drive disconnects.
func fakeWSProvider(t *testing.T, url, nodeID string, tokens int, delay time.Duration) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	reg, _ := protocol.New(protocol.TypeRegister, protocol.Register{
		NodeID: nodeID, Name: nodeID, Chip: "Test", MemoryGB: 16,
		Models: []string{"test-model"}, Version: "test",
	})
	if err := conn.WriteJSON(reg); err != nil {
		t.Fatalf("register: %v", err)
	}
	var ok protocol.Envelope
	if err := conn.ReadJSON(&ok); err != nil || ok.Type != protocol.TypeRegisterOK {
		t.Fatalf("register_ok: %v %v", ok.Type, err)
	}

	go func() {
		for {
			var env protocol.Envelope
			if err := conn.ReadJSON(&env); err != nil {
				return
			}
			if env.Type != protocol.TypeInferenceRequest {
				continue
			}
			var req protocol.InferenceRequest
			if err := env.Decode(&req); err != nil {
				continue
			}
			acc, _ := protocol.New(protocol.TypeInferenceAccepted, protocol.InferenceAccepted{RequestID: req.RequestID})
			_ = conn.WriteJSON(acc)
			for i := 0; i < tokens; i++ {
				time.Sleep(delay)
				chunk, _ := protocol.New(protocol.TypeInferenceChunk, protocol.InferenceChunk{
					RequestID: req.RequestID, Delta: "t" + string(rune('a'+i)) + " ",
				})
				_ = conn.WriteJSON(chunk)
			}
			done, _ := protocol.New(protocol.TypeInferenceComplete, protocol.InferenceComplete{
				RequestID: req.RequestID,
				Usage:     protocol.Usage{PromptTokens: 5, CompletionTokens: tokens},
			})
			_ = conn.WriteJSON(done)
		}
	}()
	return conn
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	reg := registry.New()
	mux := NewHandler(reg, Config{APIKeys: []string{"test-key"}})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// Provider-side handshake helper: dial + register, then return the first
// envelope the coordinator sends back (register_ok or register_denied).
func dialAndRegister(t *testing.T, url, nodeID, code string) protocol.Envelope {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	reg, _ := protocol.New(protocol.TypeRegister, protocol.Register{
		NodeID: nodeID, Name: nodeID, Chip: "T", MemoryGB: 8,
		Models: []string{"m"}, Version: "t", JoinCode: code,
	})
	if err := conn.WriteJSON(reg); err != nil {
		t.Fatalf("register: %v", err)
	}
	var env protocol.Envelope
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatalf("read: %v", err)
	}
	return env
}

func TestJoinCodeAccepted(t *testing.T) {
	reg := registry.New()
	mux := NewHandler(reg, Config{APIKeys: []string{"k"}, JoinCode: "secret-1"})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	env := dialAndRegister(t, wsURL(srv.URL), "good-node", "secret-1")
	if env.Type != protocol.TypeRegisterOK {
		t.Fatalf("want register_ok, got %s", env.Type)
	}
}

func TestJoinCodeRejected(t *testing.T) {
	reg := registry.New()
	mux := NewHandler(reg, Config{APIKeys: []string{"k"}, JoinCode: "secret-1"})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, code := range []string{"", "wrong"} {
		env := dialAndRegister(t, wsURL(srv.URL), "bad-node", code)
		if env.Type != protocol.TypeRegisterDenied {
			t.Fatalf("code %q: want register_denied, got %s", code, env.Type)
		}
		var denied protocol.RegisterDenied
		if err := env.Decode(&denied); err != nil || denied.Reason == "" {
			t.Fatalf("denied should carry a reason: %v", err)
		}
	}
	if len(reg.Snapshot()) != 0 {
		t.Fatal("denied nodes must not enter the registry")
	}
}

func chatPost(srvURL, body string) (*http.Response, []byte, error) {
	resp, err := http.Post(srvURL+"/v1/chat/completions", "application/json",
		bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp, buf.Bytes(), nil
}

func TestAuthRejectsBadKey(t *testing.T) {
	srv := newTestServer(t)
	resp, _, err := chatPost(srv.URL, `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestNoProviderReturns503(t *testing.T) {
	srv := newTestServer(t)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
}

func TestNonStreamingEndToEnd(t *testing.T) {
	srv := newTestServer(t)
	fakeWSProvider(t, wsURL(srv.URL), "node-1", 3, 5*time.Millisecond)

	resp, body, err := chatPostAuthed(srv.URL, `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	var cr completionResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(cr.Choices) != 1 || cr.Choices[0].Message == nil {
		t.Fatalf("bad choices: %s", body)
	}
	if got := cr.Choices[0].Message.Content; got != "ta tb tc " {
		t.Fatalf("want %q, got %q", "ta tb tc ", got)
	}
	if cr.Usage == nil || cr.Usage.CompletionTokens != 3 {
		t.Fatalf("usage not propagated: %s", body)
	}
}

func TestStreamingEndToEnd(t *testing.T) {
	srv := newTestServer(t)
	fakeWSProvider(t, wsURL(srv.URL), "node-1", 3, 5*time.Millisecond)

	req, _ := http.NewRequest("POST", srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("want SSE, got %s", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	dataLines, sawDone := 0, false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			dataLines++
			if line == "data: [DONE]" {
				sawDone = true
			}
		}
	}
	// role chunk + 3 content chunks + finish chunk + [DONE] = 6 data lines
	if dataLines != 6 || !sawDone {
		t.Fatalf("want 6 data lines + [DONE], got %d done=%v", dataLines, sawDone)
	}
}

func TestProviderDisconnectFailsInFlight(t *testing.T) {
	srv := newTestServer(t)
	conn := fakeWSProvider(t, wsURL(srv.URL), "node-1", 100, 50*time.Millisecond) // slow

	var wg sync.WaitGroup
	statusCh := make(chan int, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, _, err := chatPostAuthed(srv.URL, `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)
		if err != nil {
			statusCh <- 0
			return
		}
		statusCh <- resp.StatusCode
	}()

	time.Sleep(50 * time.Millisecond) // let the request dispatch
	conn.Close()                      // provider dies mid-stream

	wg.Wait()
	select {
	case code := <-statusCh:
		if code != http.StatusBadGateway {
			t.Fatalf("want 502 after provider death, got %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request hung after provider disconnect")
	}
}

func chatPostAuthed(srvURL, body string) (*http.Response, []byte, error) {
	req, _ := http.NewRequest("POST", srvURL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp, buf.Bytes(), nil
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + "/ws/provider"
}
