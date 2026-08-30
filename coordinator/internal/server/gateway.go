package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"idlegrid/coordinator/internal/registry"
	"idlegrid/coordinator/store"
	"idlegrid/protocol"
)

// Gateway is the developer-facing, OpenAI-compatible HTTP API.
type Gateway struct {
	Reg                *registry.Registry
	Hub                *Hub
	Router             *Router
	APIKeys            map[string]bool // env keys = admin
	Billing            store.Billing   // nil in dev (no DATABASE_URL): env keys only, no metering
	PlatformFeePercent int
	RequireBalance     bool // developers need a non-negative balance (default on when billing active)
}

func NewGateway(reg *registry.Registry, hub *Hub, router *Router, apiKeys []string, billing store.Billing, feePercent int, requireBalance bool) *Gateway {
	keys := make(map[string]bool, len(apiKeys))
	for _, k := range apiKeys {
		keys[k] = true
	}
	if feePercent < 0 || feePercent > 100 {
		feePercent = 10
	}
	return &Gateway{Reg: reg, Hub: hub, Router: router, APIKeys: keys, Billing: billing, PlatformFeePercent: feePercent, RequireBalance: requireBalance}
}

// ---- request context ----

type ctxKey int

const (
	ctxUserID ctxKey = iota
	ctxIsAdmin
)

func userIDFrom(ctx context.Context) *int64 {
	if v, ok := ctx.Value(ctxUserID).(*int64); ok {
		return v
	}
	return nil
}

func isAdminFrom(ctx context.Context) bool {
	v, _ := ctx.Value(ctxIsAdmin).(bool)
	return v
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
	MaxTokens *int          `json:"max_tokens,omitempty"` // pointer: omit when unset
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

// handleDebugProviders backs the dashboard fleet view (bearer-authenticated).
func (g *Gateway) handleDebugProviders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(g.Reg.Snapshot())
}

