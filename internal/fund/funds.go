package fund

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/id"
)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// CreateFund makes a new fund owned by the caller (max 10) and returns its id.
func (s *Service) CreateFund(ctx context.Context, user auth.User, name string) (string, error) {
	clean, err := cleanName(name)
	if err != nil {
		return "", err
	}
	owned, err := s.q.CountOwnedFunds(ctx, user.ID)
	if err != nil {
		return "", err
	}
	if int(owned) >= MaxOwnedFunds {
		return "", apperr.Newf(400, "You can own up to %d funds. Delete one first.", MaxOwnedFunds)
	}
	return s.createFundTx(ctx, user.ID, clean, user.JoinedViaInvite)
}

// purgeFund deletes a fund's rows in an order that respects allocation -> item RESTRICT.
func purgeFund(ctx context.Context, q *dbgen.Queries, fundID string) error {
	for _, step := range []func(context.Context, string) error{
		q.PurgeFundAllocations, q.PurgeFundContributions, q.PurgeFundItems,
		q.PurgeFundInvites, q.PurgeFundMembers, q.DeleteFund,
	} {
		if err := step(ctx, fundID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteFund removes a fund and everything in it (owner only).
func (s *Service) DeleteFund(ctx context.Context, user auth.User, fundID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := s.access(ctx, q, user.ID, fundID, NeedOwner); err != nil {
			return err
		}
		return purgeFund(ctx, q, fundID)
	})
}

// LeaveFund removes the caller from a fund they were added to. Owners delete instead.
func (s *Service) LeaveFund(ctx context.Context, user auth.User, fundID string) error {
	role, err := s.roleIn(ctx, s.q, user.ID, fundID)
	if err != nil {
		return err
	}
	if role == "" {
		return apperr.New(404, "That fund is not available to you.")
	}
	if role == RoleOwner {
		return apperr.New(400, "You own this fund. Delete it instead of leaving.")
	}
	return s.q.DeleteMember(ctx, dbgen.DeleteMemberParams{FundID: fundID, UserID: user.ID})
}

func validRole(r string) error {
	if r != "editor" && r != "viewer" {
		return apperr.New(400, "Pick a role.")
	}
	return nil
}

func (s *Service) newInviteToken() string { return id.Token(32) }

func (s *Service) deliverInvite(ctx context.Context, f Fund, inviter, email, role, token string) error {
	err := s.mail.Invite(ctx, email, InviteMail{FundName: f.Name, Inviter: inviter, Role: role, URL: s.publicURL + "/invite/" + token})
	if err != nil {
		s.log.Error("invite email failed", "fundId", f.ID, "err", err)
		return apperr.New(502, "The invite was saved but the email could not be sent. Try again.")
	}
	return nil
}

// InviteMember invites an address to the fund (owner only). Inviting an address that already
// has a live invite rotates its token, like a resend, without using a daily slot.
func (s *Service) InviteMember(ctx context.Context, user auth.User, fundID, email, role string) error {
	acc, err := s.access(ctx, s.q, user.ID, fundID, NeedOwner)
	if err != nil {
		return err
	}
	email = auth.NormalizeEmail(email)
	if !auth.ValidEmail(email) {
		return apperr.New(400, "Enter a valid email.")
	}
	if err := validRole(role); err != nil {
		return err
	}
	if email == strings.ToLower(user.Email) {
		return apperr.New(400, "You already own this fund.")
	}
	if _, err := s.q.GetMemberByEmail(ctx, dbgen.GetMemberByEmailParams{FundID: fundID, Email: email}); err == nil {
		return apperr.New(400, "They already have access.")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	n, err := s.q.CountMembersAndInvites(ctx, fundID)
	if err != nil {
		return err
	}
	if int(n) >= MaxMembers {
		return apperr.Newf(400, "A fund can have up to %d people.", MaxMembers)
	}

	token := s.newInviteToken()
	expires := ts(s.now().Add(InviteDays * 24 * time.Hour))
	live, err := s.q.GetInviteByEmail(ctx, dbgen.GetInviteByEmailParams{FundID: fundID, Email: email})
	switch {
	case err == nil:
		if err := s.q.RotateInvite(ctx, dbgen.RotateInviteParams{ID: live.ID, Token: token, ExpiresAt: expires, Role: role, InvitedBy: user.ID}); err != nil {
			return err
		}
	case errors.Is(err, pgx.ErrNoRows):
		// The cooldown is only set by a revoke, so re-inviting after accept+leave still works.
		if ok, retry := s.limiter.Check(fmt.Sprintf("invite-recipient:%s:%s", user.ID, email), InviteCooldown); !ok {
			return apperr.Newf(429, "Wait about %d minutes before inviting this address again.", (retry+59)/60)
		}
		if ok, _ := s.limiter.Take("invite-daily:"+user.ID, MaxInvitesDay, 24*time.Hour); !ok {
			return apperr.Newf(429, "You can send up to %d new invites per day. Try again tomorrow.", MaxInvitesDay)
		}
		if err := s.q.InsertInvite(ctx, dbgen.InsertInviteParams{ID: id.New(), FundID: fundID, Email: email, Role: role,
			Token: token, InvitedBy: user.ID, ExpiresAt: expires}); err != nil {
			return err
		}
	default:
		return err
	}
	return s.deliverInvite(ctx, acc.Fund, user.Name, email, role, token)
}

// ResendInvite rotates the token and emails the link again (no daily slot used).
func (s *Service) ResendInvite(ctx context.Context, user auth.User, fundID, inviteID string) error {
	acc, err := s.access(ctx, s.q, user.ID, fundID, NeedOwner)
	if err != nil {
		return err
	}
	live, err := s.q.GetInviteByID(ctx, dbgen.GetInviteByIDParams{ID: inviteID, FundID: fundID})
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.New(404, "That invite is gone.")
	}
	if err != nil {
		return err
	}
	if live.ExpiresAt.Time.Before(s.now()) {
		return apperr.New(410, "That invite has expired. Send a new one.")
	}
	token := s.newInviteToken()
	if err := s.q.RotateInvite(ctx, dbgen.RotateInviteParams{ID: live.ID, Token: token,
		ExpiresAt: ts(s.now().Add(InviteDays * 24 * time.Hour)), Role: live.Role, InvitedBy: live.InvitedBy}); err != nil {
		return err
	}
	return s.deliverInvite(ctx, acc.Fund, user.Name, live.Email, live.Role, token)
}

// RevokeInvite deletes an invite and starts the recipient cooldown.
func (s *Service) RevokeInvite(ctx context.Context, user auth.User, fundID, inviteID string) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedOwner); err != nil {
		return err
	}
	live, err := s.q.GetInviteByID(ctx, dbgen.GetInviteByIDParams{ID: inviteID, FundID: fundID})
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err := s.q.DeleteInvite(ctx, inviteID); err != nil {
		return err
	}
	if found {
		s.limiter.Mark(fmt.Sprintf("invite-recipient:%s:%s", user.ID, live.Email))
	}
	return nil
}

