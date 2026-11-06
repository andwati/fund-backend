package plan

import "strconv"

type ItemDeadlineRisk struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Deadline     string  `json:"deadline"`
	Finish       string  `json:"finish"`
	MonthsLate   int     `json:"monthsLate"`
	DaysLate     int     `json:"daysLate"`
	RequiredPace float64 `json:"requiredPace"`
}

type remainingItem struct {
	id        string
	remaining float64
}

func itemRemaining(item ItemInput, cs []ContributionInput) float64 {
	r := item.Price - allocated(item.ID, cs)
	if r < 0 {
		return 0
	}
	return r
}

func calendarMonthsLate(deadline, finish string) int {
	dy, _ := strconv.Atoi(deadline[0:4])
	dm, _ := strconv.Atoi(deadline[5:7])
	fy, _ := strconv.Atoi(finish[0:4])
	fm, _ := strconv.Atoi(finish[5:7])
	return fy*12 + fm - (dy*12 + dm)
}

// simulateFinishes pays items in input order (not by deadline) from a pool per payday.
func simulateFinishes(today string, s Schedule, pace float64, items []remainingItem) map[string]string {
	left := make(map[string]float64, len(items))
	for _, it := range items {
		left[it.id] = it.remaining
	}
	done := map[string]string{}
	date, ok := NextPayday(today, true, s)
	for step := 0; ok && step < 600; step++ {
		pool := pace
		if a, has := s.amount(date); has {
			pool = a
		}
		for _, it := range items {
			need := left[it.id]
			if need <= 0 {
				continue
			}
			pay := pool
			if need < pay {
				pay = need
			}
			after := need - pay
			left[it.id] = after
			pool -= pay
			if _, seen := done[it.id]; after <= 0 && !seen {
				done[it.id] = date
			}
		}
		all := true
		for _, v := range left {
			if v > 0 {
				all = false
				break
			}
		}
		if all {
			break
		}
		date, ok = NextPayday(date, false, s)
	}
	return done
}

func finishesByPace(in FundInput, pace float64) map[string]string {
	items := make([]remainingItem, 0, len(in.Items))
	for _, it := range in.Items {
		items = append(items, remainingItem{it.ID, itemRemaining(it, in.Contributions)})
	}
	return simulateFinishes(in.today(), in.Schedule, pace, items)
}

func itemMeetsDeadline(in FundInput, pace float64, itemID string) bool {
	var item *ItemInput
	for i := range in.Items {
		if in.Items[i].ID == itemID {
			item = &in.Items[i]
			break
		}
	}
	if item == nil {
		return true
	}
	finish, ok := finishesByPace(in, pace)[itemID]
	return ok && finish <= item.Deadline
}

func minPaceForItem(in FundInput, itemID string) float64 {
	var remaining float64
	for _, it := range in.Items {
		remaining += itemRemaining(it, in.Contributions)
	}
	lo := 1.0
	pace := in.Pace
	if pace == 0 {
		pace = 1
	}
	hi := remaining
	if pace > hi {
		hi = pace
	}
	for !itemMeetsDeadline(in, hi, itemID) {
		hi *= 2
		if hi > 1_000_000_000 {
			return hi
		}
	}
	for lo < hi {
		mid := float64(int64((lo + hi) / 2))
		if itemMeetsDeadline(in, mid, itemID) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// DeadlineRisks lists items that the current pace would finish after their deadline.
func DeadlineRisks(in FundInput) []ItemDeadlineRisk {
	risks := []ItemDeadlineRisk{}
	if in.Pace <= 0 {
		return risks
	}
	type row struct {
		ItemInput
		remaining float64
	}
	rows := make([]row, 0, len(in.Items))
	sim := make([]remainingItem, 0, len(in.Items))
	for _, it := range in.Items {
		r := itemRemaining(it, in.Contributions)
		rows = append(rows, row{it, r})
		sim = append(sim, remainingItem{it.ID, r})
	}
	finishes := simulateFinishes(in.today(), in.Schedule, in.Pace, sim)
	for _, it := range rows {
		if it.remaining <= 0 {
			continue
		}
		finish, ok := finishes[it.ID]
		if !ok || finish <= it.Deadline {
			continue
		}
		risks = append(risks, ItemDeadlineRisk{
			ID: it.ID, Name: it.Name, Deadline: it.Deadline, Finish: finish,
			MonthsLate:   calendarMonthsLate(it.Deadline, finish),
			DaysLate:     DaysBetween(it.Deadline, finish),
			RequiredPace: minPaceForItem(in, it.ID),
		})
	}
	return risks
}
