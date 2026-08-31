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
	// SettlePayout marks a payout paid and records the debit from the
	// provider owner's earnings account, atomically.
	SettlePayout(ctx context.Context, payoutID int64, rail, railRef string) error

	// --- Phase 4: enrollment + deposits ---

	// GetOrCreateEnrollmentCode returns the user's provider enrollment code,
	// generating it on first use.
	GetOrCreateEnrollmentCode(ctx context.Context, userID int64) (string, error)
	// EnrollNode binds a provider node to the user owning the code. Returns
	// the owning user id. Unknown code -> ok=false.
	EnrollNode(ctx context.Context, code, nodeID string) (userID int64, ok bool, err error)
	// NodeOwner returns the account a node is enrolled to (nil = unassigned).
	NodeOwner(ctx context.Context, nodeID string) (*int64, error)
	// DodoCredit records a completed Dodo payment and credits the user's
	// developer balance. Idempotent on dodoPaymentID.
	DodoCredit(ctx context.Context, dodoPaymentID string, userID int64, amountMicro int64, raw []byte) error

	// --- Phase 5: device authorization (RFC 8628-style provider login) ---

	// CreateDeviceCode stores a new login attempt. deviceCodeHash is the
	// hashed CLI polling secret; userCode is the canonical short code the
	// user types in the console.
	CreateDeviceCode(ctx context.Context, deviceCodeHash, userCode string, expiresAt time.Time) error
	// ApproveDeviceCode binds the login attempt to the approving user.
	// ok=false when the code is unknown, expired, or already approved.
	ApproveDeviceCode(ctx context.Context, userCode string, userID int64) (ok bool, err error)
	// RedeemDeviceCode is the CLI's poll. status is "pending" (not yet
	// approved), "expired", "invalid" (unknown or already consumed), or "ok"
	// (approved — providerTokenHash was issued and the attempt consumed).
	RedeemDeviceCode(ctx context.Context, deviceCodeHash, providerTokenHash string) (userID int64, status string, err error)
	// ResolveProviderToken maps a provider token hash to its owner.
	ResolveProviderToken(ctx context.Context, tokenHash string) (userID int64, email string, ok bool, err error)
	// BindNode binds a provider node to a user account (idempotent).
	BindNode(ctx context.Context, userID int64, nodeID string) error
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