// RemoveMember removes someone from the fund (owner only).
func (s *Service) RemoveMember(ctx context.Context, user auth.User, fundID, memberID string) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedOwner); err != nil {
		return err
	}
	return s.q.DeleteMember(ctx, dbgen.DeleteMemberParams{FundID: fundID, UserID: memberID})
}

// SetMemberRole changes a member's role between editor and viewer (owner only).
func (s *Service) SetMemberRole(ctx context.Context, user auth.User, fundID, memberID, role string) error {
	if _, err := s.access(ctx, s.q, user.ID, fundID, NeedOwner); err != nil {
		return err
	}
	if err := validRole(role); err != nil {
		return err
	}
	n, err := s.q.SetMemberRole(ctx, dbgen.SetMemberRoleParams{FundID: fundID, UserID: memberID, Role: role})
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.New(404, "That person is not in this fund.")
	}
	return nil
}

// TransferOwnership makes an editor the owner; the current owner becomes an editor.
func (s *Service) TransferOwnership(ctx context.Context, user auth.User, fundID, newOwnerID string) error {
	acc, err := s.access(ctx, s.q, user.ID, fundID, NeedOwner)
	if err != nil {
		return err
	}
	if newOwnerID == user.ID {
		return apperr.New(400, "Pick an editor to take over.")
	}
	member, err := s.q.GetMember(ctx, dbgen.GetMemberParams{FundID: fundID, UserID: newOwnerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.New(404, "That person is not in this fund.")
	}
	if err != nil {
		return err
	}
	if member.Role != "editor" {
		return apperr.New(400, "Only an editor can become the owner.")
	}
	owned, err := s.q.CountOwnedFunds(ctx, newOwnerID)
	if err != nil {
		return err
	}
	if int(owned) >= MaxOwnedFunds {
		return apperr.Newf(400, "They already own the maximum of %d funds.", MaxOwnedFunds)
	}
	newOwner, err := s.q.GetUserByID(ctx, newOwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.New(404, "That person is not in this fund.")
	}
	if err != nil {
		return err
	}
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.SetFundOwner(ctx, dbgen.SetFundOwnerParams{ID: fundID, UserID: newOwnerID}); err != nil {
			return err
		}
		if err := q.DeleteMember(ctx, dbgen.DeleteMemberParams{FundID: fundID, UserID: newOwnerID}); err != nil {
			return err
		}
		return q.UpsertMember(ctx, dbgen.UpsertMemberParams{FundID: fundID, UserID: user.ID, Role: "editor"})
	}); err != nil {
		return err
	}
	s.log.Info("ownership transferred", "fundId", fundID, "from", user.ID, "to", newOwnerID)
	if err := s.mail.OwnershipTransfer(ctx, TransferMail{FundID: fundID, FundName: acc.Fund.Name,
		PreviousOwner: Person{user.Name, user.Email}, NewOwner: Person{newOwner.Name, newOwner.Email}}); err != nil {
		return apperr.New(502, "Ownership moved but the emails could not be sent. Tell each other directly.")
	}
	return nil
}

