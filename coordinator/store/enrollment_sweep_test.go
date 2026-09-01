package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// Enrollment sweep + node ownership hygiene, against live Postgres:
//
//	docker run -d --name ig-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=idlegrid -p 5433:5432 postgres:16-alpine
//	TEST_DATABASE_URL='postgres://postgres:pw@localhost:5433/idlegrid?sslmode=disable' go test ./coordinator/store/
func TestEnrollmentSweepAndRevoke(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	p, err := NewPostgresBilling(ctx, url)
	if err != nil {
		t.Fatalf("connect/migrate: %v", err)
	}
	ts := time.Now().Format("150405.000000000")

	owner, err := p.EnsureUser(ctx, "sweep-owner-"+ts+"@example.com", "user")
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	nodeID := "sweep-node-" + ts

	// Simulate an UNENROLLED node serving a settled request: escrow credit.
	dev, err := p.EnsureUser(ctx, "sweep-dev-"+ts+"@example.com", "developer")
	if err != nil {
		t.Fatalf("EnsureUser dev: %v", err)
	}
	reqID := "req-sweep-" + ts
	if err := p.RecordUsage(ctx, UsageEvent{
		RequestID: reqID, UserID: &dev, NodeID: nodeID, Model: "m",
		EstInputTokens: 10, EstOutputTokens: 20, WithinTolerance: true, Status: "completed",
	}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	if _, err := p.SettleRequest(ctx, SettleParams{RequestID: reqID, UserID: &dev, NodeID: nodeID}, 1000, 900, 100); err != nil {
		t.Fatalf("SettleRequest: %v", err)
	}
	if bal, _ := p.AccountBalance(ctx, nil, "provider_earnings"); bal < 900 {
		t.Fatalf("expected escrow >= 900 micro, got %d", bal)
	}

	// Issue a real provider token via the device flow, then bind with it.
	dcHash := HashKey("device-" + ts)
	if err := p.CreateDeviceCode(ctx, dcHash, "SWEEP"+ts[:3], time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("CreateDeviceCode: %v", err)
	}
	if ok, err := p.ApproveDeviceCode(ctx, "SWEEP"+ts[:3], owner); err != nil || !ok {
		t.Fatalf("ApproveDeviceCode: ok=%v err=%v", ok, err)
	}
	if uid, status, err := p.RedeemDeviceCode(ctx, dcHash, "tokenhash-x"); err != nil || status != "ok" || uid != owner {
		t.Fatalf("RedeemDeviceCode: uid=%d status=%q err=%v", uid, status, err)
	}
	if _, _, ok, _ := p.ResolveProviderToken(ctx, "tokenhash-x"); !ok {
		t.Fatal("issued provider token does not resolve")
	}

	// Bind the node → the escrowed 900 must move to the owner.
	if err := p.BindNode(ctx, owner, nodeID, "tokenhash-x"); err != nil {
		t.Fatalf("BindNode: %v", err)
	}
	if bal, _ := p.AccountBalance(ctx, &owner, "provider_earnings"); bal != 900 {
		t.Fatalf("owner earnings = %d, want swept 900", bal)
	}

	// Binding again (e.g. reconnect) must NOT double-sweep.
	if err := p.BindNode(ctx, owner, nodeID, "tokenhash-x"); err != nil {
		t.Fatalf("BindNode re-bind: %v", err)
	}
	if bal, _ := p.AccountBalance(ctx, &owner, "provider_earnings"); bal != 900 {
		t.Fatalf("double sweep! owner earnings = %d, want 900", bal)
	}

	// Node listing shows health + that a revocable token bound it.
	nodes, err := p.ListProviderNodes(ctx, owner)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("ListProviderNodes: %v rows=%d", err, len(nodes))
	}
	if !nodes[0].TokenBacked {
		t.Fatal("expected TokenBacked=true (bound with tokenhash-x)")
	}
	if nodes[0].LastSeen == nil {
		t.Fatal("expected last_seen set at bind")
	}

	// Heartbeat touch updates last_seen.
	before := *nodes[0].LastSeen
	time.Sleep(1100 * time.Millisecond)
	if err := p.TouchNode(ctx, nodeID); err != nil {
		t.Fatalf("TouchNode: %v", err)
	}
	nodes, _ = p.ListProviderNodes(ctx, owner)
	if !nodes[0].LastSeen.After(before) {
		t.Fatal("TouchNode did not advance last_seen")
	}

	// Revoke unbinds the node; ownership check blocks other users.
	other, _ := p.EnsureUser(ctx, "sweep-other-"+ts+"@example.com", "user")
	if ok, _ := p.RevokeNode(ctx, other, nodeID); ok {
		t.Fatal("RevokeNode succeeded for a non-owner")
	}
	if ok, err := p.RevokeNode(ctx, owner, nodeID); err != nil || !ok {
		t.Fatalf("RevokeNode: ok=%v err=%v", ok, err)
	}
	if nodes, _ := p.ListProviderNodes(ctx, owner); len(nodes) != 0 {
		t.Fatal("node still listed after revoke")
	}
	if id, _, ok, _ := p.ResolveProviderToken(ctx, "tokenhash-x"); ok || id != 0 {
		t.Fatal("bound token still resolves after revoke — must be revoked")
	}
}
