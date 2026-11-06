package reminders

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/mail"
	"github.com/andwati/fund-backend/internal/money"
	"github.com/andwati/fund-backend/internal/plan"
	"github.com/andwati/fund-backend/internal/testdb"
)

var bg = context.Background()

type sent struct {
	To string
	D  mail.ReminderDetails
}

type fakeSender struct {
	mu   sync.Mutex
	sent []sent
	fail map[string]bool
}

func (f *fakeSender) Reminder(_ context.Context, to string, d mail.ReminderDetails) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[to] {
		return errors.New("smtp down")
	}
	f.sent = append(f.sent, sent{to, d})
	return nil
}

type fixture struct {
	tb   *testing.T
	pool *pgxpool.Pool
	q    *dbgen.Queries
	mail *fakeSender
	run  *Runner
	now  time.Time
	// tomorrow's calendar day, which the fund's payday is set to
	fundID string
}

// newFixture builds a fund (UTC) whose only payday is tomorrow, at 12:00 UTC today, with one item.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	q := dbgen.New(pool)
	f := &fixture{tb: t, pool: pool, q: q, mail: &fakeSender{fail: map[string]bool{}}}
	utc := time.Now().UTC()
	f.now = time.Date(utc.Year(), utc.Month(), utc.Day(), 12, 0, 0, 0, time.UTC)
	f.run = &Runner{Q: q, Mail: f.mail, Secret: []byte("0123456789abcdef-secret"), APIURL: "https://api.test",
		PublicURL: "https://app.test", Now: func() time.Time { return f.now }}
	f.fundID = f.addFund("ann", "Home")
	return f
}

func (f *fixture) addUser(id string, verified bool) {
	f.t().Helper()
	if _, err := f.q.CreateUser(bg, dbgen.CreateUserParams{ID: id, Name: id, Email: id + "@example.com", EmailVerified: verified}); err != nil {
		f.t().Fatal(err)
	}
}

func (f *fixture) t() *testing.T { return f.tb }

func (f *fixture) addFund(owner, name string) string {
	f.addUser(owner, true)
	fid := "fund-" + owner
	if err := f.q.InsertFund(bg, dbgen.InsertFundParams{ID: fid, UserID: owner, Name: name, Currency: "KES", Timezone: "UTC", OnboardingCompleted: true}); err != nil {
		f.t().Fatal(err)
	}
	tomorrow := f.now.AddDate(0, 0, 1)
	if err := f.q.InsertPayday(bg, dbgen.InsertPaydayParams{FundID: fid, Day: int32(tomorrow.Day())}); err != nil {
		f.t().Fatal(err)
	}
	if err := f.q.InsertItem(bg, dbgen.InsertItemParams{ID: "item-" + owner, FundID: fid, Name: "Laptop", Price: 12000,
		Deadline: f.now.AddDate(0, 6, 0).Format("2006-01-02"), PlanStart: f.now.AddDate(0, -1, 0).Format("2006-01-02"), Position: 1}); err != nil {
		f.t().Fatal(err)
	}
	if err := f.q.UpsertReminderPref(bg, dbgen.UpsertReminderPrefParams{FundID: fid, UserID: owner, Enabled: true}); err != nil {
		f.t().Fatal(err)
	}
	return fid
}

func (f *fixture) lastOn(fundID, userID string) string {
	p, err := f.q.GetReminderPref(bg, dbgen.GetReminderPrefParams{FundID: fundID, UserID: userID})
	if err != nil {
		f.t().Fatal(err)
	}
	return p.LastReminderOn
}

