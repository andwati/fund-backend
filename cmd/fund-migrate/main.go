// Command fund-migrate applies database migrations.
//
//	fund-migrate up        apply pending migrations (fresh databases, tests)
//	fund-migrate baseline  record the Drizzle-0011 baseline as applied on an existing database
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/andwati/fund-backend/internal/db"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "baseline") {
		fmt.Fprintln(os.Stderr, "usage: fund-migrate up|baseline  (reads DATABASE_URL)")
		os.Exit(2)
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	conn, err := sql.Open("pgx", url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx := context.Background()
	switch os.Args[1] {
	case "up":
		err = db.Up(ctx, conn)
	case "baseline":
		err = db.Baseline(ctx, conn)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	fmt.Println("ok")
}
