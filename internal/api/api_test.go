package api

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

const future = "2099-01-01"

func TestHealthAndReadiness(t *testing.T) {
	a := newApp(t, opts{})
	if r := a.do("GET", "/healthz", "", nil); r.Status != 200 || string(r.Body) != `{"ok":true}` {
		t.Fatalf("healthz %d %s", r.Status, r.Body)
	}
	if r := a.do("GET", "/readyz", "", nil); r.Status != 200 {
		t.Fatalf("readyz %d %s", r.Status, r.Body)
	}
	if r := a.do("POST", "/healthz", "", nil); r.Status != 405 {
		t.Fatalf("POST healthz: %d", r.Status)
	}
}

func TestAuthGates(t *testing.T) {
	a := newApp(t, opts{})
	wantStatus(t, a.do("GET", "/funds", "", nil), 401, "Sign in required.")
	wantStatus(t, a.do("GET", "/funds", "garbage-token", nil), 401, "Sign in required.")
	wantStatus(t, a.do("GET", "/auth/session", "", nil), 401, "Sign in required.")

	// An unverified account can sign in but cannot use funds.
	r := a.do("POST", "/auth/sign-up", "", map[string]string{"name": "Ann", "email": "ann@example.com", "password": "hunter2hunter2"})
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	tok := r.json()["token"].(string)
	if u := a.do("GET", "/auth/session", tok, nil).json()["user"].(map[string]any); u["emailVerified"] != false {
		t.Fatalf("user: %+v", u)
	}
	wantStatus(t, a.do("GET", "/funds", tok, nil), 403, "Verify your email before using your fund.")
}

func TestSignUpVerifyAndSignInJourney(t *testing.T) {
	a := newApp(t, opts{})
	wantStatus(t, a.do("POST", "/auth/sign-up", "", map[string]string{"name": "A", "email": "bad", "password": "hunter2hunter2"}), 400, "Enter a valid email address.")
	wantStatus(t, a.do("POST", "/auth/sign-in", "", map[string]string{"email": "x@example.com", "password": "nope-nope-nope"}), 401, "Invalid email or password.")
	wantStatus(t, a.do("POST", "/auth/sign-up", "", "{not json"), 400, "Send a valid JSON body.")

	tok := a.signUp("Ann", "ann@example.com")
	wantStatus(t, a.do("POST", "/auth/sign-up", "", map[string]string{"name": "Ann", "email": "ANN@example.com", "password": "hunter2hunter2"}), 422, "User already exists. Use another email.")

	in := a.do("POST", "/auth/sign-in", "", map[string]string{"email": "ann@example.com", "password": "hunter2hunter2"})
	if in.Status != 200 || in.json()["token"] == "" || in.json()["user"].(map[string]any)["emailVerified"] != true {
		t.Fatalf("sign-in %d %s", in.Status, in.Body)
	}
	// Sessions listing, current marker and revoke-others.
	list := a.do("GET", "/auth/sessions", tok, nil).json()["sessions"].([]any)
	if len(list) != 3 { // sign-up, email verification, sign-in
		t.Fatalf("%d sessions", len(list))
	}
	if r := a.do("POST", "/auth/sessions/revoke-others", tok, nil); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	wantStatus(t, a.do("GET", "/auth/session", in.json()["token"].(string), nil), 401, "")
	if r := a.do("POST", "/auth/sign-out", tok, nil); r.Status != 200 {
		t.Fatalf("sign-out %d", r.Status)
	}
	wantStatus(t, a.do("GET", "/auth/session", tok, nil), 401, "")
}

