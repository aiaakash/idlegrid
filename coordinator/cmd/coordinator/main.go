// Coordinator entrypoint. Serves the OpenAI-compatible gateway and the
// provider WebSocket hub from a single process with an in-memory registry.
package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"idlegrid/coordinator/internal/registry"
	"idlegrid/coordinator/internal/server"
)

func main() {
	port := envOr("PORT", "8090")
	apiKeys := strings.Split(envOr("IDLEGRID_API_KEYS", "dev-key"), ",")
	joinCode := os.Getenv("IDLEGRID_PROVIDER_CODE")

	reg := registry.New()
	handler := server.NewHandler(reg, server.Config{
		APIKeys:  apiKeys,
		JoinCode: joinCode,
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("[coordinator] listening on :%s (api keys: %d configured)", port, len(apiKeys))
	log.Fatal(srv.ListenAndServe())
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
