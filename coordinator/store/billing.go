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
	"fmt"
	"strings"
	"sync"
	"time"
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

// Billing is the persistence surface for auth + metering + settlement.
type Billing interface {
	// EnsureUser creates the user if missing, returns the user id.
	EnsureUser(ctx context.Context, email, role string) (int64, error)
	// CreateAPIKey stores a key hash for a user.
	CreateAPIKey(ctx context.Context, userID int64, keyHash, label string) error
	// ResolveAPIKey maps a key hash back to its user (auth).
	ResolveAPIKey(ctx context.Context, keyHash string) (APIKeyAuth, bool, error)
	// RecordUsage inserts the metering row; idempotent on RequestID.
	RecordUsage(ctx context.Context, e UsageEvent) error

	// --- Phase 2: pricing + settlement ---

	// GetModelPrice returns micro-USD per 1M tokens (in, out).
	// ErrNotFound when no override row exists (caller applies fallback).
	GetModelPrice(ctx context.Context, model string) (inMicro, outMicro int64, err error)
	// UpsertModelPrice sets an override price for a model.
	UpsertModelPrice(ctx context.Context, model string, inMicro, outMicro int64) error
	// SettleRequest records the money for one request in a single
	// transaction. Idempotent on SettleParams.RequestID: a second call for
	// the same request returns AlreadySettled and changes nothing.
	SettleRequest(ctx context.Context, p SettleParams, gross, providerCredit, platformFee int64) (SettleResult, error)
	// UsageRows lists recent metering rows (newest first). userID nil = all.
	UsageRows(ctx context.Context, userID *int64, limit int) ([]UsageRow, error)
	// ListModelPrices returns active per-model overrides.
	ListModelPrices(ctx context.Context) ([]ModelPriceRow, error)
	// AccountBalance sums ledger entries for one account kind
	// (developer_balance | provider_earnings | platform_revenue).
	// userID nil = platform-level accounts.
	AccountBalance(ctx context.Context, userID *int64, kind string) (int64, error)
}

// SettleParams selects the request to settle; tokens are the SETTLED counts
// (chosen by the tolerance rule, not necessarily the provider's raw report).
type SettleParams struct {
	RequestID    string
	UserID       *int64 // developer; nil = admin traffic (still settles platform side)
	NodeID       string
	Model        string
	InputTokens  int
	OutputTokens int
}

// SettleResult reports the money movement for one settlement.
type SettleResult struct {
	Gross          int64 // micro-USD charged to the developer
	ProviderCredit int64 // micro-USD accrued to the provider (90%)
	PlatformFee    int64 // micro-USD kept by the platform (10%)
	AlreadySettled bool
}

