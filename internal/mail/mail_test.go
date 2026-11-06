package mail

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/andwati/fund-backend/internal/fund"
)

type golden struct {
	Texts    map[string]string `json:"texts"`
	Subjects struct {
		InviteNew string   `json:"inviteNew"`
		InviteHas string   `json:"inviteHas"`
		Role      []string `json:"role"`
	} `json:"subjects"`
}

func loadGolden(t *testing.T) golden {
	t.Helper()
	f, err := os.Open("testdata/mail.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, _ := gzip.NewReader(f)
	var g golden
	if err := json.NewDecoder(zr).Decode(&g); err != nil {
		t.Fatal(err)
	}
	return g
}

// norm lowercases and collapses whitespace and the colons Go adds after button labels.
func norm(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, ": ", " "))
	return strings.Join(strings.Fields(s), " ")
}

// Known, deliberate copy differences from the web app.
var deviations = map[string][]string{
	// The app is no longer "a browser".
	"changed": {"The password for ann@example.com was just updated. Other devices were signed out, except the browser where you made this change."},
}

func TestCopyMatchesReactEmailTemplates(t *testing.T) {
	g := loadGolden(t)
	const url = "https://app.test/verify-email?token=T123"
	msgs := map[string]Message{}
	add := func(k string, m Message, err error) {
		if err != nil {
			t.Fatal(err)
		}
		msgs[k] = m
	}
	m, err := VerifyEmail("ann@example.com", url)
	add("verify", m, err)
	m, err = VerifyNewEmail("ann@example.com", url)
	add("verifyNew", m, err)
	m, err = ChangeEmailConfirmation("ann@example.com", "new@example.com", url)
	add("changeConfirm", m, err)
	m, err = ResetPassword("ann@example.com", url)
	add("reset", m, err)
	m, err = PasswordChanged("ann@example.com")
	add("changed", m, err)
	m, err = AccountDeleted("ann@example.com")
	add("deleted", m, err)
	m, err = Invite("x@example.com", fund.InviteMail{FundName: "Wedding", Inviter: "Sam", Role: "editor", URL: "https://app.test/invite/TOK"}, false)
	add("inviteNew", m, err)
	m, err = Invite("x@example.com", fund.InviteMail{FundName: "Wedding", Inviter: "Sam", Role: "viewer", URL: "https://app.test/invite/TOK"}, true)
	add("inviteHas", m, err)
	m, err = OwnershipReceived("b@example.com", "Wedding", "Sam", "https://app.test/settings")
	add("ownerGot", m, err)
	m, err = OwnershipHandedOff("a@example.com", "Wedding", "Alex", "https://app.test/")
	add("ownerGave", m, err)
	m, err = PaydayReminder("a@example.com", ReminderDetails{FundName: "Home", Payday: "Fri 31 Oct", Amount: "KES 12,000",
		URL: "https://app.test/contributions?new=1", UnsubscribeURL: "https://api.test/unsubscribe/reminders?token=X"})
	add("reminder", m, err)

	if len(msgs) != len(g.Texts) {
		t.Fatalf("%d messages vs %d golden texts", len(msgs), len(g.Texts))
	}
	for key, want := range g.Texts {
		got := norm(msgs[key].Text)
		skip := map[string]bool{}
		for _, d := range deviations[key] {
			skip[norm(d)] = true
		}
		for _, line := range strings.Split(want, "\n") {
			n := norm(line)
			if n == "" || skip[n] || strings.Trim(n, "-") == "" { // blank lines and <hr> rules
				continue
			}
			if !strings.Contains(got, n) {
				t.Errorf("%s: missing copy %q\n--- go text ---\n%s", key, line, msgs[key].Text)
			}
		}
	}
	if msgs["inviteNew"].Subject != g.Subjects.InviteNew || msgs["inviteHas"].Subject != g.Subjects.InviteHas {
		t.Errorf("invite subjects %q / %q want %q / %q", msgs["inviteNew"].Subject, msgs["inviteHas"].Subject, g.Subjects.InviteNew, g.Subjects.InviteHas)
	}
	if roleLabel("editor") != g.Subjects.Role[0] || roleLabel("viewer") != g.Subjects.Role[1] {
		t.Errorf("role labels differ: %v", g.Subjects.Role)
	}
}

