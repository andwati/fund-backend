// Package db holds database access: migrations, sqlc queries and pool setup.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// AdvisoryLockKey is shared with the Next.js app (src/instrumentation-node.ts) so both
// sides serialise migrations while they run against the same database.
const AdvisoryLockKey int64 = 724001

// BaselineVersion is the goose version that represents the schema at Drizzle migration 0011.
const BaselineVersion int64 = 1

func provider(db *sql.DB) (*goose.Provider, error) {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, sub)
}

// Up applies pending migrations while holding the shared advisory lock.
// On a database that already has the Drizzle schema, run Baseline first.
func Up(ctx context.Context, db *sql.DB) error {
	return withLock(ctx, db, func(_ *sql.Conn) error {
		p, err := provider(db)
		if err != nil {
			return err
		}
		_, err = p.Up(ctx)
		return err
	})
}

// Baseline marks migration 00001 as applied WITHOUT running it. It refuses to run on an
// empty database (use Up there) and is a no-op if the baseline is already recorded.
func Baseline(ctx context.Context, db *sql.DB) error {
	return withLock(ctx, db, func(conn *sql.Conn) error {
		var exists bool
		if err := conn.QueryRowContext(ctx, `select to_regclass('public."user"') is not null`).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errors.New("no existing Fund schema found (public.user missing); run `up` instead of `baseline`")
		}
		p, err := provider(db)
		if err != nil {
			return err
		}
		cur, err := p.GetDBVersion(ctx) // also creates goose_db_version if missing
		if err != nil {
			return err
		}
		if cur >= BaselineVersion {
			return nil
		}
		// goose records versions in goose_db_version; insert version 1 directly.
		if _, err := conn.ExecContext(ctx,
			`insert into goose_db_version (version_id, is_applied) values ($1, true)`, BaselineVersion); err != nil {
			return fmt.Errorf("record baseline: %w", err)
		}
		return nil
	})
}

func withLock(ctx context.Context, db *sql.DB, fn func(*sql.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `select pg_advisory_lock($1)`, AdvisoryLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `select pg_advisory_unlock($1)`, AdvisoryLockKey) //nolint:errcheck
	return fn(conn)
}
