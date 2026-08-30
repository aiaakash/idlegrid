// Coordinator entrypoint. Serves the OpenAI-compatible gateway and the
// provider WebSocket hub from a single process with an in-memory registry.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"idlegrid/coordinator/store"

	"idlegrid/coordinator/internal/registry"
	"idlegrid/coordinator/internal/server"
)

func main() {
	port := envOr("PORT", "8090")
	apiKeys := strings.Split(envOr("IDLEGRID_API_KEYS", "dev-key"), ",")
	joinCode := os.Getenv("IDLEGRID_PROVIDER_CODE")
	canaryInterval := 0
	if v := os.Getenv("IDLEGRID_CANARY_INTERVAL_SECS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			canaryInterval = n
		}
	}
	feePct := 10
	if v := os.Getenv("IDLEGRID_PLATFORM_FEE_PCT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			feePct = n
		}
	}

	reg := registry.New()
	cfg := server.Config{
		APIKeys:            apiKeys,
		JoinCode:           joinCode,
		PlatformFeePercent: feePct,
		RequireBalance:     os.Getenv("IDLEGRID_REQUIRE_BALANCE") != "0",
		CanaryIntervalSecs: canaryInterval,
		ListenPort:         port,
	}

	// Billing store: Postgres when DATABASE_URL is set (production);
	// otherwise nil → admin env keys only, no metering rows (local dev).
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		billing, err := store.NewPostgresBilling(ctx, dbURL)
		cancel()
		if err != nil {
			log.Fatalf("[coordinator] DATABASE_URL set but store unavailable: %v", err)
		}
		cfg.Billing = billing
		log.Printf("[coordinator] billing store: postgres (metering + per-user keys active)")
	} else {
		log.Printf("[coordinator] billing store: none (DATABASE_URL not set — dev mode)")
	}

	handler := server.NewHandler(reg, cfg)

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