func TestPasswordResetAndAccountSettingsOverHTTP(t *testing.T) {
	a := newApp(t, opts{})
	tok := a.signUp("Ann", "ann@example.com")

	// Reset: the answer is identical for known and unknown addresses.
	for _, e := range []string{"ann@example.com", "ghost@example.com"} {
		if r := a.do("POST", "/auth/request-password-reset", "", map[string]string{"email": e}); r.Status != 200 {
			t.Fatalf("%s: %d %s", e, r.Status, r.Body)
		}
	}
	u, _ := url.Parse(a.mail.last("reset"))
	reset := u.Query().Get("token")
	wantStatus(t, a.do("POST", "/auth/reset-password", "", map[string]string{"token": reset, "password": "short"}), 400, "")
	if r := a.do("POST", "/auth/reset-password", "", map[string]string{"token": reset, "password": "brand-new-pass"}); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if r := a.do("POST", "/auth/sign-in", "", map[string]string{"email": "ann@example.com", "password": "brand-new-pass"}); r.Status != 200 {
		t.Fatalf("sign-in after reset %d", r.Status)
	}

	wantStatus(t, a.do("POST", "/auth/change-password", tok, map[string]any{"currentPassword": "wrong", "newPassword": "another-pass-1"}), 400, "Invalid password.")
	if r := a.do("POST", "/auth/change-password", tok, map[string]any{"currentPassword": "brand-new-pass", "newPassword": "another-pass-1", "revokeOtherSessions": true}); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if p := a.mail.last("pw-changed"); p != "" {
		t.Fatal("password-changed email has no link")
	}

	if r := a.do("PATCH", "/auth/profile", tok, map[string]string{"name": "  Ann Lee "}); r.Status != 200 || r.json()["user"].(map[string]any)["name"] != "Ann Lee" {
		t.Fatalf("profile %d %s", r.Status, r.Body)
	}
	if r := a.do("POST", "/auth/change-email", tok, map[string]string{"newEmail": "ann.new@example.com"}); r.Status != 200 {
		t.Fatalf("change email %d %s", r.Status, r.Body)
	}
	cu, _ := url.Parse(a.mail.last("change-confirm"))
	step1 := a.do("POST", "/auth/verify-email", "", map[string]string{"token": cu.Query().Get("token")}).json()
	if step1["status"] != "confirmation-sent" {
		t.Fatalf("%v", step1)
	}
	vu, _ := url.Parse(a.mail.last("verify-new"))
	if s := a.do("POST", "/auth/verify-email", "", map[string]string{"token": vu.Query().Get("token")}).json()["status"]; s != "email-changed" {
		t.Fatalf("status %v", s)
	}
	if r := a.do("POST", "/auth/sign-in", "", map[string]string{"email": "ann.new@example.com", "password": "another-pass-1"}); r.Status != 200 {
		t.Fatalf("sign in with new email %d", r.Status)
	}

	wantStatus(t, a.do("DELETE", "/auth/account", tok, map[string]string{"password": ""}), 400, "")
	wantStatus(t, a.do("DELETE", "/auth/account", tok, map[string]string{"password": "wrong"}), 400, "Invalid password.")
	if r := a.do("DELETE", "/auth/account", tok, map[string]string{"password": "another-pass-1"}); r.Status != 200 {
		t.Fatalf("delete %d %s", r.Status, r.Body)
	}
	wantStatus(t, a.do("GET", "/auth/session", tok, nil), 401, "")
}

