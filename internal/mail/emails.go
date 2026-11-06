package mail

import (
	"context"
	"fmt"
	"strings"

	"github.com/andwati/fund-backend/internal/fund"
)

// Message is a rendered email ready to send.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
	Headers map[string]string
}

func build(to, subject string, c Content, headers map[string]string) (Message, error) {
	h, t, err := Render(c)
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: subject, HTML: h, Text: t, Headers: headers}, nil
}

func pasteLink(url string) Para {
	return P(T("If the button does not work, paste this link into your browser: " + url))
}

func VerifyEmail(to, url string) (Message, error) {
	return build(to, "Verify your email for Fund", Content{
		Preview: "Confirm this address to open your fund.", Kicker: "Fund", Heading: "Verify your email",
		Paragraphs: []Para{P(T("Confirm this address so you can sign in to Fund. This link expires in 1 hour."))},
		Button:     "Verify email", URL: url,
		Footnotes: []Para{pasteLink(url), P(T("If you did not create a Fund account, you can ignore this email."))},
	}, nil)
}

func VerifyNewEmail(to, url string) (Message, error) {
	return build(to, "Verify your new email for Fund", Content{
		Preview: "Confirm your new Fund sign-in email.", Kicker: "Fund", Heading: "Verify your new email",
		Paragraphs: []Para{P(T("Confirm this address to finish updating your Fund sign-in. This link expires in 1 hour."))},
		Button:     "Verify new email", URL: url,
		Footnotes: []Para{pasteLink(url), P(T("If you did not request this change, you can ignore this email."))},
	}, nil)
}

func ChangeEmailConfirmation(to, newEmail, url string) (Message, error) {
	return build(to, "Approve your Fund email change", Content{
		Preview: "Approve changing your Fund sign-in email.", Kicker: "Fund", Heading: "Approve email change",
		Paragraphs: []Para{P(T("Someone requested to change the email on your Fund account to "), B(newEmail),
			T(". If this was you, confirm to send a verification link to that address. This step expires in 1 hour."))},
		Button: "Approve change", URL: url,
		Footnotes: []Para{pasteLink(url), P(T("If you did not request this, ignore this email. Your sign-in email will stay the same."))},
	}, nil)
}

func ResetPassword(to, url string) (Message, error) {
	return build(to, "Reset your password for Fund", Content{
		Preview: "This link expires in 1 hour.", Kicker: "Fund", Heading: "Reset your password",
		Paragraphs: []Para{P(T("A password reset was requested for " + to + ". This link expires in 1 hour and works once."))},
		Button:     "Reset password", URL: url, Rule: true,
		Footnotes: []Para{
			P(T("If you did not ask for this, you can ignore this email. Your password will stay the same.")),
			pasteLink(url),
		},
	}, nil)
}

func PasswordChanged(to string) (Message, error) {
	return build(to, "Your Fund password was changed", Content{
		Preview: "Your Fund password was changed.", Kicker: "Fund", Heading: "Your password was changed",
		Paragraphs: []Para{P(T("The password for " + to + " was just updated. Other devices were signed out, except the one where you made this change."))},
		Rule:       true,
		Footnotes:  []Para{P(T("If you did not change your password, sign in and change it again right away, or contact support if you cannot get in."))},
	}, nil)
}

func AccountDeleted(to string) (Message, error) {
	return build(to, "Your Fund account was deleted", Content{
		Preview: "Your Fund account was deleted.", Heading: "Account deleted",
		Paragraphs: []Para{
			P(T("This confirms that the Fund account for "), B(to), T(" and its data were permanently removed.")),
			P(T("If you did not request this, contact support right away.")),
		},
		Rule: true, Footnotes: []Para{P(T("Fund"))},
	}, nil)
}

func inviteSubject(fundName string, hasAccount bool) string {
	if hasAccount {
		return "Sign in to join " + fundName
	}
	return "Create your account to join " + fundName
}

func roleLabel(role string) string {
	if role == "editor" {
		return "an editor"
	}
	return "a viewer"
}

func Invite(to string, m fund.InviteMail, hasAccount bool) (Message, error) {
	subject := inviteSubject(m.FundName, hasAccount)
	action, verb := "Create your account", "create your account and"
	if hasAccount {
		action, verb = "Sign in", "sign in and"
	}
	return build(to, subject, Content{
		Preview: subject, Kicker: "Fund", Heading: subject,
		Paragraphs: []Para{P(T(fmt.Sprintf("%s invited you to join %s as %s. Use this email address to %s accept the invite. The link expires in 7 days.",
			m.Inviter, m.FundName, roleLabel(m.Role), verb)))},
		Button: action, URL: m.URL,
		Footnotes: []Para{pasteLink(m.URL), P(T("If you were not expecting this, you can ignore the email."))},
	}, nil)
}

func OwnershipReceived(to, fundName, previousOwner, url string) (Message, error) {
	return build(to, "You are now the owner of "+fundName, Content{
		Preview: "You are now the owner of " + fundName + ".", Kicker: "Fund", Heading: "You own " + fundName + " now",
		Paragraphs: []Para{P(T(previousOwner + " transferred ownership of this fund to you. You can change settings, invite people, and delete the fund."))},
		Button:     "Open settings", URL: url, Footnotes: []Para{pasteLink(url)},
	}, nil)
}

func OwnershipHandedOff(to, fundName, newOwner, url string) (Message, error) {
	return build(to, newOwner+" owns "+fundName+" now", Content{
		Preview: newOwner + " is the owner of " + fundName + " now.", Kicker: "Fund", Heading: "Ownership transferred",
		Paragraphs: []Para{P(T(newOwner + " is now the owner of " + fundName + ". You are an editor and can still add and change items and contributions."))},
		Button:     "Open the fund", URL: url, Footnotes: []Para{pasteLink(url)},
	}, nil)
}

// ReminderDetails feeds the payday reminder.
type ReminderDetails struct {
	FundName       string
	Payday         string // e.g. "Fri 31 Oct"
	Amount         string // e.g. "KES 12,000"
	URL            string
	UnsubscribeURL string
}

func PaydayReminder(to string, d ReminderDetails) (Message, error) {
	return build(to, "Payday tomorrow: set aside "+d.Amount, Content{
		Preview: "Payday is " + d.Payday + ". Your plan needs about " + d.Amount + ".", Kicker: d.FundName, Heading: "Payday is tomorrow",
		Paragraphs: []Para{P(T(d.Payday + " is your next payday. To stay on plan, set aside about " + d.Amount + "."))},
		Button:     "Log your contribution", URL: d.URL,
		Footnotes: []Para{P(T("You get this because reminders are on."))},
		FootLabel: "Unsubscribe", FootURL: d.UnsubscribeURL,
	}, map[string]string{
		"List-Unsubscribe":      "<" + d.UnsubscribeURL + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	})
}

var _ = context.Background
var _ = strings.TrimSpace
