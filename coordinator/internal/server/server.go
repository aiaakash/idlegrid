// Package server wires the hub, router, and gateway into one HTTP handler.
package server

import (
	"net/http"
	"time"

	"idlegrid/coordinator/internal/registry"
)

// Config for NewHandler.
type Config struct {
	APIKeys  []string
	JoinCode string // when set, providers must present it at register
}

// NewHandler builds the full coordinator HTTP surface:
//
//	POST /v1/chat/completions   OpenAI-compatible inference (stream + non-stream)
//	GET  /v1/models             models servable by live providers
//	GET  /ws/provider           provider WebSocket endpoint
//	GET  /healthz               liveness
//	GET  /debug/providers       registered nodes (dev)
func NewHandler(reg *registry.Registry, cfg Config) http.Handler {
	router := NewRouter()
	hub := NewHub(reg, router, cfg.JoinCode)
	gateway := NewGateway(reg, hub, router, cfg.APIKeys)
	hub.StartSweeper(5 * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDashboard)
	mux.HandleFunc("/ws/provider", hub.HandleWS)
	mux.HandleFunc("/v1/chat/completions", gateway.withAuth(gateway.handleChatCompletions))
	mux.HandleFunc("/v1/models", gateway.withAuth(gateway.handleModels))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/debug/providers", gateway.withAuth(gateway.handleDebugProviders))
	return mux
}
