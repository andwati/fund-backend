package reminders

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/mail"
	"github.com/andwati/fund-backend/internal/money"
	"github.com/andwati/fund-backend/internal/plan"
)

// Concurrency is how many recipients are processed at once.
const Concurrency = 8

// Sender sends the reminder email.
type Sender interface {
	Reminder(ctx context.Context, to string, d mail.ReminderDetails) error
}

// Runner is the hourly "payday is tomorrow" job. Each fund is emailed at most once per
// fund-local day, from 08:00 fund-local onward (later hourly runs catch up if one was missed).
type Runner struct {
	Q         *dbgen.Queries
	Mail      Sender
	Secret    []byte
	APIURL    string // where unsubscribe links point
	PublicURL string // the client app, for the "log your contribution" link
	Now       func() time.Time
	Log       *slog.Logger
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Result is what a run did.
type Result struct {
	Checked int `json:"checked"`
	Sent    int `json:"sent"`
}

// Run processes every enabled, verified recipient. One failure never stops the others.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	rows, err := r.Q.ListReminderRecipients(ctx)
	if err != nil {
		return Result{}, err
	}
	var sent atomic.Int32
	sem := make(chan struct{}, Concurrency)
	var wg sync.WaitGroup
	for _, row := range rows {
		row := row
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ok, err := r.Process(ctx, row)
			if err != nil {
				r.log().Error("reminder failed", "fundId", row.FundID, "userId", row.UserID, "err", err)
				return
			}
			if ok {
				sent.Add(1)
			}
		}()
	}
	wg.Wait()
	res := Result{Checked: len(rows), Sent: int(sent.Load())}
	r.log().Info("reminders done", "checked", res.Checked, "sent", res.Sent)
	return res, nil
}

// Process decides and sends for one recipient. It reports whether an email went out.
func (r *Runner) Process(ctx context.Context, row dbgen.ListReminderRecipientsRow) (bool, error) {
	now := r.now()
	today := plan.TodayIn(row.Timezone, now)
	var last *string
	if row.LastReminderOn != "" {
		last = &row.LastReminderOn
	}
	if !plan.ReminderSendAllowed(row.Timezone, last, now) {
		return false, nil
	}

	items, err := r.Q.ListItems(ctx, row.FundID)
	if err != nil {
		return false, err
	}
	active := make([]plan.ItemInput, 0, len(items))
	activeIDs := map[string]bool{}
	for _, it := range items {
		if it.ArchivedAt.Valid {
			continue
		}
		active = append(active, plan.ItemInput{ID: it.ID, Name: it.Name, Price: float64(it.Price), Deadline: it.Deadline, PlanStart: it.PlanStart})
		activeIDs[it.ID] = true
	}
	if len(active) == 0 {
		return false, nil
	}

	contribs, err := r.Q.ListContributions(ctx, row.FundID)
	if err != nil {
		return false, err
	}
	allocs, err := r.Q.ListAllocationsForFund(ctx, row.FundID)
	if err != nil {
		return false, err
	}
	byContribution := map[string][]plan.AllocationInput{}
	for _, a := range allocs {
		if activeIDs[a.ItemID] { // archived items drop out of the plan
			byContribution[a.ContributionID] = append(byContribution[a.ContributionID], plan.AllocationInput{ItemID: a.ItemID, Amount: float64(a.Amount)})
		}
	}
	inputs := make([]plan.ContributionInput, 0, len(contribs))
	for _, c := range contribs {
		lines := byContribution[c.ID]
		if lines == nil {
			lines = []plan.AllocationInput{}
		}
		inputs = append(inputs, plan.ContributionInput{ID: c.ID, Date: c.Date, Note: c.Note, Kind: c.Kind, Allocations: lines})
	}

	skips, err := r.Q.ListSkipsFrom(ctx, dbgen.ListSkipsFromParams{FundID: row.FundID, FromDate: today})
	if err != nil {
		return false, err
	}
	adjRows, err := r.Q.ListAdjustments(ctx, row.FundID)
	if err != nil {
		return false, err
	}
	adjustments := make([]plan.Adjustment, 0, len(adjRows))
	for _, a := range adjRows {
		adjustments = append(adjustments, plan.Adjustment{Date: a.Date, Amount: float64(a.Amount)})
	}
	days, err := r.Q.ListPaydays(ctx, row.FundID)
	if err != nil {
		return false, err
	}
	paydays := []int{31}
	if len(days) > 0 {
		paydays = paydays[:0]
		for _, d := range days {
			paydays = append(paydays, int(d))
		}
	}
	pace := 0.0
	if row.Pace != nil {
		pace = float64(*row.Pace)
	}

	progress := plan.Compute(plan.FundInput{
		Schedule: plan.ScheduleOf(paydays, skips, adjustments), Pace: pace, Timezone: row.Timezone,
		Today: today, Items: active, Contributions: inputs,
	})
	payday, ok := plan.ReminderPayday(progress)
	if !ok {
		return false, nil
	}
	err = r.Mail.Reminder(ctx, row.Email, mail.ReminderDetails{
		FundName:       row.FundName,
		Payday:         money.FmtLong(payday, false),
		Amount:         money.Money(row.Currency, math.Ceil(plan.ExpectedOn(progress, payday))),
		URL:            r.PublicURL + "/contributions?new=1",
		UnsubscribeURL: UnsubscribeURL(r.APIURL, r.Secret, row.FundID, row.UserID),
	})
	if err != nil {
		return false, err
	}
	if err := r.Q.MarkReminderSent(ctx, dbgen.MarkReminderSentParams{FundID: row.FundID, UserID: row.UserID, SentOn: today}); err != nil {
		return false, err
	}
	return true, nil
}

// Unsubscribe turns reminders off for the token's member and fund. It reports whether the token
// was valid; an unknown member or fund is a quiet no-op (the web app's UPDATE matches no row).
func (r *Runner) Unsubscribe(ctx context.Context, token string) (bool, error) {
	fundID, userID, ok := VerifyUnsubscribe(r.Secret, token)
	if !ok {
		return false, nil
	}
	return true, r.Q.DisableReminder(ctx, dbgen.DisableReminderParams{FundID: fundID, UserID: userID})
}
