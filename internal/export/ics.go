package export

import (
	"regexp"
	"strings"
	"time"
)

func icsText(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, ",", `\,`)
	return strings.ReplaceAll(v, "\n", `\n`)
}

func icsDate(iso string) string { return strings.ReplaceAll(iso, "-", "") }

func nextDay(iso string) string {
	t, _ := time.Parse("2006-01-02", iso)
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

// ScheduleICS writes all-day calendar events, one per upcoming payday.
func ScheduleICS(fundName string, paydays []string, summary func(date string) string, stamp time.Time) string {
	ts := stamp.UTC().Format("20060102T150405Z")
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Fund//Payday schedule//EN",
		"CALSCALE:GREGORIAN", "X-WR-CALNAME:" + icsText(fundName),
	}
	for _, d := range paydays {
		lines = append(lines,
			"BEGIN:VEVENT",
			"UID:payday-"+icsDate(d)+"@fund",
			"DTSTAMP:"+ts,
			"DTSTART;VALUE=DATE:"+icsDate(d),
			"DTEND;VALUE=DATE:"+icsDate(nextDay(d)),
			"SUMMARY:"+icsText(summary(d)),
			"END:VEVENT")
	}
	lines = append(lines, "END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n"
}

var (
	slugRe   = regexp.MustCompile(`[^a-z0-9]+`)
	trimDash = regexp.MustCompile(`^-+|-+$`)
)

// FundExportFolder is "<slug>-<first 8 of id>" for the account export.
func FundExportFolder(name, id string) string {
	slug := trimDash.ReplaceAllString(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "")
	if slug == "" {
		slug = "fund"
	}
	if len(id) > 8 {
		id = id[:8]
	}
	return slug + "-" + id
}
