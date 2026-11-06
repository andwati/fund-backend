package fund

import (
	"strings"
	"testing"
	"time"

	"github.com/andwati/fund-backend/internal/db/dbgen"
)

func tokenFromURL(u string) string { return u[strings.LastIndex(u, "/")+1:] }

func TestSeveralFunds(t *testing.T) {
	e := setup(t)
	first := e.fundOf(e.A)
	second, err := e.svc.CreateFund(bg, e.A, " Holiday ")
	if err != nil {
		t.Fatal(err)
	}
	p := e.payload(e.A, second)
	if p.Fund.Name != "Holiday" || len(p.Funds) != 2 || p.Funds[0].ID != first {
		t.Fatalf("payload: %+v", p)
	}
	// A fund is not visible to someone with no access.
	_, err = e.svc.Payload(bg, e.B, second)
	wantErr(t, err, 404, "That fund is not available to you.")
	_, err = e.svc.CreateFund(bg, e.A, " ")
	wantErr(t, err, 400, "Enter a name.")
}

func TestCapsOwnedFunds(t *testing.T) {
	e := setup(t)
	e.fundOf(e.A)
	for i := 1; i < MaxOwnedFunds; i++ {
		if _, err := e.svc.CreateFund(bg, e.A, "F"); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.svc.CreateFund(bg, e.A, "One too many")
	wantErr(t, err, 400, "You can own up to 10 funds. Delete one first.")
}

func TestInviteAcceptFlow(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	if err := e.svc.InviteMember(bg, e.A, f, " USER-B@Example.com ", "editor"); err != nil {
		t.Fatal(err)
	}
	inv := e.mail.lastInvite()
	if inv.To != "user-b@example.com" || inv.Mail.FundName != "Fund" || inv.Mail.Inviter != "user-a" || inv.Mail.Role != "editor" {
		t.Fatalf("invite mail: %+v", inv)
	}
	token := tokenFromURL(inv.Mail.URL)
	if !strings.HasPrefix(inv.Mail.URL, "https://fund.test/invite/") {
		t.Fatalf("invite url %q", inv.Mail.URL)
	}
	info, _ := e.svc.ReadInvite(bg, token)
	if info.State != "ok" || info.Account != "verified" || info.Inviter != "user-a" || info.FundName != "Fund" {
		t.Fatalf("invite info: %+v", info)
	}
	// Only the invited account may accept.
	c := e.user("user-c")
	_, err := e.svc.AcceptInvite(bg, c, token, "1.1.1.1")
	wantErr(t, err, 403, "This invite was sent to a different email address.")
	e.fundOf(e.B) // B opens the app once, which creates their own fund
	got, err := e.svc.AcceptInvite(bg, e.B, token, "1.1.1.1")
	if err != nil || got != f {
		t.Fatalf("accept: %q %v", got, err)
	}
	p := e.payload(e.B, f)
	if p.Role != RoleEditor || p.Sharing != nil {
		t.Fatalf("member payload: role=%s sharing=%v", p.Role, p.Sharing)
	}
	if st, _ := e.svc.ReadInvite(bg, token); st.State != "invalid" {
		t.Fatalf("invite reusable after accept: %+v", st)
	}
	owner := e.payload(e.A, f)
	if owner.Sharing == nil || len(owner.Sharing.Members) != 1 || owner.Sharing.Members[0].Email != "user-b@example.com" {
		t.Fatalf("owner sharing: %+v", owner.Sharing)
	}
	// The member's funds list includes the shared fund and it starts with their own.
	funds, _ := e.svc.ListFunds(bg, e.B)
	if len(funds) != 2 || funds[0].Role != RoleOwner || funds[1].Role != RoleEditor {
		t.Fatalf("member funds: %+v", funds)
	}
}

func TestEditorsViewersAndSettings(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "editor"))
	if err := e.svc.AddItem(bg, e.B, f, ItemInput{Name: "Shared", Price: 10, Deadline: future}); err != nil {
		t.Fatal(err)
	}
	n := "Renamed"
	wantErr(t, e.svc.UpdateFund(bg, e.B, f, FundPatch{Name: &n}), 403, "Only the owner can do that.")
	wantErr(t, e.svc.InviteMember(bg, e.B, f, "x@example.com", "viewer"), 403, "Only the owner can do that.")

	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer"))
	wantErr(t, e.svc.AddItem(bg, e.B, f, ItemInput{Name: "Nope", Price: 10, Deadline: future}), 403, "You can view this fund but not change it.")
	if _, err := e.svc.Payload(bg, e.B, f); err != nil {
		t.Fatalf("viewers may read: %v", err)
	}
}

