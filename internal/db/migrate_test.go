package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// newTempDB creates an empty database on the TEST_DATABASE_URL server and returns a handle to it.
func newTempDB(t *testing.T) *sql.DB {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("fund_mig_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`create database ` + name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	conn, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		_, _ = admin.Exec(`drop database ` + name + ` with (force)`)
		admin.Close()
	})
	return conn
}

func tableCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	err := db.QueryRow(`select count(*) from information_schema.tables
		where table_schema='public' and table_name <> 'goose_db_version'`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUpBuildsBaselineOnFreshDatabase(t *testing.T) {
	db := newTempDB(t)
	if err := Up(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if n := tableCount(t, db); n != 14 {
		t.Fatalf("want 14 tables, got %d", n)
	}
	// Running again is a no-op.
	if err := Up(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineRecordsWithoutRunningAndIsIdempotent(t *testing.T) {
	db := newTempDB(t)
	// Simulate an existing Drizzle-built database by running the baseline SQL directly.
	body, err := migrationsFS.ReadFile("migrations/00001_baseline_0011.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.SplitN(strings.SplitN(string(body), "-- +goose Up", 2)[1], "-- +goose Down", 2)[0]
	if _, err := db.Exec(up); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := Baseline(ctx, db); err != nil {
			t.Fatalf("baseline #%d: %v", i, err)
		}
	}
	// Up must not try to recreate existing tables.
	if err := Up(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineRefusesEmptyDatabase(t *testing.T) {
	db := newTempDB(t)
	if err := Baseline(context.Background(), db); err == nil {
		t.Fatal("expected refusal on an empty database")
	}
}