func TestSendsTheDayBeforePaydayOncePerLocalDay(t *testing.T) {
	f := newFixture(t)
	res, err := f.run.Run(bg)
	if err != nil || res.Checked != 1 || res.Sent != 1 {
		t.Fatalf("run: %+v %v", res, err)
	}
	m := f.mail.sent[0]
	today := plan.TodayIn("UTC", f.now)
	if m.To != "ann@example.com" || m.D.FundName != "Home" || !strings.HasPrefix(m.D.Amount, "KES") {
		t.Fatalf("email: %+v", m)
	}
	if m.D.URL != "https://app.test/contributions?new=1" || !strings.HasPrefix(m.D.UnsubscribeURL, "https://api.test/unsubscribe/reminders?token=") {
		t.Fatalf("links: %+v", m.D)
	}
	if want := money.FmtLong(f.now.AddDate(0, 0, 1).Format("2006-01-02"), false); m.D.Payday != want {
		t.Fatalf("payday label %q want %q", m.D.Payday, want)
	}
	if f.lastOn(f.fundID, "ann") != today {
		t.Fatalf("last_reminder_on = %q want %q", f.lastOn(f.fundID, "ann"), today)
	}
	// A second run the same local day sends nothing.
	res, _ = f.run.Run(bg)
	if res.Sent != 0 || len(f.mail.sent) != 1 {
		t.Fatalf("sent twice: %+v", res)
	}
	// Next day it is no longer "the day before" (payday is today), so still nothing.
	f.now = f.now.AddDate(0, 0, 1)
	res, _ = f.run.Run(bg)
	if res.Sent != 0 {
		t.Fatalf("reminded on payday itself: %+v", res)
	}
}

func TestNotBeforeEightLocal(t *testing.T) {
	f := newFixture(t)
	f.now = time.Date(f.now.Year(), f.now.Month(), f.now.Day(), 7, 59, 0, 0, time.UTC)
	if res, _ := f.run.Run(bg); res.Sent != 0 {
		t.Fatal("sent before 08:00 local")
	}
	f.now = f.now.Add(time.Minute) // 08:00 sharp; a later hourly run catches up
	if res, _ := f.run.Run(bg); res.Sent != 1 {
		t.Fatal("not sent at 08:00")
	}
}

func TestUsesTheFundsTimezone(t *testing.T) {
	f := newFixture(t)
	// 23:00 UTC is 02:00 next day in Nairobi (UTC+3): tomorrow-in-Nairobi is a day later, so no reminder yet,
	// and 02:00 is before 08:00 anyway.
	if _, err := f.pool.Exec(bg, `update fund set timezone='Africa/Nairobi'`); err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(f.now.Year(), f.now.Month(), f.now.Day(), 23, 0, 0, 0, time.UTC)
	if res, _ := f.run.Run(bg); res.Sent != 0 {
		t.Fatalf("sent at 02:00 Nairobi: %+v", res)
	}
}

func TestNoReminderForSkippedPayday(t *testing.T) {
	f := newFixture(t)
	tomorrow := f.now.AddDate(0, 0, 1).Format("2006-01-02")
	if err := f.q.InsertSkip(bg, dbgen.InsertSkipParams{FundID: f.fundID, Date: tomorrow}); err != nil {
		t.Fatal(err)
	}
	if res, _ := f.run.Run(bg); res.Sent != 0 {
		t.Fatalf("reminded for a skipped payday: %+v", res)
	}
	if f.lastOn(f.fundID, "ann") != "" {
		t.Fatal("last_reminder_on set without a send")
	}
}

func TestAdjustedAmountGoesIntoTheReminder(t *testing.T) {
	f := newFixture(t)
	tomorrow := f.now.AddDate(0, 0, 1).Format("2006-01-02")
	if err := f.q.UpsertAdjustment(bg, dbgen.UpsertAdjustmentParams{FundID: f.fundID, Date: tomorrow, Amount: 20000}); err != nil {
		t.Fatal(err)
	}
	if res, _ := f.run.Run(bg); res.Sent != 1 {
		t.Fatal("no reminder")
	}
	if got := f.mail.sent[0].D.Amount; got != "KES 20,000" {
		t.Fatalf("amount %q, want the adjusted 20,000", got)
	}
}