// InviteInfo is what the invite page shows. Holding the token is what grants the view.
type InviteInfo struct {
	State    string `json:"state"` // invalid | expired | ok
	FundName string `json:"fundName,omitempty"`
	Role     string `json:"role,omitempty"`
	Email    string `json:"email,omitempty"`
	Inviter  string `json:"inviter,omitempty"`
	Account  string `json:"account,omitempty"` // none | unverified | verified
}

func (s *Service) ReadInvite(ctx context.Context, token string) (InviteInfo, error) {
	if token == "" {
		return InviteInfo{State: "invalid"}, nil
	}
	row, err := s.q.GetInviteView(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return InviteInfo{State: "invalid"}, nil
	}
	if err != nil {
		return InviteInfo{}, err
	}
	if row.ExpiresAt.Time.Before(s.now()) {
		return InviteInfo{State: "expired"}, nil
	}
	account := "none"
	if u, err := s.q.GetUserByEmail(ctx, row.Email); err == nil {
		account = "unverified"
		if u.EmailVerified {
			account = "verified"
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return InviteInfo{}, err
	}
	return InviteInfo{State: "ok", FundName: row.FundName, Role: row.Role, Email: row.Email, Inviter: row.Inviter, Account: account}, nil
}

func (s *Service) guardAccept(ip, token string) error {
	if ok, _ := s.limiter.Take("invite-accept-ip:"+ip, acceptIPLimit, acceptWindow); !ok {
		return apperr.New(429, "Too many attempts. Wait a few minutes and try again.")
	}
	if ok, _ := s.limiter.Take("invite-accept-token:"+token, acceptTokenLim, acceptWindow); !ok {
		return apperr.New(429, "Too many attempts on this invite. Wait a few minutes and try again.")
	}
	return nil
}

func (s *Service) loadLiveInvite(ctx context.Context, q *dbgen.Queries, token string) (dbgen.FundInvite, dbgen.GetFundRow, error) {
	inv, err := q.GetInviteByToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return inv, dbgen.GetFundRow{}, apperr.New(404, "That invite is no longer valid.")
	}
	if err != nil {
		return inv, dbgen.GetFundRow{}, err
	}
	if inv.ExpiresAt.Time.Before(s.now()) {
		return inv, dbgen.GetFundRow{}, apperr.New(410, "That invite has expired. Ask for a new one.")
	}
	f, err := q.GetFund(ctx, inv.FundID)
	if errors.Is(err, pgx.ErrNoRows) {
		return inv, f, apperr.New(404, "That fund no longer exists.")
	}
	return inv, f, err
}

// AcceptInvite joins the signed-in account to the fund. The invite email must match the account.
func (s *Service) AcceptInvite(ctx context.Context, user auth.User, token, ip string) (string, error) {
	if err := s.guardAccept(ip, token); err != nil {
		return "", err
	}
	inv, target, err := s.loadLiveInvite(ctx, s.q, token)
	if err != nil {
		return "", err
	}
	if inv.Email != strings.ToLower(user.Email) {
		return "", apperr.New(403, "This invite was sent to a different email address.")
	}
	if target.UserID == user.ID {
		return "", apperr.New(400, "You already own this fund.")
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.UpsertMember(ctx, dbgen.UpsertMemberParams{FundID: inv.FundID, UserID: user.ID, Role: inv.Role}); err != nil {
			return err
		}
		return q.DeleteInvite(ctx, inv.ID)
	})
	return inv.FundID, err
}