// handleCreateUser is the admin bootstrap: create a developer + first key.
func (g *Gateway) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !isAdminFrom(r.Context()) {
		openAIError(w, http.StatusForbidden, "admin key required", "auth_error", "admin_only")
		return
	}
	if g.Billing == nil {
		openAIError(w, http.StatusServiceUnavailable, "billing store unavailable (set DATABASE_URL)", "server_error", "no_db")
		return
	}
	var body struct {
		Email string `json:"email"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.Contains(body.Email, "@") {
		openAIError(w, http.StatusBadRequest, `body must be {"email":"...","label":"..."}`, "invalid_request_error", "")
		return
	}
	uid, err := g.Billing.EnsureUser(r.Context(), body.Email, "developer")
	if err != nil {
		openAIError(w, http.StatusInternalServerError, err.Error(), "server_error", "")
		return
	}
	raw := "sk-ig-" + newID() + newID() // 48 hex chars; shown exactly once
	if err := g.Billing.CreateAPIKey(r.Context(), uid, store.HashKey(raw), body.Label); err != nil {
		openAIError(w, http.StatusInternalServerError, err.Error(), "server_error", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"user_id": uid, "email": body.Email, "api_key": raw})
}

// reqOutcome accumulates billing-relevant facts while a request runs.
type reqOutcome struct {
	text    strings.Builder
	status  string // completed | failed | cancelled | timeout
	estIn   int
	provIn  *int
	provOut *int
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

	// Input estimate for metering (independent second opinion).
	inputChars := 0
	for _, m := range req.Messages {
		inputChars += len(m.Content)
	}
	outcome := &reqOutcome{status: "completed", estIn: store.EstimateTokens(strings.Repeat("x", inputChars))}

	// 0. Balance guard: prepaid model — developers with a negative balance
	// are blocked until they top up. Admin keys bypass.
	if g.RequireBalance && g.Billing != nil {
		if uid := userIDFrom(r.Context()); uid != nil {
			if bal, err := g.Billing.AccountBalance(r.Context(), uid, "developer_balance"); err == nil && bal < -store.MinChargeMicro {
				openAIError(w, http.StatusPaymentRequired,
					"insufficient balance — top up at https://console.sqlguroo.com", "insufficient_quota", "insufficient_balance")
				return
			}
		}
	}

	// 1. Schedule: pick the least-loaded provider with the model resident
	// and atomically reserve capacity.
	node, err := g.Reg.Reserve(req.Model, estTokens)
	if err != nil {
		openAIError(w, http.StatusServiceUnavailable,
			"no provider available for model "+req.Model, "server_error", "no_provider")
		return
	}

	// Create the event channel BEFORE dispatch: if the provider dies right
	// after Send, the hub's failure event must have a channel to land on.
	reqID := "req-" + newID()
	events := g.Router.Create(reqID)
	defer g.Router.Resolve(reqID)

	body, _ := json.Marshal(req)
	env, _ := protocol.New(protocol.TypeInferenceRequest, protocol.InferenceRequest{
		RequestID: reqID,
		Model:     req.Model,
		Stream:    req.Stream,
		Body:      body,
	})
	if !g.Hub.Send(node.ID, env) {
		g.finishUsage(r, reqID, req.Model, node.ID, inputChars, outcome, true)
		openAIError(w, http.StatusBadGateway, "provider connection lost before dispatch", "server_error", "provider_down")
		return
	}
	log.Printf("[gateway] request %s dispatched to %s (model=%s stream=%v)", reqID, node.ID, req.Model, req.Stream)

	if req.Stream {
		failed := g.streamResponse(w, r, reqID, req.Model, node.ID, events, outcome)
		g.finishUsage(r, reqID, req.Model, node.ID, inputChars, outcome, failed)
		return
	}
	failed := g.collectResponse(w, r, reqID, req.Model, node.ID, events, outcome)
	g.finishUsage(r, reqID, req.Model, node.ID, inputChars, outcome, failed)
}

// finishUsage writes the metering row when billing is configured.
func (g *Gateway) finishUsage(r *http.Request, reqID, model, nodeID string, inputChars int, o *reqOutcome, failed bool) {
	if g.Billing == nil {
		return
	}
	if failed && o.status == "completed" {
		o.status = "failed"
	}
	estIn := store.EstimateTokens(strings.Repeat("x", inputChars))
	estOut := store.EstimateTokens(o.text.String())
	ev := store.UsageEvent{
		RequestID:       reqID,
		UserID:          userIDFrom(r.Context()),
		NodeID:          nodeID,
		Model:           model,
		EstInputTokens:  estIn,
		EstOutputTokens: estOut,
		ProviderInput:   o.provIn,
		ProviderOutput:  o.provOut,
		WithinTolerance: store.CountsMatch(estOut, derefInt(o.provOut)),
		Status:          o.status,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := g.Billing.RecordUsage(ctx, ev); err != nil {
		log.Printf("[gateway] RecordUsage %s: %v", reqID, err)
		return
	}

	// Settle: provider counts win when inside tolerance, else the estimate.
	in, out := estIn, estOut
	if o.provIn != nil && store.CountsMatch(estIn, *o.provIn) {
		in = *o.provIn
	}
	if o.provOut != nil && store.CountsMatch(estOut, *o.provOut) {
		out = *o.provOut
	}
	inRate, outRate, err := store.PriceFor(ctx, g.Billing, model)
	if err != nil {
		log.Printf("[gateway] PriceFor %s: %v", model, err)
		return
	}
	gross := store.GrossMicro(int64(in), int64(out), inRate, outRate)
	if gross == 0 {
		return // nothing generated (e.g. failed before first token): free
	}
	credit, fee := store.SplitProviderFee(gross, g.PlatformFeePercent)
	res, err := g.Billing.SettleRequest(ctx, store.SettleParams{
		RequestID:    reqID,
		UserID:       userIDFrom(r.Context()),
		NodeID:       nodeID,
		Model:        model,
		InputTokens:  in,
		OutputTokens: out,
	}, gross, credit, fee)
	if err != nil {
		log.Printf("[gateway] SettleRequest %s: %v", reqID, err)
		return
	}
	if !res.AlreadySettled {
		log.Printf("[gateway] settled %s: gross=%d micro (provider %d / platform %d, %d/%d tok)",
			reqID, res.Gross, res.ProviderCredit, res.PlatformFee, in, out)
	}
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// collectResponse handles non-streaming: buffer chunks, return one JSON body.
// Returns true if the request failed (drives scheduler cooldown).
func (g *Gateway) collectResponse(w http.ResponseWriter, r *http.Request, reqID, model, nodeID string, events <-chan envelope, o *reqOutcome) bool {
	timeout := time.After(120 * time.Second)
	for {
		select {
		case env := <-events:
			switch env.Type {
			case protocol.TypeInferenceChunk:
				var c protocol.InferenceChunk
				if err := env.Decode(&c); err == nil {
					o.text.WriteString(c.Delta)
				}
			case protocol.TypeInferenceComplete:
				var done protocol.InferenceComplete
				pIn, pOut := 0, 0
				if err := env.Decode(&done); err == nil {
					pIn, pOut = done.Usage.PromptTokens, done.Usage.CompletionTokens
					o.provIn = &done.Usage.PromptTokens
					o.provOut = &done.Usage.CompletionTokens
				}
				finish := "stop"
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(completionResponse{
					ID: "chatcmpl-" + reqID, Object: "chat.completion",
					Created: time.Now().Unix(), Model: model,
					Choices: []choice{{
						Index: 0, Message: &delta{Role: "assistant", Content: o.text.String()},
						FinishReason: &finish,
					}},
					Usage: &usageStats{
						PromptTokens:     pIn,
						CompletionTokens: pOut,
						TotalTokens:      pIn + pOut,
					},
				})
				return false
			case protocol.TypeInferenceError:
				var e protocol.InferenceError
				_ = env.Decode(&e)
				o.status = "failed"
				openAIError(w, http.StatusBadGateway, e.Error, "server_error", "provider_error")
				return true
			}
		case <-timeout:
			o.status = "timeout"
			g.Hub.Send(nodeID, mustEnvelope(protocol.TypeCancel, protocol.Cancel{RequestID: reqID}))
			openAIError(w, http.StatusGatewayTimeout, "inference timed out", "server_error", "timeout")
			return true
		case <-r.Context().Done():
			o.status = "cancelled"
			g.Hub.Send(nodeID, mustEnvelope(protocol.TypeCancel, protocol.Cancel{RequestID: reqID}))
			return true
		}
	}
}

// streamResponse handles streaming: relay chunks as OpenAI SSE events.
// Returns true if the request failed.
func (g *Gateway) streamResponse(w http.ResponseWriter, r *http.Request, reqID, model, nodeID string, events <-chan envelope, o *reqOutcome) bool {
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
					o.text.WriteString(c.Delta)
					writeChunk(delta{Content: c.Delta}, nil)
				}
			case protocol.TypeInferenceComplete:
				var done protocol.InferenceComplete
				pIn, pOut := 0, 0
				if err := env.Decode(&done); err == nil {
					pIn, pOut = done.Usage.PromptTokens, done.Usage.CompletionTokens
					o.provIn = &done.Usage.PromptTokens
					o.provOut = &done.Usage.CompletionTokens
				}
				writeChunk(delta{}, &stop)
				// OpenAI-style trailing usage chunk (empty choices)
				fmt.Fprint(w, "data: ")
				json.NewEncoder(w).Encode(map[string]any{
					"id": id, "object": "chat.completion.chunk",
					"created": time.Now().Unix(), "model": model,
					"choices": []any{},
					"usage": usageStats{
						PromptTokens:     pIn,
						CompletionTokens: pOut,
						TotalTokens:      pIn + pOut,
					},
				})
				fmt.Fprint(w, "\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
				return false
			case protocol.TypeInferenceError:
				var e protocol.InferenceError
				_ = env.Decode(&e)
				o.status = "failed"
				fmt.Fprintf(w, "data: {\"error\":{\"message\":%q,\"type\":\"server_error\"}}\n\n", e.Error)
				flusher.Flush()
				return true
			}
		case <-timeout:
			o.status = "timeout"
			g.Hub.Send(nodeID, mustEnvelope(protocol.TypeCancel, protocol.Cancel{RequestID: reqID}))
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return true
		case <-r.Context().Done():
			o.status = "cancelled"
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

// handlePricing is public: what the platform charges per model.
func (g *Gateway) handlePricing(w http.ResponseWriter, r *http.Request) {
	defaults := []map[string]any{{
		"model":               "default",
		"input_micro_per_1m":  store.DefaultInputMicroPer1M,
		"output_micro_per_1m": store.DefaultOutputMicroPer1M,
	}}
	overrides := []store.ModelPriceRow{}
	if g.Billing != nil {
		if rows, err := g.Billing.ListModelPrices(r.Context()); err == nil {
			overrides = rows
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"defaults": defaults, "overrides": overrides,
		"unit": "micro-USD per 1M tokens",
	})
}

// handleSetPrice is the admin pricing override.
func (g *Gateway) handleSetPrice(w http.ResponseWriter, r *http.Request) {
	if !isAdminFrom(r.Context()) {
		openAIError(w, http.StatusForbidden, "admin key required", "auth_error", "admin_only")
		return
	}
	if g.Billing == nil {
		openAIError(w, http.StatusServiceUnavailable, "billing store unavailable", "server_error", "no_db")
		return
	}
	var body struct {
		Model    string `json:"model"`
		InMicro  int64  `json:"input_micro_per_1m"`
		OutMicro int64  `json:"output_micro_per_1m"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" || body.InMicro <= 0 || body.OutMicro <= 0 {
		openAIError(w, http.StatusBadRequest, `body must be {"model":"...","input_micro_per_1m":N,"output_micro_per_1m":N}`, "invalid_request_error", "")
		return
	}
	if err := g.Billing.UpsertModelPrice(r.Context(), body.Model, body.InMicro, body.OutMicro); err != nil {
		openAIError(w, http.StatusInternalServerError, err.Error(), "server_error", "")
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "model": body.Model})
}