func TestSkipsFundsWithoutActiveItemsAndIgnoresArchived(t *testing.T) {
	f := newFixture(t)
	if _, err := f.pool.Exec(bg, `update item set archived_at = now()`); err != nil {
		t.Fatal(err)
	}
	if res, _ := f.run.Run(bg); res.Sent != 0 {
		t.Fatal("reminded a fund with only archived items")
	}
	if _, err := f.pool.Exec(bg, `delete from item`); err != nil {
		t.Fatal(err)
	}
	if res, _ := f.run.Run(bg); res.Sent != 0 {
		t.Fatal("reminded a fund with no items")
	}
}

func TestOnlyEnabledVerifiedMembersAreChecked(t *testing.T) {
	f := newFixture(t)
	f.addUser("off", true)
	f.addUser("unverified", false)
	f.addUser("member", true)
	for _, u := range []string{"off", "unverified", "member"} {
		_ = f.q.UpsertMember(bg, dbgen.UpsertMemberParams{FundID: f.fundID, UserID: u, Role: "viewer"})
	}
	_ = f.q.UpsertReminderPref(bg, dbgen.UpsertReminderPrefParams{FundID: f.fundID, UserID: "off", Enabled: false})
	_ = f.q.UpsertReminderPref(bg, dbgen.UpsertReminderPrefParams{FundID: f.fundID, UserID: "unverified", Enabled: true})
	_ = f.q.UpsertReminderPref(bg, dbgen.UpsertReminderPrefParams{FundID: f.fundID, UserID: "member", Enabled: true})
	res, err := f.run.Run(bg)
	if err != nil || res.Checked != 2 || res.Sent != 2 {
		t.Fatalf("run: %+v %v", res, err)
	}
	got := map[string]bool{}
	for _, m := range f.mail.sent {
		got[m.To] = true
	}
	if !got["ann@example.com"] || !got["member@example.com"] || got["off@example.com"] || got["unverified@example.com"] {
		t.Fatalf("recipients: %v", got)
	}
}

func TestOneFailureDoesNotStopTheOthers(t *testing.T) {
	f := newFixture(t)
	f.addFund("bob", "Bobs")
	f.mail.fail["ann@example.com"] = true
	res, err := f.run.Run(bg)
	if err != nil || res.Checked != 2 || res.Sent != 1 {
		t.Fatalf("run: %+v %v", res, err)
	}
	if f.lastOn(f.fundID, "ann") != "" {
		t.Fatal("a failed send must not mark the day as done (the next hourly run retries)")
	}
	f.mail.fail["ann@example.com"] = false
	if res, _ := f.run.Run(bg); res.Sent != 1 {
		t.Fatalf("retry run: %+v", res)
	}
}

func TestUnsubscribe(t *testing.T) {
	f := newFixture(t)
	tok := SignUnsubscribe(f.run.Secret, f.fundID, "ann")
	for i := 0; i < 2; i++ { // idempotent
		ok, err := f.run.Unsubscribe(bg, tok)
		if err != nil || !ok {
			t.Fatalf("unsubscribe: %v %v", ok, err)
		}
	}
	p, _ := f.q.GetReminderPref(bg, dbgen.GetReminderPrefParams{FundID: f.fundID, UserID: "ann"})
	if p.Enabled {
		t.Fatal("reminders still on")
	}
	if res, _ := f.run.Run(bg); res.Checked != 0 {
		t.Fatalf("unsubscribed member still checked: %+v", res)
	}
	if ok, _ := f.run.Unsubscribe(bg, "garbage"); ok {
		t.Fatal("garbage token accepted")
	}
	// A valid token for a member/fund with no preference row is a quiet no-op.
	if ok, err := f.run.Unsubscribe(bg, SignUnsubscribe(f.run.Secret, "no-fund", "nobody")); !ok || err != nil {
		t.Fatalf("unknown target: %v %v", ok, err)
	}
}