// AcceptInviteSignup creates (or claims an unverified) account for the invited address, joins
// the fund and signs the person in, all in one transaction, with no personal fund. The invite
// token proves control of the inbox.
func (s *Service) AcceptInviteSignup(ctx context.Context, token, name, password, ip, ua string) (auth.Session, string, error) {
	if err := s.guardAccept(ip, token); err != nil {
		return auth.Session{}, "", err
	}
	clean, err := cleanName(name)
	if err != nil {
		return auth.Session{}, "", err
	}
	if n := jsLen(password); n < 8 {
		return auth.Session{}, "", apperr.New(400, "Use at least 8 characters.")
	} else if n > 128 {
		return auth.Session{}, "", apperr.New(400, "Use 128 characters or fewer.")
	}
	inv, _, err := s.loadLiveInvite(ctx, s.q, token)
	if err != nil {
		return auth.Session{}, "", err
	}
	existing, err := s.q.GetUserByEmail(ctx, inv.Email)
	hasUser := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return auth.Session{}, "", err
	}
	if hasUser && existing.EmailVerified {
		return auth.Session{}, "", apperr.New(409, "An account already exists for this email. Sign in to join.")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return auth.Session{}, "", err
	}

	var sess auth.Session
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		userID := existing.ID
		if hasUser {
			if err := q.ClaimUnverifiedUser(ctx, dbgen.ClaimUnverifiedUserParams{ID: userID, Name: clean}); err != nil {
				return err
			}
			if _, err := q.GetCredentialAccount(ctx, userID); err == nil {
				if err := q.UpdateCredentialPassword(ctx, dbgen.UpdateCredentialPasswordParams{UserID: userID, Password: &hash}); err != nil {
					return err
				}
			} else if errors.Is(err, pgx.ErrNoRows) {
				if err := q.CreateCredentialAccount(ctx, dbgen.CreateCredentialAccountParams{ID: id.New(), AccountID: userID, Password: &hash}); err != nil {
					return err
				}
			} else {
				return err
			}
		} else {
			userID = id.New()
			if _, err := q.CreateUser(ctx, dbgen.CreateUserParams{ID: userID, Name: clean, Email: inv.Email, EmailVerified: true, JoinedViaInvite: true}); err != nil {
				return err
			}
			if err := q.CreateCredentialAccount(ctx, dbgen.CreateCredentialAccountParams{ID: id.New(), AccountID: userID, Password: &hash}); err != nil {
				return err
			}
		}
		if err := q.UpsertMember(ctx, dbgen.UpsertMemberParams{FundID: inv.FundID, UserID: userID, Role: inv.Role}); err != nil {
			return err
		}
		if err := q.DeleteInvite(ctx, inv.ID); err != nil {
			return err
		}
		var serr error
		sess, serr = s.auth.StartSessionTx(ctx, tx, userID, ip, ua)
		return serr
	})
	if err != nil {
		if isUnique(err) {
			return auth.Session{}, "", apperr.New(409, "An account already exists for this email. Sign in to join.")
		}
		return auth.Session{}, "", err
	}
	return sess, inv.FundID, nil
}

// PrepareAccountDeletion is the auth.BeforeDelete hook: refuse while an owned fund still has
// members, otherwise purge the owned funds. Memberships elsewhere cascade with the user row.
func PrepareAccountDeletion(ctx context.Context, tx pgx.Tx, userID string) error {
	q := dbgen.New(tx)
	owned, err := q.ListOwnedFundsWithMemberCount(ctx, userID)
	if err != nil {
		return err
	}
	var names []string
	for _, f := range owned {
		if f.MemberCount > 0 {
			names = append(names, f.Name)
		}
	}
	if len(names) == 1 {
		return apperr.New(409, "Transfer or delete “"+names[0]+"” before deleting your account.")
	}
	if len(names) > 1 {
		return apperr.New(409, "Transfer or delete these owned funds before deleting your account: "+strings.Join(names, ", ")+".")
	}
	for _, f := range owned {
		if err := purgeFund(ctx, q, f.ID); err != nil {
			return err
		}
	}
	return nil
}
