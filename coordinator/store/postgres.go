package store

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// PostgresBilling is the production Billing implementation.
type PostgresBilling struct {
	pool *pgxpool.Pool
}

// NewPostgresBilling connects, runs embedded migrations, and returns the store.
func NewPostgresBilling(ctx context.Context, databaseURL string) (*PostgresBilling, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	b := &PostgresBilling{pool: pool}
	if err := b.migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return b, nil
}

func (p *PostgresBilling) migrate(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		var version int
		if _, err := fmt.Sscanf(name, "migrations/%d_", &version); err != nil {
			return fmt.Errorf("migration filename must start with a version number: %s", name)
		}
		var applied bool
		err := p.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		sqlBytes, err := migrationsFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := p.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (p *PostgresBilling) EnsureUser(ctx context.Context, email, role string) (int64, error) {
	var id int64
	err := p.pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ($1, $2)
		ON CONFLICT (email) DO UPDATE SET role = EXCLUDED.role
		RETURNING id`, email, role).Scan(&id)
	return id, err
}

func (p *PostgresBilling) CreateAPIKey(ctx context.Context, userID int64, keyHash, label string) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO api_keys (user_id, key_hash, label) VALUES ($1, $2, $3)`,
		userID, keyHash, label)
	return err
}

func (p *PostgresBilling) ResolveAPIKey(ctx context.Context, keyHash string) (APIKeyAuth, bool, error) {
	var a APIKeyAuth
	err := p.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.role
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = $1 AND k.revoked_at IS NULL`, keyHash).
		Scan(&a.UserID, &a.Email, &a.Role)
	if err != nil {
		return APIKeyAuth{}, false, nil // not found or error: treat as no-match
	}
	return a, true, nil
}

func (p *PostgresBilling) RecordUsage(ctx context.Context, e UsageEvent) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO usage_events
			(request_id, user_id, node_id, model,
			 est_input_tokens, est_output_tokens,
			 provider_input_tokens, provider_output_tokens,
			 counts_within_tolerance, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (request_id) DO NOTHING`,
		e.RequestID, e.UserID, e.NodeID, e.Model,
		e.EstInputTokens, e.EstOutputTokens,
		e.ProviderInput, e.ProviderOutput,
		e.WithinTolerance, e.Status)
	return err
}

// ---- Phase 2: pricing + settlement ----

func (p *PostgresBilling) GetModelPrice(ctx context.Context, model string) (int64, int64, error) {
	var in, out int64
	err := p.pool.QueryRow(ctx,
		`SELECT input_micro_per_1m, output_micro_per_1m FROM model_prices WHERE model_id=$1 AND active`,
		model).Scan(&in, &out)
	if err != nil {
		return 0, 0, ErrNotFound
	}
	return in, out, nil
}

func (p *PostgresBilling) UpsertModelPrice(ctx context.Context, model string, inMicro, outMicro int64) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO model_prices (model_id, input_micro_per_1m, output_micro_per_1m)
		VALUES ($1, $2, $3)
		ON CONFLICT (model_id) DO UPDATE SET
			input_micro_per_1m = EXCLUDED.input_micro_per_1m,
			output_micro_per_1m = EXCLUDED.output_micro_per_1m`,
		model, inMicro, outMicro)
	return err
}

// platformAccountID caches the platform revenue/escrow account ids.
func (p *PostgresBilling) ensureAccountTx(ctx context.Context, tx pgx.Tx, userNullable *int64, kind string) (int64, error) {
	var accountID int64
	if userNullable != nil {
		err := tx.QueryRow(ctx, `
			INSERT INTO accounts (user_id, kind) VALUES ($1, $2)
			ON CONFLICT (user_id, kind) DO UPDATE SET kind = EXCLUDED.kind
			RETURNING id`, *userNullable, kind).Scan(&accountID)
		return accountID, err
	}
	// platform accounts anchor to the seeded platform user
	var platformUserID int64
	if err := tx.QueryRow(ctx,
		`SELECT id FROM users WHERE email='platform@idlegrid.system'`).Scan(&platformUserID); err != nil {
		return 0, fmt.Errorf("platform user missing: %w", err)
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO accounts (user_id, kind) VALUES ($1, $2)
		ON CONFLICT (user_id, kind) DO UPDATE SET kind = EXCLUDED.kind
		RETURNING id`, platformUserID, kind).Scan(&accountID)
	return accountID, err
}