func TestSubjectsMatchTheWebApp(t *testing.T) {
	want := map[string]string{
		"verify":        "Verify your email for Fund",
		"verifyNew":     "Verify your new email for Fund",
		"changeConfirm": "Approve your Fund email change",
		"reset":         "Reset your password for Fund",
		"changed":       "Your Fund password was changed",
		"deleted":       "Your Fund account was deleted",
		"ownerGot":      "You are now the owner of Wedding",
		"ownerGave":     "Alex owns Wedding now",
		"reminder":      "Payday tomorrow: set aside KES 12,000",
	}
	got := map[string]string{}
	m, _ := VerifyEmail("a@b.co", "u")
	got["verify"] = m.Subject
	m, _ = VerifyNewEmail("a@b.co", "u")
	got["verifyNew"] = m.Subject
	m, _ = ChangeEmailConfirmation("a@b.co", "n@b.co", "u")
	got["changeConfirm"] = m.Subject
	m, _ = ResetPassword("a@b.co", "u")
	got["reset"] = m.Subject
	m, _ = PasswordChanged("a@b.co")
	got["changed"] = m.Subject
	m, _ = AccountDeleted("a@b.co")
	got["deleted"] = m.Subject
	m, _ = OwnershipReceived("a@b.co", "Wedding", "Sam", "u")
	got["ownerGot"] = m.Subject
	m, _ = OwnershipHandedOff("a@b.co", "Wedding", "Alex", "u")
	got["ownerGave"] = m.Subject
	m, _ = PaydayReminder("a@b.co", ReminderDetails{Amount: "KES 12,000"})
	got["reminder"] = m.Subject
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s subject %q want %q", k, got[k], v)
		}
	}
}

func TestReminderCarriesUnsubscribeHeaders(t *testing.T) {
	m, _ := PaydayReminder("a@b.co", ReminderDetails{UnsubscribeURL: "https://api.test/unsubscribe/reminders?token=X"})
	if m.Headers["List-Unsubscribe"] != "<https://api.test/unsubscribe/reminders?token=X>" || m.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("headers: %+v", m.Headers)
	}
	if v, _ := VerifyEmail("a@b.co", "u"); v.Headers != nil {
		t.Fatal("only reminders carry unsubscribe headers")
	}
}

func TestHTMLEscapesUserText(t *testing.T) {
	m, err := Invite("x@example.com", fund.InviteMail{FundName: `<script>alert(1)</script>`, Inviter: `A & "B"`, Role: "viewer", URL: "https://app.test/invite/T?a=1&b=2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.HTML, "<script>") || !strings.Contains(m.HTML, "&lt;script&gt;") || !strings.Contains(m.HTML, "A &amp; &#34;B&#34;") {
		t.Fatalf("html not escaped:\n%s", m.HTML)
	}
	if !strings.Contains(m.Text, `<script>alert(1)</script>`) {
		t.Fatal("plain text should keep the raw name")
	}
	if !strings.Contains(m.HTML, `href="https://app.test/invite/T?a=1&amp;b=2"`) {
		t.Fatalf("link not escaped: %s", m.HTML)
	}
}

func TestResendRetriesOnceAndSendsExpectedRequest(t *testing.T) {
	var calls atomic.Int32
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer re_key" || r.URL.Path != "/emails" {
			t.Errorf("bad request %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		if calls.Add(1) == 1 {
			http.Error(w, `{"message":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := &Resend{APIKey: "re_key", From: "Fund <hi@fund.test>", BaseURL: srv.URL}
	err := r.Send(context.Background(), Message{To: "a@b.co", Subject: "S", HTML: "<p>x</p>", Text: "x", Headers: map[string]string{"List-Unsubscribe": "<u>"}})
	if err != nil || calls.Load() != 2 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	if got["from"] != "Fund <hi@fund.test>" || got["to"] != "a@b.co" || got["subject"] != "S" || got["text"] != "x" {
		t.Fatalf("payload %+v", got)
	}
	if h, _ := got["headers"].(map[string]any); h["List-Unsubscribe"] != "<u>" {
		t.Fatalf("headers %+v", got["headers"])
	}
}

func TestResendGivesUpAfterTwoAttemptsAndHandlesNoKey(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	r := &Resend{APIKey: "k", From: "f@x.co", BaseURL: srv.URL}
	if err := r.Send(context.Background(), Message{To: "a@b.co"}); err == nil || calls.Load() != 2 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	// No key: logs only, never errors, never calls out.
	calls.Store(0)
	if err := (&Resend{BaseURL: srv.URL}).Send(context.Background(), Message{To: "a@b.co"}); err != nil || calls.Load() != 0 {
		t.Fatalf("no-key send: err=%v calls=%d", err, calls.Load())
	}
	if err := (&Resend{APIKey: "k", BaseURL: srv.URL}).Send(context.Background(), Message{}); err == nil {
		t.Fatal("missing EMAIL_FROM should error")
	}
}
