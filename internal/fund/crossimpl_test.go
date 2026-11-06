package fund

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/plan"
	"github.com/andwati/fund-backend/internal/ratelimit"
)

// Node -> Go: the web app's real code wrote the database; Go must read it and produce the same
// payload the web app's own loadFund() returned. Run via `make parity-db`.
func TestPayloadMatchesWebAppOnWebAppData(t *testing.T) {
	dsn, file := os.Getenv("PARITY_DATABASE_URL"), os.Getenv("PARITY_PAYLOADS")
	if dsn == "" || file == "" {
		t.Skip("PARITY_DATABASE_URL / PARITY_PAYLOADS not set (run `make parity-db`)")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		FundID string                    `json:"fundId"`
		Users  map[string]map[string]any `json:"users"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := NewService(Options{Pool: pool, Mailer: &mailLog{}, Limiter: ratelimit.New(), PublicURL: "https://fund.test"})
	q := dbgen.New(pool)

	for userID, want := range golden.Users {
		row, err := q.GetUserByID(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		u := auth.User{ID: row.ID, Name: row.Name, Email: row.Email, EmailVerified: row.EmailVerified, JoinedViaInvite: row.JoinedViaInvite}
		got, err := svc.Payload(ctx, u, golden.FundID)
		if err != nil {
			t.Fatalf("%s: %v", userID, err)
		}
		b, _ := json.Marshal(got)
		var gotAny map[string]any
		_ = json.Unmarshal(b, &gotAny)
		if !reflect.DeepEqual(want, gotAny) {
			wb, _ := json.MarshalIndent(want, "", " ")
			gb, _ := json.MarshalIndent(gotAny, "", " ")
			os.WriteFile(fmt.Sprintf("/tmp/payload-%s-node.json", userID), wb, 0o600)
			os.WriteFile(fmt.Sprintf("/tmp/payload-%s-go.json", userID), gb, 0o600)
			t.Fatalf("%s: payload differs from the web app's. Compare /tmp/payload-%s-node.json and -go.json", userID, userID)
		}
		t.Logf("%s: payload identical (%d items, %d contributions)", userID, len(got.Items), len(got.Contributions))
	}
}

// scenario seeds the same rich fund the Node seeder builds, through Go services.
func seedScenario(t *testing.T, ctx context.Context, svc *Service, q *dbgen.Queries, mail *mailLog) string {
	t.Helper()
	day := func(off int) string { return time.Now().UTC().AddDate(0, 0, off).Format("2006-01-02") }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"user-a", "user-b"} {
		_, err := q.CreateUser(ctx, dbgen.CreateUserParams{ID: id, Name: id, Email: id + "@example.com", EmailVerified: true})
		must(err)
	}
	a := auth.User{ID: "user-a", Name: "user-a", Email: "user-a@example.com", EmailVerified: true}
	b := auth.User{ID: "user-b", Name: "user-b", Email: "user-b@example.com", EmailVerified: true}

	funds, err := svc.ListFunds(ctx, a)
	must(err)
	fid := funds[0].ID
	name, cur, tz, conf, pace := "  Home  ", "USD", "Africa/Nairobi", true, NullableInt{Set: true, Value: 2500}
	days := []int{25, 10}
	must(svc.UpdateFund(ctx, a, fid, FundPatch{Name: &name, Paydays: &days, Pace: pace, Currency: &cur, Timezone: &tz, TimezoneConfirmed: &conf}))
	for _, it := range []struct {
		name  string
		price int
		off   int
	}{{"Laptop", 1000, 200}, {"Phone", 500, 100}, {"Bike", 300, 60}, {"Café ☕", 80, 30}} {
		must(svc.AddItem(ctx, a, fid, ItemInput{Name: it.name, Price: it.price, Deadline: day(it.off)}))
	}
	p, err := svc.Payload(ctx, a, fid)
	must(err)
	id := map[string]string{}
	for _, it := range p.Items {
		id[it.Name] = it.ID
	}
	must(svc.AddContribution(ctx, a, fid, ContributionInput{Date: day(-20), Note: "start", Allocations: []Line{{id["Laptop"], 400}, {id["Phone"], 100}}}))
	must(svc.AddContribution(ctx, a, fid, ContributionInput{ID: "4f0c2f2e-2a52-4a53-9a4e-0d6a3b1c7e11", Date: day(-10), Note: " bonus ", Allocations: []Line{{id["Laptop"], 200}}}))
	must(svc.AddContribution(ctx, a, fid, ContributionInput{Date: day(-5), Kind: "withdrawal", Allocations: []Line{{id["Laptop"], 100}}}))
	must(svc.AddContribution(ctx, a, fid, ContributionInput{Date: day(-5), Note: "tie", Allocations: []Line{{id["Bike"], 50}}}))
	must(svc.ImportContributions(ctx, a, fid, []ImportRow{{Date: day(-3), Note: "imported", Line: 3, Kind: "deposit", Allocations: []ImportAlloc{{"phone", 25}, {"Café ☕", 5}}}}))
	must(svc.SetItemArchived(ctx, a, fid, id["Bike"], true, &Purchase{ActualPrice: 280, PurchasedOn: day(-2)}))
	must(svc.MoveItem(ctx, a, fid, id["Phone"], "up"))

	p, err = svc.Payload(ctx, a, fid)
	must(err)
	today := plan.TodayIn(p.Fund.Timezone, time.Now())
	sch := plan.ScheduleOf(p.Fund.Paydays, nil, nil)
	p1, _ := plan.NextPayday(today, true, sch)
	p2, _ := plan.NextPayday(p1, false, sch)
	p3, _ := plan.NextPayday(p2, false, sch)
	must(svc.SkipPayday(ctx, a, fid, p1))
	must(svc.AdjustPayday(ctx, a, fid, p2, 3000))
	must(svc.AdjustPayday(ctx, a, fid, p3, 1200))
	must(svc.SetReminders(ctx, a, fid, true))
	must(svc.InviteMember(ctx, a, fid, "user-b@example.com", "viewer"))
	must(svc.InviteMember(ctx, a, fid, "pending@example.com", "editor"))
	var token string
	for _, inv := range mail.invites {
		if inv.To == "user-b@example.com" {
			token = tokenFromURL(inv.Mail.URL)
		}
	}
	_, err = svc.AcceptInvite(ctx, b, token, "127.0.0.1")
	must(err)
	must(svc.SetReminders(ctx, b, fid, false))
	return fid
}

func dumpPayloads(t *testing.T, ctx context.Context, svc *Service, q *dbgen.Queries, fid, out string) {
	t.Helper()
	res := map[string]any{"fundId": fid, "users": map[string]any{}}
	for _, uid := range []string{"user-a", "user-b"} {
		row, err := q.GetUserByID(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		u := auth.User{ID: row.ID, Name: row.Name, Email: row.Email, EmailVerified: row.EmailVerified, JoinedViaInvite: row.JoinedViaInvite}
		p, err := svc.Payload(ctx, u, fid)
		if err != nil {
			t.Fatal(err)
		}
		res["users"].(map[string]any)[uid] = p
	}
	b, _ := json.MarshalIndent(res, "", " ")
	if err := os.WriteFile(out, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Go -> Node, step 1: Go writes the database and records its payloads.
func TestSeedScenarioForNode(t *testing.T) {
	dsn, out := os.Getenv("GO_SEED_DATABASE_URL"), os.Getenv("GO_PAYLOADS_OUT")
	if dsn == "" || out == "" {
		t.Skip("GO_SEED_DATABASE_URL / GO_PAYLOADS_OUT not set (run `make parity-db`)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	mail := &mailLog{}
	svc := NewService(Options{Pool: pool, Mailer: mail, Limiter: ratelimit.New(), PublicURL: "https://fund.test"})
	fid := seedScenario(t, ctx, svc, dbgen.New(pool), mail)
	dumpPayloads(t, ctx, svc, dbgen.New(pool), fid, out)
}

// Go -> Node -> Go, step 3: after the web app read and then CHANGED Go's data, Go must read the
// web app's changes (its own payloads, taken after its mutations).
func TestPayloadMatchesWebAppAfterItMutatedGoData(t *testing.T) {
	dsn, file := os.Getenv("GO_SEED_DATABASE_URL"), os.Getenv("PARITY_PAYLOADS_AFTER")
	if dsn == "" || file == "" {
		t.Skip("GO_SEED_DATABASE_URL / PARITY_PAYLOADS_AFTER not set (run `make parity-db`)")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		FundID string                    `json:"fundId"`
		Users  map[string]map[string]any `json:"users"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := NewService(Options{Pool: pool, Mailer: &mailLog{}, Limiter: ratelimit.New(), PublicURL: "https://fund.test"})
	q := dbgen.New(pool)
	for userID, want := range golden.Users {
		row, _ := q.GetUserByID(ctx, userID)
		u := auth.User{ID: row.ID, Name: row.Name, Email: row.Email, EmailVerified: row.EmailVerified, JoinedViaInvite: row.JoinedViaInvite}
		got, err := svc.Payload(ctx, u, golden.FundID)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(got)
		var gotAny map[string]any
		_ = json.Unmarshal(b, &gotAny)
		if !reflect.DeepEqual(want, gotAny) {
			wb, _ := json.MarshalIndent(want, "", " ")
			gb, _ := json.MarshalIndent(gotAny, "", " ")
			os.WriteFile("/tmp/after-"+userID+"-node.json", wb, 0o600)
			os.WriteFile("/tmp/after-"+userID+"-go.json", gb, 0o600)
			t.Fatalf("%s: Go's payload differs from the web app's after it mutated Go-written data", userID)
		}
	}
}
