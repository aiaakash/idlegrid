package registry

import (
	"errors"
	"time"
)

var ErrNoProvider = errors.New("no online provider has this model resident")

// Reserve picks the provider with the lowest estimated completion cost for a
// request (Darkbloom's cost-minimization dispatch, simplified for v0) and
// atomically reserves capacity on it.
//
// cost = QueueDepth*2000ms + PendingTokens*2ms
//
// Queue depth dominates because a queued request behind a long decode pays
// for the whole stream; token backlog refines ties between nodes with
// equal depth. estTokens is the admission charge released by Release.
func (r *Registry) Reserve(model string, estTokens int) (*Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	var best *Node
	var bestCost float64
	for _, n := range r.nodes {
		if n.LastSeen.Before(now.Add(-staleAfter)) {
			continue // heartbeat-stale
		}
		if !n.Models[model] {
			continue // model not resident
		}
		if now.Before(n.ErrCooldownUntil) {
			continue // recently failed
		}
		cost := float64(n.QueueDepth)*2000 + float64(n.PendingTokens)*2
		if best == nil || cost < bestCost {
			best, bestCost = n, cost
		}
	}
	if best == nil {
		return nil, ErrNoProvider
	}
	best.QueueDepth++
	best.PendingTokens += estTokens
	best.TotalRequests++
	cp := *best
	return &cp, nil
}

// Release returns a reservation after the request finishes.
// failed=true puts the node on a short cooldown so the scheduler stops
// routing to it while it recovers.
func (r *Registry) Release(nodeID string, estTokens int, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[nodeID]
	if !ok {
		return
	}
	if n.QueueDepth > 0 {
		n.QueueDepth--
	}
	if n.PendingTokens >= estTokens {
		n.PendingTokens -= estTokens
	} else {
		n.PendingTokens = 0
	}
	if failed {
		n.ErrCooldownUntil = time.Now().Add(5 * time.Second)
	}
}
