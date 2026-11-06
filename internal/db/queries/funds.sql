-- name: ListOwnedFunds :many
SELECT id, name, created_at FROM fund WHERE user_id = $1 ORDER BY created_at, id;

-- name: ListSharedFunds :many
SELECT f.id, f.name, m.role, f.created_at
FROM fund_member m JOIN fund f ON f.id = m.fund_id
WHERE m.user_id = $1 ORDER BY f.created_at, f.id;

-- name: CountOwnedFunds :one
SELECT count(*)::int FROM fund WHERE user_id = $1;

-- name: GetFund :one
SELECT id, user_id, name, pace, currency, timezone, timezone_confirmed,
       onboarding_completed, created_at
FROM fund WHERE id = $1;

-- name: LockFund :one
SELECT id FROM fund WHERE id = $1 FOR UPDATE;

-- name: InsertFund :exec
INSERT INTO fund (id, user_id, name, currency, timezone, onboarding_completed)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpdateFundSettings :exec
UPDATE fund
SET name = $2, pace = $3, currency = $4, timezone = $5,
    timezone_confirmed = $6, onboarding_completed = $7
WHERE id = $1;

-- name: SetFundOwner :exec
UPDATE fund SET user_id = $2 WHERE id = $1;

-- name: DeleteFund :exec
DELETE FROM fund WHERE id = $1;

-- name: GetMember :one
SELECT fund_id, user_id, role, created_at FROM fund_member WHERE fund_id = $1 AND user_id = $2;

-- name: ListMembers :many
SELECT m.user_id, u.name, m.role, m.created_at
FROM fund_member m JOIN "user" u ON u.id = m.user_id
WHERE m.fund_id = $1 ORDER BY m.created_at, m.user_id;

-- name: UpsertMember :exec
INSERT INTO fund_member (fund_id, user_id, role) VALUES ($1, $2, $3)
ON CONFLICT (fund_id, user_id) DO UPDATE SET role = EXCLUDED.role;

-- name: DeleteMember :exec
DELETE FROM fund_member WHERE fund_id = $1 AND user_id = $2;

-- name: SetMemberRole :execrows
UPDATE fund_member SET role = $3 WHERE fund_id = $1 AND user_id = $2;

-- name: CountMembersAndInvites :one
SELECT (
  (SELECT count(*) FROM fund_member fm WHERE fm.fund_id = sqlc.arg(fund_id))
  + (SELECT count(*) FROM fund_invite fi WHERE fi.fund_id = sqlc.arg(fund_id))
)::int;

-- name: ListPaydays :many
SELECT day FROM fund_payday WHERE fund_id = $1 ORDER BY day;

-- name: DeletePaydays :exec
DELETE FROM fund_payday WHERE fund_id = $1;

-- name: InsertPayday :exec
INSERT INTO fund_payday (fund_id, day) VALUES ($1, $2);

-- name: ListSkipsFrom :many
SELECT date::text AS date FROM payday_skip WHERE fund_id = $1 AND date >= sqlc.arg(from_date)::date ORDER BY date;

-- name: InsertSkip :exec
INSERT INTO payday_skip (fund_id, date) VALUES ($1, sqlc.arg(date)::date) ON CONFLICT DO NOTHING;

-- name: DeleteSkip :exec
DELETE FROM payday_skip WHERE fund_id = $1 AND date = sqlc.arg(date)::date;

-- name: DeleteSkipsBefore :exec
DELETE FROM payday_skip WHERE fund_id = $1 AND date < sqlc.arg(before)::date;

-- name: ListSkipDates :many
SELECT date::text AS date FROM payday_skip WHERE fund_id = $1 ORDER BY date;

-- name: ListAdjustments :many
SELECT date::text AS date, amount FROM payday_amount WHERE fund_id = $1 ORDER BY date;

-- name: UpsertAdjustment :exec
INSERT INTO payday_amount (fund_id, date, amount) VALUES ($1, sqlc.arg(date)::date, $2)
ON CONFLICT (fund_id, date) DO UPDATE SET amount = EXCLUDED.amount, updated_at = now();

-- name: DeleteAdjustment :exec
DELETE FROM payday_amount WHERE fund_id = $1 AND date = sqlc.arg(date)::date;

-- name: ListAdjustmentDates :many
SELECT date::text AS date FROM payday_amount WHERE fund_id = $1 ORDER BY date;

-- name: GetReminderPref :one
SELECT enabled, COALESCE(last_reminder_on::text, '') AS last_reminder_on
FROM fund_reminder_pref WHERE fund_id = $1 AND user_id = $2;

-- name: UpsertReminderPref :exec
INSERT INTO fund_reminder_pref (fund_id, user_id, enabled) VALUES ($1, $2, $3)
ON CONFLICT (fund_id, user_id) DO UPDATE SET enabled = EXCLUDED.enabled;

-- name: MarkReminderSent :exec
UPDATE fund_reminder_pref SET last_reminder_on = sqlc.arg(sent_on)::date
WHERE fund_id = $1 AND user_id = $2;

-- name: ListReminderRecipients :many
SELECT p.fund_id, p.user_id, COALESCE(p.last_reminder_on::text, '') AS last_reminder_on,
       u.email, u.name AS user_name,
       f.name AS fund_name, f.pace, f.currency, f.timezone
FROM fund_reminder_pref p
JOIN "user" u ON u.id = p.user_id
JOIN fund f ON f.id = p.fund_id
WHERE p.enabled AND u.email_verified;

-- name: ListOwnedFundsWithMemberCount :many
SELECT f.id, f.name,
       (SELECT count(*) FROM fund_member fm WHERE fm.fund_id = f.id)::int AS member_count
FROM fund f WHERE f.user_id = $1 ORDER BY f.created_at, f.id;
