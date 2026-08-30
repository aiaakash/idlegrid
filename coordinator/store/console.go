package store

import (
	"context"
	"time"
)

// ConsoleStore is the console-facing slice of persistence (Phase 3).
// Implemented by PostgresBilling; the console requires DATABASE_URL.
type ConsoleStore interface {
	SetPasswordHash(ctx context.Context, userID int64, hash string) error
	GetUserByEmail(ctx context.Context, email string) (id int64, role, passwordHash string, err error)
	CreateSession(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error
	ResolveSession(ctx context.Context, tokenHash string) (APIKeyAuth, bool, error)
	DeleteSession(ctx context.Context, tokenHash string) error

	CreateAPIKeyWithID(ctx context.Context, userID int64, keyHash, label string) (int64, error)
	ListAPIKeys(ctx context.Context, userID int64) ([]KeyRow, error)
	RevokeAPIKey(ctx context.Context, userID, keyID int64) error

	ListUsers(ctx context.Context) ([]UserRow, error)
	EnsureUser(ctx context.Context, email, role string) (int64, error)

	CreatePayoutRequest(ctx context.Context, userID int64, amountMicro int64) (int64, error)
	ListPayouts(ctx context.Context, userID *int64) ([]PayoutRow, error)
	MarkPayout(ctx context.Context, payoutID int64, status, rail, railRef string) error
	// SettlePayout marks a payout paid and records the escrow debit atomically.
	SettlePayout(ctx context.Context, payoutID int64, rail, railRef string) error
}

// KeyRow is a listed API key (never the plaintext).
type KeyRow struct {
	ID        int64     `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	Revoked   bool      `json:"revoked"`
}

// UserRow is a console/admin user listing.
type UserRow struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// PayoutRow is one provider payout (request/approval/paid lifecycle).
type PayoutRow struct {
	ID          int64      `json:"id"`
	UserID      int64      `json:"user_id"`
	AmountMicro int64      `json:"amount_micro"`
	Status      string     `json:"status"` // requested | approved | paid
	Rail        string     `json:"rail"`
	RailRef     string     `json:"rail_ref"`
	CreatedAt   time.Time  `json:"created_at"`
	PaidAt      *time.Time `json:"paid_at"`
}
