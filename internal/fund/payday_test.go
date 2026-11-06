package fund

import (
	"reflect"
	"testing"

	"github.com/andwati/fund-backend/internal/plan"
)

// upcoming returns today and the next two paydays of the fund.
func upcoming(e *env, f string) (today, first, second string) {
	e.t.Helper()
	p := e.payload(e.A, f)
	today = plan.TodayIn(p.Fund.Timezone, e.svc.now())
	sch := plan.ScheduleOf(p.Fund.Paydays, nil, nil)
	first, _ = plan.NextPayday(today, true, sch)
	second, _ = plan.NextPayday(first, false, sch)
	return
}

func TestPaydaySkips(t *testing.T) {
	t.Run("skip and restore are idempotent", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		_, first, second := upcoming(e, f)
		for _, d := range []string{first, first, second} {
			if err := e.svc.SkipPayday(bg, e.A, f, d); err != nil {
				t.Fatal(err)
			}
		}
		if got := e.payload(e.A, f).Skips; !reflect.DeepEqual(got, []string{first, second}) {
			t.Fatalf("skips %v", got)
		}
		for i := 0; i < 2; i++ {
			if err := e.svc.RestorePayday(bg, e.A, f, first); err != nil {
				t.Fatal(err)
			}
		}
		if got := e.payload(e.A, f).Skips; !reflect.DeepEqual(got, []string{second}) {
			t.Fatalf("skips after restore %v", got)
		}
	})
	t.Run("editors may skip, viewers may not", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		_, first, _ := upcoming(e, f)
		if err := e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer")); err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.svc.SkipPayday(bg, e.B, f, first), 403, "")
		wantErr(t, e.svc.RestorePayday(bg, e.B, f, first), 403, "")
		if err := e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "editor")); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.SkipPayday(bg, e.B, f, first); err != nil {
			t.Fatal(err)
		}
		if got := e.payload(e.B, f).Skips; !reflect.DeepEqual(got, []string{first}) {
			t.Fatalf("skips %v", got)
		}
	})
	t.Run("rejects past dates, non-paydays and impossible dates", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		today, first, _ := upcoming(e, f)
		wantErr(t, e.svc.SkipPayday(bg, e.A, f, "2020-01-31"), 400, "Only a payday from today on can be skipped.")
		notPayday := today
		if first == today {
			notPayday = "2099-01-02"
		}
		wantErr(t, e.svc.SkipPayday(bg, e.A, f, notPayday), 400, "That date is not a payday.")
		wantErr(t, e.svc.SkipPayday(bg, e.A, f, "2026-02-31"), 400, "Pick a real date.")
		if len(e.payload(e.A, f).Skips) != 0 {
			t.Fatal("a rejected skip was saved")
		}
	})
	t.Run("prunes old skips on write and hides them from clients", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		_, first, _ := upcoming(e, f)
		if _, err := e.pool.Exec(bg, `insert into payday_skip(fund_id,date) values($1,'2020-01-31')`, f); err != nil {
			t.Fatal(err)
		}
		if len(e.payload(e.A, f).Skips) != 0 {
			t.Fatal("old skip reached the client")
		}
		if err := e.svc.SkipPayday(bg, e.A, f, first); err != nil {
			t.Fatal(err)
		}
		dates, _ := e.q.ListSkipDates(bg, f)
		if !reflect.DeepEqual(dates, []string{first}) {
			t.Fatalf("stored skips %v", dates)
		}
	})
	t.Run("skips are removed with the fund", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		_, first, _ := upcoming(e, f)
		_ = e.svc.SkipPayday(bg, e.A, f, first)
		if err := e.svc.DeleteFund(bg, e.A, f); err != nil {
			t.Fatal(err)
		}
		var n int
		_ = e.pool.QueryRow(bg, `select count(*) from payday_skip`).Scan(&n)
		if n != 0 {
			t.Fatalf("%d skips left", n)
		}
	})
}

