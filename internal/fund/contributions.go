package fund

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/id"
	"github.com/andwati/fund-backend/internal/plan"
)

type Line struct {
	ItemID string `json:"itemId"`
	Amount int    `json:"amount"`
}

// ContributionInput is a deposit or withdrawal split across items. ID is optional and makes a
// retried create idempotent (the offline queue relies on it).
type ContributionInput struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	Note        string `json:"note"`
	Kind        string `json:"kind"` // "" = deposit when creating, current kind when editing
	Allocations []Line `json:"allocations"`
}

func (c ContributionInput) validate() error {
	if c.ID != "" && !uuidRe.MatchString(c.ID) {
		return apperr.New(400, "That entry has an invalid id.")
	}
	if jsLen(c.Note) > NoteMax {
		return apperr.New(400, "Use 80 characters or fewer.")
	}
	if c.Kind != "" && c.Kind != plan.KindDeposit && c.Kind != plan.KindWithdrawal {
		return apperr.New(400, "The kind must be deposit or withdrawal.")
	}
	if len(c.Allocations) == 0 {
		return apperr.New(400, "Add at least one item.")
	}
	for _, l := range c.Allocations {
		if l.ItemID == "" {
			return apperr.New(400, "Pick an item.")
		}
		if err := checkAmount(l.Amount, "That amount is too large."); err != nil {
			return err
		}
	}
	return nil
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// breachError builds the 400 for a ledger that would go negative.
func (s *Service) assertLedgerNonNegative(ctx context.Context, q *dbgen.Queries, fundID string, itemIDs []string) error {
	uniq := map[string]bool{}
	ids := make([]string, 0, len(itemIDs))
	for _, v := range itemIDs {
		if !uniq[v] {
			uniq[v] = true
			ids = append(ids, v)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.ListLedgerLinesForItems(ctx, dbgen.ListLedgerLinesForItemsParams{FundID: fundID, ItemIds: ids})
	if err != nil {
		return err
	}
	lines := make([]plan.LedgerLine, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, plan.LedgerLine{ContributionID: r.ContributionID, ItemID: r.ItemID, Date: r.Date,
			Kind: r.Kind, Amount: float64(r.Amount), CreatedAt: r.CreatedAt.Time})
	}
	b := plan.FirstLedgerBreach(lines, nil)
	if b == nil {
		return nil
	}
	name := "That item"
	if n, err := q.GetItemName(ctx, b.ItemID); err == nil {
		name = n
	}
	return apperr.New(400, fmt.Sprintf("%s would hold less than 0 on %s.", name, b.Date))
}

// checkContribution validates date, items and withdrawal rules and returns the cleaned note.
func (s *Service) checkContribution(ctx context.Context, q *dbgen.Queries, f Fund, in ContributionInput, kind string, existing map[string]bool) (string, error) {
	if !plan.ValidDate(in.Date) {
		if kind == plan.KindWithdrawal {
			return "", apperr.New(400, "Pick the date you took the money out.")
		}
		return "", apperr.New(400, "Pick the date you set the money aside.")
	}
	if in.Date > plan.TodayIn(f.Timezone, s.now()) {
		return "", apperr.New(400, "The date cannot be later than today.")
	}
	items, err := q.ListFundItemsForContribution(ctx, f.ID)
	if err != nil {
		return "", err
	}
	byID := map[string]dbgen.ListFundItemsForContributionRow{}
	for _, it := range items {
		byID[it.ID] = it
	}
	for _, l := range in.Allocations {
		if _, ok := byID[l.ItemID]; !ok {
			return "", apperr.New(400, "One of those items is not in your fund.")
		}
	}
	if kind == plan.KindWithdrawal {
		for _, l := range in.Allocations {
			it := byID[l.ItemID]
			if it.Archived && !existing[it.ID] {
				return "", apperr.New(400, fmt.Sprintf("%s is purchased, so you cannot withdraw from it.", it.Name))
			}
		}
	}
	return jsSlice(jsTrim(in.Note), NoteMax), nil
}

func linesKey(ls []Line) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = fmt.Sprintf("%s:%d", l.ItemID, l.Amount)
	}
	sort.Strings(out)
	return out
}

