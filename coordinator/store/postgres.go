package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

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

	// provider credit → platform escrow until node enrollment
	if providerCredit > 0 {
		escrow, err := p.ensureAccountTx(ctx, tx, nil, "provider_earnings")
		if err != nil {
			return SettleResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, entry_type, amount_micro, ref_type, ref_id)
			VALUES ($1, 'provider_earning', $2, 'request', $3)
			ON CONFLICT DO NOTHING`,
			escrow, providerCredit, prm.RequestID); err != nil {
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
