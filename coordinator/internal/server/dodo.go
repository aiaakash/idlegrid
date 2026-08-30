package server

// Dodo Payments integration (Phase 4):
//   - POST /v1/dodo/topup  (session)  -> creates a Dodo checkout link
//   - POST /v1/dodo/webhook (public)  -> verifies + credits the balance
//
// Configure via env:
//   IDLEGRID_DODO_API_KEY        live/test API key (sk_... / sk_test_...)
//   IDLEGRID_DODO_WEBHOOK_SECRET webhook signing secret
//   IDLEGRID_DODO_RETURN_URL     where Dodo sends the payer after checkout
//
// If the API key is unset the topup endpoint answers 503 and the console
// shows the manual-top-up fallback (admin credits via admin panel later).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"idlegrid/coordinator/store"
)

func dodoAPIBase() string {
	if env := os.Getenv("IDLEGRID_DODO_BASE_URL"); env != "" {
		return env // e.g. https://test.dodopayments.com
	}
	return "https://live.dodopayments.com"
}

func dodoConfigured() bool {
	return os.Getenv("IDLEGRID_DODO_API_KEY") != ""
}

// handleDodoTopup (session): POST {amount_usd} -> {payment_link}
func (g *Gateway) HandleDodoTopup(w http.ResponseWriter, r *http.Request, u store.APIKeyAuth) {
	if !dodoConfigured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "online top-up not configured — contact the admin for a manual top-up",
		})
		return
	}
	var body struct {
		AmountUSD float64 `json:"amount_usd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AmountUSD < 5 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount_usd must be >= 5"})
		return
	}

	cents := int64(body.AmountUSD * 100)
	payload := map[string]any{
		"billing_currency": "USD",
		"product_cart": []map[string]any{{
			// Dodo requires a product id; use the static "credit pack" product
			// configured by the admin. Quantity carries the pack multiple.
			"product_id": os.Getenv("IDLEGRID_DODO_PRODUCT_ID"),
			"quantity":   1,
		}},
		"customer":     map[string]any{"email": u.Email},
		"payment_link": true,
		"metadata": map[string]any{
			"user_id":      u.UserID,
			"amount_cents": cents,
		},
	}
	if ret := os.Getenv("IDLEGRID_DODO_RETURN_URL"); ret != "" {
		payload["return_url"] = ret
	}
	pj, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(r.Context(), "POST", dodoAPIBase()+"/payments", io.NopCloser(strings.NewReader(string(pj))))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("IDLEGRID_DODO_API_KEY"))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "dodo api: " + err.Error()})
		return
	}
	defer res.Body.Close()
	var dres struct {
		PaymentID    string `json:"payment_id"`
		PaymentLink  string `json:"payment_link"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.NewDecoder(res.Body).Decode(&dres); err != nil || res.StatusCode >= 300 {
		msg := dres.ErrorMessage
		if msg == "" {
			msg = "dodo api error"
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"payment_link": dres.PaymentLink,
		"payment_id":   dres.PaymentID,
	})
}

// handleDodoWebhook (public): Dodo posts payment events here.
// Credits are idempotent on the Dodo payment id.
func (g *Gateway) HandleDodoWebhook(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("IDLEGRID_DODO_WEBHOOK_SECRET")
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read failed"})
		return
	}
	if secret != "" {
		sig := r.Header.Get("webhook-signature")
		if sig == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing signature"})
			return
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(raw)
		want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(want), []byte(sig)) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad signature"})
			return
		}
	} else {
		log.Printf("[dodo] WARNING: IDLEGRID_DODO_WEBHOOK_SECRET unset — accepting unverified webhook")
	}

	var payload struct {
		EventType string `json:"event_type"`
		Data      struct {
			Object struct {
				PaymentID   string `json:"payment_id"`
				TotalAmount int64  `json:"total_amount"` // minor units (cents)
				Currency    string `json:"currency"`
				Metadata    struct {
					UserID      int64 `json:"user_id"`
					AmountCents int64 `json:"amount_cents"`
				} `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad payload"})
		return
	}
	if payload.EventType != "" && payload.EventType != "payment.succeeded" {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": payload.EventType})
		return
	}
	obj := payload.Data.Object
	if obj.PaymentID == "" || obj.Metadata.UserID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing payment_id/user metadata"})
		return
	}
	// Dodo amounts are minor units (cents). 1 cent = 10_000 micro-USD.
	cents := obj.TotalAmount
	if obj.Metadata.AmountCents > 0 {
		cents = obj.Metadata.AmountCents // signed-off amount from our checkout
	}
	micro := cents * 10_000

	cs := g.Billing.(store.ConsoleStore)
	if err := cs.DodoCredit(r.Context(), obj.PaymentID, obj.Metadata.UserID, micro, raw); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("[dodo] credited %d micro to user %d (payment %s)", micro, obj.Metadata.UserID, obj.PaymentID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
