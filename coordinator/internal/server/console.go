package server

// Console API — session-authenticated endpoints for the Next.js console.
// The console UI holds the session token in an httpOnly cookie and proxies
// every call here with the X-Session-Token header. All business logic
// (passwords, sessions, money) lives in the coordinator/store.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"idlegrid/coordinator/store"
)

const sessionTTL = 7 * 24 * time.Hour

func (g *Gateway) consoleStore(w http.ResponseWriter, r *http.Request) (store.ConsoleStore, bool) {
	if g.Billing == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "billing store unavailable (set DATABASE_URL)"})
		return nil, false
	}
	cs, ok := g.Billing.(store.ConsoleStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "console store unavailable"})
		return nil, false
	}
	return cs, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func newSessionToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// sessionUser resolves the X-Session-Token header to a user.
func (g *Gateway) sessionUser(r *http.Request) (store.APIKeyAuth, bool) {
	token := r.Header.Get("X-Session-Token")
	if token == "" || g.Billing == nil {
		return store.APIKeyAuth{}, false
	}
	cs, ok := g.Billing.(store.ConsoleStore)
	if !ok {
		return store.APIKeyAuth{}, false
	}
	a, valid, err := cs.ResolveSession(r.Context(), store.HashKey(token))
	if err != nil || !valid {
		return store.APIKeyAuth{}, false
	}
	return a, true
}

func (g *Gateway) requireSession(next func(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := g.sessionUser(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired session"})
			return
		}
		next(w, r, u)
	}
}

func (g *Gateway) requireAdminSession(next func(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Bootstrap path: the admin API key (env) also works here, so the
		// first console user can be created before any session exists.
		if key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); key != "" && g.APIKeys[key] {
			next(w, r, store.APIKeyAuth{UserID: 0, Email: "admin@api-key", Role: "admin"})
			return
		}
		g.requireSession(func(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
			if u.Role != "admin" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin only"})
				return
			}
			next(w, r, u)
		})(w, r)
	}
}

// Login attempts are brute-forceable credentials — throttle hard. Keyed per
// email (console calls arrive via the Next.js proxy, so RemoteAddr is the
// proxy and only the email key really discriminates).
var loginLimiter = newRateLimiter(0.2, 5) // ~1 try per 5s, burst 5

// HandleConsoleLogin: POST {email, password} -> {token, user}
func (g *Gateway) HandleConsoleLogin(w http.ResponseWriter, r *http.Request) {
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" || body.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password required"})
		return
	}
	if !loginLimiter.Allow("email:"+strings.ToLower(body.Email)) || !loginLimiter.Allow("ip:"+r.RemoteAddr) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts — wait a minute and retry"})
		return
	}
	id, role, hash, err := cs.GetUserByEmail(r.Context(), body.Email)
	if err != nil || hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
		return
	}
	token := newSessionToken()
	if err := cs.CreateSession(r.Context(), store.HashKey(token), id, time.Now().Add(sessionTTL)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  map[string]any{"id": id, "email": body.Email, "role": role},
	})
}

