// Package auth implements email+password accounts on the existing user/account/session/
// verification tables, compatible with the rows better-auth wrote.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andwati/fund-backend/internal/apperr"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/id"
)

const (
	sessionTTL       = 7 * 24 * time.Hour // better-auth default
	sessionUpdateAge = 24 * time.Hour     // sliding refresh
	linkTTL          = time.Hour          // verification and reset links
	minPassword      = 8
	maxPassword      = 128
	maxName          = 80
)

// Mailer sends the account emails. Failures never fail the request that triggered them.
type Mailer interface {
	VerifyEmail(ctx context.Context, to, url string) error
	VerifyNewEmail(ctx context.Context, to, url string) error
	ChangeEmailConfirmation(ctx context.Context, oldEmail, newEmail, url string) error
	ResetPassword(ctx context.Context, to, url string) error
	PasswordChanged(ctx context.Context, to string) error
	AccountDeleted(ctx context.Context, to string) error
}

// BeforeDelete runs inside the deletion transaction, before the user row goes away. The funds
// layer uses it to refuse deletion while owned funds have members and to purge owned funds.
type BeforeDelete func(ctx context.Context, tx pgx.Tx, userID string) error

// User is the public shape of an account.
type User struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Email           string    `json:"email"`
	EmailVerified   bool      `json:"emailVerified"`
	Image           *string   `json:"image"`
	JoinedViaInvite bool      `json:"joinedViaInvite"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Session is a signed-in session with its user.
type Session struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	User      User      `json:"user"`
}

type Service struct {
	pool         *pgxpool.Pool
	q            *dbgen.Queries
	mail         Mailer
	secret       []byte
	publicURL    string
	beforeDelete BeforeDelete
	now          func() time.Time
	log          *slog.Logger
}

type Options struct {
	Pool         *pgxpool.Pool
	Mailer       Mailer
	Secret       string
	PublicURL    string
	BeforeDelete BeforeDelete
	Logger       *slog.Logger
	Now          func() time.Time
}

func NewService(o Options) *Service {
	s := &Service{
		pool: o.Pool, q: dbgen.New(o.Pool), mail: o.Mailer, secret: []byte(o.Secret),
		publicURL: strings.TrimRight(o.PublicURL, "/"), beforeDelete: o.BeforeDelete,
		now: o.Now, log: o.Logger,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s
}

var emailRe = regexp.MustCompile(`^[A-Za-z0-9_'+\-.]*[A-Za-z0-9_+-]@([A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$`)

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func validEmail(e string) bool {
	if len(e) > 254 || strings.Contains(e, "..") || strings.HasPrefix(e, ".") {
		return false
	}
	if _, err := mail.ParseAddress(e); err != nil {
		return false
	}
	return emailRe.MatchString(e)
}

func checkPassword(p string) error {
	n := utf8.RuneCountInString(p)
	if n < minPassword {
		return apperr.Newf(400, "Password must be at least %d characters.", minPassword)
	}
	if n > maxPassword {
		return apperr.Newf(400, "Password must be at most %d characters.", maxPassword)
	}
	return nil
}

func toUser(u dbgen.User) User {
	return User{ID: u.ID, Name: u.Name, Email: u.Email, EmailVerified: u.EmailVerified, Image: u.Image,
		JoinedViaInvite: u.JoinedViaInvite, CreatedAt: u.CreatedAt.Time}
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func (s *Service) newSession(ctx context.Context, q *dbgen.Queries, userID, ip, ua string) (dbgen.Session, error) {
	var ipp, uap *string
	if ip != "" {
		ipp = &ip
	}
	if ua != "" {
		uap = &ua
	}
	return q.CreateSession(ctx, dbgen.CreateSessionParams{
		ID: id.New(), Token: id.Token(32), UserID: userID, IpAddress: ipp, UserAgent: uap,
		ExpiresAt: ts(s.now().Add(sessionTTL)),
	})
}

// SignUp creates the account and its first session, then sends the verification email.
func (s *Service) SignUp(ctx context.Context, name, email, password, ip, ua string) (Session, error) {
	name = strings.TrimSpace(name)
	email = normalizeEmail(email)
	if name == "" || utf8.RuneCountInString(name) > maxName {
		return Session{}, apperr.New(400, "Enter your name (up to 80 characters).")
	}
	if !validEmail(email) {
		return Session{}, apperr.New(400, "Enter a valid email address.")
	}
	if err := checkPassword(password); err != nil {
		return Session{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Session{}, err
	}

	var user dbgen.User
	var sess dbgen.Session
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.GetUserByEmail(ctx, email); err == nil {
			return apperr.New(422, "User already exists. Use another email.")
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		uid := id.New()
		u, err := q.CreateUser(ctx, dbgen.CreateUserParams{ID: uid, Name: name, Email: email})
		if err != nil {
			if isUnique(err) {
				return apperr.New(422, "User already exists. Use another email.")
			}
			return err
		}
		if err := q.CreateCredentialAccount(ctx, dbgen.CreateCredentialAccountParams{ID: id.New(), AccountID: uid, Password: &hash}); err != nil {
			return err
		}
		user = u
		sess, err = s.newSession(ctx, q, uid, ip, ua)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	s.sendVerification(ctx, user.Email)
	return s.toSession(sess, user), nil
}

func (s *Service) toSession(sess dbgen.Session, u dbgen.User) Session {
	return Session{ID: sess.ID, Token: sess.Token, ExpiresAt: sess.ExpiresAt.Time, User: toUser(u)}
}

// SignIn checks the password and starts a session.
func (s *Service) SignIn(ctx context.Context, email, password, ip, ua string) (Session, error) {
	email = normalizeEmail(email)
	bad := apperr.New(401, "Invalid email or password.")
	user, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		VerifyPassword(password, dummyHash) // equalise timing
		return Session{}, bad
	}
	if err != nil {
		return Session{}, err
	}
	acct, err := s.q.GetCredentialAccount(ctx, user.ID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && acct.Password == nil) {
		VerifyPassword(password, dummyHash)
		return Session{}, bad
	}
	if err != nil {
		return Session{}, err
	}
	if !VerifyPassword(password, *acct.Password) {
		return Session{}, bad
	}
	sess, err := s.newSession(ctx, s.q, user.ID, ip, ua)
	if err != nil {
		return Session{}, err
	}
	return s.toSession(sess, user), nil
}

// GetSession resolves a bearer token. It refreshes the expiry once the session is older than
// the update age (better-auth's sliding window). Returns nil when the token is unknown or expired.
func (s *Service) GetSession(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, nil
	}
	row, err := s.q.GetSessionByToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	expires := row.ExpiresAt.Time
	now := s.now()
	if !expires.Add(-sessionTTL).Add(sessionUpdateAge).After(now) {
		expires = now.Add(sessionTTL)
		if err := s.q.TouchSession(ctx, dbgen.TouchSessionParams{ID: row.SessionID, ExpiresAt: ts(expires)}); err != nil {
			return nil, err
		}
	}
	return &Session{
		ID: row.SessionID, Token: row.Token, ExpiresAt: expires,
		User: User{ID: row.UserID, Name: row.Name, Email: row.Email, EmailVerified: row.EmailVerified,
			Image: row.Image, JoinedViaInvite: row.JoinedViaInvite, CreatedAt: row.UserCreatedAt.Time},
	}, nil
}

func (s *Service) SignOut(ctx context.Context, token string) error {
	return s.q.DeleteSessionByToken(ctx, token)
}

type SessionInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	IPAddress *string   `json:"ipAddress"`
	UserAgent *string   `json:"userAgent"`
	Current   bool      `json:"current"`
}

func (s *Service) ListSessions(ctx context.Context, userID, currentToken string) ([]SessionInfo, error) {
	rows, err := s.q.ListSessions(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, SessionInfo{ID: r.ID, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
			ExpiresAt: r.ExpiresAt.Time, IPAddress: r.IpAddress, UserAgent: r.UserAgent, Current: r.Token == currentToken})
	}
	return out, nil
}