func TestInviteRulesAndErrors(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	wantErr(t, e.svc.InviteMember(bg, e.A, f, "user-a@example.com", "editor"), 400, "You already own this fund.")
	wantErr(t, e.svc.InviteMember(bg, e.A, f, "not-an-email", "editor"), 400, "Enter a valid email.")
	wantErr(t, e.svc.InviteMember(bg, e.A, f, "x@example.com", "owner"), 400, "Pick a role.")
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer"))
	wantErr(t, e.svc.InviteMember(bg, e.A, f, "user-b@example.com", "editor"), 400, "They already have access.")

	// Expired invites cannot be accepted or resent, and do not count toward the cap.
	_ = e.svc.InviteMember(bg, e.A, f, "late@example.com", "viewer")
	token := tokenFromURL(e.mail.lastInvite().Mail.URL)
	if _, err := e.pool.Exec(bg, `update fund_invite set expires_at = now() - interval '1 day'`); err != nil {
		t.Fatal(err)
	}
	if info, _ := e.svc.ReadInvite(bg, token); info.State != "expired" {
		t.Fatalf("state %q", info.State)
	}
	late := e.user("late")
	_, err := e.svc.AcceptInvite(bg, auth_user(late, "late@example.com"), token, "2.2.2.2")
	wantErr(t, err, 410, "That invite has expired. Ask for a new one.")
	if sh := e.payload(e.A, f).Sharing; len(sh.Invites) != 0 {
		t.Fatalf("expired invite listed: %+v", sh.Invites)
	}
	invs, _ := e.q.ListInvites(bg, f)
	_ = invs
	var id string
	_ = e.pool.QueryRow(bg, `select id from fund_invite limit 1`).Scan(&id)
	wantErr(t, e.svc.ResendInvite(bg, e.A, f, id), 410, "That invite has expired. Send a new one.")
	wantErr(t, e.svc.ResendInvite(bg, e.A, f, "gone"), 404, "That invite is gone.")
}

func TestOwnerRemovesMembersAndMembersLeave(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer"))
	if err := e.svc.SetMemberRole(bg, e.A, f, e.B.ID, "editor"); err != nil {
		t.Fatal(err)
	}
	if e.payload(e.B, f).Role != RoleEditor {
		t.Fatal("role not changed")
	}
	wantErr(t, e.svc.SetMemberRole(bg, e.A, f, "ghost", "editor"), 404, "That person is not in this fund.")
	wantErr(t, e.svc.SetMemberRole(bg, e.A, f, e.B.ID, "owner"), 400, "Pick a role.")
	if err := e.svc.LeaveFund(bg, e.B, f); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Payload(bg, e.B, f)
	wantErr(t, err, 404, "")
	wantErr(t, e.svc.LeaveFund(bg, e.A, f), 400, "You own this fund. Delete it instead of leaving.")
	wantErr(t, e.svc.LeaveFund(bg, e.B, f), 404, "That fund is not available to you.")

	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer"))
	if err := e.svc.RemoveMember(bg, e.A, f, e.B.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Payload(bg, e.B, f)
	wantErr(t, err, 404, "")
}

func TestDeleteFundIsOwnerOnlyAndRemovesEverything(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	it := e.addItem(e.A, f, "Laptop", 1000)
	e.mustContribute(e.A, f, ContributionInput{Date: "2026-10-01", Allocations: line(it.ID, 10)})
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "editor"))
	_ = e.svc.InviteMember(bg, e.A, f, "pending@example.com", "viewer")
	wantErr(t, e.svc.DeleteFund(bg, e.B, f), 403, "Only the owner can do that.")
	if err := e.svc.DeleteFund(bg, e.A, f); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"fund", "item", "contribution", "allocation", "fund_member", "fund_invite", "fund_payday"} {
		var n int
		_ = e.pool.QueryRow(bg, `select count(*) from `+table).Scan(&n)
		if n != 0 {
			t.Errorf("%s: %d rows left", table, n)
		}
	}
}