func TestFundJourney(t *testing.T) {
	a := newApp(t, opts{})
	tok := a.signUp("Ann", "ann@example.com")
	fid := a.firstFund(tok)
	base := "/funds/" + fid

	p := payloadOf(t, a.do("GET", base, tok, nil))
	if p["role"] != "owner" || p["needsOnboarding"] != true || p["sharing"] == nil {
		t.Fatalf("payload: %+v", p)
	}

	// Settings, then onboarding completes.
	p = payloadOf(t, a.do("PATCH", base, tok, map[string]any{"name": "Home", "paydays": []int{15, 31}, "pace": 2500, "currency": "KES", "timezone": "Africa/Nairobi"}))
	fv := p["fund"].(map[string]any)
	if fv["name"] != "Home" || fv["pace"].(float64) != 2500 || len(fv["paydays"].([]any)) != 2 {
		t.Fatalf("fund: %+v", fv)
	}
	if p := payloadOf(t, a.do("PATCH", base, tok, map[string]any{"pace": nil})); p["fund"].(map[string]any)["pace"] != nil {
		t.Fatal("pace null should clear")
	}
	wantStatus(t, a.do("PATCH", base, tok, map[string]any{"timezone": "Mars/Phobos"}), 400, "Pick a valid timezone.")
	wantStatus(t, a.do("PATCH", base, tok, map[string]any{"paydays": []int{}}), 400, "Add at least one payday.")
	if p := payloadOf(t, a.do("POST", base+"/onboarding/complete", tok, nil)); p["needsOnboarding"] != false {
		t.Fatal("onboarding not completed")
	}

	// Items.
	p = payloadOf(t, a.do("POST", base+"/items", tok, map[string]any{"name": "Laptop", "price": 1000, "deadline": future}))
	laptop := p["items"].([]any)[0].(map[string]any)["id"].(string)
	wantStatus(t, a.do("POST", base+"/items", tok, map[string]any{"name": "Yacht", "price": 9999999999, "deadline": future}), 400, "That price is too large.")
	wantStatus(t, a.do("PATCH", base+"/items/nope", tok, map[string]any{"name": "x"}), 404, "That item is not in your fund.")

	// Idempotent contribution create (client id) and the ledger rule.
	cid := "4f0c2f2e-2a52-4a53-9a4e-0d6a3b1c7e11"
	body := map[string]any{"id": cid, "date": "2026-10-01", "note": " bonus ", "allocations": []map[string]any{{"itemId": laptop, "amount": 200}}}
	for i := 0; i < 2; i++ {
		p = payloadOf(t, a.do("POST", base+"/contributions", tok, body))
		if n := len(p["contributions"].([]any)); n != 1 {
			t.Fatalf("%d contributions after %d sends", n, i+1)
		}
	}
	body["allocations"] = []map[string]any{{"itemId": laptop, "amount": 201}}
	wantStatus(t, a.do("POST", base+"/contributions", tok, body), 409, "That entry id is already used by another entry.")
	wantStatus(t, a.do("POST", base+"/contributions", tok, map[string]any{"date": "2026-10-02", "kind": "withdrawal", "allocations": []map[string]any{{"itemId": laptop, "amount": 500}}}), 400, "Laptop would hold less than 0 on 2026-10-02.")
	wantStatus(t, a.do("POST", base+"/contributions", tok, map[string]any{"date": future, "allocations": []map[string]any{{"itemId": laptop, "amount": 5}}}), 400, "The date cannot be later than today.")

	// Edit and delete.
	p = payloadOf(t, a.do("PATCH", base+"/contributions/"+cid, tok, map[string]any{"date": "2026-10-01", "allocations": []map[string]any{{"itemId": laptop, "amount": 250}}}))
	if amt := p["contributions"].([]any)[0].(map[string]any)["allocations"].([]any)[0].(map[string]any)["amount"].(float64); amt != 250 {
		t.Fatalf("amount %v", amt)
	}
	wantStatus(t, a.do("DELETE", base+"/items/"+laptop, tok, nil), 409, "Remove the contributions on this item first.")
	p = payloadOf(t, a.do("DELETE", base+"/contributions/"+cid, tok, nil))
	if len(p["contributions"].([]any)) != 0 {
		t.Fatal("contribution not deleted")
	}

	// CSV import over HTTP: parse errors are 400s, success returns the payload.
	wantStatus(t, a.do("POST", base+"/contributions/import", tok, map[string]string{"csv": "wrong,header\n"}), 400, "The first row must be: date, note, item, amount.")
	csv := "date,note,item,amount\r\n2026-10-03,pay,laptop,40\r\n"
	p = payloadOf(t, a.do("POST", base+"/contributions/import", tok, map[string]string{"csv": csv}))
	if len(p["contributions"].([]any)) != 1 {
		t.Fatal("import did not add the row")
	}

	// Archive / move / reorder.
	p = payloadOf(t, a.do("POST", base+"/items", tok, map[string]any{"name": "Phone", "price": 500, "deadline": future}))
	phone := ""
	for _, it := range p["items"].([]any) {
		if it.(map[string]any)["name"] == "Phone" {
			phone = it.(map[string]any)["id"].(string)
		}
	}
	payloadOf(t, a.do("POST", base+"/items/"+phone+"/move", tok, map[string]string{"direction": "up"}))
	payloadOf(t, a.do("POST", base+"/items/"+phone+"/reorder", tok, map[string]int{"toIndex": 0}))
	wantStatus(t, a.do("POST", base+"/items/"+phone+"/move", tok, map[string]string{"direction": "sideways"}), 400, "Direction must be up or down.")
	p = payloadOf(t, a.do("POST", base+"/items/"+phone+"/archive", tok, map[string]any{"archived": true, "purchase": map[string]any{"actualPrice": 450, "purchasedOn": "2026-10-05"}}))
	for _, it := range p["items"].([]any) {
		if m := it.(map[string]any); m["id"] == phone && (m["archived"] != true || m["actualPrice"].(float64) != 450) {
			t.Fatalf("archived item %+v", m)
		}
	}
	payloadOf(t, a.do("POST", base+"/items/"+phone+"/archive", tok, map[string]any{"archived": false}))

	// Reminders preference.
	if p := payloadOf(t, a.do("PUT", base+"/reminders", tok, map[string]bool{"enabled": true})); p["fund"].(map[string]any)["remindersEnabled"] != true {
		t.Fatal("reminders not enabled")
	}

	// Creating and deleting a second fund.
	p = payloadOf(t, a.do("POST", "/funds", tok, map[string]string{"name": "Holiday"}))
	second := p["fund"].(map[string]any)["id"].(string)
	if len(p["funds"].([]any)) != 2 {
		t.Fatal("fund list should have 2")
	}
	if r := a.do("DELETE", "/funds/"+second, tok, nil); r.Status != 200 {
		t.Fatalf("delete fund %d %s", r.Status, r.Body)
	}
	wantStatus(t, a.do("GET", "/funds/"+second, tok, nil), 404, "That fund is not available to you.")
}