// handleUsage: developers see their own rows; admins see everything (?all=1).
func (g *Gateway) handleUsage(w http.ResponseWriter, r *http.Request) {
	if g.Billing == nil {
		openAIError(w, http.StatusServiceUnavailable, "billing store unavailable", "server_error", "no_db")
		return
	}
	var uid *int64
	if !isAdminFrom(r.Context()) || r.URL.Query().Get("all") != "1" {
		uid = userIDFrom(r.Context())
	}
	rows, err := g.Billing.UsageRows(r.Context(), uid, 100)
	if err != nil {
		openAIError(w, http.StatusInternalServerError, err.Error(), "server_error", "")
		return
	}
	if rows == nil {
		rows = []store.UsageRow{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"usage": rows})
}

// handleBalance: the caller's developer balance (admins get platform totals).
func (g *Gateway) handleBalance(w http.ResponseWriter, r *http.Request) {
	if g.Billing == nil {
		openAIError(w, http.StatusServiceUnavailable, "billing store unavailable", "server_error", "no_db")
		return
	}
	ctx := r.Context()
	if isAdminFrom(ctx) {
		escrow, _ := g.Billing.AccountBalance(ctx, nil, "provider_earnings")
		rev, _ := g.Billing.AccountBalance(ctx, nil, "platform_revenue")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"scope":                          "platform",
			"provider_earnings_escrow_micro": escrow,
			"platform_revenue_micro":         rev,
		})
		return
	}
	uid := userIDFrom(ctx)
	bal, err := g.Billing.AccountBalance(ctx, uid, "developer_balance")
	if err != nil {
		openAIError(w, http.StatusInternalServerError, err.Error(), "server_error", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"scope": "developer", "balance_micro": bal,
	})
}

// withAuth enforces bearer keys: env keys are admin; DB keys are per-user.
func (g *Gateway) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			openAIError(w, http.StatusUnauthorized, "invalid API key", "auth_error", "invalid_api_key")
			return
		}
		key := strings.TrimPrefix(auth, "Bearer ")
		if g.APIKeys[key] {
			next(w, r.WithContext(context.WithValue(r.Context(), ctxIsAdmin, true)))
			return
		}
		if g.Billing != nil {
			if a, ok, _ := g.Billing.ResolveAPIKey(r.Context(), store.HashKey(key)); ok {
				uid := a.UserID
				next(w, r.WithContext(context.WithValue(r.Context(), ctxUserID, &uid)))
				return
			}
		}
		openAIError(w, http.StatusUnauthorized, "invalid API key", "auth_error", "invalid_api_key")
	}
}
