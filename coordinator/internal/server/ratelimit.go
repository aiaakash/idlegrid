package server

import (
	"log"
	"net/http"
	"sync"
	"time"
)

// Per-user token-bucket rate limiting for inference requests.
// In-memory (single coordinator instance for alpha).

var (
	rateLimitRPS   = 10.0
	rateLimitBurst = 20
)

type rateBucket struct {
	tokens float64
	last   time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	rps     float64
	burst   float64
}

func newRateLimiter(rps float64, burst int) *rateLimiter {
	return &rateLimiter{buckets: make(map[string]*rateBucket), rps: rps, burst: float64(burst)}
}

// Allow consumes one token for key; false = over the limit.
func (l *rateLimiter) Allow(key string) bool {
	if l.rps <= 0 {
		return true // disabled
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &rateBucket{tokens: l.burst, last: time.Now()}
		l.buckets[key] = b
	}
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * l.rps
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// limitInference wraps the chat handler with per-key rate limiting.
func (g *Gateway) limitInference(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.rateLimiter != nil {
			// admin keys are exempt; users keyed by their user id
			var key string
			if isAdminFrom(r.Context()) {
				key = "admin"
			} else if uid := userIDFrom(r.Context()); uid != nil {
				key = "user-" + hexEncodeUint(uint64(*uid))
			} else {
				key = r.RemoteAddr
			}
			if !g.rateLimiter.Allow(key) {
				log.Printf("[gateway] rate limit hit for %s", key)
				w.Header().Set("Retry-After", "1")
				openAIError(w, http.StatusTooManyRequests,
					"rate limit exceeded — slow down or contact the admin to raise it",
					"rate_limit_error", "rate_limit_exceeded")
				return
			}
		}
		next(w, r)
	}
}