// replayed reports whether id already exists with exactly this payload in this fund (a retried
// create, so a no-op). The same id with anything else is a 409.
func (s *Service) replayed(ctx context.Context, q *dbgen.Queries, fundID, cid, date, note, kind string, lines []Line) (bool, error) {
	row, err := q.GetContribution(ctx, cid)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	allocs, err := q.ListContributionAllocations(ctx, cid)
	if err != nil {
		return false, err
	}
	have := make([]Line, 0, len(allocs))
	for _, a := range allocs {
		have = append(have, Line{ItemID: a.ItemID, Amount: int(a.Amount)})
	}
	a, b := linesKey(have), linesKey(lines)
	same := row.FundID == fundID && row.Date == date && row.Note == note && row.Kind == kind && len(a) == len(b)
	for i := 0; same && i < len(a); i++ {
		same = a[i] == b[i]
	}
	if !same {
		return false, apperr.New(409, "That entry id is already used by another entry.")
	}
	return true, nil
}

// AddContribution records a deposit or withdrawal. A client id makes a retry a no-op.
func (s *Service) AddContribution(ctx context.Context, user auth.User, fundID string, in ContributionInput) error {
	if err := in.validate(); err != nil {
		return err
	}
	kind := in.Kind
	if kind == "" {
		kind = plan.KindDeposit
	}
	acc, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit)
	if err != nil {
		return err
	}
	cid := in.ID
	if cid == "" {
		cid = id.New()
	}
	sentNote := jsSlice(jsTrim(in.Note), NoteMax)
	// A retried entry is answered before any other check, so an accepted withdrawal is not
	// judged again against the new balance.
	if in.ID != "" {
		if ok, err := s.replayed(ctx, s.q, fundID, in.ID, in.Date, sentNote, kind, in.Allocations); err != nil || ok {
			return err
		}
	}
	note, err := s.checkContribution(ctx, s.q, acc.Fund, in, kind, nil)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockFund(ctx, fundID); err != nil {
			return err
		}
		if err := q.InsertContribution(ctx, dbgen.InsertContributionParams{ID: cid, FundID: fundID, Date: in.Date, Note: note, Kind: kind}); err != nil {
			return err
		}
		ids := make([]string, 0, len(in.Allocations))
		for _, l := range in.Allocations {
			if err := q.InsertAllocation(ctx, dbgen.InsertAllocationParams{ID: id.New(), ContributionID: cid, ItemID: l.ItemID, Amount: int32(l.Amount)}); err != nil {
				return err
			}
			ids = append(ids, l.ItemID)
		}
		return s.assertLedgerNonNegative(ctx, q, fundID, ids)
	})
	if err != nil && in.ID != "" && isUnique(err) {
		// Two requests with the same id raced: the loser re-checks the winner.
		if ok, rerr := s.replayed(ctx, s.q, fundID, in.ID, in.Date, sentNote, kind, in.Allocations); rerr != nil || ok {
			return rerr
		}
	}
	return err
}

// UpdateContribution replaces a contribution's header and lines, keeping the ledger non-negative.
func (s *Service) UpdateContribution(ctx context.Context, user auth.User, fundID, cid string, in ContributionInput) error {
	if err := in.validate(); err != nil {
		return err
	}
	acc, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit)
	if err != nil {
		return err
	}
	cur, err := s.q.GetContribution(ctx, cid)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && cur.FundID != fundID) {
		return apperr.New(404, "That contribution is not in your fund.")
	}
	if err != nil {
		return err
	}
	kind := in.Kind
	if kind == "" {
		kind = cur.Kind
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockFund(ctx, fundID); err != nil {
			return err
		}
		before, err := q.ListContributionAllocations(ctx, cid)
		if err != nil {
			return err
		}
		existing := map[string]bool{}
		ids := make([]string, 0, len(before)+len(in.Allocations))
		for _, b := range before {
			existing[b.ItemID] = true
			ids = append(ids, b.ItemID)
		}
		note, err := s.checkContribution(ctx, q, acc.Fund, in, kind, existing)
		if err != nil {
			return err
		}
		if err := q.UpdateContributionHeader(ctx, dbgen.UpdateContributionHeaderParams{ID: cid, FundID: fundID, Date: in.Date, Note: note, Kind: kind}); err != nil {
			return err
		}
		if err := q.DeleteAllocationsFor(ctx, cid); err != nil {
			return err
		}
		for _, l := range in.Allocations {
			if err := q.InsertAllocation(ctx, dbgen.InsertAllocationParams{ID: id.New(), ContributionID: cid, ItemID: l.ItemID, Amount: int32(l.Amount)}); err != nil {
				return err
			}
			ids = append(ids, l.ItemID)
		}
		return s.assertLedgerNonNegative(ctx, q, fundID, ids)
	})
}

