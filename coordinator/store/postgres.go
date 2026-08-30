package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

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