func TestPaydaySkipAndAdjustOverHTTP(t *testing.T) {
	a := newApp(t, opts{})
	tok := a.signUp("Ann", "ann@example.com")
	fid := a.firstFund(tok)
	base := "/funds/" + fid
	// Pick a real upcoming payday through the public payload: paydays [31] is the default.
	wantStatus(t, a.do("POST", base+"/paydays/2020-01-31/skip", tok, nil), 400, "Only a payday from today on can be skipped.")
	wantStatus(t, a.do("POST", base+"/paydays/2099-01-02/skip", tok, nil), 400, "That date is not a payday.")
	wantStatus(t, a.do("PUT", base+"/paydays/2099-01-31/amount", tok, map[string]float64{"amount": 1.5}), 400, "Enter a whole amount.")
	p := payloadOf(t, a.do("PUT", base+"/paydays/2099-01-31/amount", tok, map[string]int{"amount": 20000}))
	if adj := p["adjustments"].([]any); len(adj) != 1 || adj[0].(map[string]any)["amount"].(float64) != 20000 {
		t.Fatalf("adjustments %v", adj)
	}
	p = payloadOf(t, a.do("POST", base+"/paydays/2099-01-31/skip", tok, nil))
	if len(p["adjustments"].([]any)) != 0 || len(p["skips"].([]any)) != 1 {
		t.Fatalf("skip should drop the amount: %+v", p)
	}
	wantStatus(t, a.do("PUT", base+"/paydays/2099-01-31/amount", tok, map[string]int{"amount": 5}), 400, "That payday is skipped. Restore it first.")
	p = payloadOf(t, a.do("DELETE", base+"/paydays/2099-01-31/skip", tok, nil))
	if len(p["skips"].([]any)) != 0 {
		t.Fatal("skip not restored")
	}
	payloadOf(t, a.do("DELETE", base+"/paydays/2099-01-31/amount", tok, nil))
}

