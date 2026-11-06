package fund

import (
	"context"

	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/plan"
)

func needsOnboarding(role Role, onboardingCompleted, joinedViaInvite bool) bool {
	return role == RoleOwner && !onboardingCompleted && !joinedViaInvite
}

// Payload loads everything the clients render for one fund (any role may view it).
func (s *Service) Payload(ctx context.Context, user auth.User, fundID string) (Payload, error) {
	return s.payload(ctx, s.q, user, fundID)
}

func (s *Service) payload(ctx context.Context, q *dbgen.Queries, user auth.User, fundID string) (Payload, error) {
	acc, err := s.access(ctx, q, user.ID, fundID, NeedView)
	if err != nil {
		return Payload{}, err
	}
	funds, err := s.ListFunds(ctx, user)
	if err != nil {
		return Payload{}, err
	}
	today := plan.TodayIn(acc.Fund.Timezone, s.now())

	items, err := q.ListItems(ctx, fundID)
	if err != nil {
		return Payload{}, err
	}
	contribs, err := q.ListContributions(ctx, fundID)
	if err != nil {
		return Payload{}, err
	}
	allocs, err := q.ListAllocationsForFund(ctx, fundID)
	if err != nil {
		return Payload{}, err
	}
	skips, err := q.ListSkipsFrom(ctx, dbgen.ListSkipsFromParams{FundID: fundID, FromDate: today})
	if err != nil {
		return Payload{}, err
	}
	adjs, err := q.ListAdjustments(ctx, fundID)
	if err != nil {
		return Payload{}, err
	}
	reminders := false
	if pref, err := q.GetReminderPref(ctx, dbgen.GetReminderPrefParams{FundID: fundID, UserID: user.ID}); err == nil {
		reminders = pref.Enabled
	}

	p := Payload{
		Role: acc.Role, Funds: funds,
		Fund: FundView{ID: acc.Fund.ID, Name: acc.Fund.Name, Paydays: acc.Fund.Paydays, Pace: acc.Fund.Pace,
			Currency: acc.Fund.Currency, Timezone: acc.Fund.Timezone, TimezoneConfirmed: acc.Fund.TimezoneConfirmed,
			OnboardingCompleted: acc.Fund.OnboardingCompleted, RemindersEnabled: reminders},
		NeedsOnboarding: needsOnboarding(acc.Role, acc.Fund.OnboardingCompleted, user.JoinedViaInvite),
		Skips:           skips,
		Adjustments:     make([]AdjustmentView, 0, len(adjs)),
		Items:           make([]ItemView, 0, len(items)),
		Contributions:   make([]ContributionView, 0, len(contribs)),
	}
	for _, a := range adjs {
		p.Adjustments = append(p.Adjustments, AdjustmentView{Date: a.Date, Amount: int(a.Amount)})
	}
	for _, it := range items {
		v := ItemView{ID: it.ID, Name: it.Name, Price: int(it.Price), Deadline: it.Deadline, PlanStart: it.PlanStart,
			Position: int(it.Position), Archived: it.ArchivedAt.Valid, ActualPrice: int32p(it.ActualPrice)}
		if it.PurchasedOn != "" {
			po := it.PurchasedOn
			v.PurchasedOn = &po
		}
		p.Items = append(p.Items, v)
	}
	byContribution := map[string][]AllocationView{}
	for _, a := range allocs {
		byContribution[a.ContributionID] = append(byContribution[a.ContributionID],
			AllocationView{ID: a.ID, ItemID: a.ItemID, Amount: int(a.Amount)})
	}
	for _, c := range contribs {
		lines := byContribution[c.ID]
		if lines == nil {
			lines = []AllocationView{}
		}
		p.Contributions = append(p.Contributions, ContributionView{ID: c.ID, Date: c.Date, Note: c.Note, Kind: c.Kind, Allocations: lines})
	}
	if acc.Role == RoleOwner {
		sh, err := s.sharing(ctx, q, fundID)
		if err != nil {
			return Payload{}, err
		}
		p.Sharing = &sh
	}
	return p, nil
}

func (s *Service) sharing(ctx context.Context, q *dbgen.Queries, fundID string) (Sharing, error) {
	members, err := q.ListMembers(ctx, fundID)
	if err != nil {
		return Sharing{}, err
	}
	invites, err := q.ListInvites(ctx, fundID)
	if err != nil {
		return Sharing{}, err
	}
	out := Sharing{Members: make([]MemberView, 0, len(members)), Invites: make([]InviteView, 0, len(invites))}
	for _, m := range members {
		u, err := q.GetUserByID(ctx, m.UserID)
		if err != nil {
			return Sharing{}, err
		}
		out.Members = append(out.Members, MemberView{UserID: m.UserID, Name: m.Name, Email: u.Email, Role: m.Role})
	}
	for _, i := range invites {
		out.Invites = append(out.Invites, InviteView{ID: i.ID, Email: i.Email, Role: i.Role,
			ExpiresAt: i.ExpiresAt.Time.UTC().Format("2006-01-02T15:04:05.000Z")}) // like Date#toISOString
	}
	return out, nil
}