// SettleRequest records the money for one request atomically:
//   - developer debit (skipped for admin traffic)
//   - provider credit into platform escrow (transferred to the provider's
//     account when node enrollment ships in Phase 3)
//   - platform fee revenue
//
// Idempotent: the usage_events row is the guard; a second call is a no-op.
func (p *PostgresBilling) SettleRequest(ctx context.Context, prm SettleParams, gross, providerCredit, platformFee int64) (SettleResult, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return SettleResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var already bool
	if err := tx.QueryRow(ctx,
		`SELECT settled FROM usage_events WHERE request_id=$1`, prm.RequestID).Scan(&already); err != nil {
		if err.Error() == "no rows in result set" {
			return SettleResult{}, fmt.Errorf("usage event missing for %s", prm.RequestID)
		}
		return SettleResult{}, err
	}
	if already {
		return SettleResult{AlreadySettled: true}, nil
	}

	// developer debit
	if prm.UserID != nil && gross > 0 {
		devAccount, err := p.ensureAccountTx(ctx, tx, prm.UserID, "developer_balance")
		if err != nil {
			return SettleResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, entry_type, amount_micro, ref_type, ref_id)
			VALUES ($1, 'usage_charge', $2, 'request', $3)
			ON CONFLICT DO NOTHING`,
			devAccount, -gross, prm.RequestID); err != nil {
			return SettleResult{}, err
		}
	}

	// provider credit: directly to the enrolled owner, else platform escrow
	// until the node is enrolled (transferred at enrollment time).
	var nodeOwner *int64
	if prm.NodeID != "" {
		nodeOwner, _ = p.NodeOwner(ctx, prm.NodeID)
	}
	if providerCredit > 0 {
		var creditAcct int64
		var err error
		transferred := false
		if nodeOwner != nil {
			creditAcct, err = p.ensureAccountTx(ctx, tx, nodeOwner, "provider_earnings")
			transferred = true
		} else {
			creditAcct, err = p.ensureAccountTx(ctx, tx, nil, "provider_earnings")
		}
		if err != nil {
			return SettleResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, entry_type, amount_micro, ref_type, ref_id)
			VALUES ($1, 'provider_earning', $2, 'request', $3)
			ON CONFLICT DO NOTHING`,
			creditAcct, providerCredit, prm.RequestID); err != nil {
			return SettleResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE usage_events SET transferred=$2 WHERE request_id=$1`,
			prm.RequestID, transferred); err != nil {
			return SettleResult{}, err
		}
	}

	// platform fee revenue
	if platformFee > 0 {
		rev, err := p.ensureAccountTx(ctx, tx, nil, "platform_revenue")
		if err != nil {
			return SettleResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, entry_type, amount_micro, ref_type, ref_id)
			VALUES ($1, 'platform_fee', $2, 'request', $3)
			ON CONFLICT DO NOTHING`,
			rev, platformFee, prm.RequestID); err != nil {
			return SettleResult{}, err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE usage_events
		SET gross_micro=$2, provider_credit_micro=$3, platform_fee_micro=$4, settled=true
		WHERE request_id=$1`,
		prm.RequestID, gross, providerCredit, platformFee); err != nil {
		return SettleResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return SettleResult{}, err
	}
	return SettleResult{Gross: gross, ProviderCredit: providerCredit, PlatformFee: platformFee}, nil
}

func (p *PostgresBilling) UsageRows(ctx context.Context, userID *int64, limit int) ([]UsageRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `
		SELECT request_id, user_id, COALESCE(node_id,''), model,
		       est_input_tokens, est_output_tokens,
		       provider_input_tokens, provider_output_tokens,
		       counts_within_tolerance,
		       gross_micro, provider_credit_micro, platform_fee_micro,
		       settled, status, created_at
		FROM usage_events
		WHERE ($1::bigint IS NULL OR user_id = $1)
		ORDER BY id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.RequestID, &r.UserID, &r.NodeID, &r.Model,
			&r.EstInput, &r.EstOutput, &r.ProviderInput, &r.ProviderOutput,
			&r.WithinTolerance, &r.Gross, &r.ProviderCredit, &r.PlatformFee,
			&r.Settled, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- payouts ----

func (p *PostgresBilling) CreatePayoutRequest(ctx context.Context, userID int64, amountMicro int64) (int64, error) {
	var id int64
	err := p.pool.QueryRow(ctx, `
		INSERT INTO payouts (user_id, amount_micro) VALUES ($1, $2) RETURNING id`,
		userID, amountMicro).Scan(&id)
	return id, err
}

func (p *PostgresBilling) ListPayouts(ctx context.Context, userID *int64) ([]PayoutRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, user_id, amount_micro, status, rail, COALESCE(rail_ref,''), created_at, paid_at
		FROM payouts
		WHERE ($1::bigint IS NULL OR user_id = $1)
		ORDER BY id DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PayoutRow
	for rows.Next() {
		var r PayoutRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.AmountMicro, &r.Status, &r.Rail, &r.RailRef, &r.CreatedAt, &r.PaidAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *PostgresBilling) MarkPayout(ctx context.Context, payoutID int64, status, rail, railRef string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE payouts SET status=$2, rail=$3, rail_ref=$4,
			paid_at = CASE WHEN $2='paid' THEN now() ELSE paid_at END
		WHERE id=$1`, payoutID, status, rail, railRef)
	return err
}

// SettlePayout marks a payout paid AND records the escrow debit in one tx.
// The payout leaves the platform's provider_earnings escrow.
func (p *PostgresBilling) SettlePayout(ctx context.Context, payoutID int64, rail, railRef string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID int64
	var amount int64
	var status string
	err = tx.QueryRow(ctx,
		`SELECT user_id, amount_micro, status FROM payouts WHERE id=$1`, payoutID).
		Scan(&userID, &amount, &status)
	if err != nil {
		return err
	}
	if status == "paid" {
		return nil // already settled
	}

	owner := userID
	payoutAcct, err := p.ensureAccountTx(ctx, tx, &owner, "provider_earnings")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (account_id, entry_type, amount_micro, ref_type, ref_id)
		VALUES ($1, 'payout', $2, 'payout', $3)
		ON CONFLICT DO NOTHING`,
		payoutAcct, -amount, fmt.Sprintf("%d", payoutID)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payouts SET status='paid', rail=$2, rail_ref=$3, paid_at=now() WHERE id=$1`,
		payoutID, rail, railRef); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- Phase 4: enrollment + deposits ----

func randomCode(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *PostgresBilling) GetOrCreateEnrollmentCode(ctx context.Context, userID int64) (string, error) {
	var code *string
	if err := p.pool.QueryRow(ctx,
		`SELECT enrollment_code FROM users WHERE id=$1`, userID).Scan(&code); err != nil {
		return "", err
	}
	if code != nil && *code != "" {
		return *code, nil
	}
	newCode := randomCode(6) // 12 hex chars
	var id int64
	err := p.pool.QueryRow(ctx, `
		UPDATE users SET enrollment_code=$2 WHERE id=$1 AND enrollment_code IS NULL RETURNING id`,
		userID, newCode).Scan(&id)
	if err != nil {
		// concurrent generation won the race — read the winner
		if err2 := p.pool.QueryRow(ctx,
			`SELECT enrollment_code FROM users WHERE id=$1`, userID).Scan(&code); err2 != nil || code == nil {
			return "", err
		}
		return *code, nil
	}
	return newCode, nil
}

func (p *PostgresBilling) EnrollNode(ctx context.Context, code, nodeID string) (int64, bool, error) {
	var userID int64
	err := p.pool.QueryRow(ctx,
		`SELECT id FROM users WHERE enrollment_code=$1`, code).Scan(&userID)
	if err != nil {
		return 0, false, nil // unknown code
	}
	if err := p.BindNode(ctx, userID, nodeID, ""); err != nil {
		return 0, false, err
	}
	return userID, true, nil
}

// BindNode binds a node to an account and, in the same transaction, sweeps
// any earnings the node accrued into platform escrow while unenrolled.
func (p *PostgresBilling) BindNode(ctx context.Context, userID int64, nodeID, tokenHash string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO provider_nodes (node_id, user_id, enrolled_at, last_seen, bound_with_token)
		VALUES ($1, $2, now(), now(), NULLIF($3, ''))
		ON CONFLICT (node_id) DO UPDATE SET
			user_id=EXCLUDED.user_id, enrolled_at=now(), last_seen=now(),
			bound_with_token=EXCLUDED.bound_with_token`,
		nodeID, userID, tokenHash); err != nil {
		return err
	}

	// Sweep: escrowed provider credits for this node's untransferred events.
	var escrowed int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(le.amount_micro), 0)
		FROM usage_events ue
		JOIN ledger_entries le ON le.ref_type='request' AND le.ref_id=ue.request_id
			AND le.entry_type='provider_earning'
		JOIN accounts a ON a.id=le.account_id AND a.kind='provider_earnings'
		JOIN users pu ON pu.id=a.user_id AND pu.email='platform@idlegrid.system'
		WHERE ue.node_id=$1 AND NOT ue.transferred`, nodeID).Scan(&escrowed); err != nil {
		return err
	}
	if escrowed > 0 {
		escrowAcct, err := p.ensureAccountTx(ctx, tx, nil, "provider_earnings")
		if err != nil {
			return err
		}
		ownerAcct, err := p.ensureAccountTx(ctx, tx, &userID, "provider_earnings")
		if err != nil {
			return err
		}
		// Double-entry: out of escrow, into the owner. ON CONFLICT guards a
		// retried bind from double-moving the money.
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, entry_type, amount_micro, ref_type, ref_id)
			VALUES ($1, 'enrollment_sweep', $2, 'node', $3) ON CONFLICT DO NOTHING`,
			escrowAcct, -escrowed, nodeID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, entry_type, amount_micro, ref_type, ref_id)
			VALUES ($1, 'enrollment_sweep', $2, 'node', $3) ON CONFLICT DO NOTHING`,
			ownerAcct, escrowed, nodeID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE usage_events ue SET transferred=true
			FROM ledger_entries le, accounts a, users pu
			WHERE le.ref_type='request' AND le.ref_id=ue.request_id
				AND le.entry_type='provider_earning' AND le.account_id=a.id
				AND a.user_id=pu.id AND pu.email='platform@idlegrid.system'
				AND a.kind='provider_earnings'
				AND ue.node_id=$1 AND NOT ue.transferred`, nodeID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *PostgresBilling) ListProviderNodes(ctx context.Context, userID int64) ([]ProviderNodeRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT node_id, enrolled_at, last_seen, error_count,
			(bound_with_token IS NOT NULL AND bound_with_token <> '') AS token_backed
		FROM provider_nodes WHERE user_id=$1 ORDER BY enrolled_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderNodeRow{}
	for rows.Next() {
		var r ProviderNodeRow
		if err := rows.Scan(&r.NodeID, &r.EnrolledAt, &r.LastSeen, &r.ErrorCount, &r.TokenBacked); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *PostgresBilling) RevokeNode(ctx context.Context, userID int64, nodeID string) (bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tokenHash *string
	err = tx.QueryRow(ctx, `
		DELETE FROM provider_nodes WHERE node_id=$1 AND user_id=$2
		RETURNING bound_with_token`, nodeID, userID).Scan(&tokenHash)
	if err != nil {
		return false, nil // not bound to this user
	}
	if tokenHash != nil && *tokenHash != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE provider_tokens SET revoked=true WHERE token_hash=$1`, *tokenHash); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (p *PostgresBilling) TouchNode(ctx context.Context, nodeID string) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE provider_nodes SET last_seen=now() WHERE node_id=$1`, nodeID)
	return err
}

