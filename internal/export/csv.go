// Package export builds the downloads: CSV, ICS and the account zip. It mirrors
// ~/dev/fund/src/lib/export.ts byte for byte.
package export

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	formulaRe = regexp.MustCompile(`^[=+\-@\t\r]`)
	needQuote = regexp.MustCompile(`[",\n\r]`)
	dateRe    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	guardRe   = regexp.MustCompile(`^'[=+\-@]`)
)

func csvCell(v string) string {
	safe := v
	if formulaRe.MatchString(v) {
		safe = "'" + v
	}
	if needQuote.MatchString(safe) {
		return `"` + strings.ReplaceAll(safe, `"`, `""`) + `"`
	}
	return safe
}

func csvRow(cells ...string) string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = csvCell(c)
	}
	return strings.Join(out, ",")
}

// ItemRow is one line of the items download.
type ItemRow struct {
	Name        string
	Price       int
	Deadline    string
	PlanStart   string
	Saved       int
	ActualPrice *int
	PurchasedOn *string
	Archived    bool
}

func ItemsCSV(rows []ItemRow) string {
	lines := []string{"name,price,deadline,plan_start,saved,actual_price,purchased_on,status"}
	for _, r := range rows {
		actual, purchased := "", ""
		if r.ActualPrice != nil {
			actual = strconv.Itoa(*r.ActualPrice)
		}
		if r.PurchasedOn != nil {
			purchased = *r.PurchasedOn
		}
		status := "active"
		if r.Archived {
			status = "purchased"
		}
		lines = append(lines, csvRow(r.Name, strconv.Itoa(r.Price), r.Deadline, r.PlanStart,
			strconv.Itoa(r.Saved), actual, purchased, status))
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// ContributionsCSVMarker is the first line of the versioned contributions file.
const ContributionsCSVMarker = "# fund-contributions v2"

type AllocationLine struct {
	ItemID string
	Amount int
}

type ContributionRow struct {
	Date        string
	Note        string
	Kind        string
	Allocations []AllocationLine
}

func ContributionsCSV(rows []ContributionRow, itemName func(id string) string) string {
	lines := []string{ContributionsCSVMarker, "date,kind,note,item,amount"}
	for _, r := range rows {
		for _, l := range r.Allocations {
			lines = append(lines, csvRow(r.Date, r.Kind, r.Note, itemName(l.ItemID), strconv.Itoa(l.Amount)))
		}
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// ParseCSV splits CSV text into rows of cells, honouring quotes and CRLF.
func ParseCSV(text string) [][]string {
	var rows [][]string
	var row []string
	var cell strings.Builder
	quoted := false
	flushRow := func() {
		row = append(row, cell.String())
		cell.Reset()
		for _, v := range row {
			if v != "" {
				rows = append(rows, row)
				break
			}
		}
		row = nil
	}
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		ch := rs[i]
		switch {
		case quoted:
			if ch == '"' && i+1 < len(rs) && rs[i+1] == '"' {
				cell.WriteRune('"')
				i++
			} else if ch == '"' {
				quoted = false
			} else {
				cell.WriteRune(ch)
			}
		case ch == '"':
			quoted = true
		case ch == ',':
			row = append(row, cell.String())
			cell.Reset()
		case ch == '\n' || ch == '\r':
			if ch == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			flushRow()
		default:
			cell.WriteRune(ch)
		}
	}
	flushRow()
	return rows
}

type ImportedAllocation struct {
	Item   string `json:"item"`
	Amount int    `json:"amount"`
}

type ImportedContribution struct {
	Date        string               `json:"date"`
	Note        string               `json:"note"`
	Kind        string               `json:"kind"`
	Line        int                  `json:"line"`
	Allocations []ImportedAllocation `json:"allocations"`
}

// jsTrim trims like String.prototype.trim: Unicode spaces and U+FEFF, but not U+0085.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == 0xFEFF || (r != 0x85 && unicode.IsSpace(r))
	})
}

var (
	decRe = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)
	hexRe = regexp.MustCompile(`^0[xX][0-9a-fA-F]+$`)
	binRe = regexp.MustCompile(`^0[bB][01]+$`)
	octRe = regexp.MustCompile(`^0[oO][0-7]+$`)
)

