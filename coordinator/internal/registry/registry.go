// Package registry tracks live provider nodes and their capacity.
// v0 keeps everything in memory (Darkbloom's MemoryStore mode) — Postgres
// persistence is a later layer.
package registry

import (
	"sync"
	"time"
)

// Node is one provider machine as the coordinator sees it.
type Node struct {
	ID            string
	Name          string
	Chip          string
	Version       string
	MemoryGB      int
	Models        map[string]bool
	PublicKey     string // X25519 (base64) — set => E2E-capable
	SignKey       string // Ed25519 public (base64) — usage signatures
	CanaryPassed  int
	CanaryFailed  int
	LastSeen      time.Time
	QueueDepth    int   // requests dispatched but not yet finished
	PendingTokens int   // estimated tokens in flight (admission budget)
	TotalRequests int64 // lifetime counter

	// ErrCooldownUntil excludes a flapping node from selection briefly
	// after a failure (Darkbloom's shape-keyed inference-error cooldown).
	ErrCooldownUntil time.Time
}

const staleAfter = 15 * time.Second

// Registry is a concurrency-safe node table.
type Registry struct {
	mu    sync.Mutex
	nodes map[string]*Node
}

func New() *Registry {
	return &Registry{nodes: make(map[string]*Node)}
}

// Register adds or refreshes a node, preserving runtime counters across
// re-registration (e.g. after a reconnect).
func (r *Registry) Register(id, name, chip, version string, memGB int, models []string, publicKey, signKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		n = &Node{ID: id}
		r.nodes[id] = n
	}
	n.Name, n.Chip, n.Version, n.MemoryGB = name, chip, version, memGB
	if publicKey != "" {
		n.PublicKey = publicKey
	}
	if signKey != "" {
		n.SignKey = signKey
	}
	n.Models = make(map[string]bool, len(models))
	for _, m := range models {
		n.Models[m] = true
	}
	n.LastSeen = time.Now()
}

// Heartbeat refreshes liveness and live capacity.
func (r *Registry) Heartbeat(id string, freeMemGB float64, queueDepth int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[id]; ok {
		n.LastSeen = time.Now()
		if n.QueueDepth == 0 && queueDepth > n.QueueDepth {
			// provider knows its own queue best; adopt if we have nothing pending
			n.QueueDepth = queueDepth
		}
	}
}

// Unregister removes a node (called on WebSocket disconnect).
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, id)
}

// Sweep marks nodes whose heartbeats stopped; returns their IDs so the hub
// can drop their connections.
func (r *Registry) Sweep() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stale []string
	cutoff := time.Now().Add(-staleAfter)
	for id, n := range r.nodes {
		if n.LastSeen.Before(cutoff) {
			stale = append(stale, id)
		}
	}
	for _, id := range stale {
		delete(r.nodes, id)
	}
	return stale
}

// CanaryResult records a canary probe outcome for reputation.
func (r *Registry) CanaryResult(id string, passed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[id]; ok {
		if passed {
			n.CanaryPassed++
		} else {
			n.CanaryFailed++
		}
	}
}

// OnlineModels is the union of models servable by live nodes.
func (r *Registry) OnlineModels() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	set := make(map[string]bool)
	for _, n := range r.nodes {
		if n.LastSeen.Before(now.Add(-staleAfter)) {
			continue
		}
		for m := range n.Models {
			set[m] = true
		}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	return out
}

// Snapshot returns a copy of all registered nodes (for debugging/status).
func (r *Registry) Snapshot() []Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		cp := *n
		out = append(out, cp)
	}
	return out
}
