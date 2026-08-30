package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run with a live Postgres:
//
//	docker run -d --name ig-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=idlegrid -p 5433:5432 postgres:16-alpine
//	TEST_DATABASE_URL='postgres://postgres:pw@localhost:5433/idlegrid?sslmode=disable' go test ./coordinator/store/
func TestPostgresBilling(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	p, err := NewPostgresBilling(ctx, url)
	if err != nil {
		t.Fatalf("connect/migrate: %v", err)
	}

	uid, err := p.EnsureUser(ctx, "ci@example.com", "developer")
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if uid2, _ := p.EnsureUser(ctx, "ci@example.com", "developer"); uid2 != uid {
		t.Fatal("EnsureUser not idempotent")
	}

	raw := "sk-ig-postgres-test-key"
	if err := p.CreateAPIKey(ctx, uid, HashKey(raw), "ci"); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	a, ok, err := p.ResolveAPIKey(ctx, HashKey(raw))
	if err != nil || !ok || a.UserID != uid {
		t.Fatalf("ResolveAPIKey: ok=%v a=%+v err=%v", ok, a, err)
	}

	ev := UsageEvent{
		RequestID:       "req-ci-" + time.Now().Format("150405.000000000"),
		UserID:          &uid,
		NodeID:          "ci-node",
		Model:           "test-model",
		EstInputTokens:  10,
		EstOutputTokens: 20,
		WithinTolerance: true,
		Status:          "completed",
	}
	if err := p.RecordUsage(ctx, ev); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	if err := p.RecordUsage(ctx, ev); err != nil {
		t.Fatalf("RecordUsage dup: %v", err)
	}

	// exactly one row for that request id
	var n int
	if err := p.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM usage_events WHERE request_id=$1`, ev.RequestID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("idempotency violated: %d rows", n)
	}
}
