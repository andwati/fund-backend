package fund

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/andwati/fund-backend/internal/id"
)

var bg = context.Background()

func TestCreatesFundOnFirstUseAndIsIdempotent(t *testing.T) {
	e := setup(t)
	first := e.fundOf(e.A)
	second := e.fundOf(e.A)
	if first != second {
		t.Fatal("fund was created twice")
	}
	p := e.payload(e.A, first)
	if p.Role != RoleOwner || !equalInts(p.Fund.Paydays, []int{31}) || p.Fund.Currency != "KES" || p.Fund.Timezone != "UTC" || !p.NeedsOnboarding {
		t.Fatalf("unexpected defaults: %+v", p)
	}
	if p.Sharing == nil || p.Skips == nil || p.Items == nil || p.Contributions == nil || p.Adjustments == nil {
		t.Fatal("payload slices must be non-nil so they encode as []")
	}
}

func TestInvitedUsersSkipOnboardingAndPersonalFund(t *testing.T) {
	e := setup(t)
	u := e.A
	u.JoinedViaInvite = true
	fid := e.fundOf(u)
	if e.payload(u, fid).NeedsOnboarding {
		t.Fatal("invited user should not need onboarding")
	}
}

func TestSavesContributionWithAllocationsTogether(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	laptop := e.addItem(e.A, f, "Laptop", 1000)
	e.mustContribute(e.A, f, ContributionInput{Date: "2026-10-01", Note: " bonus ", Allocations: line(laptop.ID, 200)})
	cs := e.payload(e.A, f).Contributions
	if len(cs) != 1 || cs[0].Note != "bonus" || len(cs[0].Allocations) != 1 || cs[0].Kind != "deposit" {
		t.Fatalf("contributions: %+v", cs)
	}
}

func TestIdempotentCreateContribution(t *testing.T) {
	cid := "4f0c2f2e-2a52-4a53-9a4e-0d6a3b1c7e11"
	t.Run("saves once for same id and payload", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		laptop, phone := e.addItem(e.A, f, "Laptop", 1000), e.addItem(e.A, f, "Phone", 500)
		body := ContributionInput{ID: cid, Date: "2026-10-01", Note: "bonus", Allocations: []Line{{laptop.ID, 200}, {phone.ID, 50}}}
		e.mustContribute(e.A, f, body)
		// Same payload with lines in another order is still the same entry.
		body.Allocations = []Line{{phone.ID, 50}, {laptop.ID, 200}}
		e.mustContribute(e.A, f, body)
		if n := len(e.payload(e.A, f).Contributions); n != 1 {
			t.Fatalf("%d contributions", n)
		}
	})
	t.Run("saves once when sent at the same time", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		laptop := e.addItem(e.A, f, "Laptop", 1000)
		body := ContributionInput{ID: cid, Date: "2026-10-01", Allocations: line(laptop.ID, 200)}
		var wg sync.WaitGroup
		errs := make(chan error, 5)
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- e.contribute(e.A, f, body) }()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent retry failed: %v", err)
			}
		}
		if n := len(e.payload(e.A, f).Contributions); n != 1 {
			t.Fatalf("%d contributions", n)
		}
	})
	t.Run("409 for same id with a different payload or kind", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		laptop := e.addItem(e.A, f, "Laptop", 1000)
		e.mustContribute(e.A, f, ContributionInput{ID: cid, Date: "2026-10-01", Allocations: line(laptop.ID, 200)})
		for name, in := range map[string]ContributionInput{
			"amount": {ID: cid, Date: "2026-10-01", Allocations: line(laptop.ID, 201)},
			"date":   {ID: cid, Date: "2026-10-02", Allocations: line(laptop.ID, 200)},
			"note":   {ID: cid, Date: "2026-10-01", Note: "x", Allocations: line(laptop.ID, 200)},
			"kind":   {ID: cid, Date: "2026-10-01", Kind: "withdrawal", Allocations: line(laptop.ID, 200)},
		} {
			wantErr(t, e.contribute(e.A, f, in), 409, "That entry id is already used by another entry.")
			_ = name
		}
	})
	t.Run("does not judge an accepted withdrawal again on retry", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		laptop := e.addItem(e.A, f, "Laptop", 1000)
		e.mustContribute(e.A, f, ContributionInput{Date: "2026-10-01", Allocations: line(laptop.ID, 100)})
		w := ContributionInput{ID: cid, Date: "2026-10-02", Kind: "withdrawal", Allocations: line(laptop.ID, 100)}
		e.mustContribute(e.A, f, w)
		e.mustContribute(e.A, f, w) // balance is now 0; a fresh withdrawal would fail
		if n := len(e.payload(e.A, f).Contributions); n != 2 {
			t.Fatalf("%d contributions", n)
		}
	})
	t.Run("409 when the id belongs to another fund", func(t *testing.T) {
		e := setup(t)
		f1 := e.fundOf(e.A)
		laptop := e.addItem(e.A, f1, "Laptop", 1000)
		e.mustContribute(e.A, f1, ContributionInput{ID: cid, Date: "2026-10-01", Allocations: line(laptop.ID, 10)})
		f2, err := e.svc.CreateFund(bg, e.A, "Second")
		if err != nil {
			t.Fatal(err)
		}
		other := e.addItem(e.A, f2, "Phone", 500)
		wantErr(t, e.contribute(e.A, f2, ContributionInput{ID: cid, Date: "2026-10-01", Allocations: line(other.ID, 10)}), 409, "")
	})
	t.Run("400 for a non-uuid id and new entries with an id are still validated", func(t *testing.T) {
		e := setup(t)
		f := e.fundOf(e.A)
		laptop := e.addItem(e.A, f, "Laptop", 1000)
		wantErr(t, e.contribute(e.A, f, ContributionInput{ID: "not-a-uuid", Date: "2026-10-01", Allocations: line(laptop.ID, 10)}), 400, "That entry has an invalid id.")
		wantErr(t, e.contribute(e.A, f, ContributionInput{ID: cid, Date: future, Allocations: line(laptop.ID, 10)}), 400, "The date cannot be later than today.")
	})
}

