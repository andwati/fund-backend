package fund

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/plan"
)

// FundPatch is a partial update of the fund. Absent fields are left alone.
type FundPatch struct {
	Name                *string     `json:"name"`
	Paydays             *[]int      `json:"paydays"`
	Pace                NullableInt `json:"pace"`
	Currency            *string     `json:"currency"`
	Timezone            *string     `json:"timezone"`
	TimezoneConfirmed   *bool       `json:"timezoneConfirmed"`
	OnboardingCompleted *bool       `json:"onboardingCompleted"`
}

func validatePaydays(days []int) error {
	if len(days) < 1 {
		return apperr.New(400, "Add at least one payday.")
	}
	if len(days) > MaxPaydays {
		return apperr.Newf(400, "Use at most %d paydays.", MaxPaydays)
	}
	seen := map[int]bool{}
	for _, d := range days {
		if d < 1 || d > 31 {
			return apperr.New(400, "Enter a day from 1 to 31.")
		}
		if seen[d] {
			return apperr.New(400, "Each payday must be a different day.")
		}
		seen[d] = true
	}
	return nil
}

// UpdateFund changes fund settings (owner only). Changing the payday schedule restarts the
// plan of active items from today and drops skips/adjustments for dates that are no longer paydays.
func (s *Service) UpdateFund(ctx context.Context, user auth.User, fundID string, p FundPatch) error {
	if p.Name != nil {
		n, err := cleanName(*p.Name)
		if err != nil {
			return err
		}
		p.Name = &n
	}
	if p.Currency != nil {
		c := jsTrim(*p.Currency)
		if c == "" || jsLen(c) > 8 {
			return apperr.New(400, "Use a currency label of 1 to 8 characters.")
		}
		p.Currency = &c
	}
	if p.Timezone != nil {
		t := jsTrim(*p.Timezone)
		if t == "" || jsLen(t) > 80 || !validTimezone(t) {
			return apperr.New(400, "Pick a valid timezone.")
		}
		p.Timezone = &t
	}
	if p.Pace.Set && !p.Pace.Null && (p.Pace.Value < 0 || p.Pace.Value > MaxAmount) {
		return apperr.New(400, "That pace is out of range.")
	}
	if p.Paydays != nil {
		if err := validatePaydays(*p.Paydays); err != nil {
			return err
		}
	}

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		acc, err := s.access(ctx, q, user.ID, fundID, NeedOwner)
		if err != nil {
			return err
		}
		if _, err := q.LockFund(ctx, fundID); err != nil {
			return err
		}
		cur := acc.Fund

		if p.Name != nil || p.Pace.Set || p.Currency != nil || p.Timezone != nil || p.TimezoneConfirmed != nil || p.OnboardingCompleted != nil {
			next := dbgen.UpdateFundSettingsParams{ID: fundID, Name: cur.Name, Currency: cur.Currency,
				Timezone: cur.Timezone, TimezoneConfirmed: cur.TimezoneConfirmed, OnboardingCompleted: cur.OnboardingCompleted}
			if cur.Pace != nil {
				v := int32(*cur.Pace)
				next.Pace = &v
			}
			if p.Name != nil {
				next.Name = *p.Name
			}
			if p.Pace.Set {
				next.Pace = nil
				if !p.Pace.Null && p.Pace.Value != 0 { // pace 0 is stored as NULL
					v := int32(p.Pace.Value)
					next.Pace = &v
				}
			}
			if p.Currency != nil {
				next.Currency = *p.Currency
			}
			if p.Timezone != nil {
				next.Timezone = *p.Timezone
			}
			if p.TimezoneConfirmed != nil {
				next.TimezoneConfirmed = *p.TimezoneConfirmed
			}
			if p.OnboardingCompleted != nil {
				next.OnboardingCompleted = *p.OnboardingCompleted
			}
			if err := q.UpdateFundSettings(ctx, next); err != nil {
				return err
			}
		}
		if p.Paydays == nil {
			return nil
		}
		days := append([]int(nil), *p.Paydays...)
		sort.Ints(days)
		if equalInts(days, cur.Paydays) {
			return nil
		}
		if err := q.DeletePaydays(ctx, fundID); err != nil {
			return err
		}
		for _, d := range days {
			if err := q.InsertPayday(ctx, dbgen.InsertPaydayParams{FundID: fundID, Day: int32(d)}); err != nil {
				return err
			}
		}
		tz := cur.Timezone
		if p.Timezone != nil {
			tz = *p.Timezone
		}
		today := plan.TodayIn(tz, s.now())
		if err := q.UpdateItemsPlanStart(ctx, dbgen.UpdateItemsPlanStartParams{FundID: fundID, Today: today}); err != nil {
			return err
		}
		schedule := plan.ScheduleOf(days, nil, nil)
		stale := func(date string) bool {
			next, ok := plan.NextPayday(date, true, schedule)
			return !ok || next != date
		}
		skips, err := q.ListSkipDates(ctx, fundID)
		if err != nil {
			return err
		}
		for _, d := range skips {
			if stale(d) {
				if err := q.DeleteSkip(ctx, dbgen.DeleteSkipParams{FundID: fundID, Date: d}); err != nil {
					return err
				}
			}
		}
		amounts, err := q.ListAdjustmentDates(ctx, fundID)
		if err != nil {
			return err
		}
		for _, d := range amounts {
			if stale(d) {
				if err := q.DeleteAdjustment(ctx, dbgen.DeleteAdjustmentParams{FundID: fundID, Date: d}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Service) isPayday(date string, days []int) bool {
	next, ok := plan.NextPayday(date, true, plan.ScheduleOf(days, nil, nil))
	return ok && next == date
}

// SkipPayday skips one upcoming payday. Skipping twice is fine; old skips are pruned.
func (s *Service) SkipPayday(ctx context.Context, user auth.User, fundID, date string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		acc, err := s.access(ctx, q, user.ID, fundID, NeedEdit)
		if err != nil {
			return err
		}
		if !plan.ValidDate(date) {
			return apperr.New(400, "Pick a real date.")
		}
		today := plan.TodayIn(acc.Fund.Timezone, s.now())
		if date < today {
			return apperr.New(400, "Only a payday from today on can be skipped.")
		}
		if !s.isPayday(date, acc.Fund.Paydays) {
			return apperr.New(400, "That date is not a payday.")
		}
		if err := q.DeleteSkipsBefore(ctx, dbgen.DeleteSkipsBeforeParams{FundID: fundID, Before: today}); err != nil {
			return err
		}
		// A skipped payday carries no amount.
		if err := q.DeleteAdjustment(ctx, dbgen.DeleteAdjustmentParams{FundID: fundID, Date: date}); err != nil {
			return err
		}
		return q.InsertSkip(ctx, dbgen.InsertSkipParams{FundID: fundID, Date: date})
	})
}

// RestorePayday puts a skipped payday back. Restoring one that is not skipped is fine.
func (s *Service) RestorePayday(ctx context.Context, user auth.User, fundID, date string) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit); err != nil {
		return err
	}
	if !plan.ValidDate(date) {
		return apperr.New(400, "Pick a real date.")
	}
	return s.q.DeleteSkip(ctx, dbgen.DeleteSkipParams{FundID: fundID, Date: date})
}

