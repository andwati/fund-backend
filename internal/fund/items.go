package fund

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/id"
	"github.com/andwati/fund-backend/internal/plan"
)

type ItemInput struct {
	Name     string `json:"name"`
	Price    int    `json:"price"`
	Deadline string `json:"deadline"`
}

type ItemPatch struct {
	Name            *string `json:"name"`
	Price           *int    `json:"price"`
	Deadline        *string `json:"deadline"`
	ReplanFromToday bool    `json:"replanFromToday"`
}

type Purchase struct {
	ActualPrice int    `json:"actualPrice"`
	PurchasedOn string `json:"purchasedOn"`
}

func checkPrice(p int) error { return checkAmount(p, "That price is too large.") }

// AddItem adds an item whose plan starts today. Positions are assigned under the fund lock.
func (s *Service) AddItem(ctx context.Context, user auth.User, fundID string, in ItemInput) error {
	name, err := cleanName(in.Name)
	if err != nil {
		return err
	}
	if err := checkPrice(in.Price); err != nil {
		return err
	}
	if !plan.ValidDate(in.Deadline) {
		return apperr.New(400, "Pick a real deadline.")
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		acc, err := s.access(ctx, q, user.ID, fundID, NeedEdit)
		if err != nil {
			return err
		}
		start := plan.TodayIn(acc.Fund.Timezone, s.now())
		if in.Deadline < start {
			return apperr.New(400, "The deadline has to be today or later.")
		}
		if _, err := q.LockFund(ctx, fundID); err != nil {
			return err
		}
		maxPos, err := q.MaxItemPosition(ctx, fundID)
		if err != nil {
			return err
		}
		return q.InsertItem(ctx, dbgen.InsertItemParams{ID: id.New(), FundID: fundID, Name: name, Price: int32(in.Price),
			Deadline: in.Deadline, PlanStart: start, Position: maxPos + 1})
	})
}

func (s *Service) ownedItem(ctx context.Context, q *dbgen.Queries, fundID, itemID string) (dbgen.GetItemRow, error) {
	it, err := q.GetItem(ctx, dbgen.GetItemParams{ID: itemID, FundID: fundID})
	if errors.Is(err, pgx.ErrNoRows) {
		return it, apperr.New(404, "That item is not in your fund.")
	}
	return it, err
}

// UpdateItem edits an item. replanFromToday restarts its plan from today.
func (s *Service) UpdateItem(ctx context.Context, user auth.User, fundID, itemID string, p ItemPatch) error {
	if p.Name != nil {
		n, err := cleanName(*p.Name)
		if err != nil {
			return err
		}
		p.Name = &n
	}
	if p.Price != nil {
		if err := checkPrice(*p.Price); err != nil {
			return err
		}
	}
	if p.Deadline != nil && !plan.ValidDate(*p.Deadline) {
		return apperr.New(400, "Pick a real deadline.")
	}
	acc, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit)
	if err != nil {
		return err
	}
	cur, err := s.ownedItem(ctx, s.q, fundID, itemID)
	if err != nil {
		return err
	}
	today := plan.TodayIn(acc.Fund.Timezone, s.now())
	deadline := cur.Deadline
	if p.Deadline != nil {
		deadline = *p.Deadline
	}
	if p.ReplanFromToday {
		if deadline < today {
			return apperr.New(400, "The deadline has to be today or later.")
		}
	} else if p.Deadline != nil && *p.Deadline < cur.PlanStart {
		return apperr.New(400, "The deadline is before this item was added.")
	}
	next := dbgen.UpdateItemParams{ID: cur.ID, FundID: fundID, Name: cur.Name, Price: cur.Price,
		Deadline: cur.Deadline, PlanStart: cur.PlanStart}
	if p.Name != nil {
		next.Name = *p.Name
	}
	if p.Price != nil {
		next.Price = int32(*p.Price)
	}
	if p.Deadline != nil {
		next.Deadline = *p.Deadline
	}
	if p.ReplanFromToday {
		next.PlanStart = today
	}
	return s.q.UpdateItem(ctx, next)
}