func TestSharingAndInvitesOverHTTP(t *testing.T) {
	a := newApp(t, opts{})
	owner := a.signUp("Ann", "ann@example.com")
	other := a.signUp("Bob", "bob@example.com")
	fid := a.firstFund(owner)
	base := "/funds/" + fid

	wantStatus(t, a.do("POST", base+"/invites", other, map[string]string{"email": "x@example.com", "role": "viewer"}), 404, "That fund is not available to you.")
	payloadOf(t, a.do("POST", base+"/invites", owner, map[string]string{"email": "Bob@Example.com", "role": "editor"}))
	inv := a.mail.invites[len(a.mail.invites)-1]
	token := inv.URL[strings.LastIndex(inv.URL, "/")+1:]

	info := a.do("GET", "/invites/"+token, "", nil).json()["invite"].(map[string]any)
	if info["state"] != "ok" || info["account"] != "verified" || info["fundName"] != "Fund" {
		t.Fatalf("invite: %+v", info)
	}
	if st := a.do("GET", "/invites/nope", "", nil).json()["invite"].(map[string]any)["state"]; st != "invalid" {
		t.Fatalf("state %v", st)
	}
	wantStatus(t, a.do("POST", "/invites/"+token+"/accept", "", nil), 401, "Sign in required.")
	p := payloadOf(t, a.do("POST", "/invites/"+token+"/accept", other, nil))
	if p["role"] != "editor" || p["sharing"] != nil {
		t.Fatalf("member payload: role=%v sharing=%v", p["role"], p["sharing"])
	}
	// Editors may edit items but not the fund's settings or sharing.
	payloadOf(t, a.do("POST", base+"/items", other, map[string]any{"name": "Shared", "price": 10, "deadline": future}))
	wantStatus(t, a.do("PATCH", base, other, map[string]any{"name": "Mine"}), 403, "Only the owner can do that.")
	wantStatus(t, a.do("POST", base+"/invites", other, map[string]string{"email": "y@example.com", "role": "viewer"}), 403, "Only the owner can do that.")

	bobID := a.do("GET", "/auth/session", other, nil).json()["user"].(map[string]any)["id"].(string)
	payloadOf(t, a.do("PATCH", base+"/members/"+bobID, owner, map[string]string{"role": "viewer"}))
	wantStatus(t, a.do("POST", base+"/items", other, map[string]any{"name": "Nope", "price": 10, "deadline": future}), 403, "You can view this fund but not change it.")
	payloadOf(t, a.do("PATCH", base+"/members/"+bobID, owner, map[string]string{"role": "editor"}))
	payloadOf(t, a.do("POST", base+"/transfer-ownership", owner, map[string]string{"userId": bobID}))
	if p := payloadOf(t, a.do("GET", base, other, nil)); p["role"] != "owner" {
		t.Fatalf("role after transfer %v", p["role"])
	}
	if r := a.do("POST", base+"/leave", owner, nil); r.Status != 200 {
		t.Fatalf("leave %d %s", r.Status, r.Body)
	}
}

func TestInviteSignupOverHTTP(t *testing.T) {
	a := newApp(t, opts{})
	owner := a.signUp("Ann", "ann@example.com")
	fid := a.firstFund(owner)
	payloadOf(t, a.do("POST", "/funds/"+fid+"/invites", owner, map[string]string{"email": "newbie@example.com", "role": "viewer"}))
	inv := a.mail.invites[len(a.mail.invites)-1]
	token := inv.URL[strings.LastIndex(inv.URL, "/")+1:]

	wantStatus(t, a.do("POST", "/invites/"+token+"/accept-signup", "", map[string]string{"name": "N", "password": "short"}), 400, "Use at least 8 characters.")
	r := a.do("POST", "/invites/"+token+"/accept-signup", "", map[string]string{"name": "Newbie", "password": "longenough1"})
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	m := r.json()
	if m["fundId"] != fid || m["token"] == "" {
		t.Fatalf("response: %v", m)
	}
	// The returned token works immediately and sees the shared fund as a viewer.
	p := payloadOf(t, a.do("GET", "/funds/"+fid, m["token"].(string), nil))
	if p["role"] != "viewer" || p["needsOnboarding"] != false {
		t.Fatalf("payload: role=%v needsOnboarding=%v", p["role"], p["needsOnboarding"])
	}
	// The invited person has no personal fund.
	if fs := a.do("GET", "/funds", m["token"].(string), nil).json()["funds"].([]any); len(fs) != 1 {
		t.Fatalf("%d funds", len(fs))
	}
}