func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	return s.q.DeleteSessionByID(ctx, dbgen.DeleteSessionByIDParams{ID: sessionID, UserID: userID})
}

func (s *Service) RevokeOtherSessions(ctx context.Context, userID, currentToken string) error {
	return s.q.DeleteOtherSessions(ctx, dbgen.DeleteOtherSessionsParams{UserID: userID, Token: currentToken})
}

// link builds a client link; the app routes handle the token.
func (s *Service) link(path, token string) string {
	return s.publicURL + path + "?token=" + url.QueryEscape(token)
}

func (s *Service) emailToken(email, updateTo, requestType string) string {
	now := s.now()
	return signJWT(s.secret, claims{Email: email, UpdateTo: updateTo, RequestType: requestType,
		Iat: now.Unix(), Exp: now.Add(linkTTL).Unix()})
}

func (s *Service) sendVerification(ctx context.Context, email string) {
	if err := s.mail.VerifyEmail(ctx, email, s.link("/verify-email", s.emailToken(email, "", ""))); err != nil {
		s.log.Warn("verification email failed", "err", err)
	}
}

// SendVerificationEmail re-sends the link. It answers the same whether or not the address
// exists or is already verified, so it cannot be used to probe accounts.
func (s *Service) SendVerificationEmail(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	u, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !u.EmailVerified {
		s.sendVerification(ctx, u.Email)
	}
	return nil
}

