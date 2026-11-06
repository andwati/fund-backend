package fund

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/andwati/fund-backend/internal/apperr"
)

// jsLen is String#length: UTF-16 code units, so limits match the web app for emoji too.
func jsLen(s string) int { return len(utf16.Encode([]rune(s))) }

// jsSlice is s.slice(0, n) over UTF-16 units (never splitting a pair).
func jsSlice(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if len(u) <= n {
		return s
	}
	cut := u[:n]
	if len(cut) > 0 && cut[len(cut)-1] >= 0xD800 && cut[len(cut)-1] <= 0xDBFF {
		cut = cut[:len(cut)-1]
	}
	return string(utf16.Decode(cut))
}

func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == 0xFEFF || (r != 0x85 && (r == ' ' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' ||
			r == 0xA0 || r == 0x1680 || (r >= 0x2000 && r <= 0x200A) || r == 0x2028 || r == 0x2029 || r == 0x202F || r == 0x205F || r == 0x3000))
	})
}

func cleanName(v string) (string, error) {
	n := jsTrim(v)
	if n == "" {
		return "", apperr.New(400, "Enter a name.")
	}
	if jsLen(n) > NameMax {
		return "", apperr.New(400, "Use 80 characters or fewer.")
	}
	return n, nil
}

func checkAmount(v int, tooLarge string) error {
	if v <= 0 {
		return apperr.New(400, "Enter a whole number above zero.")
	}
	if v > MaxAmount {
		return apperr.New(400, tooLarge)
	}
	return nil
}

var uuidRe = regexp.MustCompile(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}|00000000-0000-0000-0000-000000000000|ffffffff-ffff-ffff-ffff-ffffffffffff)$`)

// today is the calendar date in the fund's timezone.
func validTimezone(tz string) bool {
	if tz == "" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// NullableInt distinguishes an absent JSON field, null and a number.
type NullableInt struct {
	Set   bool
	Null  bool
	Value int
}

func (n *NullableInt) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Null = true
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return apperr.New(400, "Use a whole number.")
	}
	if f != float64(int64(f)) {
		return apperr.New(400, "Use a whole number.")
	}
	n.Value = int(f)
	return nil
}

func (n NullableInt) MarshalJSON() ([]byte, error) {
	if !n.Set || n.Null {
		return []byte("null"), nil
	}
	return []byte(fmt.Sprint(n.Value)), nil
}