func (p *PostgresBilling) NodeOwner(ctx context.Context, nodeID string) (*int64, error) {
	var owner *int64
	err := p.pool.QueryRow(ctx,
		`SELECT user_id FROM provider_nodes WHERE node_id=$1`, nodeID).Scan(&owner)
	if err != nil {
		return nil, nil // no row = unassigned
	}
	return owner, nil
}

// ---- Phase 5: device authorization ----

func (p *PostgresBilling) CreateDeviceCode(ctx context.Context, deviceCodeHash, userCode string, expiresAt time.Time) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO device_codes (device_code_hash, user_code, expires_at) VALUES ($1, $2, $3)`,
		deviceCodeHash, userCode, expiresAt)
	return err
}

func (p *PostgresBilling) ApproveDeviceCode(ctx context.Context, userCode string, userID int64) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
		UPDATE device_codes SET user_id=$2, approved_at=now()
		WHERE user_code=$1 AND approved_at IS NULL AND expires_at > now()`,
		userCode, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (p *PostgresBilling) RedeemDeviceCode(ctx context.Context, deviceCodeHash, providerTokenHash string) (int64, string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID *int64
	var expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT user_id, expires_at, consumed_at FROM device_codes
		WHERE device_code_hash=$1 FOR UPDATE`, deviceCodeHash).
		Scan(&userID, &expiresAt, &consumedAt)
	if err != nil {
		return 0, "invalid", nil // unknown device code
	}
	if consumedAt != nil {
		return 0, "invalid", nil // token already issued — client must store it
	}
	if time.Now().After(expiresAt) {
		return 0, "expired", nil
	}
	if userID == nil {
		return 0, "pending", nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE device_codes SET consumed_at=now() WHERE device_code_hash=$1`,
		deviceCodeHash); err != nil {
		return 0, "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO provider_tokens (token_hash, user_id, label) VALUES ($1, $2, 'device login')`,
		providerTokenHash, *userID); err != nil {
		return 0, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, "", err
	}
	return *userID, "ok", nil
}

