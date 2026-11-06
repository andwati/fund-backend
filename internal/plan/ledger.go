package plan

import (
	"sort"
	"time"
)

// LedgerLine is one allocation line with the contribution it belongs to.
type LedgerLine struct {
	ContributionID string
	ItemID         string
	Date           string
	Kind           string
	Amount         float64 // always positive; Kind decides the direction
	CreatedAt      time.Time
}

type LedgerBreach struct {
	ItemID string
	Date   string
}

// CompareLedgerLines is the replay order: date, deposits before withdrawals, creation
// time, then contribution id so the order is total.
func CompareLedgerLines(a, b LedgerLine) int {
	if a.Date != b.Date {
		if a.Date < b.Date {
			return -1
		}
		return 1
	}
	if a.Kind != b.Kind {
		if a.Kind == KindDeposit {
			return -1
		}
		return 1
	}
	if d := a.CreatedAt.Sub(b.CreatedAt); d != 0 {
		if d < 0 {
			return -1
		}
		return 1
	}
	if a.ContributionID != b.ContributionID {
		if a.ContributionID < b.ContributionID {
			return -1
		}
		return 1
	}
	return 0
}

// FirstLedgerBreach replays each item's lines in ledger order and returns the earliest
// point where an item would hold less than zero. only, when non-nil, limits the items checked.
func FirstLedgerBreach(lines []LedgerLine, only map[string]bool) *LedgerBreach {
	byItem := map[string][]LedgerLine{}
	order := []string{}
	for _, l := range lines {
		if only != nil && !only[l.ItemID] {
			continue
		}
		if _, ok := byItem[l.ItemID]; !ok {
			order = append(order, l.ItemID)
		}
		byItem[l.ItemID] = append(byItem[l.ItemID], l)
	}
	var worst *LedgerBreach
	for _, itemID := range order {
		list := append([]LedgerLine(nil), byItem[itemID]...)
		sort.SliceStable(list, func(i, j int) bool { return CompareLedgerLines(list[i], list[j]) < 0 })
		var balance float64
		for _, l := range list {
			if l.Kind == KindWithdrawal {
				balance -= l.Amount
			} else {
				balance += l.Amount
			}
			if balance < 0 {
				if worst == nil || l.Date < worst.Date {
					worst = &LedgerBreach{ItemID: itemID, Date: l.Date}
				}
				break
			}
		}
	}
	return worst
}
