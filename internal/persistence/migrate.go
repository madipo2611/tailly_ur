package persistence

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_initial.sql
var initialMigration string

//go:embed migrations/002_subscriptions.sql
var subscriptionsMigration string

//go:embed migrations/003_query_indexes.sql
var indexesMigration string

//go:embed migrations/004_template_version.sql
var templateVersionMigration string

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL URL: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	for _, migration := range []struct{ version, sql string }{{"001_initial", initialMigration}, {"002_subscriptions", subscriptionsMigration}, {"003_query_indexes", indexesMigration}, {"004_template_version", templateVersionMigration}} {
		var applied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, migration.version).Scan(&applied); err != nil {
			return err
		}
		if !applied {
			if _, err = tx.Exec(ctx, migration.sql); err != nil {
				return fmt.Errorf("apply %s: %w", migration.version, err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, migration.version); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}
