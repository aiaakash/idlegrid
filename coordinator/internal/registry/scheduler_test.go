package registry

import (
	"testing"
	"time"
)

func reg(t *testing.T) *Registry {
	t.Helper()
	return New()
}

func TestReserveRequiresResidentModel(t *testing.T) {
	r := reg(t)
	r.Register("a", "A", "M4 Pro", "v0", 24, []string{"llama-8b"}, "", "")
	if _, err := r.Reserve("qwen-7b", 100); err != ErrNoProvider {
		t.Fatalf("want ErrNoProvider, got %v", err)
	}
	if _, err := r.Reserve("llama-8b", 100); err != nil {
		t.Fatalf("want ok, got %v", err)
	}
}

func TestReservePicksLeastLoaded(t *testing.T) {
	r := reg(t)
	r.Register("a", "A", "M4 Pro", "v0", 24, []string{"m"}, "", "")
	r.Register("b", "B", "M1 Air", "v0", 16, []string{"m"}, "", "")

	first, err := r.Reserve("m", 100)
	if err != nil {
		t.Fatal(err)
	}
	// After one reservation, that node's cost (2000+100*2) exceeds the
	// untouched node's (0), so the next pick must differ.
	second, err := r.Reserve("m", 100)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("scheduler should spread load: both picked %s", first.ID)
	}
}

func TestReleaseRestoresCapacity(t *testing.T) {
	r := reg(t)
	r.Register("a", "A", "M4 Pro", "v0", 24, []string{"m"}, "", "")
	if _, err := r.Reserve("m", 100); err != nil {
		t.Fatal(err)
	}
	r.Release("a", 100, false)
	nodes := r.Snapshot()
	if nodes[0].QueueDepth != 0 || nodes[0].PendingTokens != 0 {
		t.Fatalf("release should restore capacity: %+v", nodes[0])
	}
}

func TestFailedRequestPutsNodeInCooldown(t *testing.T) {
	r := reg(t)
	r.Register("a", "A", "M4 Pro", "v0", 24, []string{"m"}, "", "")
	r.Reserve("m", 100)
	r.Release("a", 100, true)
	if _, err := r.Reserve("m", 100); err != ErrNoProvider {
		t.Fatalf("node in cooldown should be skipped, got %v", err)
	}
	// cooldown expires
	r.mu.Lock()
	r.nodes["a"].ErrCooldownUntil = time.Now().Add(-time.Second)
	r.mu.Unlock()
	if _, err := r.Reserve("m", 100); err != nil {
		t.Fatalf("after cooldown node should be eligible: %v", err)
	}
}

func TestSweepDropsStaleNodes(t *testing.T) {
	r := reg(t)
	r.Register("a", "A", "M4 Pro", "v0", 24, []string{"m"}, "", "")
	r.mu.Lock()
	r.nodes["a"].LastSeen = time.Now().Add(-30 * time.Second)
	r.mu.Unlock()
	if stale := r.Sweep(); len(stale) != 1 || stale[0] != "a" {
		t.Fatalf("want [a], got %v", stale)
	}
	if len(r.Snapshot()) != 0 {
		t.Fatal("stale node should be gone")
	}
}
