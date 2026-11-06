package plan

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"testing"
	"time"
)

// parity.json.gz is generated from the web app's real code by tools/parity (make parity).
type parityFile struct {
	Cases []struct {
		Input struct {
			Days        []int    `json:"days"`
			Skips       []string `json:"skips"`
			Adjustments []struct {
				Date   string  `json:"date"`
				Amount float64 `json:"amount"`
			} `json:"adjustments"`
			Pace          *float64            `json:"pace"`
			Timezone      string              `json:"timezone"`
			Items         []ItemInput         `json:"items"`
			Contributions []ContributionInput `json:"contributions"`
			Today         string              `json:"today"`
		} `json:"input"`
		Progress       json.RawMessage    `json:"progress"`
		Risks          json.RawMessage    `json:"risks"`
		SavedByItem    map[string]float64 `json:"savedByItem"`
		Upcoming       []string           `json:"upcoming"`
		ReminderPayday *string            `json:"reminderPayday"`
		ExpectedOn     [][2]any           `json:"expectedOn"`
	} `json:"cases"`
	Ledgers []struct {
		Lines []struct {
			ContributionID string  `json:"contributionId"`
			ItemID         string  `json:"itemId"`
			Date           string  `json:"date"`
			Kind           string  `json:"kind"`
			Amount         float64 `json:"amount"`
			CreatedAt      int64   `json:"createdAt"`
		} `json:"lines"`
		Only   []string `json:"only"`
		Breach *struct {
			ItemID string `json:"itemId"`
			Date   string `json:"date"`
		} `json:"breach"`
	} `json:"ledgers"`
	Times []struct {
		TZ      string  `json:"tz"`
		Now     string  `json:"now"`
		Today   string  `json:"today"`
		Hour    int     `json:"hour"`
		Allowed [2]bool `json:"allowed"`
	} `json:"times"`
}

func load(t *testing.T) parityFile {
	t.Helper()
	file, err := os.Open("testdata/parity.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	var f parityFile
	if err := json.NewDecoder(zr).Decode(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

// equalJSON compares two JSON documents, allowing a tiny float tolerance.
func equalJSON(a, b any, path string) error {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		if !ok {
			return fmt.Errorf("%s: want number, got %T (%v)", path, b, b)
		}
		if math.Abs(x-y) > 1e-9*math.Max(1, math.Abs(x)) {
			return fmt.Errorf("%s: want %v, got %v", path, x, y)
		}
		return nil
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return fmt.Errorf("%s: array mismatch want %v got %v", path, x, b)
		}
		for i := range x {
			if err := equalJSON(x[i], y[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return fmt.Errorf("%s: object mismatch want keys %d got %d (%v vs %v)", path, len(x), len(y), x, b)
		}
		for k, v := range x {
			if err := equalJSON(v, y[k], path+"."+k); err != nil {
				return err
			}
		}
		return nil
	default:
		if !reflect.DeepEqual(a, b) {
			return fmt.Errorf("%s: want %v, got %v", path, a, b)
		}
		return nil
	}
}

func toAny(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParityWithNode(t *testing.T) {
	f := load(t)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for i, c := range f.Cases {
		adj := make([]Adjustment, 0, len(c.Input.Adjustments))
		for _, a := range c.Input.Adjustments {
			adj = append(adj, Adjustment{a.Date, a.Amount})
		}
		pace := 0.0
		if c.Input.Pace != nil {
			pace = *c.Input.Pace
		}
		in := FundInput{
			Schedule: ScheduleOf(c.Input.Days, c.Input.Skips, adj), Pace: pace,
			Timezone: c.Input.Timezone, Items: c.Input.Items,
			Contributions: c.Input.Contributions, Today: c.Input.Today,
		}
		p := Compute(in)

		var want any
		_ = json.Unmarshal(c.Progress, &want)
		if err := equalJSON(want, toAny(t, p), "progress"); err != nil {
			t.Fatalf("case %d compute: %v", i, err)
		}
		var wantRisks any
		_ = json.Unmarshal(c.Risks, &wantRisks)
		if err := equalJSON(wantRisks, toAny(t, DeadlineRisks(in)), "risks"); err != nil {
			t.Fatalf("case %d risks: %v", i, err)
		}
		saved := SavedByItem(c.Input.Contributions)
		if len(saved) != len(c.SavedByItem) {
			t.Fatalf("case %d savedByItem size %v vs %v", i, saved, c.SavedByItem)
		}
		for k, v := range c.SavedByItem {
			if saved[k] != v {
				t.Fatalf("case %d savedByItem[%s] = %v want %v", i, k, saved[k], v)
			}
		}
		up := UpcomingPaydays(p)
		if !reflect.DeepEqual(up, c.Upcoming) {
			t.Fatalf("case %d upcoming %v want %v", i, up, c.Upcoming)
		}
		got, ok := ReminderPayday(p)
		switch {
		case c.ReminderPayday == nil && ok:
			t.Fatalf("case %d reminderPayday %q want none", i, got)
		case c.ReminderPayday != nil && (!ok || got != *c.ReminderPayday):
			t.Fatalf("case %d reminderPayday %q/%v want %q", i, got, ok, *c.ReminderPayday)
		}
		for _, e := range c.ExpectedOn {
			if g := ExpectedOn(p, e[0].(string)); math.Abs(g-e[1].(float64)) > 1e-9*math.Max(1, math.Abs(g)) {
				t.Fatalf("case %d expectedOn(%v) = %v want %v", i, e[0], g, e[1])
			}
		}
	}
}

func TestParityLedger(t *testing.T) {
	for i, c := range load(t).Ledgers {
		lines := make([]LedgerLine, 0, len(c.Lines))
		for _, l := range c.Lines {
			lines = append(lines, LedgerLine{l.ContributionID, l.ItemID, l.Date, l.Kind, l.Amount, time.UnixMilli(l.CreatedAt)})
		}
		var only map[string]bool
		if c.Only != nil {
			only = map[string]bool{}
			for _, o := range c.Only {
				only[o] = true
			}
		}
		got := FirstLedgerBreach(lines, only)
		switch {
		case c.Breach == nil && got != nil:
			t.Fatalf("ledger %d: got breach %+v want none", i, got)
		case c.Breach != nil && (got == nil || got.ItemID != c.Breach.ItemID || got.Date != c.Breach.Date):
			t.Fatalf("ledger %d: got %+v want %+v", i, got, c.Breach)
		}
	}
}

func TestParityTimeHelpers(t *testing.T) {
	for _, c := range load(t).Times {
		now, err := time.Parse(time.RFC3339, c.Now)
		if err != nil {
			t.Fatal(err)
		}
		if got := TodayIn(c.TZ, now); got != c.Today {
			t.Errorf("TodayIn(%s,%s) = %s want %s", c.TZ, c.Now, got, c.Today)
		}
		if got := HourIn(c.TZ, now); got != c.Hour {
			t.Errorf("HourIn(%s,%s) = %d want %d", c.TZ, c.Now, got, c.Hour)
		}
		today := c.Today
		for i, last := range []*string{nil, &today} {
			if got := ReminderSendAllowed(c.TZ, last, now); got != c.Allowed[i] {
				t.Errorf("ReminderSendAllowed(%s,%s,%d) = %v want %v", c.TZ, c.Now, i, got, c.Allowed[i])
			}
		}
	}
}