func (p *PostgresBilling) ResolveProviderToken(ctx context.Context, tokenHash string) (int64, string, bool, error) {
	var userID int64
	var email string
	err := p.pool.QueryRow(ctx, `
		SELECT pt.user_id, u.email FROM provider_tokens pt
		JOIN users u ON u.id = pt.user_id
		WHERE pt.token_hash=$1 AND NOT pt.revoked`, tokenHash).Scan(&userID, &email)
	if err != nil {
		return 0, "", false, nil // unknown or revoked token
	}
	return userID, email, true, nil
}

// DodoCredit records a completed deposit and credits the developer balance.
// Idempotent on the Dodo payment id.
func (p *PostgresBilling) DodoCredit(ctx context.Context, dodoPaymentID string, userID int64, amountMicro int64, raw []byte) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var already bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM dodo_payments WHERE dodo_payment_id=$1)`, dodoPaymentID).Scan(&already); err != nil {
		return err
	}
	if already {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO dodo_payments (dodo_payment_id, user_id, amount_micro, status, raw)
		VALUES ($1, $2, $3, 'succeeded', $4)`,
		dodoPaymentID, userID, amountMicro, raw); err != nil {
		return err
	}

	devAccount, err := p.ensureAccountTx(ctx, tx, &userID, "developer_balance")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (account_id, entry_type, amount_micro, ref_type, ref_id)
		VALUES ($1, 'deposit', $2, 'payment', $3)
		ON CONFLICT DO NOTHING`,
		devAccount, amountMicro, dodoPaymentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
