// Package cockroach is the CockroachDB adapter. It is the only place in the
// runner where SQL and pgx are allowed to appear. Domain and app layers
// depend on ports, not on this package.
package cockroach

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies any pending migrations to the target database.
// Safe to call on every startup — goose records applied versions in a
// goose_db_version table and skips anything already at head.
//
// Note: no session locker is used. Goose's Postgres session locker relies on
// pg_advisory_lock, which CockroachDB does not implement. Phase 1 runs a
// single runner instance, so concurrent-migrator races are out of scope.
// If we ever run multiple runners, use a table-based lock or coordinate
// migrations out-of-band.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// goose wants a *sql.DB; pgx's stdlib shim adapts a pgx pool to one.
	// The returned *sql.DB does not own the pool — closing it would close
	// the underlying pool, which we do not want here.
	db := stdlib.OpenDBFromPool(pool)

	subFS, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("scope migrations fs: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, subFS)
	if err != nil {
		return fmt.Errorf("new goose provider: %w", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
