package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"idlegrid/coordinator/internal/registry"
	"idlegrid/protocol"
)

// Gateway is the developer-facing, OpenAI-compatible HTTP API.
type Gateway struct {
	Reg     *registry.Registry
	Hub     *Hub
	Router  *Router
	APIKeys map[string]bool
}

func NewGateway(reg *registry.Registry, hub *Hub, router *Router, apiKeys []string) *Gateway {
	keys := make(map[string]bool, len(apiKeys))
	for _, k := range apiKeys {
		keys[k] = true
	}
	return &Gateway{Reg: reg, Hub: hub, Router: router, APIKeys: keys}
}

// ---- OpenAI wire shapes ----

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	MaxTokens *int          `json:"max_tokens,omitempty"` // pointer: omit when unset (0 means "1 token" to llama.cpp)
}

type delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type choice struct {
	Index        int     `json:"index"`
	Delta        *delta  `json:"delta,omitempty"`
	Message      *delta  `json:"message,omitempty"`
	FinishReason *string `json:"finish_reason"`
}

type usageStats struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type completionResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []choice    `json:"choices"`
	Usage   *usageStats `json:"usage,omitempty"`
}

func openAIError(w http.ResponseWriter, status int, msg, typ, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": msg, "type": typ, "code": code},
	})
}

// ---- handlers ----

func (g *Gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	models := g.Reg.OnlineModels()
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]any{"id": m, "object": "model", "owned_by": "idlegrid"})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": out})
}

func (g *Gateway) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		openAIError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error", "")
		return
	}
	if req.Model == "" || len(req.Messages) == 0 {
		openAIError(w, http.StatusBadRequest, "model and messages are required", "invalid_request_error", "")
		return
	}

	estTokens := 512
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		estTokens = *req.MaxTokens
	}
	if estTokens > 4096 {
		estTokens = 4096
	}

	// 1. Schedule: pick the least-loaded provider with the model resident
	// and atomically reserve capacity.
	node, err := g.Reg.Reserve(req.Model, estTokens)
	if err != nil {
		openAIError(w, http.StatusServiceUnavailable,
			"no provider available for model "+req.Model, "server_error", "no_provider")
		return
	}
	failed := false
	defer func() { g.Reg.Release(node.ID, estTokens, failed) }()

	// 2. Dispatch over the provider's WebSocket.
	reqID := "req-" + newID()
	body, _ := json.Marshal(req)
	env, _ := protocol.New(protocol.TypeInferenceRequest, protocol.InferenceRequest{
		RequestID: reqID,
		Model:     req.Model,
		Stream:    req.Stream,
		Body:      body,
	})
	// Create the event channel BEFORE dispatch: if the provider dies right
	// after Send, the hub's failure event must have a channel to land on.
	events := g.Router.Create(reqID)
	defer g.Router.Resolve(reqID)

	if !g.Hub.Send(node.ID, env) {
		failed = true
		openAIError(w, http.StatusBadGateway, "provider connection lost before dispatch", "server_error", "provider_down")
		return
	}
	log.Printf("[gateway] request %s dispatched to %s (model=%s stream=%v)", reqID, node.ID, req.Model, req.Stream)

	if req.Stream {
		failed = g.streamResponse(w, r, reqID, req.Model, node.ID, events)
		return
	}
	failed = g.collectResponse(w, r, reqID, req.Model, node.ID, events)
}

