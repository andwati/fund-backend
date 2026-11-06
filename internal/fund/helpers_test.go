package fund

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/ratelimit"
	"github.com/andwati/fund-backend/internal/testdb"
)

const future = "2099-01-01"

type mailLog struct {
	mu           sync.Mutex
	invites      []sentInvite
	transfers    []TransferMail
	failInvite   bool
	failTransfer bool
}

type sentInvite struct {
	To   string
	Mail InviteMail
}

func (m *mailLog) Invite(_ context.Context, to string, im InviteMail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invites = append(m.invites, sentInvite{to, im})
	if m.failInvite {
		return context.DeadlineExceeded
	}
	return nil
}

func (m *mailLog) OwnershipTransfer(_ context.Context, t TransferMail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transfers = append(m.transfers, t)
	if m.failTransfer {
		return context.DeadlineExceeded
	}
	return nil
}

func (m *mailLog) lastInvite() sentInvite {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.invites[len(m.invites)-1]
}

// authMail swallows the account emails.
type authMail struct{}

func (authMail) VerifyEmail(context.Context, string, string) error                     { return nil }
func (authMail) VerifyNewEmail(context.Context, string, string) error                  { return nil }
func (authMail) ChangeEmailConfirmation(context.Context, string, string, string) error { return nil }
func (authMail) ResetPassword(context.Context, string, string) error                   { return nil }
func (authMail) PasswordChanged(context.Context, string) error                         { return nil }
func (authMail) AccountDeleted(context.Context, string) error                          { return nil }

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	q    *dbgen.Queries
	svc  *Service
	auth *auth.Service
	mail *mailLog
	A, B auth.User
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := testdb.New(t)
	e := &env{t: t, pool: pool, q: dbgen.New(pool), mail: &mailLog{}}
	e.auth = auth.NewService(auth.Options{Pool: pool, Mailer: authMail{}, Secret: "0123456789abcdef-secret",
		PublicURL: "https://fund.test", BeforeDelete: PrepareAccountDeletion})
	e.svc = NewService(Options{Pool: pool, Auth: e.auth, Mailer: e.mail, Limiter: ratelimit.New(), PublicURL: "https://fund.test"})
	e.A = e.user("user-a")
	e.B = e.user("user-b")
	return e
}

func (e *env) user(name string) auth.User {
	e.t.Helper()
	u, err := e.q.CreateUser(context.Background(), dbgen.CreateUserParams{ID: name, Name: name, Email: name + "@example.com", EmailVerified: true})
	if err != nil {
		e.t.Fatal(err)
	}
	return auth.User{ID: u.ID, Name: u.Name, Email: u.Email, EmailVerified: true}
}

// fundOf returns the id of the user's first (auto-created) fund.
func (e *env) fundOf(u auth.User) string {
	e.t.Helper()
	funds, err := e.svc.ListFunds(context.Background(), u)
	if err != nil || len(funds) == 0 {
		e.t.Fatalf("funds: %v %v", funds, err)
	}
	return funds[0].ID
}

func (e *env) payload(u auth.User, fundID string) Payload {
	e.t.Helper()
	p, err := e.svc.Payload(context.Background(), u, fundID)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) addItem(u auth.User, fundID, name string, price int) ItemView {
	e.t.Helper()
	if err := e.svc.AddItem(context.Background(), u, fundID, ItemInput{Name: name, Price: price, Deadline: future}); err != nil {
		e.t.Fatal(err)
	}
	for _, it := range e.payload(u, fundID).Items {
		if it.Name == name {
			return it
		}
	}
	e.t.Fatal("item missing")
	return ItemView{}
}

func (e *env) contribute(u auth.User, fundID string, in ContributionInput) error {
	return e.svc.AddContribution(context.Background(), u, fundID, in)
}

func (e *env) mustContribute(u auth.User, fundID string, in ContributionInput) {
	e.t.Helper()
	if err := e.contribute(u, fundID, in); err != nil {
		e.t.Fatal(err)
	}
}

func status(err error) int {
	if ae, ok := apperr.As(err); ok {
		return ae.Status
	}
	if err == nil {
		return 0
	}
	return -1
}

func wantErr(t *testing.T, err error, st int, msg string) {
	t.Helper()
	ae, ok := apperr.As(err)
	if !ok {
		t.Fatalf("want apperr %d %q, got %v", st, msg, err)
	}
	if ae.Status != st || (msg != "" && ae.Message != msg) {
		t.Fatalf("want %d %q, got %d %q", st, msg, ae.Status, ae.Message)
	}
}

func line(itemID string, amount int) []Line { return []Line{{ItemID: itemID, Amount: amount}} }

func upsertMember(fundID, userID, role string) dbgen.UpsertMemberParams {
	return dbgen.UpsertMemberParams{FundID: fundID, UserID: userID, Role: role}
}

func hashOf(t *testing.T, pw string) string {
	t.Helper()
	h, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// auth_user builds an auth.User for an existing row with a given email.
func auth_user(u auth.User, email string) auth.User { u.Email = email; return u }
