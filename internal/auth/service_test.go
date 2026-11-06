package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/testdb"
)

type sentMail struct{ kind, to, link, newEmail string }

type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
	fail bool
}

func (f *fakeMailer) rec(kind, to, link, ne string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMail{kind, to, link, ne})
	if f.fail {
		return errors.New("smtp down")
	}
	return nil
}
func (f *fakeMailer) VerifyEmail(_ context.Context, to, u string) error {
	return f.rec("verify", to, u, "")
}
func (f *fakeMailer) VerifyNewEmail(_ context.Context, to, u string) error {
	return f.rec("verify-new", to, u, "")
}
func (f *fakeMailer) ChangeEmailConfirmation(_ context.Context, old, ne, u string) error {
	return f.rec("change-confirm", old, u, ne)
}
func (f *fakeMailer) ResetPassword(_ context.Context, to, u string) error {
	return f.rec("reset", to, u, "")
}
func (f *fakeMailer) PasswordChanged(_ context.Context, to string) error {
	return f.rec("pw-changed", to, "", "")
}
func (f *fakeMailer) AccountDeleted(_ context.Context, to string) error {
	return f.rec("deleted", to, "", "")
}
func (f *fakeMailer) last(kind string) sentMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].kind == kind {
			return f.sent[i]
		}
	}
	return sentMail{}
}
func (f *fakeMailer) count(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sent {
		if s.kind == kind {
			n++
		}
	}
	return n
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Query().Get("token") == "" {
		t.Fatalf("no token in %q", link)
	}
	return u.Query().Get("token")
}

type env struct {
	svc  *Service
	pool *pgxpool.Pool
	q    *dbgen.Queries
	mail *fakeMailer
	now  time.Time
}

func setup(t *testing.T, bd BeforeDelete) *env {
	t.Helper()
	pool := testdb.New(t)
	e := &env{pool: pool, q: dbgen.New(pool), mail: &fakeMailer{}, now: time.Now().UTC().Truncate(time.Second)}
	e.svc = NewService(Options{Pool: pool, Mailer: e.mail, Secret: "0123456789abcdef-secret", PublicURL: "https://fund.test/",
		BeforeDelete: bd, Now: func() time.Time { return e.now }})
	return e
}

func code(err error) int {
	if ae, ok := apperr.As(err); ok {
		return ae.Status
	}
	return 0
}