// UsageRow is a metering row as returned to consoles.
type UsageRow struct {
	RequestID       string    `json:"request_id"`
	UserID          *int64    `json:"user_id"`
	NodeID          string    `json:"node_id"`
	Model           string    `json:"model"`
	EstInput        int       `json:"est_input_tokens"`
	EstOutput       int       `json:"est_output_tokens"`
	ProviderInput   *int      `json:"provider_input_tokens"`
	ProviderOutput  *int      `json:"provider_output_tokens"`
	WithinTolerance bool      `json:"counts_within_tolerance"`
	Gross           int64     `json:"gross_micro"`
	ProviderCredit  int64     `json:"provider_credit_micro"`
	PlatformFee     int64     `json:"platform_fee_micro"`
	Settled         bool      `json:"settled"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

// Default rates when a model has no override (micro-USD per 1M tokens):
// $0.05 / 1M input, $0.20 / 1M output — roughly half of hosted-API list.
const (
	DefaultInputMicroPer1M  int64 = 50_000
	DefaultOutputMicroPer1M int64 = 200_000
	MinChargeMicro          int64 = 100 // $0.0001 per request
)

// PriceFor resolves rates for a model: DB override, else defaults.
func PriceFor(ctx context.Context, b Billing, model string) (inRate, outRate int64, err error) {
	in, out, err := b.GetModelPrice(ctx, model)
	if err != nil || in <= 0 || out <= 0 {
		return DefaultInputMicroPer1M, DefaultOutputMicroPer1M, nil
	}
	return in, out, nil
}

// GrossMicro computes the charge for a request. Rates are micro-USD per 1M
// tokens; per-request components round UP so tiny requests still bill, and
// a floor (MinChargeMicro) applies whenever any tokens were produced.
func GrossMicro(inTokens, outTokens, inRate, outRate int64) int64 {
	if inTokens <= 0 && outTokens <= 0 {
		return 0
	}
	gross := ceilDiv(inTokens*inRate, 1_000_000) + ceilDiv(outTokens*outRate, 1_000_000)
	if gross < MinChargeMicro {
		gross = MinChargeMicro
	}
	return gross
}

func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// SplitProviderFee divides gross into (providerCredit, platformFee) at
// feePercent (0-100). Provider gets the remainder so the split always
// sums exactly to gross.
func SplitProviderFee(gross int64, feePercent int) (providerCredit, platformFee int64) {
	if feePercent < 0 {
		feePercent = 0
	}
	if feePercent > 100 {
		feePercent = 100
	}
	fee := gross * int64(feePercent) / 100
	return gross - fee, fee
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
	mu       sync.Mutex
	nextID   int64
	users    map[int64]memUser
	byEmail  map[string]int64
	keys     map[string]APIKeyAuth // key_hash -> auth
	usage    map[string]UsageEvent // request_id -> event (idempotency)
	prices   map[string][2]int64   // model -> [in, out] micro per 1M
	settled  map[string]bool       // request_id -> settled (idempotency)
	balances map[string]int64      // "userID:kind" | "platform:kind" -> micro
}

func NewMemoryBilling() *MemoryBilling {
	return &MemoryBilling{
		nextID:   1,
		users:    make(map[int64]memUser),
		byEmail:  make(map[string]int64),
		keys:     make(map[string]APIKeyAuth),
		usage:    make(map[string]UsageEvent),
		prices:   make(map[string][2]int64),
		settled:  make(map[string]bool),
		balances: make(map[string]int64),
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

func (m *MemoryBilling) GetModelPrice(_ context.Context, model string) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr, ok := m.prices[model]
	if !ok {
		return 0, 0, ErrNotFound
	}
	return pr[0], pr[1], nil
}

func (m *MemoryBilling) UpsertModelPrice(_ context.Context, model string, inMicro, outMicro int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prices[model] = [2]int64{inMicro, outMicro}
	return nil
}

func (m *MemoryBilling) SettleRequest(_ context.Context, p SettleParams, gross, providerCredit, platformFee int64) (SettleResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.settled[p.RequestID]; dup {
		return SettleResult{AlreadySettled: true}, nil
	}
	m.settled[p.RequestID] = true
	if p.UserID != nil && gross > 0 {
		m.balances[fmt.Sprintf("%d:developer_balance", *p.UserID)] -= gross
	}
	if providerCredit > 0 {
		m.balances["platform:provider_earnings"] += providerCredit
	}
	if platformFee > 0 {
		m.balances["platform:platform_revenue"] += platformFee
	}
	return SettleResult{Gross: gross, ProviderCredit: providerCredit, PlatformFee: platformFee}, nil
}

func (m *MemoryBilling) UsageRows(_ context.Context, userID *int64, limit int) ([]UsageRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []UsageRow
	for _, e := range m.usage {
		if userID != nil && (e.UserID == nil || *e.UserID != *userID) {
			continue
		}
		out = append(out, UsageRow{
			RequestID: e.RequestID, UserID: e.UserID, NodeID: e.NodeID,
			Model: e.Model, EstInput: e.EstInputTokens, EstOutput: e.EstOutputTokens,
			ProviderInput: e.ProviderInput, ProviderOutput: e.ProviderOutput,
			WithinTolerance: e.WithinTolerance, Status: e.Status,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *MemoryBilling) ListModelPrices(_ context.Context) ([]ModelPriceRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ModelPriceRow, 0, len(m.prices))
	for model, pr := range m.prices {
		out = append(out, ModelPriceRow{Model: model, InMicro: pr[0], OutMicro: pr[1]})
	}
	return out, nil
}

func (m *MemoryBilling) AccountBalance(_ context.Context, userID *int64, kind string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for _, e := range m.usage {
		_ = e
	}
	// memory impl tracks balances inline during settle
	for key, bal := range m.balances {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 || parts[1] != kind {
			continue
		}
		if userID != nil && parts[0] != fmt.Sprintf("%d", *userID) {
			continue
		}
		if userID == nil && parts[0] != "platform" {
			continue
		}
		sum += bal
	}
	return sum, nil
}

func (m *MemoryBilling) SettledCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.settled)
}

type BizError string

func (e BizError) Error() string { return string(e) }

const ErrNotFound = BizError("not found")

// ModelPriceRow is one priced model as returned to consoles/admin.
type ModelPriceRow struct {
	Model    string `json:"model"`
	InMicro  int64  `json:"input_micro_per_1m"`
	OutMicro int64  `json:"output_micro_per_1m"`
}

func (p *PostgresBilling) ListModelPrices(ctx context.Context) ([]ModelPriceRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT model_id, input_micro_per_1m, output_micro_per_1m
		FROM model_prices WHERE active ORDER BY model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelPriceRow
	for rows.Next() {
		var r ModelPriceRow
		if err := rows.Scan(&r.Model, &r.InMicro, &r.OutMicro); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AccountBalance sums the ledger for one account. userID nil = platform-level
// accounts (escrow/revenue). Missing account = 0.
func (p *PostgresBilling) AccountBalance(ctx context.Context, userID *int64, kind string) (int64, error) {
	var sum int64
	err := p.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(le.amount_micro), 0)
		FROM ledger_entries le
		JOIN accounts a ON a.id = le.account_id
		WHERE a.kind = $2 AND ($1::bigint IS NULL OR a.user_id = $1)`,
		userID, kind).Scan(&sum)
	return sum, err
}

