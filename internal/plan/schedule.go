package plan

import (
	"sort"
	"strconv"
)

// maxPaydays is the most paydays PaydaysBetween returns (4 a month for 10 years).
const maxPaydays = 480

// Schedule is when a fund gets paid: days of the month (31 means the last day, clamped
// in short months), skipped occurrences and adjusted amounts for single paydays.
type Schedule struct {
	Days    []int
	Skipped map[string]bool
	Amounts map[string]float64
}

// Adjustment is an expected saving for one payday.
type Adjustment struct {
	Date   string
	Amount float64
}

// ScheduleOf builds a Schedule. A skipped date never carries an adjustment.
func ScheduleOf(paydays []int, skips []string, adjustments []Adjustment) Schedule {
	seen := map[int]bool{}
	days := []int{}
	for _, d := range paydays {
		if !seen[d] {
			seen[d] = true
			days = append(days, d)
		}
	}
	sort.Ints(days)
	skipped := map[string]bool{}
	for _, s := range skips {
		skipped[s] = true
	}
	amounts := map[string]float64{}
	for _, a := range adjustments {
		if !skipped[a.Date] {
			amounts[a.Date] = a.Amount
		}
	}
	return Schedule{Days: days, Skipped: skipped, Amounts: amounts}
}

func (s Schedule) amount(date string) (float64, bool) {
	v, ok := s.Amounts[date]
	return v, ok
}

// weightOf is how much a payday counts in the plan split: 1, or amount/reference.
func weightOf(s Schedule, reference float64, date string) float64 {
	amount, ok := s.amount(date)
	if !ok || reference <= 0 {
		return 1
	}
	return amount / reference
}

// NextPayday is the earliest non-skipped payday after (or, if inclusive, at) from.
func NextPayday(from string, inclusive bool, s Schedule) (string, bool) {
	if len(s.Days) == 0 {
		return "", false
	}
	year, _ := strconv.Atoi(from[0:4])
	monthIndex, _ := strconv.Atoi(from[5:7])
	monthIndex--
	for i := 0; i < 2000; i++ {
		length := daysInMonth(year, monthIndex)
		dates := make([]string, 0, len(s.Days))
		for _, day := range s.Days {
			d := day
			if length < d {
				d = length
			}
			dates = append(dates, ymd(year, monthIndex+1, d))
		}
		sort.Strings(dates)
		for _, date := range dates {
			if s.Skipped[date] {
				continue
			}
			if inclusive && date >= from || !inclusive && date > from {
				return date, true
			}
		}
		monthIndex++
		if monthIndex > 11 {
			monthIndex = 0
			year++
		}
	}
	return "", false
}

// PaydaysBetween lists paydays after start (exclusive) up to deadline (inclusive).
func PaydaysBetween(start, deadline string, s Schedule) []string {
	dates := []string{}
	cursor := start
	date, ok := NextPayday(cursor, false, s)
	for ok && date <= deadline && len(dates) < maxPaydays {
		dates = append(dates, date)
		cursor = date
		date, ok = NextPayday(cursor, false, s)
	}
	return dates
}
