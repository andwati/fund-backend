package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/fund"
	"github.com/andwati/fund-backend/internal/mail"
	"github.com/andwati/fund-backend/internal/ratelimit"
	"github.com/andwati/fund-backend/internal/reminders"
	"github.com/andwati/fund-backend/internal/testdb"
)

// capture records every email the app would send.
type capture struct {
	mu        sync.Mutex
	links     map[string][]string // kind -> links
	invites   []fund.InviteMail
	reminders []mail.ReminderDetails
}

func (c *capture) add(kind, link string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.links == nil {
		c.links = map[string][]string{}
	}
	c.links[kind] = append(c.links[kind], link)
	return nil
}
func (c *capture) last(kind string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.links[kind]
	if len(l) == 0 {
		return ""
	}
	return l[len(l)-1]
}
func (c *capture) VerifyEmail(_ context.Context, _, u string) error    { return c.add("verify", u) }
func (c *capture) VerifyNewEmail(_ context.Context, _, u string) error { return c.add("verify-new", u) }
func (c *capture) ChangeEmailConfirmation(_ context.Context, _, _, u string) error {
	return c.add("change-confirm", u)
}
func (c *capture) ResetPassword(_ context.Context, _, u string) error { return c.add("reset", u) }
func (c *capture) PasswordChanged(context.Context, string) error      { return c.add("pw-changed", "") }
func (c *capture) AccountDeleted(context.Context, string) error       { return c.add("deleted", "") }
func (c *capture) Invite(_ context.Context, _ string, m fund.InviteMail) error {
	c.mu.Lock()
	c.invites = append(c.invites, m)
	c.mu.Unlock()
	return nil
}
func (c *capture) OwnershipTransfer(context.Context, fund.TransferMail) error { return nil }
func (c *capture) Reminder(_ context.Context, _ string, d mail.ReminderDetails) error {
	c.mu.Lock()
	c.reminders = append(c.reminders, d)
	c.mu.Unlock()
	return nil
}

type app struct {
	t    *testing.T
	srv  *httptest.Server
	mail *capture
	q    *dbgen.Queries
}

type opts struct {
	rateLimit bool
	cron      string
	cors      []string
}

func newApp(t *testing.T, o opts) *app {
	t.Helper()
	pool := testdb.New(t)
	cap := &capture{}
	lim := ratelimit.New()
	authSvc := auth.NewService(auth.Options{Pool: pool, Mailer: cap, Secret: "0123456789abcdef-secret", PublicURL: "https://app.test",
		BeforeDelete: fund.PrepareAccountDeletion})
	fundSvc := fund.NewService(fund.Options{Pool: pool, Auth: authSvc, Mailer: cap, Limiter: lim, PublicURL: "https://app.test"})
	q := dbgen.New(pool)
	runner := &reminders.Runner{Q: q, Mail: cap, Secret: []byte("0123456789abcdef-secret"), APIURL: "https://api.test", PublicURL: "https://app.test"}
	h := New(Deps{Pool: pool, Auth: authSvc, Fund: fundSvc, Reminders: runner, Limiter: lim, CronSecret: o.cron,
		CORSOrigins: o.cors, TrustProxy: true, RateLimitEnabled: o.rateLimit}).Handler()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &app{t: t, srv: srv, mail: cap, q: q}
}

type resp struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r resp) json() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.Body, &m)
	return m
}

func (a *app) do(method, path, token string, body any, hdr ...string) resp {
	a.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, a.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, b}
}

// signUp creates a verified account and returns its bearer token.
func (a *app) signUp(name, email string) string {
	a.t.Helper()
	r := a.do("POST", "/auth/sign-up", "", map[string]string{"name": name, "email": email, "password": "hunter2hunter2"})
	if r.Status != 200 {
		a.t.Fatalf("sign-up %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(a.mail.last("verify"))
	v := a.do("POST", "/auth/verify-email", "", map[string]string{"token": u.Query().Get("token")})
	if v.Status != 200 {
		a.t.Fatalf("verify %d %s", v.Status, v.Body)
	}
	return v.json()["token"].(string)
}

func (a *app) firstFund(token string) string {
	a.t.Helper()
	r := a.do("GET", "/funds", token, nil)
	if r.Status != 200 {
		a.t.Fatalf("funds %d %s", r.Status, r.Body)
	}
	return r.json()["funds"].([]any)[0].(map[string]any)["id"].(string)
}

func payloadOf(t *testing.T, r resp) map[string]any {
	t.Helper()
	m := r.json()
	if r.Status != 200 || m["ok"] != true {
		t.Fatalf("not ok: %d %s", r.Status, r.Body)
	}
	return m["payload"].(map[string]any)
}

func wantStatus(t *testing.T, r resp, status int, msg string) {
	t.Helper()
	m := r.json()
	if r.Status != status || m["ok"] != false || int(m["status"].(float64)) != status || (msg != "" && m["error"] != msg) {
		t.Fatalf("want %d %q, got %d %s", status, msg, r.Status, r.Body)
	}
}

func zipNames(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(body)
	}
	return out
}

var _ = time.Second

func reminders_sign(fundID, userID string) string {
	return reminders.SignUnsubscribe([]byte("0123456789abcdef-secret"), fundID, userID)
}

func (a *app) reminderEnabled(fundID, userID string) bool {
	p, err := a.q.GetReminderPref(context.Background(), dbgen.GetReminderPrefParams{FundID: fundID, UserID: userID})
	if err != nil {
		a.t.Fatal(err)
	}
	return p.Enabled
}