func TestDownloads(t *testing.T) {
	a := newApp(t, opts{})
	tok := a.signUp("Ann", "ann@example.com")
	fid := a.firstFund(tok)
	base := "/funds/" + fid
	p := payloadOf(t, a.do("POST", base+"/items", tok, map[string]any{"name": "=Laptop", "price": 1000, "deadline": future}))
	laptop := p["items"].([]any)[0].(map[string]any)["id"].(string)
	payloadOf(t, a.do("POST", base+"/contributions", tok, map[string]any{"date": "2026-10-01", "note": "pay", "allocations": []map[string]any{{"itemId": laptop, "amount": 200}}}))

	c := a.do("GET", base+"/export/contributions.csv", tok, nil)
	if c.Status != 200 || !strings.HasPrefix(c.Header.Get("Content-Type"), "text/csv") || !strings.Contains(c.Header.Get("Content-Disposition"), "fund-contributions.csv") ||
		!strings.HasPrefix(string(c.Body), "# fund-contributions v2\r\n") || !strings.Contains(string(c.Body), "2026-10-01,deposit,pay,'=Laptop,200") {
		t.Fatalf("contributions.csv %d %q %q", c.Status, c.Header, c.Body)
	}
	it := a.do("GET", base+"/export/items.csv", tok, nil)
	if it.Status != 200 || !strings.Contains(string(it.Body), "'=Laptop,1000,2099-01-01") || !strings.Contains(string(it.Body), ",200,,,active") {
		t.Fatalf("items.csv %d %q", it.Status, it.Body)
	}
	ics := a.do("GET", base+"/export/schedule.ics", tok, nil)
	if ics.Status != 200 || !strings.HasPrefix(ics.Header.Get("Content-Type"), "text/calendar") || !strings.HasPrefix(string(ics.Body), "BEGIN:VCALENDAR\r\n") || !strings.Contains(string(ics.Body), "X-WR-CALNAME:Fund") {
		t.Fatalf("ics %d %q", ics.Status, ics.Body)
	}
	wantStatus(t, a.do("GET", base+"/export/items.csv", "", nil), 401, "")

	z := a.do("GET", "/account/export", tok, nil)
	if z.Status != 200 || z.Header.Get("Content-Type") != "application/zip" || z.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("zip %d %v", z.Status, z.Header)
	}
	files := zipNames(t, z.Body)
	if !strings.Contains(files["account.json"], `"email": "ann@example.com"`) {
		t.Fatalf("account.json: %s", files["account.json"])
	}
	found := false
	for name := range files {
		if strings.HasPrefix(name, "owned/fund-") && strings.HasSuffix(name, "/contributions.csv") {
			found = true
		}
	}
	if !found {
		t.Fatalf("zip entries: %v", files)
	}
	for i := 0; i < 4; i++ { // five exports per hour in total
		if r := a.do("GET", "/account/export", tok, nil); r.Status != 200 {
			t.Fatalf("export %d: %d", i+2, r.Status)
		}
	}
	if r := a.do("GET", "/account/export", tok, nil); r.Status != 429 || !strings.HasPrefix(r.json()["error"].(string), "You can export your data up to 5 times per hour.") {
		t.Fatalf("sixth export: %d %s", r.Status, r.Body)
	}
}

func TestRateLimitsAndCORSAndBodyLimits(t *testing.T) {
	a := newApp(t, opts{rateLimit: true, cors: []string{"https://app.test"}})
	for i := 0; i < 10; i++ {
		wantStatus(t, a.do("POST", "/auth/sign-in", "", map[string]string{"email": "x@example.com", "password": "nope-nope-nope"}), 401, "")
	}
	r := a.do("POST", "/auth/sign-in", "", map[string]string{"email": "x@example.com", "password": "nope-nope-nope"})
	wantStatus(t, r, 429, "Too many requests. Wait a little and try again.")
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
	// A different client IP has its own bucket (the proxy header is trusted).
	if r := a.do("POST", "/auth/sign-in", "", map[string]string{"email": "x@example.com", "password": "nope-nope-nope"}, "X-Forwarded-For", "203.0.113.9, 10.0.0.1"); r.Status != 401 {
		t.Fatalf("other IP: %d", r.Status)
	}
	// Sign-up allows 5 per hour per IP; reset requests allow 3.
	for i := 0; i < 3; i++ {
		if r := a.do("POST", "/auth/request-password-reset", "", map[string]string{"email": "x@example.com"}, "X-Forwarded-For", "198.51.100.1"); r.Status != 200 {
			t.Fatalf("reset %d: %d", i, r.Status)
		}
	}
	if r := a.do("POST", "/auth/request-password-reset", "", map[string]string{"email": "x@example.com"}, "X-Forwarded-For", "198.51.100.1"); r.Status != 429 {
		t.Fatalf("4th reset: %d", r.Status)
	}

	// CORS: only listed origins get headers; preflight answers 204.
	ok := a.do("OPTIONS", "/funds", "", nil, "Origin", "https://app.test")
	if ok.Status != 204 || ok.Header.Get("Access-Control-Allow-Origin") != "https://app.test" || !strings.Contains(ok.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight %d %v", ok.Status, ok.Header)
	}
	if bad := a.do("GET", "/healthz", "", nil, "Origin", "https://evil.test"); bad.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin got CORS headers")
	}

	// Oversized JSON bodies are refused.
	a2 := newApp(t, opts{})
	huge := `{"name":"` + strings.Repeat("a", 2<<20) + `"}`
	if r := a2.do("POST", "/auth/sign-up", "", huge); r.Status != 413 {
		t.Fatalf("huge body: %d", r.Status)
	}
}