func TestRollsBackWholeContributionWhenAnAllocationFails(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	laptop := e.addItem(e.A, f, "Laptop", 1000)
	err := e.contribute(e.A, f, ContributionInput{Date: "2026-10-01", Allocations: []Line{{laptop.ID, 10}, {"missing-item", 10}}})
	wantErr(t, err, 400, "One of those items is not in your fund.")
	if n := len(e.payload(e.A, f).Contributions); n != 0 {
		t.Fatalf("%d contributions survived", n)
	}
}

func TestItemPositionsAreUniqueUnderConcurrency(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := e.svc.AddItem(bg, e.A, f, ItemInput{Name: "C" + string(rune('1'+i)), Price: 100, Deadline: future}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	seen := map[int]bool{}
	for _, it := range e.payload(e.A, f).Items {
		if seen[it.Position] {
			t.Fatalf("duplicate position %d", it.Position)
		}
		seen[it.Position] = true
	}
	if len(seen) != 8 {
		t.Fatalf("%d items", len(seen))
	}
}

func TestFirstItemGetsPositionOne(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	if p := e.addItem(e.A, f, "First", 10).Position; p != 1 {
		t.Fatalf("first position %d (web app starts at 1)", p)
	}
	if p := e.addItem(e.A, f, "Second", 10).Position; p != 2 {
		t.Fatalf("second position %d", p)
	}
}

func TestRejectsAmountsThatOverflowTheDatabase(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	wantErr(t, e.svc.AddItem(bg, e.A, f, ItemInput{Name: "Yacht", Price: 9_999_999_999, Deadline: future}), 400, "That price is too large.")
	for name, in := range map[string]ItemInput{
		"zero price": {Name: "x", Price: 0, Deadline: future},
		"no name":    {Name: "  ", Price: 5, Deadline: future},
		"long name":  {Name: strings.Repeat("a", 81), Price: 5, Deadline: future},
		"bad date":   {Name: "x", Price: 5, Deadline: "2026-02-30"},
		"past date":  {Name: "x", Price: 5, Deadline: "2020-01-01"},
	} {
		if status(e.svc.AddItem(bg, e.A, f, in)) != 400 {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestItemEditsAndReplan(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	it := e.addItem(e.A, f, "Laptop", 1000)
	// A normal edit keeps planStart but rejects a deadline before it.
	name := "Laptop 2"
	if err := e.svc.UpdateItem(bg, e.A, f, it.ID, ItemPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	got := e.payload(e.A, f).Items[0]
	if got.Name != "Laptop 2" || got.PlanStart != it.PlanStart {
		t.Fatalf("edit: %+v", got)
	}
	early := "2000-01-01"
	wantErr(t, e.svc.UpdateItem(bg, e.A, f, it.ID, ItemPatch{Deadline: &early}), 400, "The deadline is before this item was added.")
	wantErr(t, e.svc.UpdateItem(bg, e.A, f, it.ID, ItemPatch{Deadline: &early, ReplanFromToday: true}), 400, "The deadline has to be today or later.")
	// Replan: planStart becomes today.
	if _, err := e.pool.Exec(bg, `update item set plan_start = '2020-01-01' where id = $1`, it.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.UpdateItem(bg, e.A, f, it.ID, ItemPatch{ReplanFromToday: true}); err != nil {
		t.Fatal(err)
	}
	if got := e.payload(e.A, f).Items[0]; got.PlanStart == "2020-01-01" {
		t.Fatalf("not replanned: %+v", got)
	}
	wantErr(t, e.svc.UpdateItem(bg, e.A, f, "nope", ItemPatch{Name: &name}), 404, "That item is not in your fund.")
}

func TestUsersCannotTouchEachOthersItems(t *testing.T) {
	e := setup(t)
	fa, fb := e.fundOf(e.A), e.fundOf(e.B)
	it := e.addItem(e.A, fa, "Laptop", 1000)
	name := "Hacked"
	wantErr(t, e.svc.UpdateItem(bg, e.B, fa, it.ID, ItemPatch{Name: &name}), 404, "That fund is not available to you.")
	wantErr(t, e.svc.UpdateItem(bg, e.B, fb, it.ID, ItemPatch{Name: &name}), 404, "That item is not in your fund.")
	wantErr(t, e.svc.DeleteItem(bg, e.B, fb, it.ID), 404, "That item is not in your fund.")
	wantErr(t, e.svc.DeleteItem(bg, e.B, fa, it.ID), 404, "That fund is not available to you.")
}

func TestRefusesToDeleteItemWithMoney(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	it := e.addItem(e.A, f, "Laptop", 1000)
	e.mustContribute(e.A, f, ContributionInput{Date: "2026-10-01", Allocations: line(it.ID, 10)})
	wantErr(t, e.svc.DeleteItem(bg, e.A, f, it.ID), 409, "Remove the contributions on this item first.")
	empty := e.addItem(e.A, f, "Empty", 5)
	if err := e.svc.DeleteItem(bg, e.A, f, empty.ID); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveRestoreAndReorderItems(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	a, b, c := e.addItem(e.A, f, "A", 10), e.addItem(e.A, f, "B", 10), e.addItem(e.A, f, "C", 10)
	order := func() []string {
		its := e.payload(e.A, f).Items
		sort.SliceStable(its, func(i, j int) bool { return its[i].Position < its[j].Position })
		out := []string{}
		for _, it := range its {
			if !it.Archived {
				out = append(out, it.Name)
			}
		}
		return out
	}
	if err := e.svc.MoveItem(bg, e.A, f, a.ID, "down"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order(), ""); got != "BAC" {
		t.Fatalf("after move down: %s", got)
	}
	if err := e.svc.ReorderItem(bg, e.A, f, c.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order(), ""); got != "CBA" {
		t.Fatalf("after reorder: %s", got)
	}
	if err := e.svc.ReorderItem(bg, e.A, f, c.ID, 99); err != nil { // clamped to the end
		t.Fatal(err)
	}
	if got := strings.Join(order(), ""); got != "BAC" {
		t.Fatalf("after clamped reorder: %s", got)
	}
	// Archiving needs a purchase; archived items leave the active order.
	wantErr(t, e.svc.SetItemArchived(bg, e.A, f, b.ID, true, nil), 400, "")
	wantErr(t, e.svc.SetItemArchived(bg, e.A, f, b.ID, true, &Purchase{ActualPrice: 9, PurchasedOn: "2026-13-01"}), 400, "Pick the date you bought it.")
	if err := e.svc.SetItemArchived(bg, e.A, f, b.ID, true, &Purchase{ActualPrice: 9, PurchasedOn: "2026-10-05"}); err != nil {
		t.Fatal(err)
	}
	for _, it := range e.payload(e.A, f).Items {
		if it.ID == b.ID && (!it.Archived || it.ActualPrice == nil || *it.ActualPrice != 9 || it.PurchasedOn == nil || *it.PurchasedOn != "2026-10-05") {
			t.Fatalf("archived item: %+v", it)
		}
	}
	if got := strings.Join(order(), ""); got != "AC" {
		t.Fatalf("active order after archive: %s", got)
	}
	if err := e.svc.SetItemArchived(bg, e.A, f, b.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	for _, it := range e.payload(e.A, f).Items {
		if it.ID == b.ID && (it.Archived || it.ActualPrice != nil || it.PurchasedOn != nil) {
			t.Fatalf("restored item: %+v", it)
		}
	}
}

func TestRejectsFutureDatedContributions(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	laptop := e.addItem(e.A, f, "Laptop", 1000)
	const msg = "The date cannot be later than today."
	wantErr(t, e.contribute(e.A, f, ContributionInput{Date: future, Allocations: line(laptop.ID, 10)}), 400, msg)
	e.mustContribute(e.A, f, ContributionInput{Date: "2026-10-01", Allocations: line(laptop.ID, 10)})
	saved := e.payload(e.A, f).Contributions[0]
	wantErr(t, e.svc.UpdateContribution(bg, e.A, f, saved.ID, ContributionInput{Date: future, Allocations: line(laptop.ID, 10)}), 400, msg)
	wantErr(t, e.svc.ImportContributions(bg, e.A, f, []ImportRow{{Date: future, Line: 2, Allocations: []ImportAlloc{{"Laptop", 5}}}}), 400, "Row 2: the date cannot be later than today.")
}

func TestImportsByItemNameAllOrNothing(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	e.addItem(e.A, f, "Laptop", 1000)
	if err := e.svc.ImportContributions(bg, e.A, f, []ImportRow{{Date: "2026-10-01", Note: "x", Line: 2, Allocations: []ImportAlloc{{"laptop", 5}}}}); err != nil {
		t.Fatal(err)
	}
	err := e.svc.ImportContributions(bg, e.A, f, []ImportRow{
		{Date: "2026-10-02", Line: 2, Allocations: []ImportAlloc{{"Laptop", 5}}},
		{Date: "2026-10-03", Line: 3, Allocations: []ImportAlloc{{"Nope", 5}}},
	})
	wantErr(t, err, 400, `"Nope" is not one of your items.`)
	if n := len(e.payload(e.A, f).Contributions); n != 1 {
		t.Fatalf("%d contributions (import must be atomic)", n)
	}
}

func TestWithdrawals(t *testing.T) {
	setupItem := func(t *testing.T) (*env, string, string) {
		e := setup(t)
		f := e.fundOf(e.A)
		it := e.addItem(e.A, f, "Laptop", 1000)
		e.mustContribute(e.A, f, ContributionInput{Date: "2026-10-01", Allocations: line(it.ID, 100)})
		return e, f, it.ID
	}
	w := func(date string, itemID string, amt int) ContributionInput {
		return ContributionInput{Date: date, Kind: "withdrawal", Allocations: line(itemID, amt)}
	}
	t.Run("rejects withdrawing more than held, allows exactly the balance", func(t *testing.T) {
		e, f, item := setupItem(t)
		wantErr(t, e.contribute(e.A, f, w("2026-10-02", item, 101)), 400, "Laptop would hold less than 0 on 2026-10-02.")
		e.mustContribute(e.A, f, w("2026-10-02", item, 100))
	})
	t.Run("same-day deposit then withdrawal is fine", func(t *testing.T) {
		e, f, item := setupItem(t)
		e.mustContribute(e.A, f, ContributionInput{Date: "2026-10-03", Allocations: line(item, 50)})
		e.mustContribute(e.A, f, w("2026-10-03", item, 150))
	})
	t.Run("rejects withdrawing before the money was there", func(t *testing.T) {
		e, f, item := setupItem(t)
		wantErr(t, e.contribute(e.A, f, w("2026-09-30", item, 10)), 400, "Laptop would hold less than 0 on 2026-09-30.")
	})
	t.Run("deleting a deposit that a later withdrawal depends on is rejected", func(t *testing.T) {
		e, f, item := setupItem(t)
		e.mustContribute(e.A, f, w("2026-10-05", item, 60))
		var deposit, withdrawal string
		for _, c := range e.payload(e.A, f).Contributions {
			if c.Kind == "deposit" {
				deposit = c.ID
			} else {
				withdrawal = c.ID
			}
		}
		wantErr(t, e.svc.DeleteContribution(bg, e.A, f, deposit), 400, "Laptop would hold less than 0 on 2026-10-05.")
		if err := e.svc.DeleteContribution(bg, e.A, f, withdrawal); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("editing a deposit down below what was withdrawn is rejected", func(t *testing.T) {
		e, f, item := setupItem(t)
		e.mustContribute(e.A, f, w("2026-10-05", item, 60))
		var deposit string
		for _, c := range e.payload(e.A, f).Contributions {
			if c.Kind == "deposit" {
				deposit = c.ID
			}
		}
		wantErr(t, e.svc.UpdateContribution(bg, e.A, f, deposit, ContributionInput{Date: "2026-10-01", Allocations: line(item, 50)}), 400, "")
		if err := e.svc.UpdateContribution(bg, e.A, f, deposit, ContributionInput{Date: "2026-10-01", Allocations: line(item, 60)}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rejects future-dated, line-less and purchased-item withdrawals", func(t *testing.T) {
		e, f, item := setupItem(t)
		wantErr(t, e.contribute(e.A, f, w(future, item, 10)), 400, "The date cannot be later than today.")
		wantErr(t, e.contribute(e.A, f, ContributionInput{Date: "2026-10-02", Kind: "withdrawal"}), 400, "")
		if err := e.svc.SetItemArchived(bg, e.A, f, item, true, &Purchase{ActualPrice: 90, PurchasedOn: "2026-10-04"}); err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.contribute(e.A, f, w("2026-10-06", item, 10)), 400, "Laptop is purchased, so you cannot withdraw from it.")
	})
	t.Run("checks the whole ledger when importing", func(t *testing.T) {
		e, f, _ := setupItem(t)
		// A file may list a withdrawal above its deposit; only the final ledger counts.
		err := e.svc.ImportContributions(bg, e.A, f, []ImportRow{
			{Date: "2026-10-10", Kind: "withdrawal", Line: 3, Allocations: []ImportAlloc{{"Laptop", 150}}},
			{Date: "2026-10-09", Kind: "deposit", Line: 4, Allocations: []ImportAlloc{{"Laptop", 100}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.svc.ImportContributions(bg, e.A, f, []ImportRow{
			{Date: "2026-10-11", Kind: "withdrawal", Line: 3, Allocations: []ImportAlloc{{"Laptop", 999}}},
		}), 400, "")
	})
	t.Run("viewers cannot withdraw", func(t *testing.T) {
		e, f, item := setupItem(t)
		if err := e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer")); err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.contribute(e.B, f, w("2026-10-02", item, 10)), 403, "You can view this fund but not change it.")
	})
}

func TestValidationOfContributionFields(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	it := e.addItem(e.A, f, "Laptop", 1000)
	long := strings.Repeat("x", 81)
	for name, in := range map[string]ContributionInput{
		"long note":   {Date: "2026-10-01", Note: long, Allocations: line(it.ID, 5)},
		"zero amount": {Date: "2026-10-01", Allocations: line(it.ID, 0)},
		"huge amount": {Date: "2026-10-01", Allocations: line(it.ID, MaxAmount+1)},
		"bad kind":    {Date: "2026-10-01", Kind: "gift", Allocations: line(it.ID, 5)},
		"bad date":    {Date: "2026-02-31", Allocations: line(it.ID, 5)},
		"no lines":    {Date: "2026-10-01"},
	} {
		if status(e.contribute(e.A, f, in)) != 400 {
			t.Errorf("%s accepted", name)
		}
	}
	// 80 UTF-16 units of emoji is 40 emoji; 41 is over the web app's limit.
	if err := e.contribute(e.A, f, ContributionInput{Date: "2026-10-01", Note: strings.Repeat("😀", 40), Allocations: line(it.ID, 5)}); err != nil {
		t.Fatal(err)
	}
	if status(e.contribute(e.A, f, ContributionInput{ID: id.New(), Date: "2026-10-01", Note: strings.Repeat("😀", 41), Allocations: line(it.ID, 5)})) != 400 {
		t.Fatal("note over 80 UTF-16 units accepted")
	}
}
