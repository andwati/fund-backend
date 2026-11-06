// Package fund is the transactional core: access control, the fund payload, plan inputs
// (items, contributions, paydays), sharing and invites. It ports ~/dev/fund/src/server/*.
package fund

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/ratelimit"
)

const (
	// MaxAmount keeps money inside Postgres integer.
	MaxAmount       = 1_000_000_000
	MaxOwnedFunds   = 10
	MaxMembers      = 20
	MaxPaydays      = 4
	MaxInvitesDay   = 20
	InviteDays      = 7
	NameMax         = 80
	NoteMax         = 80
	ImportRowsMax   = 2000
	InviteCooldown  = time.Hour
	acceptIPLimit   = 20
	acceptTokenLim  = 10
	acceptWindow    = 15 * time.Minute
	defaultFundName = "Fund"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// Need is what an action requires of the caller.
type Need int

const (
	NeedView Need = iota + 1
	NeedEdit
	NeedOwner
)

func rank(r Role) int {
	switch r {
	case RoleOwner:
		return 3
	case RoleEditor:
		return 2
	default:
		return 1
	}
}

func allows(r Role, n Need) bool { return rank(r) >= int(n) }

// InviteMail is the data for an invite email.
type InviteMail struct {
	FundName string
	Inviter  string
	Role     string
	URL      string
}

// TransferMail is the data for the two ownership-transfer emails.
type TransferMail struct {
	FundID        string
	FundName      string
	PreviousOwner Person
	NewOwner      Person
}

type Person struct{ Name, Email string }

// Mailer sends the sharing emails.
type Mailer interface {
	Invite(ctx context.Context, to string, m InviteMail) error
	OwnershipTransfer(ctx context.Context, m TransferMail) error
}

type Service struct {
	pool      *pgxpool.Pool
	q         *dbgen.Queries
	auth      *auth.Service
	mail      Mailer
	limiter   *ratelimit.Limiter
	publicURL string
	now       func() time.Time
	log       *slog.Logger
}

type Options struct {
	Pool      *pgxpool.Pool
	Auth      *auth.Service
	Mailer    Mailer
	Limiter   *ratelimit.Limiter
	PublicURL string
	Logger    *slog.Logger
	Now       func() time.Time
}

func NewService(o Options) *Service {
	s := &Service{pool: o.Pool, q: dbgen.New(o.Pool), auth: o.Auth, mail: o.Mailer, limiter: o.Limiter,
		publicURL: trimSlash(o.PublicURL), now: o.Now, log: o.Logger}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.limiter == nil {
		s.limiter = ratelimit.New()
	}
	return s
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// Payload is what the clients render: the same shape as the web app's FundPayload.
type Payload struct {
	Role            Role               `json:"role"`
	Funds           []FundSummary      `json:"funds"`
	Sharing         *Sharing           `json:"sharing"`
	Fund            FundView           `json:"fund"`
	NeedsOnboarding bool               `json:"needsOnboarding"`
	Skips           []string           `json:"skips"`
	Adjustments     []AdjustmentView   `json:"adjustments"`
	Items           []ItemView         `json:"items"`
	Contributions   []ContributionView `json:"contributions"`
}

type FundSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role Role   `json:"role"`
}

type FundView struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Paydays             []int  `json:"paydays"`
	Pace                *int   `json:"pace"`
	Currency            string `json:"currency"`
	Timezone            string `json:"timezone"`
	TimezoneConfirmed   bool   `json:"timezoneConfirmed"`
	OnboardingCompleted bool   `json:"onboardingCompleted"`
	RemindersEnabled    bool   `json:"remindersEnabled"`
}

type AdjustmentView struct {
	Date   string `json:"date"`
	Amount int    `json:"amount"`
}

type ItemView struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Price       int     `json:"price"`
	Deadline    string  `json:"deadline"`
	PlanStart   string  `json:"planStart"`
	Position    int     `json:"position"`
	Archived    bool    `json:"archived"`
	ActualPrice *int    `json:"actualPrice"`
	PurchasedOn *string `json:"purchasedOn"`
}

type AllocationView struct {
	ID     string `json:"id"`
	ItemID string `json:"itemId"`
	Amount int    `json:"amount"`
}

type ContributionView struct {
	ID          string           `json:"id"`
	Date        string           `json:"date"`
	Note        string           `json:"note"`
	Kind        string           `json:"kind"`
	Allocations []AllocationView `json:"allocations"`
}

type Sharing struct {
	Members []MemberView `json:"members"`
	Invites []InviteView `json:"invites"`
}

type MemberView struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type InviteView struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expiresAt"`
}
