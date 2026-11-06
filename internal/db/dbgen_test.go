package db_test

import (
	"context"
	"testing"

	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/testdb"
)

// Dates must round-trip as YYYY-MM-DD strings through pgx and sqlc.
func TestDatesRoundTripAsStrings(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := dbgen.New(pool)

	if _, err := q.CreateUser(ctx, dbgen.CreateUserParams{ID: "u1", Name: "Ann", Email: "Ann@Example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertFund(ctx, dbgen.InsertFundParams{ID: "f1", UserID: "u1", Name: "Fund", Currency: "KES", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertItem(ctx, dbgen.InsertItemParams{ID: "i1", FundID: "f1", Name: "Laptop", Price: 1000, Deadline: "2027-02-28", PlanStart: "2026-11-06", Position: 0}); err != nil {
		t.Fatal(err)
	}
	items, err := q.ListItems(ctx, "f1")
	if err != nil || len(items) != 1 {
		t.Fatalf("list items: %v %d", err, len(items))
	}
	if items[0].Deadline != "2027-02-28" || items[0].PlanStart != "2026-11-06" || items[0].PurchasedOn != "" {
		t.Fatalf("unexpected item: %+v", items[0])
	}

	if err := q.InsertSkip(ctx, dbgen.InsertSkipParams{FundID: "f1", Date: "2026-12-31"}); err != nil {
		t.Fatal(err)
	}
	skips, err := q.ListSkipsFrom(ctx, dbgen.ListSkipsFromParams{FundID: "f1", FromDate: "2026-12-01"})
	if err != nil || len(skips) != 1 || skips[0] != "2026-12-31" {
		t.Fatalf("skips: %v %v", skips, err)
	}

	// Email lookups are case-insensitive and emails are stored lower-cased.
	u, err := q.GetUserByEmail(ctx, "ANN@example.COM")
	if err != nil || u.Email != "ann@example.com" {
		t.Fatalf("user: %+v %v", u, err)
	}

	// Reminder pref with a nullable date.
	if err := q.UpsertReminderPref(ctx, dbgen.UpsertReminderPrefParams{FundID: "f1", UserID: "u1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	pref, err := q.GetReminderPref(ctx, dbgen.GetReminderPrefParams{FundID: "f1", UserID: "u1"})
	if err != nil || !pref.Enabled || pref.LastReminderOn != "" {
		t.Fatalf("pref: %+v %v", pref, err)
	}
	if err := q.MarkReminderSent(ctx, dbgen.MarkReminderSentParams{FundID: "f1", UserID: "u1", SentOn: "2026-11-06"}); err != nil {
		t.Fatal(err)
	}
	pref, _ = q.GetReminderPref(ctx, dbgen.GetReminderPrefParams{FundID: "f1", UserID: "u1"})
	if pref.LastReminderOn != "2026-11-06" {
		t.Fatalf("last reminder: %+v", pref)
	}
}

func TestReminderRecipientsQuery(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := dbgen.New(pool)
	if _, err := q.CreateUser(ctx, dbgen.CreateUserParams{ID: "u1", Name: "Ann", Email: "ann@example.com", EmailVerified: true}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertFund(ctx, dbgen.InsertFundParams{ID: "f1", UserID: "u1", Name: "Fund", Currency: "KES", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertReminderPref(ctx, dbgen.UpsertReminderPrefParams{FundID: "f1", UserID: "u1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rows, err := q.ListReminderRecipients(ctx)
	if err != nil || len(rows) != 1 || rows[0].LastReminderOn != "" || rows[0].Email != "ann@example.com" {
		t.Fatalf("recipients: %+v %v", rows, err)
	}
}