// DeleteContribution removes a contribution; refused if that would leave an item negative.
func (s *Service) DeleteContribution(ctx context.Context, user auth.User, fundID, cid string) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit); err != nil {
		return err
	}
	cur, err := s.q.GetContribution(ctx, cid)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && cur.FundID != fundID) {
		return apperr.New(404, "That contribution is not in your fund.")
	}
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockFund(ctx, fundID); err != nil {
			return err
		}
		before, err := q.ListContributionAllocations(ctx, cid)
		if err != nil {
			return err
		}
		if err := q.DeleteContribution(ctx, dbgen.DeleteContributionParams{ID: cid, FundID: fundID}); err != nil {
			return err
		}
		ids := make([]string, 0, len(before))
		for _, b := range before {
			ids = append(ids, b.ItemID)
		}
		return s.assertLedgerNonNegative(ctx, q, fundID, ids)
	})
}

// ImportRow is one contribution parsed from a CSV file.
type ImportRow struct {
	Date        string
	Note        string
	Line        int
	Kind        string
	Allocations []ImportAlloc
}

type ImportAlloc struct {
	Item   string
	Amount int
}

// ImportContributions adds parsed CSV rows all-or-nothing. Items are matched by trimmed,
// lower-cased name among active items.
func (s *Service) ImportContributions(ctx context.Context, user auth.User, fundID string, rows []ImportRow) error {
	acc, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit)
	if err != nil {
		return err
	}
	items, err := s.q.ListActiveItemNames(ctx, fundID)
	if err != nil {
		return err
	}
	byName := map[string]string{}
	for _, it := range items {
		byName[strings.ToLower(jsTrim(it.Name))] = it.ID // later duplicates win, like a JS Map
	}
	today := plan.TodayIn(acc.Fund.Timezone, s.now())
	type prepared struct {
		date, note, kind string
		lines            []Line
	}
	prep := make([]prepared, 0, len(rows))
	for _, r := range rows {
		lines := make([]Line, 0, len(r.Allocations))
		for _, a := range r.Allocations {
			itemID, ok := byName[strings.ToLower(jsTrim(a.Item))]
			if !ok {
				return apperr.New(400, fmt.Sprintf("\"%s\" is not one of your items.", a.Item))
			}
			lines = append(lines, Line{ItemID: itemID, Amount: a.Amount})
		}
		kind := r.Kind
		if kind == "" {
			kind = plan.KindDeposit
		}
		body := ContributionInput{Date: r.Date, Note: jsSlice(r.Note, NoteMax), Kind: kind, Allocations: lines}
		if err := body.validate(); err != nil {
			return err
		}
		if !plan.ValidDate(body.Date) {
			return apperr.New(400, fmt.Sprintf("%s is not a real date.", r.Date))
		}
		if body.Date > today {
			return apperr.New(400, fmt.Sprintf("Row %d: the date cannot be later than today.", r.Line))
		}
		prep = append(prep, prepared{body.Date, jsTrim(body.Note), kind, lines})
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockFund(ctx, fundID); err != nil {
			return err
		}
		var ids []string
		for _, p := range prep {
			cid := id.New()
			if err := q.InsertContribution(ctx, dbgen.InsertContributionParams{ID: cid, FundID: fundID, Date: p.date, Note: p.note, Kind: p.kind}); err != nil {
				return err
			}
			for _, l := range p.lines {
				if err := q.InsertAllocation(ctx, dbgen.InsertAllocationParams{ID: id.New(), ContributionID: cid, ItemID: l.ItemID, Amount: int32(l.Amount)}); err != nil {
					return err
				}
				ids = append(ids, l.ItemID)
			}
		}
		// After every row is in, so a file may list a deposit below its withdrawal.
		return s.assertLedgerNonNegative(ctx, q, fundID, ids)
	})
}

var _ = time.Second