func TestCronRemindersAndUnsubscribe(t *testing.T) {
	// No secret configured: the endpoint is off.
	off := newApp(t, opts{})
	wantStatus(t, off.do("POST", "/cron/reminders", "", nil), 404, "Not enabled.")

	a := newApp(t, opts{cron: "cron-secret-0123456789"})
	wantStatus(t, a.do("POST", "/cron/reminders", "", nil), 401, "Unauthorized.")
	wantStatus(t, a.do("POST", "/cron/reminders", "wrong-secret", nil), 401, "Unauthorized.")
	for _, method := range []string{"GET", "POST"} {
		r := a.do(method, "/cron/reminders", "cron-secret-0123456789", nil)
		if r.Status != 200 || r.json()["checked"] == nil || r.json()["sent"] == nil {
			t.Fatalf("%s cron %d %s", method, r.Status, r.Body)
		}
	}

	// Unsubscribe: GET click, one-click POST (query token and form token), and bad input.
	tok := a.signUp("Ann", "ann@example.com")
	fid := a.firstFund(tok)
	payloadOf(t, a.do("PUT", "/funds/"+fid+"/reminders", tok, map[string]bool{"enabled": true}))
	userID := a.do("GET", "/auth/session", tok, nil).json()["user"].(map[string]any)["id"].(string)
	token := reminders_sign(fid, userID)

	if r := a.do("GET", "/unsubscribe/reminders?token="+url.QueryEscape(token), "", nil); r.Status != 200 || !strings.Contains(string(r.Body), "Payday reminders are off") {
		t.Fatalf("GET unsubscribe %d %s", r.Status, r.Body)
	}
	if on := a.reminderEnabled(fid, userID); on {
		t.Fatal("reminders still enabled")
	}
	wantStatus(t, a.do("GET", "/unsubscribe/reminders", "", nil), 400, "Missing token.")
	wantStatus(t, a.do("GET", "/unsubscribe/reminders?token=garbage", "", nil), 400, "Invalid token.")
	wantStatus(t, a.do("POST", "/unsubscribe/reminders?token="+url.QueryEscape(token), "", "List-Unsubscribe=Nope", "Content-Type", "application/x-www-form-urlencoded"), 400, "Unsupported body.")
	if r := a.do("POST", "/unsubscribe/reminders?token="+url.QueryEscape(token), "", "List-Unsubscribe=One-Click", "Content-Type", "application/x-www-form-urlencoded"); r.Status != 200 {
		t.Fatalf("one-click %d %s", r.Status, r.Body)
	}
	if r := a.do("POST", "/unsubscribe/reminders", "", "List-Unsubscribe=One-Click&token="+url.QueryEscape(token), "Content-Type", "application/x-www-form-urlencoded"); r.Status != 200 {
		t.Fatalf("one-click form token %d %s", r.Status, r.Body)
	}
}

var _ = context.Background

func TestTimestampsAreUTC(t *testing.T) {
	a := newApp(t, opts{})
	r := a.do("POST", "/auth/sign-up", "", map[string]string{"name": "Ann", "email": "ann@example.com", "password": "hunter2hunter2"})
	m := r.json()
	if exp := m["expiresAt"].(string); !strings.HasSuffix(exp, "Z") {
		t.Fatalf("expiresAt %q is not UTC", exp)
	}
	if c := m["user"].(map[string]any)["createdAt"].(string); !strings.HasSuffix(c, "Z") {
		t.Fatalf("createdAt %q is not UTC", c)
	}
}