// jsNumber mimics Number(text) for amounts. ok=false means JS would give NaN or a value that
// can never be a valid amount (Infinity). The empty string is 0, like JS.
func jsNumber(text string) (float64, bool) {
	t := jsTrim(text)
	switch {
	case t == "":
		return 0, true
	case decRe.MatchString(t):
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	case hexRe.MatchString(t):
		n, err := strconv.ParseInt(t[2:], 16, 64)
		return float64(n), err == nil
	case binRe.MatchString(t):
		n, err := strconv.ParseInt(t[2:], 2, 64)
		return float64(n), err == nil
	case octRe.MatchString(t):
		n, err := strconv.ParseInt(t[2:], 8, 64)
		return float64(n), err == nil
	}
	return 0, false
}

// ReadContributionsCSV reads the CSV that ContributionsCSV writes: the v2 file or the older
// unversioned one (date, note, item, amount) whose rows are all deposits. Rows with the same
// date, kind and note become one contribution. On failure the message is returned in errMsg.
func ReadContributionsCSV(text string) (rows []ImportedContribution, errMsg string) {
	parsed := ParseCSV(strings.TrimPrefix(text, "\uFEFF"))
	marker := false
	if len(parsed) > 0 && len(parsed[0]) == 1 && strings.HasPrefix(jsTrim(parsed[0][0]), "# fund-contributions") {
		marker = true
		if strings.ToLower(jsTrim(parsed[0][0])) != ContributionsCSVMarker {
			return nil, `This file is from a newer version. Expected "` + ContributionsCSVMarker + `".`
		}
	}
	rest := parsed
	if marker {
		rest = parsed[1:]
	}
	expected := []string{"date", "note", "item", "amount"}
	if marker {
		expected = []string{"date", "kind", "note", "item", "amount"}
	}
	if len(rest) == 0 {
		if marker {
			return nil, "After the version line, the header must be: date, kind, note, item, amount."
		}
		return nil, "The first row must be: date, note, item, amount."
	}
	head, body := rest[0], rest[1:]
	for i, name := range expected {
		cell := ""
		if i < len(head) {
			cell = strings.ToLower(jsTrim(head[i]))
		}
		if cell != name {
			if marker {
				return nil, "After the version line, the header must be: date, kind, note, item, amount."
			}
			return nil, "The first row must be: date, note, item, amount."
		}
	}
	if len(body) == 0 {
		return nil, "That file has no rows."
	}
	if len(body) > 2000 {
		return nil, "Import at most 2,000 rows at a time."
	}
	type key struct{ date, kind, note string }
	index := map[key]int{}
	var out []ImportedContribution
	for i, cells := range body {
		line := i + 2
		if marker {
			line = i + 3
		}
		trimmed := make([]string, len(cells))
		for j, c := range cells {
			trimmed[j] = jsTrim(c)
		}
		at := func(j int) string {
			if j < len(trimmed) {
				return trimmed[j]
			}
			return ""
		}
		var date, kindText, note, item, amountText string
		if marker {
			date, kindText, note, item, amountText = at(0), at(1), at(2), at(3), at(4)
		} else {
			date, kindText, note, item, amountText = at(0), "deposit", at(1), at(2), at(3)
		}
		if marker && len(trimmed) < 2 {
			kindText = "deposit"
		}
		kind := strings.ToLower(kindText)
		if kind != "deposit" && kind != "withdrawal" {
			return nil, "Row " + strconv.Itoa(line) + ": the kind must be deposit or withdrawal."
		}
		amount, numOK := jsNumber(amountText)
		if !dateRe.MatchString(date) {
			return nil, "Row " + strconv.Itoa(line) + ": the date must look like 2026-10-31."
		}
		if item == "" {
			return nil, "Row " + strconv.Itoa(line) + ": the item is empty."
		}
		if !numOK || amount != float64(int64(amount)) || amount <= 0 {
			return nil, "Row " + strconv.Itoa(line) + ": the amount must be a whole number above zero."
		}
		clean := note
		if strings.HasPrefix(note, "'") && guardRe.MatchString(note) {
			clean = note[1:]
		}
		k := key{date, kind, clean}
		gi, ok := index[k]
		if !ok {
			gi = len(out)
			index[k] = gi
			out = append(out, ImportedContribution{Date: date, Note: clean, Kind: kind, Line: line, Allocations: []ImportedAllocation{}})
		}
		out[gi].Allocations = append(out[gi].Allocations, ImportedAllocation{Item: item, Amount: int(amount)})
	}
	return out, ""
}