// DeleteItem removes an item that has no contributions.
func (s *Service) DeleteItem(ctx context.Context, user auth.User, fundID, itemID string) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit); err != nil {
		return err
	}
	if _, err := s.ownedItem(ctx, s.q, fundID, itemID); err != nil {
		return err
	}
	n, err := s.q.CountItemAllocations(ctx, itemID)
	if err != nil {
		return err
	}
	if n > 0 {
		return apperr.New(409, "Remove the contributions on this item first.")
	}
	return s.q.DeleteItem(ctx, dbgen.DeleteItemParams{ID: itemID, FundID: fundID})
}

// SetItemArchived marks an item purchased (with its price and date) or brings it back.
func (s *Service) SetItemArchived(ctx context.Context, user auth.User, fundID, itemID string, archived bool, purchase *Purchase) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit); err != nil {
		return err
	}
	cur, err := s.ownedItem(ctx, s.q, fundID, itemID)
	if err != nil {
		return err
	}
	if !archived {
		return s.q.UnarchiveItem(ctx, dbgen.UnarchiveItemParams{ID: cur.ID, FundID: fundID})
	}
	if purchase == nil {
		return apperr.New(400, "Enter what you paid and the date you bought it.")
	}
	if err := checkAmount(purchase.ActualPrice, "That amount is too large."); err != nil {
		return err
	}
	if !plan.ValidDate(purchase.PurchasedOn) {
		return apperr.New(400, "Pick the date you bought it.")
	}
	price := int32(purchase.ActualPrice)
	return s.q.ArchiveItem(ctx, dbgen.ArchiveItemParams{ID: cur.ID, FundID: fundID, ActualPrice: &price, PurchasedOn: purchase.PurchasedOn})
}

// saveItemOrder renumbers the whole list (positions can tie, so swapping two is not enough).
func (s *Service) saveItemOrder(ctx context.Context, fundID string, ids []string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		for pos, itemID := range ids {
			if err := q.SetItemPosition(ctx, dbgen.SetItemPositionParams{ID: itemID, FundID: fundID, Position: int32(pos)}); err != nil {
				return err
			}
		}
		return nil
	})
}

func indexOf(ids []string, v string) int {
	for i, x := range ids {
		if x == v {
			return i
		}
	}
	return -1
}

// MoveItem swaps an item with its neighbour in the display order.
func (s *Service) MoveItem(ctx context.Context, user auth.User, fundID, itemID, direction string) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit); err != nil {
		return err
	}
	cur, err := s.ownedItem(ctx, s.q, fundID, itemID)
	if err != nil {
		return err
	}
	order, err := s.q.ListActiveItemIDs(ctx, fundID)
	if err != nil {
		return err
	}
	i := indexOf(order, cur.ID)
	j := i + 1
	if direction == "up" {
		j = i - 1
	}
	if i == -1 { // archived item: the web app only renumbers, never reorders
		if direction == "up" {
			return nil
		}
		return s.saveItemOrder(ctx, fundID, order)
	}
	if j < 0 || j >= len(order) {
		return nil
	}
	order[i], order[j] = order[j], order[i]
	return s.saveItemOrder(ctx, fundID, order)
}

// ReorderItem moves an item to a zero-based index among active items (clamped).
func (s *Service) ReorderItem(ctx context.Context, user auth.User, fundID, itemID string, toIndex int) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedEdit); err != nil {
		return err
	}
	cur, err := s.ownedItem(ctx, s.q, fundID, itemID)
	if err != nil {
		return err
	}
	order, err := s.q.ListActiveItemIDs(ctx, fundID)
	if err != nil {
		return err
	}
	from := indexOf(order, cur.ID)
	if from == -1 {
		return nil
	}
	target := toIndex
	if target > len(order)-1 {
		target = len(order) - 1
	}
	if target < 0 {
		target = 0
	}
	if from == target {
		return nil
	}
	order = append(order[:from], order[from+1:]...)
	order = append(order[:target], append([]string{cur.ID}, order[target:]...)...)
	return s.saveItemOrder(ctx, fundID, order)
}
