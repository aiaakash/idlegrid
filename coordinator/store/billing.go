// Package store holds the billing-grade persistence layer.
//
// BillingStore is the interface the gateway uses for auth + metering.
// It has two implementations: memory (dev/tests, lost on restart) and
// postgres (production). Money questions must always go through the
// Postgres implementation.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// UsageEvent is the billing-grade metering record for one request.
type UsageEvent struct {
	RequestID       string
	UserID          *int64 // developer; nil = admin key
	NodeID          string
	Model           string
	EstInputTokens  int
	EstOutputTokens int
	ProviderInput   *int
	ProviderOutput  *int
	WithinTolerance bool
	Status          string // completed | failed | cancelled | timeout
}

// APIKeyAuth is what auth needs to know about a key.
type APIKeyAuth struct {
	UserID int64
	Email  string
	Role   string
}

// Billing is the persistence surface for auth + metering (Phase 1).
// Ledger/settlement methods arrive in Phase 2 on the same interface.
type Billing interface {
	// EnsureUser creates the user if missing, returns the user id.
	EnsureUser(ctx context.Context, email, role string) (int64, error)
	// CreateAPIKey stores a key hash for a user.
	CreateAPIKey(ctx context.Context, userID int64, keyHash, label string) error
	// ResolveAPIKey maps a key hash back to its user (auth).
	ResolveAPIKey(ctx context.Context, keyHash string) (APIKeyAuth, bool, error)
	// RecordUsage inserts the metering row; idempotent on RequestID.
	RecordUsage(ctx context.Context, e UsageEvent) error
}

// HashKey is the canonical at-rest form of an API key (sha256 hex).
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// ---- estimator ----

// EstimateTokens approximates the token count of text (~4 chars/token).
// The coordinator's independent second opinion of the provider's count.
func EstimateTokens(text string) int {
	n := len(text) / 4
	if n < 1 {
		n = 1
	}
	return n
}

// CountsMatch reports whether the provider's engine count agrees with the
// coordinator's estimate within ±25% (outside that = flag for audit).
func CountsMatch(est, provider int) bool {
	if provider <= 0 {
		return est <= 2 // tiny generations: provider may legitimately report 0
	}
	d := est - provider
	if d < 0 {
		d = -d
	}
	return float64(d) <= 0.25*float64(provider)
}

// ---- memory implementation ----

type memUser struct {
	id    int64
	email string
	role  string
}

// MemoryBilling is an in-memory Billing for dev/tests. Nothing survives a
// restart — never use it where money is real.
type MemoryBilling struct {
	mu      sync.Mutex
	nextID  int64
	users   map[int64]memUser
	byEmail map[string]int64
	keys    map[string]APIKeyAuth // key_hash -> auth
	usage   map[string]UsageEvent // request_id -> event (idempotency)
}

func NewMemoryBilling() *MemoryBilling {
	return &MemoryBilling{
		nextID:  1,
		users:   make(map[int64]memUser),
		byEmail: make(map[string]int64),
		keys:    make(map[string]APIKeyAuth),
		usage:   make(map[string]UsageEvent),
	}
}

func (m *MemoryBilling) EnsureUser(_ context.Context, email, role string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byEmail[email]; ok {
		return id, nil
	}
	id := m.nextID
	m.nextID++
	m.users[id] = memUser{id: id, email: email, role: role}
	m.byEmail[email] = id
	return id, nil
}

func (m *MemoryBilling) CreateAPIKey(_ context.Context, userID int64, keyHash, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	m.keys[keyHash] = APIKeyAuth{UserID: userID, Email: u.email, Role: u.role}
	_ = label
	return nil
}

func (m *MemoryBilling) ResolveAPIKey(_ context.Context, keyHash string) (APIKeyAuth, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.keys[keyHash]
	return a, ok, nil
}

func (m *MemoryBilling) RecordUsage(_ context.Context, e UsageEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.usage[e.RequestID]; dup {
		return nil // idempotent
	}
	m.usage[e.RequestID] = e
	return nil
}

// UsageCount exposes how many usage rows are recorded (tests).
func (m *MemoryBilling) UsageCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.usage)
}

type BizError string

func (e BizError) Error() string { return string(e) }

const ErrNotFound = BizError("not found")