func TestTransferOwnership(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer"))
	wantErr(t, e.svc.TransferOwnership(bg, e.A, f, e.B.ID), 400, "Only an editor can become the owner.")
	wantErr(t, e.svc.TransferOwnership(bg, e.A, f, e.A.ID), 400, "Pick an editor to take over.")
	wantErr(t, e.svc.TransferOwnership(bg, e.A, f, "ghost"), 404, "That person is not in this fund.")
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "editor"))
	// Reminder opt-in stays with the member through a transfer.
	_ = e.svc.SetReminders(bg, e.B, f, true)
	if err := e.svc.TransferOwnership(bg, e.A, f, e.B.ID); err != nil {
		t.Fatal(err)
	}
	if len(e.mail.transfers) != 1 || e.mail.transfers[0].NewOwner.Email != "user-b@example.com" || e.mail.transfers[0].PreviousOwner.Email != "user-a@example.com" {
		t.Fatalf("transfer mail: %+v", e.mail.transfers)
	}
	if e.payload(e.B, f).Role != RoleOwner || e.payload(e.A, f).Role != RoleEditor {
		t.Fatal("roles not swapped")
	}
	if !e.payload(e.B, f).Fund.RemindersEnabled {
		t.Fatal("reminder opt-in lost in transfer")
	}
	// The new owner cannot become owner of an 11th fund.
	e.fundOf(e.B)
	for i := 1; i < MaxOwnedFunds-1; i++ {
		_, _ = e.svc.CreateFund(bg, e.B, "F")
	}
	f2, _ := e.svc.CreateFund(bg, e.A, "Another")
	_ = e.q.UpsertMember(bg, upsertMember(f2, e.B.ID, "editor"))
	_, _ = e.svc.CreateFund(bg, e.B, "Last")
	wantErr(t, e.svc.TransferOwnership(bg, e.A, f2, e.B.ID), 400, "They already own the maximum of 10 funds.")
}

func TestTransferEmailFailureReports502AfterMoving(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "editor"))
	e.mail.failTransfer = true
	wantErr(t, e.svc.TransferOwnership(bg, e.A, f, e.B.ID), 502, "Ownership moved but the emails could not be sent. Tell each other directly.")
	if e.payload(e.B, f).Role != RoleOwner {
		t.Fatal("ownership should have moved before the email failed")
	}
}

func TestInviteEmailFailureKeepsInvite(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	e.mail.failInvite = true
	wantErr(t, e.svc.InviteMember(bg, e.A, f, "x@example.com", "viewer"), 502, "The invite was saved but the email could not be sent. Try again.")
	if len(e.payload(e.A, f).Sharing.Invites) != 1 {
		t.Fatal("invite should have been saved")
	}
}

func TestInviteSignupCreatesAccountInOneStep(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	_ = e.svc.InviteMember(bg, e.A, f, "newbie@example.com", "editor")
	token := tokenFromURL(e.mail.lastInvite().Mail.URL)
	if info, _ := e.svc.ReadInvite(bg, token); info.Account != "none" {
		t.Fatalf("account state %q", info.Account)
	}
	wantErr(t, mustSignupErr(e, token, "N", "short"), 400, "Use at least 8 characters.")
	wantErr(t, mustSignupErr(e, token, " ", "longenough1"), 400, "Enter a name.")
	sess, fundID, err := e.svc.AcceptInviteSignup(bg, token, " Newbie ", "longenough1", "3.3.3.3", "ua")
	if err != nil || fundID != f {
		t.Fatalf("signup: %v %q", err, fundID)
	}
	if !sess.User.EmailVerified || !sess.User.JoinedViaInvite || sess.User.Name != "Newbie" || sess.Token == "" {
		t.Fatalf("session: %+v", sess)
	}
	if got, _ := e.auth.GetSession(bg, sess.Token); got == nil {
		t.Fatal("returned session is not valid")
	}
	if _, err := e.auth.SignIn(bg, "newbie@example.com", "longenough1", "", ""); err != nil {
		t.Fatalf("password not set: %v", err)
	}
	// No personal fund was created; the only fund is the shared one.
	funds, _ := e.q.ListFundsForUser(bg, sess.User.ID)
	if len(funds) != 1 || funds[0].ID != f || funds[0].Role != "editor" {
		t.Fatalf("funds: %+v", funds)
	}
	if info, _ := e.svc.ReadInvite(bg, token); info.State != "invalid" {
		t.Fatal("invite reusable")
	}
}

