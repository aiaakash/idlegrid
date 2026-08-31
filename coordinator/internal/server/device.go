package server

// Device authorization flow (RFC 8628-style) for provider onboarding:
//
//	POST /v1/device/code            (public)  CLI starts a login attempt
//	POST /v1/device/token           (public)  CLI polls until approved
//	POST /v1/console/device/approve (session) user approves in the console
//
// The CLI gets a revocable provider token bound to the approving account;
// the node presents it as auth_token at WS register (hub.go). This replaces
// the static --enroll-code, which remains as a legacy fallback.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"idlegrid/coordinator/store"
)

const deviceCodeTTL = 10 * time.Minute

// Per-IP guard on the two public endpoints (login attempts are cheap to
// create, so keep creation slow: ~1 per 2s, burst 5).
var deviceLimiter = newRateLimiter(0.5, 5)

func consoleBaseURL() string {
	if v := os.Getenv("IDLEGRID_CONSOLE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://console.sqlguroo.com"
}

// Unambiguous alphabet (no 0/O, 1/I/L) for codes humans transcribe.
const userCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newUserCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	var sb strings.Builder
	for i, c := range b {
		if i == 4 {
			sb.WriteByte('-')
		}
		sb.WriteByte(userCodeAlphabet[int(c)%len(userCodeAlphabet)])
	}
	return sb.String() // "ABCD-2345"
}

// HandleDeviceCode (public): POST {} -> device_code + user_code to approve.
func (g *Gateway) HandleDeviceCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if !deviceLimiter.Allow(r.RemoteAddr) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow down"})
		return
	}
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	b := make([]byte, 32)
	rand.Read(b)
	deviceCode := "igd_" + hex.EncodeToString(b)
	userCode := newUserCode()
	expiresAt := time.Now().Add(deviceCodeTTL)
	// user_code is stored canonically (no dash) so console input is forgiving.
	if err := cs.CreateDeviceCode(r.Context(), store.HashKey(deviceCode),
		strings.ReplaceAll(userCode, "-", ""), expiresAt); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":      deviceCode,
		"user_code":        userCode,
		"verification_url": consoleBaseURL() + "/link",
		"expires_in":       int(deviceCodeTTL.Seconds()),
		"interval":         3,
	})
}

// HandleDeviceToken (public): POST {device_code} -> provider token when the
// user has approved. RFC 8628-style errors drive the CLI's polling loop.
func (g *Gateway) HandleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if !deviceLimiter.Allow(r.RemoteAddr) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow_down"})
		return
	}
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DeviceCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	// Generate the token now but only persist (hashed) on a successful redeem.
	tb := make([]byte, 32)
	rand.Read(tb)
	token := "igp_" + hex.EncodeToString(tb)
	_, status, err := cs.RedeemDeviceCode(r.Context(), store.HashKey(body.DeviceCode), store.HashKey(token))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	switch status {
	case "pending":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "authorization_pending"})
	case "expired":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expired_token"})
	case "invalid":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"provider_token": token,
			"token_type":     "Bearer",
		})
	}
}

// HandleConsoleDeviceApprove (session): POST {user_code} — approves a pending
// device login for the signed-in account.
func (g *Gateway) HandleConsoleDeviceApprove(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	var body struct {
		UserCode string `json:"user_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_code required"})
		return
	}
	code := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(body.UserCode))
	if len(code) != 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code must be 8 characters"})
		return
	}
	cs, ok := g.consoleStore(w, r)
	if !ok {
		return
	}
	approved, err := cs.ApproveDeviceCode(r.Context(), code, u.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !approved {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown, expired, or already-approved code"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