// VerifyEmailResult says what a link did. Session is set when a normal verification signs in.
type VerifyEmailResult struct {
	Status  string // "verified" | "email-changed" | "confirmation-sent"
	Session *Session
	User    User
}

// VerifyEmail redeems a link token: normal verification (and auto sign-in), the confirmation
// step of an email change, or the final verification of the new address.
func (s *Service) VerifyEmail(ctx context.Context, token, ip, ua string) (VerifyEmailResult, error) {
	bad := apperr.New(400, "That link is invalid or has expired.")
	c, err := parseJWT(s.secret, token, s.now())
	if err != nil {
		return VerifyEmailResult{}, bad
	}
	user, err := s.q.GetUserByEmail(ctx, c.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return VerifyEmailResult{}, bad
	}
	if err != nil {
		return VerifyEmailResult{}, err
	}

	switch c.RequestType {
	case reqChangeEmailConfirm:
		// Approved from the old address: now verify the new one.
		newEmail := normalizeEmail(c.UpdateTo)
		if _, err := s.q.GetUserByEmail(ctx, newEmail); err == nil {
			return VerifyEmailResult{}, apperr.New(422, "That email address is already in use.")
		}
		verifyTok := s.emailToken(user.Email, newEmail, reqChangeEmailVerify)
		if err := s.mail.VerifyNewEmail(ctx, newEmail, s.link("/verify-email", verifyTok)); err != nil {
			s.log.Warn("verify-new-email failed", "err", err)
		}
		return VerifyEmailResult{Status: "confirmation-sent", User: toUser(user)}, nil
	case reqChangeEmailVerify:
		newEmail := normalizeEmail(c.UpdateTo)
		if !validEmail(newEmail) {
			return VerifyEmailResult{}, bad
		}
		if err := s.q.UpdateUserEmail(ctx, dbgen.UpdateUserEmailParams{ID: user.ID, Email: newEmail}); err != nil {
			if isUnique(err) {
				return VerifyEmailResult{}, apperr.New(422, "That email address is already in use.")
			}
			return VerifyEmailResult{}, err
		}
		user.Email, user.EmailVerified = newEmail, true
		return VerifyEmailResult{Status: "email-changed", User: toUser(user)}, nil
	}

	if !user.EmailVerified {
		if err := s.q.SetUserEmailVerified(ctx, user.ID); err != nil {
			return VerifyEmailResult{}, err
		}
		user.EmailVerified = true
	}
	sess, err := s.newSession(ctx, s.q, user.ID, ip, ua)
	if err != nil {
		return VerifyEmailResult{}, err
	}
	out := s.toSession(sess, user)
	return VerifyEmailResult{Status: "verified", Session: &out, User: toUser(user)}, nil
}

// RequestPasswordReset always succeeds from the caller's point of view.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	u, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	token := id.Token(24)
	if err := s.q.CreateVerification(ctx, dbgen.CreateVerificationParams{
		ID: id.New(), Identifier: "reset-password:" + token, Value: u.ID, ExpiresAt: ts(s.now().Add(linkTTL)),
	}); err != nil {
		return err
	}
	if err := s.mail.ResetPassword(ctx, u.Email, s.link("/reset-password", token)); err != nil {
		s.log.Warn("reset email failed", "err", err)
	}
	return nil
}

// ResetPassword redeems a reset token and sets the new password.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := checkPassword(newPassword); err != nil {
		return err
	}
	bad := apperr.New(400, "That reset link is invalid or has expired.")
	ident := "reset-password:" + token
	v, err := s.q.GetVerification(ctx, ident)
	if errors.Is(err, pgx.ErrNoRows) {
		return bad
	}
	if err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.DeleteVerification(ctx, ident); err != nil {
			return err
		}
		return s.setPassword(ctx, q, v.Value, hash)
	})
}

// setPassword updates the credential row, creating it for accounts that have none.
func (s *Service) setPassword(ctx context.Context, q *dbgen.Queries, userID, hash string) error {
	if _, err := q.GetCredentialAccount(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
		return q.CreateCredentialAccount(ctx, dbgen.CreateCredentialAccountParams{ID: id.New(), AccountID: userID, Password: &hash})
	} else if err != nil {
		return err
	}
	return q.UpdateCredentialPassword(ctx, dbgen.UpdateCredentialPasswordParams{UserID: userID, Password: &hash})
}

