package fund

import (
	"context"
	"fmt"
	"time"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/export"
	"github.com/andwati/fund-backend/internal/money"
	"github.com/andwati/fund-backend/internal/plan"
)

const (
	AccountExportLimit  = 5
	AccountExportWindow = time.Hour
)

// isoMillis formats like Date#toISOString.
func isoMillis(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func planContributions(cs []ContributionView, keep map[string]bool) []plan.ContributionInput {
	out := make([]plan.ContributionInput, 0, len(cs))
	for _, c := range cs {
		lines := make([]plan.AllocationInput, 0, len(c.Allocations))
		for _, a := range c.Allocations {
			if keep == nil || keep[a.ItemID] {
				lines = append(lines, plan.AllocationInput{ItemID: a.ItemID, Amount: float64(a.Amount)})
			}
		}
		out = append(out, plan.ContributionInput{ID: c.ID, Date: c.Date, Note: c.Note, Kind: c.Kind, Allocations: lines})
	}
	return out
}

// ExportContributionsCSV is the fund's contributions download (any role).
func (s *Service) ExportContributionsCSV(ctx context.Context, user auth.User, fundID string) (string, error) {
	p, err := s.Payload(ctx, user, fundID)
	if err != nil {
		return "", err
	}
	names := map[string]string{}
	for _, it := range p.Items {
		names[it.ID] = it.Name
	}
	rows := make([]export.ContributionRow, 0, len(p.Contributions))
	for _, c := range p.Contributions {
		lines := make([]export.AllocationLine, 0, len(c.Allocations))
		for _, a := range c.Allocations {
			lines = append(lines, export.AllocationLine{ItemID: a.ItemID, Amount: a.Amount})
		}
		rows = append(rows, export.ContributionRow{Date: c.Date, Note: c.Note, Kind: c.Kind, Allocations: lines})
	}
	return export.ContributionsCSV(rows, func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		return "Item"
	}), nil
}

// ExportItemsCSV is the fund's items download (any role).
func (s *Service) ExportItemsCSV(ctx context.Context, user auth.User, fundID string) (string, error) {
	p, err := s.Payload(ctx, user, fundID)
	if err != nil {
		return "", err
	}
	saved := plan.SavedByItem(planContributions(p.Contributions, nil))
	rows := make([]export.ItemRow, 0, len(p.Items))
	for _, it := range p.Items {
		rows = append(rows, export.ItemRow{Name: it.Name, Price: it.Price, Deadline: it.Deadline, PlanStart: it.PlanStart,
			Saved: int(saved[it.ID]), ActualPrice: it.ActualPrice, PurchasedOn: it.PurchasedOn, Archived: it.Archived})
	}
	return export.ItemsCSV(rows), nil
}

// ExportScheduleICS is an all-day calendar event per upcoming payday (any role).
func (s *Service) ExportScheduleICS(ctx context.Context, user auth.User, fundID string) (string, error) {
	p, err := s.Payload(ctx, user, fundID)
	if err != nil {
		return "", err
	}
	active := make([]plan.ItemInput, 0, len(p.Items))
	ids := map[string]bool{}
	for _, it := range p.Items {
		if it.Archived {
			continue
		}
		active = append(active, plan.ItemInput{ID: it.ID, Name: it.Name, Price: float64(it.Price), Deadline: it.Deadline, PlanStart: it.PlanStart})
		ids[it.ID] = true
	}
	adj := make([]plan.Adjustment, 0, len(p.Adjustments))
	for _, a := range p.Adjustments {
		adj = append(adj, plan.Adjustment{Date: a.Date, Amount: float64(a.Amount)})
	}
	pace := 0.0
	if p.Fund.Pace != nil {
		pace = float64(*p.Fund.Pace)
	}
	progress := plan.Compute(plan.FundInput{
		Schedule: plan.ScheduleOf(p.Fund.Paydays, p.Skips, adj), Pace: pace, Timezone: p.Fund.Timezone,
		Now: s.now(), Items: active, Contributions: planContributions(p.Contributions, ids),
	})
	return export.ScheduleICS(p.Fund.Name, plan.UpcomingPaydays(progress), func(date string) string {
		if amount := plan.ExpectedOn(progress, date); amount > 0 {
			return "Payday: set aside about " + money.Money(p.Fund.Currency, amount)
		}
		return "Payday"
	}, s.now()), nil
}

