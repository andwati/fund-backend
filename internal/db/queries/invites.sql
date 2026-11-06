-- name: ListInvites :many
SELECT id, email, role, expires_at, created_at FROM fund_invite
WHERE fund_id = $1 AND expires_at > now() ORDER BY created_at, id;

-- name: GetInviteByID :one
SELECT id, fund_id, email, role, token, invited_by, expires_at, created_at
FROM fund_invite WHERE id = $1 AND fund_id = $2;

-- name: GetInviteByToken :one
SELECT id, fund_id, email, role, token, invited_by, expires_at, created_at
FROM fund_invite WHERE token = $1;

-- name: GetInviteByEmail :one
SELECT id, fund_id, email, role, token, invited_by, expires_at, created_at
FROM fund_invite WHERE fund_id = $1 AND lower(email) = lower(sqlc.arg(email));

-- name: InsertInvite :exec
INSERT INTO fund_invite (id, fund_id, email, role, token, invited_by, expires_at)
VALUES ($1, $2, lower(sqlc.arg(email)), $3, $4, $5, $6);

-- name: RotateInvite :exec
UPDATE fund_invite SET token = $2, expires_at = $3, role = $4, invited_by = $5 WHERE id = $1;

-- name: DeleteInvite :exec
DELETE FROM fund_invite WHERE id = $1;

-- name: CountInvitesSentSince :one
SELECT count(*)::int FROM fund_invite WHERE invited_by = $1 AND created_at > $2;

-- name: GetInviteView :one
SELECT i.email, i.role, i.expires_at, f.name AS fund_name, u.name AS inviter
FROM fund_invite i
JOIN fund f ON f.id = i.fund_id
JOIN "user" u ON u.id = i.invited_by
WHERE i.token = $1;
