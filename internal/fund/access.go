package fund

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/id"
)

// Fund is a fund row with its paydays (ascending; a fund with none counts as [31]).
type Fund struct {
	ID                  string
	OwnerID             string
	Name                string
	Pace                *int
	Currency            string
	Timezone            string
	TimezoneConfirmed   bool
	OnboardingCompleted bool
	Paydays             []int
}

// Access is a caller's view of one fund.
type Access struct {
	Fund Fund
	Role Role
}

func int32p(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func (s *Service) loadPaydays(ctx context.Context, q *dbgen.Queries, fundID string) ([]int, error) {
	days, err := q.ListPaydays(ctx, fundID)
	if err != nil {
		return nil, err
	}
	if len(days) == 0 {
		return []int{31}, nil
	}
	out := make([]int, len(days))
	for i, d := range days {
		out[i] = int(d)
	}
	return out, nil
}

func (s *Service) loadFund(ctx context.Context, q *dbgen.Queries, fundID string) (Fund, error) {
	row, err := q.GetFund(ctx, fundID)
	if err != nil {
		return Fund{}, err
	}
	days, err := s.loadPaydays(ctx, q, fundID)
	if err != nil {
		return Fund{}, err
	}
	return Fund{ID: row.ID, OwnerID: row.UserID, Name: row.Name, Pace: int32p(row.Pace), Currency: row.Currency,
		Timezone: row.Timezone, TimezoneConfirmed: row.TimezoneConfirmed,
		OnboardingCompleted: row.OnboardingCompleted, Paydays: days}, nil
}

// roleIn is the caller's role in a fund: owner, a member role, or "" when they have no access.
func (s *Service) roleIn(ctx context.Context, q *dbgen.Queries, userID, fundID string) (Role, error) {
	f, err := q.GetFund(ctx, fundID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if f.UserID == userID {
		return RoleOwner, nil
	}
	m, err := q.GetMember(ctx, dbgen.GetMemberParams{FundID: fundID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return Role(m.Role), nil
}

func denied(need Need) *apperr.Error {
	if need == NeedOwner {
		return apperr.New(403, "Only the owner can do that.")
	}
	return apperr.New(403, "You can view this fund but not change it.")
}

// access resolves the caller's role in a named fund and checks it against need.
func (s *Service) access(ctx context.Context, q *dbgen.Queries, userID, fundID string, need Need) (Access, error) {
	role, err := s.roleIn(ctx, q, userID, fundID)
	if err != nil {
		return Access{}, err
	}
	if role == "" {
		return Access{}, apperr.New(404, "That fund is not available to you.")
	}
	if !allows(role, need) {
		return Access{}, denied(need)
	}
	f, err := s.loadFund(ctx, q, fundID)
	if err != nil {
		return Access{}, err
	}
	return Access{Fund: f, Role: role}, nil
}

// ListFunds returns every fund the user owns or was added to, owned first. A user with no
// fund at all gets a fresh one, so the app always has something to show.
func (s *Service) ListFunds(ctx context.Context, user auth.User) ([]FundSummary, error) {
	rows, err := s.q.ListFundsForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		if _, err := s.createFundTx(ctx, user.ID, defaultFundName, user.JoinedViaInvite); err != nil {
			return nil, err
		}
		if rows, err = s.q.ListFundsForUser(ctx, user.ID); err != nil {
			return nil, err
		}
	}
	out := make([]FundSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, FundSummary{ID: r.ID, Name: r.Name, Role: Role(r.Role)})
	}
	return out, nil
}

// createFundTx creates a fund with the defaults and the single payday 31, atomically.
func (s *Service) createFundTx(ctx context.Context, userID, name string, onboardingDone bool) (string, error) {
	var fundID string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		fundID, err = s.createFundFor(ctx, s.q.WithTx(tx), userID, name, onboardingDone)
		return err
	})
	return fundID, err
}

func (s *Service) createFundFor(ctx context.Context, q *dbgen.Queries, userID, name string, onboardingDone bool) (string, error) {
	fundID := id.New()
	if err := q.InsertFund(ctx, dbgen.InsertFundParams{ID: fundID, UserID: userID, Name: name,
		Currency: "KES", Timezone: "UTC", OnboardingCompleted: onboardingDone}); err != nil {
		return "", err
	}
	if err := q.InsertPayday(ctx, dbgen.InsertPaydayParams{FundID: fundID, Day: 31}); err != nil {
		return "", err
	}
	return fundID, nil
}