// AdjustPayday sets what the fund expects to save on one upcoming, non-skipped payday.
func (s *Service) AdjustPayday(ctx context.Context, user auth.User, fundID, date string, amount int) error {
	if amount < 1 || amount > MaxAmount {
		return apperr.New(400, "Enter an amount from 1 to 1,000,000,000.")
	}
	acc, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit)
	if err != nil {
		return err
	}
	if !plan.ValidDate(date) {
		return apperr.New(400, "Pick a real date.")
	}
	if date < plan.TodayIn(acc.Fund.Timezone, s.now()) {
		return apperr.New(400, "Only a payday from today on can be adjusted.")
	}
	if !s.isPayday(date, acc.Fund.Paydays) {
		return apperr.New(400, "That date is not a payday.")
	}
	skips, err := s.q.ListSkipsFrom(ctx, dbgen.ListSkipsFromParams{FundID: fundID, FromDate: date})
	if err != nil {
		return err
	}
	for _, d := range skips {
		if d == date {
			return apperr.New(400, "That payday is skipped. Restore it first.")
		}
	}
	return s.q.UpsertAdjustment(ctx, dbgen.UpsertAdjustmentParams{FundID: fundID, Date: date, Amount: int32(amount)})
}

// ClearPaydayAdjustment goes back to the usual amount. Clearing an unadjusted payday is fine.
func (s *Service) ClearPaydayAdjustment(ctx context.Context, user auth.User, fundID, date string) error {
	acc, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit)
	if err != nil {
		return err
	}
	if !plan.ValidDate(date) {
		return apperr.New(400, "Pick a real date.")
	}
	if date < plan.TodayIn(acc.Fund.Timezone, s.now()) {
		return apperr.New(400, "Only a payday from today on can be changed.")
	}
	return s.q.DeleteAdjustment(ctx, dbgen.DeleteAdjustmentParams{FundID: fundID, Date: date})
}

// SetReminders opts the caller in or out of payday emails for this fund (any role).
func (s *Service) SetReminders(ctx context.Context, user auth.User, fundID string, enabled bool) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedView); err != nil {
		return err
	}
	return s.q.UpsertReminderPref(ctx, dbgen.UpsertReminderPrefParams{FundID: fundID, UserID: user.ID, Enabled: enabled})
}