func (s *Service) checkCurrentPassword(ctx context.Context, userID, password string) error {
	acct, err := s.q.GetCredentialAccount(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && acct.Password == nil) {
		return apperr.New(400, "Invalid password.")
	}
	if err != nil {
		return err
	}
	if !VerifyPassword(password, *acct.Password) {
		return apperr.New(400, "Invalid password.")
	}
	return nil
}

// ChangePassword checks the current password, sets the new one and notifies by email.
func (s *Service) ChangePassword(ctx context.Context, user User, currentToken, current, next string, revokeOthers bool) error {
	if err := checkPassword(next); err != nil {
		return err
	}
	if err := s.checkCurrentPassword(ctx, user.ID, current); err != nil {
		return err
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := s.setPassword(ctx, q, user.ID, hash); err != nil {
			return err
		}
		if revokeOthers {
			return q.DeleteOtherSessions(ctx, dbgen.DeleteOtherSessionsParams{UserID: user.ID, Token: currentToken})
		}
		return nil
	}); err != nil {
		return err
	}
	if err := s.mail.PasswordChanged(ctx, user.Email); err != nil {
		s.log.Warn("password-changed email failed", "err", err)
	}
	return nil
}

// ChangeEmail starts an email change. A verified account first approves from the current address;
// an unverified one goes straight to verifying the new address.
func (s *Service) ChangeEmail(ctx context.Context, user User, newEmail string) error {
	newEmail = normalizeEmail(newEmail)
	if !validEmail(newEmail) {
		return apperr.New(400, "Enter a valid email address.")
	}
	if newEmail == user.Email {
		return apperr.New(400, "That is already your email address.")
	}
	if _, err := s.q.GetUserByEmail(ctx, newEmail); err == nil {
		return apperr.New(422, "That email address is already in use.")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if user.EmailVerified {
		tok := s.emailToken(user.Email, newEmail, reqChangeEmailConfirm)
		if err := s.mail.ChangeEmailConfirmation(ctx, user.Email, newEmail, s.link("/verify-email", tok)); err != nil {
			s.log.Warn("change-email confirmation failed", "err", err)
		}
		return nil
	}
	tok := s.emailToken(user.Email, newEmail, reqChangeEmailVerify)
	if err := s.mail.VerifyNewEmail(ctx, newEmail, s.link("/verify-email", tok)); err != nil {
		s.log.Warn("verify-new-email failed", "err", err)
	}
	return nil
}

// UpdateProfile changes the display name (and image when given).
func (s *Service) UpdateProfile(ctx context.Context, userID string, name *string, image *string) (User, error) {
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || utf8.RuneCountInString(n) > maxName {
			return User{}, apperr.New(400, "Enter your name (up to 80 characters).")
		}
		if err := s.q.UpdateUserName(ctx, dbgen.UpdateUserNameParams{ID: userID, Name: n}); err != nil {
			return User{}, err
		}
	}
	if image != nil {
		if err := s.q.UpdateUserImage(ctx, dbgen.UpdateUserImageParams{ID: userID, Image: image}); err != nil {
			return User{}, err
		}
	}
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return User{}, err
	}
	return toUser(u), nil
}

// DeleteAccount verifies the password, lets the funds layer clean up, then removes the user
// (sessions, credentials and memberships cascade).
func (s *Service) DeleteAccount(ctx context.Context, user User, password string) error {
	if err := s.checkCurrentPassword(ctx, user.ID, password); err != nil {
		return err
	}
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if s.beforeDelete != nil {
			if err := s.beforeDelete(ctx, tx, user.ID); err != nil {
				return err
			}
		}
		return s.q.WithTx(tx).DeleteUser(ctx, user.ID)
	}); err != nil {
		return err
	}
	if err := s.mail.AccountDeleted(ctx, user.Email); err != nil {
		s.log.Warn("account-deleted email failed", "err", err)
	}
	return nil
}

// StartSessionTx starts a session for an existing user inside the caller's transaction.
// The funds layer uses it so invite sign-up is one atomic step (no half-created accounts).
func (s *Service) StartSessionTx(ctx context.Context, tx pgx.Tx, userID, ip, ua string) (Session, error) {
	q := s.q.WithTx(tx)
	u, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return Session{}, err
	}
	sess, err := s.newSession(ctx, q, userID, ip, ua)
	if err != nil {
		return Session{}, err
	}
	return s.toSession(sess, u), nil
}

// NormalizeEmail lower-cases and trims an address.
func NormalizeEmail(e string) string { return normalizeEmail(e) }

// ValidEmail reports whether e looks like a deliverable address.
func ValidEmail(e string) bool { return validEmail(e) }

// CheckPassword applies the password length rules (8 to 128 characters).
func CheckPassword(p string) error { return checkPassword(p) }
