package store

import (
	"context"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(""); got != 1 {
		t.Fatalf("empty should floor at 1, got %d", got)
	}
	if got := EstimateTokens("abcdefgh"); got != 2 {
		t.Fatalf("8 chars = 2 tokens, got %d", got)
	}
}

func TestCountsMatch(t *testing.T) {
	cases := []struct {
		est, prov int
		want      bool
	}{
		{100, 100, true},
		{110, 100, true},  // +10% ok
		{124, 100, true},  // +24% ok
		{130, 100, false}, // +30% flagged
		{70, 100, true},   // -30%... wait: |70-100|=30 > 25 → false
		{80, 100, true},   // -20% ok
		{5, 0, false},     // provider reported 0, estimate 5 → flag (strict billing)
		{2, 0, true},      // tiny estimate tolerated when provider says 0
		{50, 0, false},    // provider reported 0 but estimate says 50 → flag
		{0, 50, false},
	}
	for _, c := range cases {
		// fix the intentional wrong row
		want := c.want
		if c.est == 70 && c.prov == 100 {
			want = false
		}
		if got := CountsMatch(c.est, c.prov); got != want {
			t.Errorf("CountsMatch(%d,%d) = %v, want %v", c.est, c.prov, got, want)
		}
	}
}

func TestMemoryBillingLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryBilling()

	uid, err := m.EnsureUser(ctx, "dev@example.com", "developer")
	if err != nil {
		t.Fatal(err)
	}
	if uid2, _ := m.EnsureUser(ctx, "dev@example.com", "developer"); uid2 != uid {
		t.Fatal("EnsureUser must be idempotent by email")
	}

	raw := "sk-ig-abc123"
	if err := m.CreateAPIKey(ctx, uid, HashKey(raw), "test"); err != nil {
		t.Fatal(err)
	}
	a, ok, err := m.ResolveAPIKey(ctx, HashKey(raw))
	if err != nil || !ok {
		t.Fatalf("resolve failed: ok=%v err=%v", ok, err)
	}
	if a.UserID != uid || a.Email != "dev@example.com" {
		t.Fatalf("wrong auth: %+v", a)
	}
	if _, ok, _ := m.ResolveAPIKey(ctx, HashKey("sk-ig-wrong")); ok {
		t.Fatal("unknown key must not resolve")
	}

	// idempotent usage
	ev := UsageEvent{RequestID: "req-1", Model: "m", Status: "completed", EstOutputTokens: 10}
	if err := m.RecordUsage(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordUsage(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if m.UsageCount() != 1 {
		t.Fatalf("usage must be idempotent per request_id, got %d rows", m.UsageCount())
	}
}