// collectResponse handles non-streaming: buffer chunks, return one JSON body.
// Returns true if the request failed (drives scheduler cooldown).
func (g *Gateway) collectResponse(w http.ResponseWriter, r *http.Request, reqID, model, nodeID string, events <-chan envelope) bool {
	var sb strings.Builder
	var usage protocol.Usage
	timeout := time.After(120 * time.Second)
	for {
		select {
		case env := <-events:
			switch env.Type {
			case protocol.TypeInferenceChunk:
				var c protocol.InferenceChunk
				if err := env.Decode(&c); err == nil {
					sb.WriteString(c.Delta)
				}
			case protocol.TypeInferenceComplete:
				var done protocol.InferenceComplete
				if err := env.Decode(&done); err == nil {
					usage = done.Usage
				}
				if usage.CompletionTokens == 0 {
					usage.CompletionTokens = len(sb.String()) / 4
				}
				finish := "stop"
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(completionResponse{
					ID: "chatcmpl-" + reqID, Object: "chat.completion",
					Created: time.Now().Unix(), Model: model,
					Choices: []choice{{
						Index: 0, Message: &delta{Role: "assistant", Content: sb.String()},
						FinishReason: &finish,
					}},
					Usage: &usageStats{
						PromptTokens:     usage.PromptTokens,
						CompletionTokens: usage.CompletionTokens,
						TotalTokens:      usage.PromptTokens + usage.CompletionTokens,
					},
				})
				return false
			case protocol.TypeInferenceError:
				var e protocol.InferenceError
				_ = env.Decode(&e)
				openAIError(w, http.StatusBadGateway, e.Error, "server_error", "provider_error")
				return true
			}
		case <-timeout:
			g.Hub.Send(nodeID, mustEnvelope(protocol.TypeCancel, protocol.Cancel{RequestID: reqID}))
			openAIError(w, http.StatusGatewayTimeout, "inference timed out", "server_error", "timeout")
			return true
		case <-r.Context().Done():
			g.Hub.Send(nodeID, mustEnvelope(protocol.TypeCancel, protocol.Cancel{RequestID: reqID}))
			return true
		}
	}
}

// streamResponse handles streaming: relay chunks as OpenAI SSE events.
// Returns true if the request failed.
func (g *Gateway) streamResponse(w http.ResponseWriter, r *http.Request, reqID, model, nodeID string, events <-chan envelope) bool {
	flusher, ok := w.(http.Flusher)
	if !ok {
		openAIError(w, http.StatusInternalServerError, "streaming unsupported", "server_error", "")
		return true
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	id := "chatcmpl-" + reqID
	writeChunk := func(d delta, finish *string) {
		fmt.Fprint(w, "data: ")
		json.NewEncoder(w).Encode(completionResponse{
			ID: id, Object: "chat.completion.chunk",
			Created: time.Now().Unix(), Model: model,
			Choices: []choice{{Index: 0, Delta: &d, FinishReason: finish}},
		})
		fmt.Fprint(w, "\n") // SSE event delimiter (Encode already ends the line)
		flusher.Flush()
	}

	writeChunk(delta{Role: "assistant"}, nil)

	stop := "stop"
	timeout := time.After(120 * time.Second)
	for {
		select {
		case env := <-events:
			switch env.Type {
			case protocol.TypeInferenceChunk:
				var c protocol.InferenceChunk
				if err := env.Decode(&c); err == nil && c.Delta != "" {
					writeChunk(delta{Content: c.Delta}, nil)
				}
			case protocol.TypeInferenceComplete:
				writeChunk(delta{}, &stop)
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
				return false
			case protocol.TypeInferenceError:
				var e protocol.InferenceError
				_ = env.Decode(&e)
				// Mid-stream we can't change the status code; send an
				// OpenAI-style error event then close.
				fmt.Fprintf(w, "data: {\"error\":{\"message\":%q,\"type\":\"server_error\"}}\n\n", e.Error)
				flusher.Flush()
				return true
			}
		case <-timeout:
			g.Hub.Send(nodeID, mustEnvelope(protocol.TypeCancel, protocol.Cancel{RequestID: reqID}))
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return true
		case <-r.Context().Done():
			g.Hub.Send(nodeID, mustEnvelope(protocol.TypeCancel, protocol.Cancel{RequestID: reqID}))
			return true
		}
	}
}

func mustEnvelope(t string, payload any) envelope {
	env, err := protocol.New(t, payload)
	if err != nil {
		panic(err)
	}
	return env
}

// withAuth enforces bearer keys from IDLEGRID_API_KEYS.
func (g *Gateway) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || !g.APIKeys[strings.TrimPrefix(auth, "Bearer ")] {
			openAIError(w, http.StatusUnauthorized, "invalid API key", "auth_error", "invalid_api_key")
			return
		}
		next(w, r)
	}
}