func TestPaydayAmounts(t *testing.T) {
	t.Run("sets, replaces and clears an amount", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		_, first, _ := upcoming(e, f)
		if err := e.svc.AdjustPayday(bg, e.A, f, first, 20000); err != nil {
			t.Fatal(err)
		}
		if got := e.payload(e.A, f).Adjustments; !reflect.DeepEqual(got, []AdjustmentView{{first, 20000}}) {
			t.Fatalf("adjustments %v", got)
		}
		_ = e.svc.AdjustPayday(bg, e.A, f, first, 5000)
		if got := e.payload(e.A, f).Adjustments; !reflect.DeepEqual(got, []AdjustmentView{{first, 5000}}) {
			t.Fatalf("replaced %v", got)
		}
		for i := 0; i < 2; i++ {
			if err := e.svc.ClearPaydayAdjustment(bg, e.A, f, first); err != nil {
				t.Fatal(err)
			}
		}
		if len(e.payload(e.A, f).Adjustments) != 0 {
			t.Fatal("adjustment survived clear")
		}
	})
	t.Run("rejects bad amounts, past dates and non-paydays", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		_, first, _ := upcoming(e, f)
		for _, amt := range []int{0, -5, 2_000_000_000} {
			wantErr(t, e.svc.AdjustPayday(bg, e.A, f, first, amt), 400, "")
		}
		wantErr(t, e.svc.AdjustPayday(bg, e.A, f, "2020-01-31", 100), 400, "Only a payday from today on can be adjusted.")
		wantErr(t, e.svc.AdjustPayday(bg, e.A, f, "2099-01-02", 100), 400, "That date is not a payday.")
		wantErr(t, e.svc.ClearPaydayAdjustment(bg, e.A, f, "2020-01-31"), 400, "Only a payday from today on can be changed.")
		if len(e.payload(e.A, f).Adjustments) != 0 {
			t.Fatal("a rejected adjustment was saved")
		}
	})
	t.Run("editors adjust, viewers do not", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		_, first, _ := upcoming(e, f)
		_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer"))
		wantErr(t, e.svc.AdjustPayday(bg, e.B, f, first, 100), 403, "")
		_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "editor"))
		if err := e.svc.AdjustPayday(bg, e.B, f, first, 100); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("skipping and adjusting are exclusive", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		_, first, second := upcoming(e, f)
		_ = e.svc.AdjustPayday(bg, e.A, f, first, 100)
		_ = e.svc.SkipPayday(bg, e.A, f, first) // skipping drops the amount
		if len(e.payload(e.A, f).Adjustments) != 0 {
			t.Fatal("skipped payday kept its amount")
		}
		wantErr(t, e.svc.AdjustPayday(bg, e.A, f, first, 100), 400, "That payday is skipped. Restore it first.")
		_ = e.svc.RestorePayday(bg, e.A, f, first)
		if err := e.svc.AdjustPayday(bg, e.A, f, first, 100); err != nil {
			t.Fatal(err)
		}
		_ = second
	})
	t.Run("a past amount stays so plan history holds", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		if _, err := e.pool.Exec(bg, `insert into payday_amount(fund_id,date,amount) values($1,'2020-01-31',700)`, f); err != nil {
			t.Fatal(err)
		}
		if got := e.payload(e.A, f).Adjustments; !reflect.DeepEqual(got, []AdjustmentView{{"2020-01-31", 700}}) {
			t.Fatalf("adjustments %v", got)
		}
	})
}

func TestPaydaySchedule(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	if got := e.payload(e.A, f).Fund.Paydays; !reflect.DeepEqual(got, []int{31}) {
		t.Fatalf("default paydays %v", got)
	}
	// Up to four distinct days, saved sorted.
	days := []int{28, 5, 15, 31}
	if err := e.svc.UpdateFund(bg, e.A, f, FundPatch{Paydays: &days}); err != nil {
		t.Fatal(err)
	}
	if got := e.payload(e.A, f).Fund.Paydays; !reflect.DeepEqual(got, []int{5, 15, 28, 31}) {
		t.Fatalf("saved paydays %v", got)
	}
	for name, bad := range map[string][]int{"none": {}, "five": {1, 2, 3, 4, 5}, "dupes": {5, 5}, "zero": {0}, "32": {32}} {
		bad := bad
		wantErr(t, e.svc.UpdateFund(bg, e.A, f, FundPatch{Paydays: &bad}), 400, "")
		_ = name
	}
	// Only the owner changes the schedule.
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "editor"))
	one := []int{10}
	wantErr(t, e.svc.UpdateFund(bg, e.B, f, FundPatch{Paydays: &one}), 403, "Only the owner can do that.")
}

