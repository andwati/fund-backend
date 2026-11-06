// Package plan is a line-for-line port of the web app's planning maths
// (~/dev/fund/src/plan/*). Behaviour must match exactly; see PARITY_NOTES.md.
package plan

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata" // IANA zones must not depend on the host (distroless image)
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ValidDate reports whether v is a real calendar date in YYYY-MM-DD form.
func ValidDate(v string) bool {
	if !dateRe.MatchString(v) {
		return false
	}
	y, _ := strconv.Atoi(v[0:4])
	m, _ := strconv.Atoi(v[5:7])
	d, _ := strconv.Atoi(v[8:10])
	if y == 0 || m == 0 || d == 0 {
		return false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t.Year() == y && int(t.Month()) == m && t.Day() == d
}

func location(tz string) *time.Location {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		return time.UTC
	}
	return loc
}

// TodayIn is the calendar date in tz at now, falling back to UTC for an unknown zone.
func TodayIn(tz string, now time.Time) string {
	return now.In(location(tz)).Format("2006-01-02")
}

// HourIn is the fund-local clock hour (0-23), falling back to UTC.
func HourIn(tz string, now time.Time) int {
	return now.In(location(tz)).Hour()
}

// DaysBetween is the whole-day difference between two YYYY-MM-DD dates (UTC).
func DaysBetween(from, to string) int {
	a, _ := time.Parse("2006-01-02", from)
	b, _ := time.Parse("2006-01-02", to)
	return int(b.Sub(a).Hours() / 24)
}

func daysInMonth(year, monthIndex int) int {
	return time.Date(year, time.Month(monthIndex+2), 0, 0, 0, 0, 0, time.UTC).Day()
}

func ymd(year, month, day int) string {
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}
