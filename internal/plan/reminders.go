package plan

import "time"

// ReminderSendAllowed is once per fund-local day, not before 8:00 local.
func ReminderSendAllowed(timezone string, lastReminderOn *string, now time.Time) bool {
	today := TodayIn(timezone, now)
	if lastReminderOn != nil && *lastReminderOn == today {
		return false
	}
	return HourIn(timezone, now) >= 8
}

// UpcomingPaydays are paydays from today on, soonest first, without skipped ones.
func UpcomingPaydays(p FundProgress) []string {
	out := []string{}
	for _, row := range p.Schedule {
		if !row.Skipped && row.Date >= p.Today {
			out = append(out, row.Date)
		}
	}
	return out
}

// ExpectedOn is what the fund is asked to set aside on date: the adjusted amount when the
// payday has one, else this payday's share (the usual amount for a payday further out).
func ExpectedOn(p FundProgress, date string) float64 {
	for _, row := range p.Schedule {
		if row.Date == date {
			if row.Adjusted != nil {
				return *row.Adjusted
			}
			break
		}
	}
	if p.NextPayday != nil && date == *p.NextPayday {
		return p.Needed
	}
	return p.Usual
}

// ReminderPayday is the next payday, when it is tomorrow and money is needed.
func ReminderPayday(p FundProgress) (string, bool) {
	up := UpcomingPaydays(p)
	if len(up) == 0 || p.Needed <= 0 {
		return "", false
	}
	if DaysBetween(p.Today, up[0]) == 1 {
		return up[0], true
	}
	return "", false
}