func mustSignupErr(e *env, token, name, pw string) error {
	_, _, err := e.svc.AcceptInviteSignup(bg, token, name, pw, "9.9.9.9", "")
	return err
}

func TestInviteSignupClaimsUnverifiedAndRefusesVerified(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)

	// An unverified account for the invited address is claimed.
	if _, err := e.auth.SignUp(bg, "Pending", "claim@example.com", "originalpass1", "", ""); err != nil {
		t.Fatal(err)
	}
	_ = e.svc.InviteMember(bg, e.A, f, "claim@example.com", "viewer")
	token := tokenFromURL(e.mail.lastInvite().Mail.URL)
	if info, _ := e.svc.ReadInvite(bg, token); info.Account != "unverified" {
		t.Fatalf("account state %q", info.Account)
	}
	sess, _, err := e.svc.AcceptInviteSignup(bg, token, "Claimed", "brandnewpass1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.User.EmailVerified || sess.User.Name != "Claimed" || !sess.User.JoinedViaInvite {
		t.Fatalf("claimed: %+v", sess.User)
	}
	if _, err := e.auth.SignIn(bg, "claim@example.com", "originalpass1", "", ""); status(err) != 401 {
		t.Fatal("old password still works after claim")
	}
	if _, err := e.auth.SignIn(bg, "claim@example.com", "brandnewpass1", "", ""); err != nil {
		t.Fatal(err)
	}

	// A verified account is told to sign in.
	_ = e.svc.InviteMember(bg, e.A, f, e.B.Email, "viewer")
	tok2 := tokenFromURL(e.mail.lastInvite().Mail.URL)
	_, _, err = e.svc.AcceptInviteSignup(bg, tok2, "B", "longenough1", "", "")
	wantErr(t, err, 409, "An account already exists for this email. Sign in to join.")
}

func TestRevokedExpiredInvitesCreateNothing(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	_ = e.svc.InviteMember(bg, e.A, f, "ghost@example.com", "viewer")
	token := tokenFromURL(e.mail.lastInvite().Mail.URL)
	var inviteID string
	_ = e.pool.QueryRow(bg, `select id from fund_invite`).Scan(&inviteID)
	if err := e.svc.RevokeInvite(bg, e.A, f, inviteID); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.svc.AcceptInviteSignup(bg, token, "Ghost", "longenough1", "", "")
	wantErr(t, err, 404, "That invite is no longer valid.")
	_, err = e.svc.AcceptInvite(bg, e.B, token, "")
	wantErr(t, err, 404, "That invite is no longer valid.")

	// Expired: no account is created either.
	_ = e.svc.InviteMember(bg, e.A, f, "late@example.com", "viewer")
	tok2 := tokenFromURL(e.mail.lastInvite().Mail.URL)
	_, _ = e.pool.Exec(bg, `update fund_invite set expires_at = now() - interval '1 day'`)
	_, _, err = e.svc.AcceptInviteSignup(bg, tok2, "Late", "longenough1", "", "")
	wantErr(t, err, 410, "That invite has expired. Ask for a new one.")
	var users int
	_ = e.pool.QueryRow(bg, `select count(*) from "user" where email in ('ghost@example.com','late@example.com')`).Scan(&users)
	if users != 0 {
		t.Fatalf("%d accounts created from dead invites", users)
	}
}

