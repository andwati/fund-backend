package mail

import (
	"context"

	"github.com/andwati/fund-backend/internal/fund"
)

// Mailer implements auth.Mailer and fund.Mailer on top of a Sender.
type Mailer struct {
	Sender Sender
	// HasAccount reports whether an address already has an account (changes the invite copy).
	HasAccount func(ctx context.Context, email string) bool
	// PublicURL is the client app base URL used for links in ownership emails.
	PublicURL string
}

func (m *Mailer) send(ctx context.Context, msg Message, err error) error {
	if err != nil {
		return err
	}
	return m.Sender.Send(ctx, msg)
}

func (m *Mailer) VerifyEmail(ctx context.Context, to, url string) error {
	msg, err := VerifyEmail(to, url)
	return m.send(ctx, msg, err)
}

func (m *Mailer) VerifyNewEmail(ctx context.Context, to, url string) error {
	msg, err := VerifyNewEmail(to, url)
	return m.send(ctx, msg, err)
}

func (m *Mailer) ChangeEmailConfirmation(ctx context.Context, oldEmail, newEmail, url string) error {
	msg, err := ChangeEmailConfirmation(oldEmail, newEmail, url)
	return m.send(ctx, msg, err)
}

func (m *Mailer) ResetPassword(ctx context.Context, to, url string) error {
	msg, err := ResetPassword(to, url)
	return m.send(ctx, msg, err)
}

func (m *Mailer) PasswordChanged(ctx context.Context, to string) error {
	msg, err := PasswordChanged(to)
	return m.send(ctx, msg, err)
}

func (m *Mailer) AccountDeleted(ctx context.Context, to string) error {
	msg, err := AccountDeleted(to)
	return m.send(ctx, msg, err)
}

func (m *Mailer) Invite(ctx context.Context, to string, im fund.InviteMail) error {
	has := false
	if m.HasAccount != nil {
		has = m.HasAccount(ctx, to)
	}
	msg, err := Invite(to, im, has)
	return m.send(ctx, msg, err)
}

func (m *Mailer) OwnershipTransfer(ctx context.Context, t fund.TransferMail) error {
	received, err := OwnershipReceived(t.NewOwner.Email, t.FundName, t.PreviousOwner.Name, m.PublicURL+"/settings")
	if err := m.send(ctx, received, err); err != nil {
		return err
	}
	handed, err := OwnershipHandedOff(t.PreviousOwner.Email, t.FundName, t.NewOwner.Name, m.PublicURL+"/")
	return m.send(ctx, handed, err)
}

// Reminder sends the payday reminder.
func (m *Mailer) Reminder(ctx context.Context, to string, d ReminderDetails) error {
	msg, err := PaydayReminder(to, d)
	return m.send(ctx, msg, err)
}