func TestSignUpAndSignIn(t *testing.T) {
	ctx := context.Background()
	e := setup(t, nil)

	s, err := e.svc.SignUp(ctx, "  Ann  ", " Ann@Example.COM ", "hunter2hunter2", "1.2.3.4", "UA")
	if err != nil {
		t.Fatal(err)
	}
	if s.User.Email != "ann@example.com" || s.User.Name != "Ann" || s.User.EmailVerified || s.Token == "" {
		t.Fatalf("unexpected session %+v", s)
	}
	if e.mail.count("verify") != 1 || e.mail.last("verify").to != "ann@example.com" {
		t.Fatalf("verification email not sent: %+v", e.mail.sent)
	}
	if got, err := e.svc.GetSession(ctx, s.Token); err != nil || got == nil || got.User.ID != s.User.ID {
		t.Fatalf("session lookup: %+v %v", got, err)
	}

	// Duplicate emails, regardless of case.
	if _, err := e.svc.SignUp(ctx, "Ann2", "ANN@example.com", "hunter2hunter2", "", ""); code(err) != 422 {
		t.Fatalf("duplicate signup: %v", err)
	}
	for name, args := range map[string][3]string{
		"empty name":     {"", "a@b.co", "hunter2hunter2"},
		"bad email":      {"X", "not-an-email", "hunter2hunter2"},
		"double dot":     {"X", "a..b@c.co", "hunter2hunter2"},
		"short password": {"X", "x@y.co", "short"},
		"long password":  {"X", "x@y.co", strings.Repeat("a", 129)},
	} {
		if _, err := e.svc.SignUp(ctx, args[0], args[1], args[2], "", ""); code(err) != 400 {
			t.Errorf("%s: want 400, got %v", name, err)
		}
	}

	if _, err := e.svc.SignIn(ctx, "ann@example.com", "wrong-password", "", ""); code(err) != 401 {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := e.svc.SignIn(ctx, "nobody@example.com", "hunter2hunter2", "", ""); code(err) != 401 {
		t.Fatalf("unknown user: %v", err)
	}
	in, err := e.svc.SignIn(ctx, "ANN@example.com", "hunter2hunter2", "", "")
	if err != nil || in.Token == s.Token {
		t.Fatalf("sign in: %+v %v", in, err)
	}
}

func TestMailFailureDoesNotFailSignUp(t *testing.T) {
	e := setup(t, nil)
	e.mail.fail = true
	if _, err := e.svc.SignUp(context.Background(), "Ann", "ann@example.com", "hunter2hunter2", "", ""); err != nil {
		t.Fatalf("signup should survive mail failure: %v", err)
	}
}

func TestLegacyBetterAuthAccountCanSignIn(t *testing.T) {
	ctx := context.Background()
	e := setup(t, nil)
	var vs []pwVector
	b, _ := os.ReadFile("testdata/passwords.json")
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}
	v := vs[0]
	// Rows exactly as better-auth writes them: lower-cased email, credential account_id = user id.
	if _, err := e.pool.Exec(ctx, `insert into "user"(id,name,email,email_verified) values('legacy1','Old','old@example.com',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `insert into account(id,account_id,provider_id,user_id,password) values('a1','legacy1','credential','legacy1',$1)`, v.Hash); err != nil {
		t.Fatal(err)
	}
	s, err := e.svc.SignIn(ctx, "old@example.com", v.Password, "", "")
	if err != nil || s.User.ID != "legacy1" || !s.User.EmailVerified {
		t.Fatalf("legacy sign-in: %+v %v", s, err)
	}
}

func TestSessionExpiryAndSlidingRefresh(t *testing.T) {
	ctx := context.Background()
	e := setup(t, nil)
	s, _ := e.svc.SignUp(ctx, "Ann", "ann@example.com", "hunter2hunter2", "", "")

	// Younger than a day: expiry is untouched.
	e.now = e.now.Add(12 * time.Hour)
	got, _ := e.svc.GetSession(ctx, s.Token)
	if got == nil || !got.ExpiresAt.Equal(s.ExpiresAt) {
		t.Fatalf("expiry changed too early: %+v vs %v", got, s.ExpiresAt)
	}
	// Older than a day: slides forward to now + 7d.
	e.now = e.now.Add(13 * time.Hour)
	got, _ = e.svc.GetSession(ctx, s.Token)
	if got == nil || !got.ExpiresAt.Equal(e.now.Add(sessionTTL)) {
		t.Fatalf("expiry not refreshed: %+v want %v", got, e.now.Add(sessionTTL))
	}
	// Expiry is checked by the database clock, so force a past expiry in the row.
	if _, err := e.pool.Exec(ctx, `update session set expires_at = now() - interval '1 minute' where token=$1`, s.Token); err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.GetSession(ctx, s.Token); err != nil || got != nil {
		t.Fatalf("expired session accepted: %+v %v", got, err)
	}
	if got, _ := e.svc.GetSession(ctx, ""); got != nil {
		t.Fatal("empty token accepted")
	}
}

func TestSignOutAndSessionManagement(t *testing.T) {
	ctx := context.Background()
	e := setup(t, nil)
	a, _ := e.svc.SignUp(ctx, "Ann", "ann@example.com", "hunter2hunter2", "", "")
	b, _ := e.svc.SignIn(ctx, "ann@example.com", "hunter2hunter2", "", "")
	c, _ := e.svc.SignIn(ctx, "ann@example.com", "hunter2hunter2", "", "")

	list, _ := e.svc.ListSessions(ctx, a.User.ID, b.Token)
	cur := 0
	for _, s := range list {
		if s.Current {
			cur++
		}
	}
	if len(list) != 3 || cur != 1 {
		t.Fatalf("sessions: %+v", list)
	}
	if err := e.svc.RevokeOtherSessions(ctx, a.User.ID, b.Token); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.svc.GetSession(ctx, a.Token); s != nil {
		t.Fatal("other session survived")
	}
	if s, _ := e.svc.GetSession(ctx, b.Token); s == nil {
		t.Fatal("current session revoked")
	}
	// Another user's id cannot revoke my session.
	_ = e.svc.RevokeSession(ctx, "someone-else", c.ID)
	_ = e.svc.SignOut(ctx, b.Token)
	if s, _ := e.svc.GetSession(ctx, b.Token); s != nil {
		t.Fatal("signed-out session still valid")
	}
}

func TestVerifyEmailLink(t *testing.T) {
	ctx := context.Background()
	e := setup(t, nil)
	s, _ := e.svc.SignUp(ctx, "Ann", "ann@example.com", "hunter2hunter2", "", "")
	tok := tokenOf(t, e.mail.last("verify").link)

	res, err := e.svc.VerifyEmail(ctx, tok, "", "")
	if err != nil || res.Status != "verified" || res.Session == nil || !res.User.EmailVerified {
		t.Fatalf("verify: %+v %v", res, err)
	}
	if got, _ := e.svc.GetSession(ctx, res.Session.Token); got == nil || got.User.ID != s.User.ID || !got.User.EmailVerified {
		t.Fatalf("auto sign-in session: %+v", got)
	}
	// Tampered, garbage and expired tokens are rejected.
	for name, bad := range map[string]string{"tampered": tok[:len(tok)-2] + "xx", "garbage": "nope", "empty": ""} {
		if _, err := e.svc.VerifyEmail(ctx, bad, "", ""); code(err) != 400 {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	e.now = e.now.Add(2 * time.Hour)
	if _, err := e.svc.VerifyEmail(ctx, tok, "", ""); code(err) != 400 {
		t.Fatalf("expired token accepted: %v", err)
	}

	// Re-send: unknown and verified addresses answer the same and send nothing.
	before := e.mail.count("verify")
	for _, addr := range []string{"ghost@example.com", "ann@example.com"} {
		if err := e.svc.SendVerificationEmail(ctx, addr); err != nil {
			t.Fatal(err)
		}
	}
	if e.mail.count("verify") != before {
		t.Fatal("verification email sent to unknown/verified address")
	}
}

func TestChangeEmailTwoStep(t *testing.T) {
	ctx := context.Background()
	e := setup(t, nil)
	s, _ := e.svc.SignUp(ctx, "Ann", "ann@example.com", "hunter2hunter2", "", "")
	if _, err := e.svc.VerifyEmail(ctx, tokenOf(t, e.mail.last("verify").link), "", ""); err != nil {
		t.Fatal(err)
	}
	user := s.User
	user.EmailVerified = true
	_, _ = e.svc.SignUp(ctx, "Bob", "bob@example.com", "hunter2hunter2", "", "")

	if err := e.svc.ChangeEmail(ctx, user, "bob@example.com"); code(err) != 422 {
		t.Fatalf("taken address: %v", err)
	}
	if err := e.svc.ChangeEmail(ctx, user, "bad"); code(err) != 400 {
		t.Fatalf("bad address: %v", err)
	}
	if err := e.svc.ChangeEmail(ctx, user, "ann@example.com"); code(err) != 400 {
		t.Fatalf("same address: %v", err)
	}
	if err := e.svc.ChangeEmail(ctx, user, "Ann.New@Example.com"); err != nil {
		t.Fatal(err)
	}
	conf := e.mail.last("change-confirm")
	if conf.to != "ann@example.com" || conf.newEmail != "ann.new@example.com" {
		t.Fatalf("confirmation goes to the OLD address: %+v", conf)
	}
	// Step 1: approve from the old address -> verification goes to the new address.
	res, err := e.svc.VerifyEmail(ctx, tokenOf(t, conf.link), "", "")
	if err != nil || res.Status != "confirmation-sent" {
		t.Fatalf("approve: %+v %v", res, err)
	}
	vn := e.mail.last("verify-new")
	if vn.to != "ann.new@example.com" {
		t.Fatalf("verify-new to %q", vn.to)
	}
	if u, _ := e.q.GetUserByID(ctx, user.ID); u.Email != "ann@example.com" {
		t.Fatal("email changed before the new address was verified")
	}
	// Step 2: verify the new address.
	res, err = e.svc.VerifyEmail(ctx, tokenOf(t, vn.link), "", "")
	if err != nil || res.Status != "email-changed" || res.User.Email != "ann.new@example.com" {
		t.Fatalf("finalise: %+v %v", res, err)
	}
	if u, _ := e.q.GetUserByID(ctx, user.ID); u.Email != "ann.new@example.com" || !u.EmailVerified {
		t.Fatalf("stored user: %+v", u)
	}
}

func TestPasswordResetFlow(t *testing.T) {
	ctx := context.Background()
	e := setup(t, nil)
	s, _ := e.svc.SignUp(ctx, "Ann", "ann@example.com", "hunter2hunter2", "", "")

	// Unknown address: no error, no email.
	if err := e.svc.RequestPasswordReset(ctx, "ghost@example.com"); err != nil || e.mail.count("reset") != 0 {
		t.Fatalf("unknown address: %v %d", err, e.mail.count("reset"))
	}
	if err := e.svc.RequestPasswordReset(ctx, "ANN@example.com"); err != nil {
		t.Fatal(err)
	}
	tok := tokenOf(t, e.mail.last("reset").link)

	if err := e.svc.ResetPassword(ctx, tok, "short"); code(err) != 400 {
		t.Fatalf("short password: %v", err)
	}
	if err := e.svc.ResetPassword(ctx, "wrong-token", "brand-new-pass"); code(err) != 400 {
		t.Fatalf("bad token: %v", err)
	}
	if err := e.svc.ResetPassword(ctx, tok, "brand-new-pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SignIn(ctx, "ann@example.com", "hunter2hunter2", "", ""); code(err) != 401 {
		t.Fatal("old password still works")
	}
	if _, err := e.svc.SignIn(ctx, "ann@example.com", "brand-new-pass", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ResetPassword(ctx, tok, "another-pass-1"); code(err) != 400 {
		t.Fatalf("reset token reusable: %v", err)
	}
	// Existing sessions are not revoked by a reset (same as the web app).
	if got, _ := e.svc.GetSession(ctx, s.Token); got == nil {
		t.Fatal("reset revoked sessions")
	}

	// Expired token.
	_ = e.svc.RequestPasswordReset(ctx, "ann@example.com")
	tok2 := tokenOf(t, e.mail.last("reset").link)
	if _, err := e.pool.Exec(ctx, `update verification set expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ResetPassword(ctx, tok2, "brand-new-pass-2"); code(err) != 400 {
		t.Fatalf("expired reset accepted: %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	ctx := context.Background()
	e := setup(t, nil)
	a, _ := e.svc.SignUp(ctx, "Ann", "ann@example.com", "hunter2hunter2", "", "")
	b, _ := e.svc.SignIn(ctx, "ann@example.com", "hunter2hunter2", "", "")

	if err := e.svc.ChangePassword(ctx, a.User, a.Token, "wrong", "new-password-1", false); code(err) != 400 {
		t.Fatalf("wrong current: %v", err)
	}
	if err := e.svc.ChangePassword(ctx, a.User, a.Token, "hunter2hunter2", "short", false); code(err) != 400 {
		t.Fatalf("weak new: %v", err)
	}
	if err := e.svc.ChangePassword(ctx, a.User, a.Token, "hunter2hunter2", "new-password-1", true); err != nil {
		t.Fatal(err)
	}
	if e.mail.count("pw-changed") != 1 {
		t.Fatal("password-changed email not sent")
	}
	if s, _ := e.svc.GetSession(ctx, b.Token); s != nil {
		t.Fatal("other session survived revokeOthers")
	}
	if s, _ := e.svc.GetSession(ctx, a.Token); s == nil {
		t.Fatal("current session revoked")
	}
	if _, err := e.svc.SignIn(ctx, "ann@example.com", "new-password-1", "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAccount(t *testing.T) {
	ctx := context.Background()
	blocked := true
	e := setup(t, func(ctx context.Context, tx pgx.Tx, userID string) error {
		if blocked {
			return apperr.New(409, "Transfer or delete “Home” before deleting your account.")
		}
		return nil
	})
	s, _ := e.svc.SignUp(ctx, "Ann", "ann@example.com", "hunter2hunter2", "", "")

	if err := e.svc.DeleteAccount(ctx, s.User, "wrong-password"); code(err) != 400 {
		t.Fatalf("wrong password: %v", err)
	}
	if err := e.svc.DeleteAccount(ctx, s.User, "hunter2hunter2"); code(err) != 409 {
		t.Fatalf("blocker should win: %v", err)
	}
	if got, _ := e.svc.GetSession(ctx, s.Token); got == nil {
		t.Fatal("blocked deletion removed the user")
	}
	if e.mail.count("deleted") != 0 {
		t.Fatal("deleted email sent although deletion was blocked")
	}
	blocked = false
	if err := e.svc.DeleteAccount(ctx, s.User, "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.GetSession(ctx, s.Token); got != nil {
		t.Fatal("session survived account deletion")
	}
	var n int
	_ = e.pool.QueryRow(ctx, `select (select count(*) from account) + (select count(*) from session)`).Scan(&n)
	if n != 0 || e.mail.count("deleted") != 1 {
		t.Fatalf("rows left %d, deleted emails %d", n, e.mail.count("deleted"))
	}
}

func TestUpdateProfile(t *testing.T) {
	ctx := context.Background()
	e := setup(t, nil)
	s, _ := e.svc.SignUp(ctx, "Ann", "ann@example.com", "hunter2hunter2", "", "")
	name := "  Ann Lee "
	u, err := e.svc.UpdateProfile(ctx, s.User.ID, &name, nil)
	if err != nil || u.Name != "Ann Lee" {
		t.Fatalf("update: %+v %v", u, err)
	}
	empty := " "
	if _, err := e.svc.UpdateProfile(ctx, s.User.ID, &empty, nil); code(err) != 400 {
		t.Fatalf("empty name: %v", err)
	}
}