func TestScheduleChangeDropsStaleSkipsAndRestartsPlans(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	days := []int{10, 20}
	if err := e.svc.UpdateFund(bg, e.A, f, FundPatch{Paydays: &days}); err != nil {
		t.Fatal(err)
	}
	today, first, second := upcoming(e, f)
	_ = e.svc.SkipPayday(bg, e.A, f, first)
	_ = e.svc.AdjustPayday(bg, e.A, f, second, 500)

	// Old plan start, deadline in the future.
	it := e.addItem(e.A, f, "Laptop", 1000)
	if _, err := e.pool.Exec(bg, `update item set plan_start='2020-01-01' where id=$1`, it.ID); err != nil {
		t.Fatal(err)
	}
	// Keep `second`'s day and drop `first`'s day.
	keep := int(second[8]-'0')*10 + int(second[9]-'0')
	drop := int(first[8]-'0')*10 + int(first[9]-'0')
	if keep == drop {
		t.Skip("both upcoming paydays share a day number")
	}
	next := []int{keep}
	if err := e.svc.UpdateFund(bg, e.A, f, FundPatch{Paydays: &next}); err != nil {
		t.Fatal(err)
	}
	p := e.payload(e.A, f)
	if len(p.Skips) != 0 {
		t.Fatalf("skip for a day that stopped being a payday survived: %v", p.Skips)
	}
	if !reflect.DeepEqual(p.Adjustments, []AdjustmentView{{second, 500}}) {
		t.Fatalf("adjustment for a still-valid payday was dropped: %v", p.Adjustments)
	}
	if p.Items[0].PlanStart != today {
		t.Fatalf("plan start %s want today %s", p.Items[0].PlanStart, today)
	}
	// An unchanged schedule leaves plans alone.
	if _, err := e.pool.Exec(bg, `update item set plan_start='2020-01-01' where id=$1`, it.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.UpdateFund(bg, e.A, f, FundPatch{Paydays: &next}); err != nil {
		t.Fatal(err)
	}
	if e.payload(e.A, f).Items[0].PlanStart != "2020-01-01" {
		t.Fatal("unchanged schedule restarted plans")
	}
}

func TestUpdateFundSettings(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	name, cur, tz := "  Home  ", "KES", "Africa/Nairobi"
	confirmed := true
	pace := NullableInt{Set: true, Value: 2500}
	if err := e.svc.UpdateFund(bg, e.A, f, FundPatch{Name: &name, Currency: &cur, Timezone: &tz, TimezoneConfirmed: &confirmed, Pace: pace}); err != nil {
		t.Fatal(err)
	}
	p := e.payload(e.A, f).Fund
	if p.Name != "Home" || p.Timezone != "Africa/Nairobi" || !p.TimezoneConfirmed || p.Pace == nil || *p.Pace != 2500 {
		t.Fatalf("fund: %+v", p)
	}
	// Pace 0 and null both clear the pace; omitting it keeps it.
	if err := e.svc.UpdateFund(bg, e.A, f, FundPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if e.payload(e.A, f).Fund.Pace == nil {
		t.Fatal("pace cleared by an unrelated patch")
	}
	if err := e.svc.UpdateFund(bg, e.A, f, FundPatch{Pace: NullableInt{Set: true, Value: 0}}); err != nil {
		t.Fatal(err)
	}
	if e.payload(e.A, f).Fund.Pace != nil {
		t.Fatal("pace 0 should be stored as null")
	}
	_ = e.svc.UpdateFund(bg, e.A, f, FundPatch{Pace: pace})
	if err := e.svc.UpdateFund(bg, e.A, f, FundPatch{Pace: NullableInt{Set: true, Null: true}}); err != nil {
		t.Fatal(err)
	}
	if e.payload(e.A, f).Fund.Pace != nil {
		t.Fatal("pace null should clear")
	}
	bad := "Mars/Phobos"
	wantErr(t, e.svc.UpdateFund(bg, e.A, f, FundPatch{Timezone: &bad}), 400, "Pick a valid timezone.")
	empty := "  "
	wantErr(t, e.svc.UpdateFund(bg, e.A, f, FundPatch{Name: &empty}), 400, "Enter a name.")
	done := true
	if err := e.svc.UpdateFund(bg, e.A, f, FundPatch{OnboardingCompleted: &done}); err != nil {
		t.Fatal(err)
	}
	if e.payload(e.A, f).NeedsOnboarding {
		t.Fatal("onboarding should be complete")
	}
}

func TestReminderPrefIsPerMember(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer"))
	if err := e.svc.SetReminders(bg, e.A, f, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetReminders(bg, e.B, f, false); err != nil { // any role may opt themselves in or out
		t.Fatal(err)
	}
	if !e.payload(e.A, f).Fund.RemindersEnabled || e.payload(e.B, f).Fund.RemindersEnabled {
		t.Fatal("reminder preference is not stored per member")
	}
}