// HandleConsoleLogout: POST with X-Session-Token.
func (g *Gateway) HandleConsoleLogout(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	_ = u
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	if token := r.Header.Get("X-Session-Token"); token != "" {
		_ = cs.DeleteSession(r.Context(), store.HashKey(token))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// HandleConsoleMe: GET -> current user + role + balances.
func (g *Gateway) HandleConsoleMe(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	uid := u.UserID
	out := map[string]any{"id": uid, "email": u.Email, "role": u.Role}
	if g.Billing != nil {
		if bal, err := g.Billing.AccountBalance(r.Context(), &uid, "developer_balance"); err == nil {
			out["developer_balance_micro"] = bal
		}
		if earn, err := g.Billing.AccountBalance(r.Context(), &uid, "provider_earnings"); err == nil {
			out["provider_earnings_micro"] = earn
		}
		if u.Role == "admin" {
			escrow, _ := g.Billing.AccountBalance(r.Context(), nil, "provider_earnings")
			rev, _ := g.Billing.AccountBalance(r.Context(), nil, "platform_revenue")
			out["provider_earnings_escrow_micro"] = escrow
			out["platform_revenue_micro"] = rev
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleConsoleKeys: GET list / POST create / DELETE revoke.
func (g *Gateway) HandleConsoleKeys(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		keys, err := cs.ListAPIKeys(r.Context(), u.UserID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if keys == nil {
			keys = []store.KeyRow{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
	case http.MethodPost:
		var body struct {
			Label string `json:"label"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw := "sk-ig-" + newSessionToken() + newSessionToken()[:16]
		id, err := cs.CreateAPIKeyWithID(r.Context(), u.UserID, store.HashKey(raw), body.Label)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "api_key": raw}) // plaintext shown once
	case http.MethodDelete:
		var body struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
			return
		}
		if err := cs.RevokeAPIKey(r.Context(), u.UserID, body.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method"})
	}
}

// HandleConsoleUsage: GET -> own usage rows. Admins see all traffic
// (including admin-key playground requests, which have no user).
func (g *Gateway) HandleConsoleUsage(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	if g.Billing == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no db"})
		return
	}
	var uid *int64
	if u.Role != "admin" {
		uid = &u.UserID
	}
	rows, err := g.Billing.UsageRows(r.Context(), uid, 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []store.UsageRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": rows})
}

// HandleConsolePayoutRequest: POST {amount_micro, rail?, rail_ref?} -> payout row (requested).
func (g *Gateway) HandleConsolePayoutRequest(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	var body struct {
		AmountMicro int64  `json:"amount_micro"`
		Rail        string `json:"rail"`
		RailRef     string `json:"rail_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AmountMicro <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount_micro required"})
		return
	}
	rail := body.Rail
	if rail != "paypal" && rail != "wise" && rail != "upi" {
		rail = "manual"
	}
	id, err := cs.CreatePayoutRequest(r.Context(), u.UserID, body.AmountMicro, rail, strings.TrimSpace(body.RailRef))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "requested"})
}

// HandleConsolePayouts: GET own payouts (providers) or all (admin).
func (g *Gateway) HandleConsolePayouts(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	var uid *int64
	if u.Role != "admin" || r.URL.Query().Get("all") != "1" {
		uid = &u.UserID
	}
	rows, err := cs.ListPayouts(r.Context(), uid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []store.PayoutRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"payouts": rows})
}

// ---- admin ----

// HandleConsoleAdminUsers: GET list (with balances) / POST create with initial password.
func (g *Gateway) HandleConsoleAdminUsers(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := cs.ListUsers(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if users == nil {
			users = []store.UserRow{}
		}
		out := make([]map[string]any, 0, len(users))
		for _, usr := range users {
			id := usr.ID
			bal, _ := g.Billing.AccountBalance(r.Context(), &id, "developer_balance")
			out = append(out, map[string]any{
				"id": usr.ID, "email": usr.Email, "role": usr.Role,
				"created_at": usr.CreatedAt, "developer_balance_micro": bal,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": out})
	case http.MethodPost:
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.Contains(body.Email, "@") || len(body.Password) < 8 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email + password (min 8 chars) required"})
			return
		}
		role := body.Role
		if role != "admin" && role != "developer" && role != "provider_owner" {
			role = "developer"
		}
		id, err := cs.EnsureUser(r.Context(), body.Email, role)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := cs.SetPasswordHash(r.Context(), id, string(hash)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "email": body.Email, "role": role})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method"})
	}
}

// HandleConsoleAdminPrices: GET list / PUT set.
func (g *Gateway) HandleConsoleAdminPrices(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	if g.Billing == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no db"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		overrides, err := g.Billing.ListModelPrices(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if overrides == nil {
			overrides = []store.ModelPriceRow{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"defaults": []store.ModelPriceRow{{
				Model: "default", InMicro: store.DefaultInputMicroPer1M, OutMicro: store.DefaultOutputMicroPer1M,
			}},
			"overrides": overrides,
		})
	case http.MethodPut:
		var body struct {
			Model    string `json:"model"`
			InMicro  int64  `json:"input_micro_per_1m"`
			OutMicro int64  `json:"output_micro_per_1m"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" || body.InMicro <= 0 || body.OutMicro <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model + positive rates required"})
			return
		}
		if err := g.Billing.UpsertModelPrice(r.Context(), body.Model, body.InMicro, body.OutMicro); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method"})
	}
}

// HandleConsoleAdminPayouts: GET all / POST {id, action: approve|markpaid, rail, rail_ref}.
func (g *Gateway) HandleConsoleAdminPayouts(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := cs.ListPayouts(r.Context(), nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if rows == nil {
			rows = []store.PayoutRow{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"payouts": rows})
	case http.MethodPost:
		var body struct {
			ID      int64  `json:"id"`
			Action  string `json:"action"` // approve | markpaid
			Rail    string `json:"rail"`
			RailRef string `json:"rail_ref"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
			return
		}
		switch body.Action {
		case "approve":
			if err := cs.MarkPayout(r.Context(), body.ID, "approved", body.Rail, body.RailRef); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "approved"})
		case "markpaid":
			if err := cs.(interface {
				SettlePayout(ctx context.Context, payoutID int64, rail, railRef string) error
			}).SettlePayout(r.Context(), body.ID, body.Rail, body.RailRef); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "paid"})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be approve|markpaid"})
		}
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method"})
	}
}

