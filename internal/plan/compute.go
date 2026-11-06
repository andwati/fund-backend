package plan

import (
	"math"
	"sort"
	"time"
)

const (
	KindDeposit    = "deposit"
	KindWithdrawal = "withdrawal"
)

type ItemInput struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Deadline  string  `json:"deadline"`
	PlanStart string  `json:"planStart"`
}

type AllocationInput struct {
	ItemID string  `json:"itemId"`
	Amount float64 `json:"amount"`
}

type ContributionInput struct {
	ID          string            `json:"id"`
	Date        string            `json:"date"`
	Note        string            `json:"note"`
	Kind        string            `json:"kind"` // "" means deposit
	Allocations []AllocationInput `json:"allocations"`
}

// FundInput is everything the maths needs. Pace <= 0 means no pace (null in the web app).
type FundInput struct {
	Schedule      Schedule
	Pace          float64
	Timezone      string
	Items         []ItemInput
	Contributions []ContributionInput
	Today         string    // optional override
	Now           time.Time // used when Today is empty
}

func (in FundInput) today() string {
	if in.Today != "" {
		return in.Today
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	return TodayIn(in.Timezone, now)
}

type ItemProgress struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Price       float64  `json:"price"`
	Deadline    string   `json:"deadline"`
	PlanStart   string   `json:"planStart"`
	Contributed float64  `json:"contributed"`
	Remaining   float64  `json:"remaining"`
	Percent     float64  `json:"percent"`
	Needed      float64  `json:"needed"`
	PaydaysLeft int      `json:"paydaysLeft"`
	Planned     float64  `json:"planned"`
	Diff        float64  `json:"diff"`
	Paydays     []string `json:"paydays"`
}

type ScheduleRow struct {
	Date      string   `json:"date"`
	PlanTotal float64  `json:"planTotal"`
	Saved     *float64 `json:"saved"`
	Status    string   `json:"status"`
	Gap       float64  `json:"gap"`
	Skipped   bool     `json:"skipped"`
	Adjusted  *float64 `json:"adjusted"`
}

type ChartPoint struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
}

type FundProgress struct {
	Today          string         `json:"today"`
	Saved          float64        `json:"saved"`
	Target         float64        `json:"target"`
	Remaining      float64        `json:"remaining"`
	Percent        float64        `json:"percent"`
	Needed         float64        `json:"needed"`
	Usual          float64        `json:"usual"`
	PaydaysLeft    int            `json:"paydaysLeft"`
	NextPayday     *string        `json:"nextPayday"`
	Planned        float64        `json:"planned"`
	Diff           float64        `json:"diff"`
	Finish         *string        `json:"finish"`
	LatestDeadline *string        `json:"latestDeadline"`
	EarliestStart  *string        `json:"earliestStart"`
	Items          []ItemProgress `json:"items"`
	Schedule       []ScheduleRow  `json:"schedule"`
	PlanLine       []ChartPoint   `json:"planLine"`
	ActualLine     []ChartPoint   `json:"actualLine"`
}

func sign(c ContributionInput) float64 {
	if c.Kind == KindWithdrawal {
		return -1
	}
	return 1
}

// contributionTotal is deposits minus withdrawals: what the contribution adds to the fund.
func contributionTotal(c ContributionInput) float64 {
	var sum float64
	for _, a := range c.Allocations {
		sum += a.Amount
	}
	return sign(c) * sum
}

// allocated is the net contributed to an item, never below zero.
func allocated(itemID string, cs []ContributionInput) float64 {
	var net float64
	for _, c := range cs {
		var inner float64
		for _, a := range c.Allocations {
			if a.ItemID == itemID {
				inner += a.Amount
			}
		}
		net += sign(c) * inner
	}
	return math.Max(0, net)
}

// SavedByItem is the net saved per item (deposits minus withdrawals), never below zero.
func SavedByItem(cs []ContributionInput) map[string]float64 {
	totals := map[string]float64{}
	for _, c := range cs {
		for _, l := range c.Allocations {
			totals[l.ItemID] += sign(c) * l.Amount
		}
	}
	for id, t := range totals {
		totals[id] = math.Max(0, t)
	}
	return totals
}