// ExportAccountZip bundles the caller's data (rate limited to 5 per hour). Other people's
// email addresses are left out on purpose.
func (s *Service) ExportAccountZip(ctx context.Context, user auth.User) ([]byte, error) {
	if ok, retry := s.limiter.Take("account-export:"+user.ID, AccountExportLimit, AccountExportWindow); !ok {
		return nil, apperr.Newf(429, "You can export your data up to %d times per hour. Try again in %d seconds.", AccountExportLimit, retry)
	}
	profile, err := s.q.GetUserByID(ctx, user.ID)
	if err != nil {
		return nil, apperr.New(404, "Account not found.")
	}
	rows, err := s.q.ListFundsForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	bundle := export.AccountBundle{
		ExportedAt: isoMillis(s.now()),
		Account: export.AccountUser{ID: profile.ID, Name: profile.Name, Email: profile.Email,
			EmailVerified: profile.EmailVerified, CreatedAt: isoMillis(profile.CreatedAt.Time)},
		Memberships: []export.Membership{},
		OwnedFunds:  []export.OwnedFund{},
	}
	for _, r := range rows {
		f, err := s.loadFund(ctx, s.q, r.ID)
		if err != nil {
			return nil, err
		}
		if r.Role == string(RoleOwner) {
			of, err := s.ownedFundExport(ctx, f, r.CreatedAt.Time)
			if err != nil {
				return nil, err
			}
			bundle.OwnedFunds = append(bundle.OwnedFunds, of)
			continue
		}
		joined := r.CreatedAt.Time
		if m, err := s.q.GetMember(ctx, dbgen.GetMemberParams{FundID: r.ID, UserID: user.ID}); err == nil {
			joined = m.CreatedAt.Time
		}
		reminders := false
		if pref, err := s.q.GetReminderPref(ctx, dbgen.GetReminderPrefParams{FundID: r.ID, UserID: user.ID}); err == nil {
			reminders = pref.Enabled
		}
		bundle.Memberships = append(bundle.Memberships, export.Membership{FundID: r.ID, Name: f.Name, Role: r.Role,
			Paydays: f.Paydays, Pace: f.Pace, Currency: f.Currency, Timezone: f.Timezone,
			TimezoneConfirmed: f.TimezoneConfirmed, RemindersEnabled: reminders, JoinedAt: isoMillis(joined)})
	}
	return export.AccountExportZip(bundle)
}

func (s *Service) ownedFundExport(ctx context.Context, f Fund, createdAt time.Time) (export.OwnedFund, error) {
	items, err := s.q.ListItems(ctx, f.ID)
	if err != nil {
		return export.OwnedFund{}, err
	}
	contribs, err := s.q.ListContributions(ctx, f.ID)
	if err != nil {
		return export.OwnedFund{}, err
	}
	allocs, err := s.q.ListAllocationsForFund(ctx, f.ID)
	if err != nil {
		return export.OwnedFund{}, err
	}
	members, err := s.q.ListMembers(ctx, f.ID)
	if err != nil {
		return export.OwnedFund{}, err
	}
	invites, err := s.q.ListInvites(ctx, f.ID)
	if err != nil {
		return export.OwnedFund{}, err
	}
	reminders := false
	if pref, err := s.q.GetReminderPref(ctx, dbgen.GetReminderPrefParams{FundID: f.ID, UserID: f.OwnerID}); err == nil {
		reminders = pref.Enabled
	}

	byContribution := map[string][]export.OwnedAllocation{}
	forPlan := map[string][]plan.AllocationInput{}
	for _, a := range allocs {
		byContribution[a.ContributionID] = append(byContribution[a.ContributionID], export.OwnedAllocation{ID: a.ID, ItemID: a.ItemID, Amount: int(a.Amount)})
		forPlan[a.ContributionID] = append(forPlan[a.ContributionID], plan.AllocationInput{ItemID: a.ItemID, Amount: float64(a.Amount)})
	}
	cs := make([]export.OwnedContribution, 0, len(contribs))
	pcs := make([]plan.ContributionInput, 0, len(contribs))
	for _, c := range contribs {
		lines := byContribution[c.ID]
		if lines == nil {
			lines = []export.OwnedAllocation{}
		}
		cs = append(cs, export.OwnedContribution{ID: c.ID, Date: c.Date, Note: c.Note, Kind: c.Kind, Allocations: lines})
		pcs = append(pcs, plan.ContributionInput{Kind: c.Kind, Allocations: forPlan[c.ID]})
	}
	saved := plan.SavedByItem(pcs)
	is := make([]export.OwnedItem, 0, len(items))
	for _, it := range items {
		oi := export.OwnedItem{ID: it.ID, Name: it.Name, Price: int(it.Price), Deadline: it.Deadline, PlanStart: it.PlanStart,
			Position: int(it.Position), Saved: int(saved[it.ID]), ActualPrice: int32p(it.ActualPrice), Archived: it.ArchivedAt.Valid}
		if it.PurchasedOn != "" {
			po := it.PurchasedOn
			oi.PurchasedOn = &po
		}
		is = append(is, oi)
	}
	ms := make([]export.OwnedMember, 0, len(members))
	for _, m := range members {
		ms = append(ms, export.OwnedMember{UserID: m.UserID, Name: m.Name, Role: m.Role})
	}
	iv := make([]export.OwnedInvite, 0, len(invites))
	for _, i := range invites {
		iv = append(iv, export.OwnedInvite{ID: i.ID, Role: i.Role, ExpiresAt: isoMillis(i.ExpiresAt.Time)})
	}
	return export.OwnedFund{
		Folder: export.FundExportFolder(f.Name, f.ID),
		Fund: export.OwnedFundInfo{ID: f.ID, Name: f.Name, Paydays: f.Paydays, Pace: f.Pace, Currency: f.Currency, Timezone: f.Timezone,
			TimezoneConfirmed: f.TimezoneConfirmed, RemindersEnabled: reminders, CreatedAt: isoMillis(createdAt)},
		Members: ms, PendingInvites: iv, Items: is, Contributions: cs,
	}, nil
}

var _ = fmt.Sprint
