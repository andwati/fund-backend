// Package money formats amounts and dates the way the web app does (src/lib/format.ts).
package money

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var codeRe = regexp.MustCompile(`^[A-Z]{3}$`)

// jsRound is Math.round: ties go toward +Infinity, and values in [-0.5, 0) round to -0.
func jsRound(x float64) float64 {
	if x < 0 && x >= -0.5 {
		return math.Copysign(0, -1)
	}
	return math.Floor(x + 0.5)
}

// group renders a non-negative integer with comma thousands separators.
func group(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func localeInt(rounded float64) string {
	neg := rounded < 0 || (rounded == 0 && math.Signbit(rounded))
	s := group(int64(math.Abs(rounded)))
	if neg {
		return "-" + s
	}
	return s
}

// Money formats amount in currency. Real ISO codes use the en-US narrow-symbol style
// (no decimals); any other label is shown as typed: "<label> N,NNN".
func Money(currency string, amount float64) string {
	rounded := jsRound(amount)
	code := strings.ToUpper(strings.TrimSpace(currency))
	if codeRe.MatchString(code) {
		if prefix, ok := prefixes[code]; ok {
			neg := rounded < 0 || (rounded == 0 && math.Signbit(rounded))
			s := prefix + group(int64(math.Abs(rounded)))
			if neg {
				return "-" + s
			}
			return s
		}
	}
	return currency + " " + localeInt(rounded)
}

var (
	months   = [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	weekdays = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
)

// FmtLong is "Wed 20 Oct" (optionally with the year) for a YYYY-MM-DD date.
func FmtLong(value string, withYear bool) string {
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return value
	}
	out := weekdays[t.Weekday()] + " " + strconv.Itoa(t.Day()) + " " + months[t.Month()-1]
	if withYear {
		out += " " + strconv.Itoa(t.Year())
	}
	return out
}