func TestInviteCapsAndCooldown(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	// Revoking starts a cooldown for that address.
	_ = e.svc.InviteMember(bg, e.A, f, "cool@example.com", "viewer")
	var inviteID string
	_ = e.pool.QueryRow(bg, `select id from fund_invite`).Scan(&inviteID)
	_ = e.svc.RevokeInvite(bg, e.A, f, inviteID)
	err := e.svc.InviteMember(bg, e.A, f, "cool@example.com", "viewer")
	wantErr(t, err, 429, "Wait about 60 minutes before inviting this address again.")

	// A re-invite of a live address is a resend and uses no daily slot.
	e2 := setup(t)
	f2 := e2.fundOf(e2.A)
	for i := 0; i < 3; i++ {
		if err := e2.svc.InviteMember(bg, e2.A, f2, "same@example.com", "viewer"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e2.payload(e2.A, f2).Sharing.Invites); n != 1 {
		t.Fatalf("%d invites for one address", n)
	}
	// The daily cap applies to new addresses (the member cap of 20 is hit first, so lift it).
	e3 := setup(t)
	f3 := e3.fundOf(e3.A)
	for i := 0; i < MaxInvitesDay; i++ {
		if err := e3.svc.InviteMember(bg, e3.A, f3, "p"+string(rune('a'+i))+"@example.com", "viewer"); err != nil {
			t.Fatalf("invite %d: %v", i, err)
		}
	}
	wantErr(t, e3.svc.InviteMember(bg, e3.A, f3, "extra@example.com", "viewer"), 400, "A fund can have up to 20 people.")
}

func TestResendRotatesToken(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	_ = e.svc.InviteMember(bg, e.A, f, "r@example.com", "viewer")
	old := tokenFromURL(e.mail.lastInvite().Mail.URL)
	var inviteID string
	_ = e.pool.QueryRow(bg, `select id from fund_invite`).Scan(&inviteID)
	if err := e.svc.ResendInvite(bg, e.A, f, inviteID); err != nil {
		t.Fatal(err)
	}
	fresh := tokenFromURL(e.mail.lastInvite().Mail.URL)
	if fresh == old {
		t.Fatal("token was not rotated")
	}
	if info, _ := e.svc.ReadInvite(bg, old); info.State != "invalid" {
		t.Fatal("old token still works")
	}
	if info, _ := e.svc.ReadInvite(bg, fresh); info.State != "ok" {
		t.Fatal("new token does not work")
	}
	_ = time.Second
}

func TestDeletingAUserRemovesTheirFund(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	it := e.addItem(e.A, f, "Laptop", 1000)
	e.mustContribute(e.A, f, ContributionInput{Date: "2026-10-01", Allocations: line(it.ID, 10)})
	if _, err := e.pool.Exec(bg, `insert into account(id,account_id,provider_id,user_id,password) values('a1','user-a','credential','user-a',$1)`, hashOf(t, "hunter2hunter2")); err != nil {
		t.Fatal(err)
	}
	if err := e.auth.DeleteAccount(bg, e.A, "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"fund", "item", "contribution", "allocation"} {
		var n int
		_ = e.pool.QueryRow(bg, `select count(*) from `+table).Scan(&n)
		if n != 0 {
			t.Errorf("%s: %d rows left", table, n)
		}
	}
}

func TestAccountDeletionBlockedWhileOwnedFundHasMembers(t *testing.T) {
	e := setup(t)
	f := e.fundOf(e.A)
	_ = e.q.UpsertMember(bg, upsertMember(f, e.B.ID, "viewer"))
	if _, err := e.pool.Exec(bg, `insert into account(id,account_id,provider_id,user_id,password) values('a1','user-a','credential','user-a',$1)`, hashOf(t, "hunter2hunter2")); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.auth.DeleteAccount(bg, e.A, "hunter2hunter2"), 409, "Transfer or delete “Fund” before deleting your account.")
	// Memberships on other people's funds do not block deletion.
	if err := e.svc.RemoveMember(bg, e.A, f, e.B.ID); err != nil {
		t.Fatal(err)
	}
	fb := e.fundOf(e.B)
	_ = e.q.UpsertMember(bg, upsertMember(fb, e.A.ID, "viewer"))
	_ = e.svc.DeleteFund(bg, e.A, f)
	if err := e.auth.DeleteAccount(bg, e.A, "hunter2hunter2"); err != nil {
		t.Fatalf("deletion should succeed: %v", err)
	}
}

var _ = dbgen.User{}