// ---- Phase 3: console ----

// SetPasswordHash stores a bcrypt hash for a user (console login).
func (p *PostgresBilling) SetPasswordHash(ctx context.Context, userID int64, hash string) error {
	_, err := p.pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, userID, hash)
	return err
}

func (p *PostgresBilling) GetUserByEmail(ctx context.Context, email string) (id int64, role, passwordHash string, err error) {
	err = p.pool.QueryRow(ctx,
		`SELECT id, role, COALESCE(password_hash,'') FROM users WHERE email=$1`, email).
		Scan(&id, &role, &passwordHash)
	return
}

// CreateSession stores a session token hash. Caller supplies the raw token
// and expiry; we persist only the hash.
func (p *PostgresBilling) CreateSession(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1,$2,$3)`,
		tokenHash, userID, expiresAt)
	return err
}

// ResolveSession maps a session token hash to its user if unexpired.
func (p *PostgresBilling) ResolveSession(ctx context.Context, tokenHash string) (APIKeyAuth, bool, error) {
	var a APIKeyAuth
	err := p.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.role
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash=$1 AND s.expires_at > now()`, tokenHash).Scan(&a.UserID, &a.Email, &a.Role)
	if err != nil {
		return APIKeyAuth{}, false, nil
	}
	return a, true, nil
}

func (p *PostgresBilling) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

func (p *PostgresBilling) CreateAPIKeyWithID(ctx context.Context, userID int64, keyHash, label string) (int64, error) {
	var id int64
	err := p.pool.QueryRow(ctx,
		`INSERT INTO api_keys (user_id, key_hash, label) VALUES ($1,$2,$3) RETURNING id`,
		userID, keyHash, label).Scan(&id)
	return id, err
}

func (p *PostgresBilling) ListAPIKeys(ctx context.Context, userID int64) ([]KeyRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, label, created_at, revoked_at IS NOT NULL
		FROM api_keys WHERE user_id=$1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyRow
	for rows.Next() {
		var r KeyRow
		if err := rows.Scan(&r.ID, &r.Label, &r.CreatedAt, &r.Revoked); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *PostgresBilling) RevokeAPIKey(ctx context.Context, userID, keyID int64) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND user_id=$2`, keyID, userID)
	return err
}

func (p *PostgresBilling) ListUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT id, email, role, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserRow
	for rows.Next() {
		var r UserRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Role, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