// HandleConsoleEnrollment: GET the user's provider enrollment code (+ how to
// use it). Providers present it with --enroll-code at registration.
func (g *Gateway) HandleConsoleEnrollment(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	code, err := cs.GetOrCreateEnrollmentCode(r.Context(), u.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enrollment_code": code,
		"instructions":    "On the Mac you want to enroll, install the provider and add: --enroll-code " + code,
	})
}

// HandleConsoleNodes: GET -> the user's enrolled Macs with health (last_seen,
// error_count). Drives the "Your enrolled Macs" card on the Provider page.
func (g *Gateway) HandleConsoleNodes(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	rows, err := cs.ListProviderNodes(r.Context(), u.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": rows})
}

// HandleConsoleNodeRevoke: POST {node_id} — unbinds the Mac AND revokes the
// login token that bound it, so it cannot silently re-enroll on reconnect.
func (g *Gateway) HandleConsoleNodeRevoke(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	var body struct {
		NodeID string `json:"node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "node_id required"})
		return
	}
	removed, err := cs.RevokeNode(r.Context(), u.UserID, body.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !removed {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "node not enrolled to your account"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node_id": body.NodeID})
}

// StartCanaryLoop periodically sends a known prompt through the normal
// gateway path and scores whether the response echoed the expected marker.
// Results feed node canary counters (reputation). Off by default; enable
// with IDLEGRID_CANARY_INTERVAL_SECS.
func (g *Gateway) StartCanaryLoop(intervalSecs int, adminKey string, port string) chan struct{} {
	stop := make(chan struct{})
	go func() {
		if intervalSecs <= 0 {
			return
		}
		t := time.NewTicker(time.Duration(intervalSecs) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				g.runCanary(adminKey, port)
			}
		}
	}()
	return stop
}

func (g *Gateway) runCanary(adminKey, port string) {
	models := g.Reg.OnlineModels()
	if len(models) == 0 {
		return
	}
	marker := "CANARY-" + newID()[:8]
	body, _ := json.Marshal(map[string]any{
		"model":      models[0],
		"messages":   []map[string]string{{"role": "user", "content": "Reply with exactly: " + marker}},
		"max_tokens": 30,
	})
	req, _ := http.NewRequest("POST", "http://127.0.0.1:"+port+"/v1/chat/completions",
		strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer res.Body.Close()
	nodeID := res.Header.Get("X-Idlegrid-Node")
	if nodeID == "" {
		return // request never reached a node
	}
	raw, _ := io.ReadAll(res.Body)
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(raw, &parsed)
	content := ""
	if len(parsed.Choices) > 0 {
		content = parsed.Choices[0].Message.Content
	}
	passed := strings.Contains(content, marker)
	g.Reg.CanaryResult(nodeID, passed)
	log.Printf("[canary] node %s passed=%v", nodeID, passed)
}