func plannedOn(dates []string, price float64, on string, weight func(string) float64) float64 {
	if len(dates) == 0 {
		return 0
	}
	var total, elapsed float64
	for _, d := range dates {
		w := weight(d)
		total += w
		if d <= on {
			elapsed += w
		}
	}
	if total <= 0 {
		return 0
	}
	return math.Min(price, (price*elapsed)/total)
}

func strp(s string) *string   { return &s }
func f64p(f float64) *float64 { return &f }

// Compute is the plan for a fund as of today. It mirrors compute() in src/plan/index.ts.
func Compute(in FundInput) FundProgress {
	today := in.today()

	type base struct {
		item        ItemInput
		paydays     []string
		contributed float64
		remaining   float64
		future      []string
	}
	bases := make([]base, 0, len(in.Items))
	for _, item := range in.Items {
		paydays := PaydaysBetween(item.PlanStart, item.Deadline, in.Schedule)
		contributed := allocated(item.ID, in.Contributions)
		remaining := math.Max(0, item.Price-contributed)
		future := []string{}
		for _, d := range paydays {
			if d >= today {
				future = append(future, d)
			}
		}
		bases = append(bases, base{item, paydays, contributed, remaining, future})
	}

	var usual float64
	for _, b := range bases {
		if b.remaining <= 0 {
			continue
		}
		if len(b.future) > 0 {
			usual += b.remaining / float64(len(b.future))
		} else {
			usual += b.remaining
		}
	}
	var planInstallment float64
	for _, b := range bases {
		if len(b.paydays) > 0 {
			planInstallment += b.item.Price / float64(len(b.paydays))
		}
	}
	reference := planInstallment
	if in.Pace > 0 {
		reference = in.Pace
	}
	weight := func(date string) float64 { return weightOf(in.Schedule, reference, date) }

	items := make([]ItemProgress, 0, len(bases))
	for _, b := range bases {
		var futureWeight float64
		for _, d := range b.future {
			futureWeight += weight(d)
		}
		var needed float64
		switch {
		case b.remaining <= 0:
			needed = 0
		case len(b.future) > 0 && futureWeight > 0:
			needed = (b.remaining * weight(b.future[0])) / futureWeight
		default:
			needed = b.remaining
		}
		planned := plannedOn(b.paydays, b.item.Price, today, weight)
		var percent float64
		if b.item.Price > 0 {
			percent = (b.contributed / b.item.Price) * 100
		}
		items = append(items, ItemProgress{
			ID: b.item.ID, Name: b.item.Name, Price: b.item.Price,
			Deadline: b.item.Deadline, PlanStart: b.item.PlanStart,
			Contributed: b.contributed, Remaining: b.remaining, Percent: percent,
			Needed: needed, PaydaysLeft: len(b.future), Planned: planned,
			Diff: b.contributed - planned, Paydays: b.paydays,
		})
	}

	var rawSaved float64
	for _, c := range in.Contributions {
		rawSaved += contributionTotal(c)
	}
	saved := math.Max(0, rawSaved)
	var target, needed, planned float64
	for _, it := range items {
		target += it.Price
		needed += it.Needed
		planned += it.Planned
	}
	remaining := math.Max(0, target-saved)

	futurePaydays := map[string]bool{}
	for _, it := range items {
		if it.Remaining <= 0 {
			continue
		}
		for _, d := range it.Paydays {
			if d >= today {
				futurePaydays[d] = true
			}
		}
	}

	dateSet := map[string]bool{}
	for _, it := range items {
		for _, d := range it.Paydays {
			dateSet[d] = true
		}
	}
	scheduleDates := sortedKeys(dateSet)
	var nextPaydayDate *string
	for _, d := range scheduleDates {
		if d >= today {
			nextPaydayDate = strp(d)
			break
		}
	}

	// Skipped occurrences stay in the rows (struck through) but carry no installment.
	unskipped := Schedule{Days: in.Schedule.Days, Skipped: map[string]bool{}, Amounts: in.Schedule.Amounts}
	skippedDates := map[string]bool{}
	for _, item := range in.Items {
		for _, d := range PaydaysBetween(item.PlanStart, item.Deadline, unskipped) {
			if in.Schedule.Skipped[d] {
				skippedDates[d] = true
			}
		}
	}
	rowSet := map[string]bool{}
	for _, d := range scheduleDates {
		rowSet[d] = true
	}
	for d := range skippedDates {
		rowSet[d] = true
	}
	rowDates := sortedKeys(rowSet)

	var latestDeadline, earliestStart *string
	for _, it := range items {
		if latestDeadline == nil || it.Deadline > *latestDeadline {
			latestDeadline = strp(it.Deadline)
		}
		if earliestStart == nil || it.PlanStart < *earliestStart {
			earliestStart = strp(it.PlanStart)
		}
	}

	var finish *string
	if in.Pace > 0 && remaining > 0 {
		left := remaining
		date, ok := NextPayday(today, true, in.Schedule)
		for count := 0; ok && count < 600; count++ {
			if a, has := in.Schedule.amount(date); has {
				left -= a
			} else {
				left -= in.Pace
			}
			if left <= 0 {
				finish = strp(date)
				break
			}
			date, ok = NextPayday(date, false, in.Schedule)
		}
	}

	planTotalOn := func(date string) float64 {
		var sum float64
		for _, it := range items {
			sum += plannedOn(it.Paydays, it.Price, date, weight)
		}
		return sum
	}

	rows := make([]ScheduleRow, 0, len(rowDates))
	for _, date := range rowDates {
		planTotal := planTotalOn(date)
		if skippedDates[date] {
			rows = append(rows, ScheduleRow{Date: date, PlanTotal: planTotal, Status: "upcoming", Skipped: true})
			continue
		}
		past := date <= today
		var savedBy *float64
		if past {
			var s float64
			for _, c := range in.Contributions {
				if c.Date <= date {
					s += contributionTotal(c)
				}
			}
			savedBy = f64p(math.Max(0, s))
		}
		status := "upcoming"
		var gap float64
		if past && savedBy != nil {
			gap = planTotal - *savedBy
			if gap <= 0.5 {
				status = "on-plan"
			} else {
				status = "short"
			}
		} else if nextPaydayDate != nil && date == *nextPaydayDate {
			status = "next"
		}
		row := ScheduleRow{Date: date, PlanTotal: planTotal, Saved: savedBy, Status: status, Gap: gap}
		if a, has := in.Schedule.amount(date); has {
			row.Adjusted = f64p(a)
		}
		rows = append(rows, row)
	}

	planLine := []ChartPoint{}
	actualLine := []ChartPoint{}
	if earliestStart != nil {
		planLine = append(planLine, ChartPoint{*earliestStart, 0})
		for _, d := range scheduleDates {
			planLine = append(planLine, ChartPoint{d, planTotalOn(d)})
		}
		if latestDeadline != nil {
			last := ""
			if n := len(planLine); n > 0 {
				last = planLine[n-1].Date
			}
			if last < *latestDeadline {
				planLine = append(planLine, ChartPoint{*latestDeadline, planTotalOn(*latestDeadline)})
			}
		}

		var running float64
		for _, c := range in.Contributions {
			if c.Date < *earliestStart {
				running += contributionTotal(c)
			}
		}
		actualLine = append(actualLine, ChartPoint{*earliestStart, math.Max(0, running)})
		ordered := make([]ContributionInput, 0, len(in.Contributions))
		for _, c := range in.Contributions {
			if c.Date >= *earliestStart {
				ordered = append(ordered, c)
			}
		}
		// Same-day deposits come before withdrawals (stable, like Array.prototype.sort).
		sort.SliceStable(ordered, func(i, j int) bool {
			a, b := ordered[i], ordered[j]
			if a.Date == b.Date {
				return sign(b)-sign(a) < 0
			}
			return a.Date < b.Date
		})
		for _, c := range ordered {
			running += contributionTotal(c)
			actualLine = append(actualLine, ChartPoint{c.Date, math.Max(0, running)})
		}
	}

	var percent float64
	if target > 0 {
		percent = (saved / target) * 100
	}
	return FundProgress{
		Today: today, Saved: saved, Target: target, Remaining: remaining, Percent: percent,
		Needed: needed, Usual: usual, PaydaysLeft: len(futurePaydays), NextPayday: nextPaydayDate,
		Planned: planned, Diff: saved - planned, Finish: finish,
		LatestDeadline: latestDeadline, EarliestStart: earliestStart,
		Items: items, Schedule: rows, PlanLine: planLine, ActualLine: actualLine,
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
