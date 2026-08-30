package server

import (
	"sync"
)

// Router delivers per-request events from provider WebSockets to the
// gateway handler awaiting them. requestID -> buffered channel.
type Router struct {
	mu    sync.Mutex
	chans map[string]chan<- envelope
}

func NewRouter() *Router {
	return &Router{chans: make(map[string]chan<- envelope)}
}

// Create registers a fresh event channel for a request.
func (rt *Router) Create(id string) <-chan envelope {
	ch := make(chan envelope, 256)
	rt.mu.Lock()
	rt.chans[id] = ch
	rt.mu.Unlock()
	return ch
}

// Resolve removes the channel; late events for the request are dropped.
func (rt *Router) Resolve(id string) {
	rt.mu.Lock()
	delete(rt.chans, id)
	rt.mu.Unlock()
}

// Deliver routes an event to the awaiting handler. Returns false if no
// handler is waiting (already finished, timed out, or unknown request).
func (rt *Router) Deliver(id string, env envelope) bool {
	rt.mu.Lock()
	ch, ok := rt.chans[id]
	rt.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- env:
		return true
	default:
		return false // handler too slow; drop rather than block the read loop
	}
}
