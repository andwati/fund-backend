// Package testdb gives each test its own migrated Postgres database.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/andwati/fund-backend/internal/db"
)

var counter atomic.Int64

// New creates an empty database on the TEST_DATABASE_URL server, runs the migrations and
// returns a pool. The test is skipped when TEST_DATABASE_URL is unset.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("fund_t_%d_%d", time.Now().UnixNano(), counter.Add(1))
	if _, err := admin.Exec(`create database ` + name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name

	mig, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Up(ctx, mig); err != nil {
		t.Fatal(err)
	}
	mig.Close()

	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(`drop database ` + name + ` with (force)`)
		admin.Close()
	})
	return pool
}
