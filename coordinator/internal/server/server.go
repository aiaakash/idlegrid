// Package server wires the hub, router, and gateway into one HTTP handler.
package server

import (
	"net/http"
	"time"

	"idlegrid/coordinator/internal/registry"
	"idlegrid/coordinator/store"
)

// Config for NewHandler.
type Config struct {
	APIKeys            []string
	JoinCode           string        // when set, providers must present it at register
	Billing            store.Billing // nil in dev (no DATABASE_URL)
	PlatformFeePercent int           // provider/platform revenue split (default 10)
	RequireBalance     bool          // block developers with negative balance (default on)
}

// NewHandler builds the full coordinator HTTP surface:
//
//	POST /v1/chat/completions   OpenAI-compatible inference (stream + non-stream)
//	GET  /v1/models             models servable by live providers
//	GET  /ws/provider           provider WebSocket endpoint
//	POST /v1/admin/users        admin: create developer + API key (needs DATABASE_URL)
//	GET  /healthz               liveness
//	GET  /debug/providers       registered nodes (auth'd)
func NewHandler(reg *registry.Registry, cfg Config) http.Handler {
	router := NewRouter()
	hub := NewHub(reg, router, cfg.JoinCode, cfg.Billing)
	gateway := NewGateway(reg, hub, router, cfg.APIKeys, cfg.Billing, cfg.PlatformFeePercent, cfg.RequireBalance)
	hub.StartSweeper(5 * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDashboard)
	mux.HandleFunc("/ws/provider", hub.HandleWS)
	mux.HandleFunc("/v1/chat/completions", gateway.withAuth(gateway.handleChatCompletions))
	mux.HandleFunc("/v1/models", gateway.withAuth(gateway.handleModels))
	mux.HandleFunc("/v1/admin/users", gateway.withAuth(gateway.handleCreateUser))
	mux.HandleFunc("/v1/admin/prices", gateway.withAuth(gateway.handleSetPrice))
	mux.HandleFunc("/v1/pricing", gateway.handlePricing)
	mux.HandleFunc("/v1/usage", gateway.withAuth(gateway.handleUsage))
	mux.HandleFunc("/v1/balance", gateway.withAuth(gateway.handleBalance))

	// console (session-authenticated; used by the Next.js console)
	mux.HandleFunc("/v1/console/login", gateway.HandleConsoleLogin)
	mux.HandleFunc("/v1/console/logout", gateway.requireSession(gateway.HandleConsoleLogout))
	mux.HandleFunc("/v1/console/me", gateway.requireSession(gateway.HandleConsoleMe))
	mux.HandleFunc("/v1/console/keys", gateway.requireSession(gateway.HandleConsoleKeys))
	mux.HandleFunc("/v1/console/keys/revoke", gateway.requireSession(gateway.HandleConsoleKeys))
	mux.HandleFunc("/v1/console/usage", gateway.requireSession(gateway.HandleConsoleUsage))
	mux.HandleFunc("/v1/console/balance", gateway.requireSession(gateway.HandleConsoleMe))
	mux.HandleFunc("/v1/console/payout-request", gateway.requireSession(gateway.HandleConsolePayoutRequest))
	mux.HandleFunc("/v1/console/payouts", gateway.requireSession(gateway.HandleConsolePayouts))
	mux.HandleFunc("/v1/console/admin/users", gateway.requireAdminSession(gateway.HandleConsoleAdminUsers))
	mux.HandleFunc("/v1/console/admin/prices", gateway.requireAdminSession(gateway.HandleConsoleAdminPrices))
	mux.HandleFunc("/v1/console/admin/payouts", gateway.requireAdminSession(gateway.HandleConsoleAdminPayouts))
	mux.HandleFunc("/v1/console/enrollment", gateway.requireSession(gateway.HandleConsoleEnrollment))
	mux.HandleFunc("/v1/console/topup", gateway.requireSession(gateway.HandleDodoTopup))
	mux.HandleFunc("/v1/dodo/topup", gateway.requireSession(gateway.HandleDodoTopup))
	mux.HandleFunc("/v1/dodo/webhook", gateway.HandleDodoWebhook)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/debug/providers", gateway.withAuth(gateway.handleDebugProviders))
	return mux
}
