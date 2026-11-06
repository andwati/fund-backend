-- name: GetUserByEmail :one
SELECT * FROM "user" WHERE lower(email) = lower(sqlc.arg(email));

-- name: GetUserByID :one
SELECT * FROM "user" WHERE id = $1;

-- name: CreateUser :one
INSERT INTO "user" (id, name, email, email_verified, joined_via_invite)
VALUES ($1, $2, lower(sqlc.arg(email)), $3, $4)
RETURNING *;

-- name: UpdateUserName :exec
UPDATE "user" SET name = $2, updated_at = now() WHERE id = $1;

-- name: UpdateUserImage :exec
UPDATE "user" SET image = $2, updated_at = now() WHERE id = $1;

-- name: SetUserEmailVerified :exec
UPDATE "user" SET email_verified = true, updated_at = now() WHERE id = $1;

-- name: UpdateUserEmail :exec
UPDATE "user" SET email = lower(sqlc.arg(email)), email_verified = true, updated_at = now() WHERE id = $1;

-- name: ClaimUnverifiedUser :exec
UPDATE "user"
SET name = $2, email_verified = true, joined_via_invite = true, updated_at = now()
WHERE id = $1;

-- name: DeleteUser :exec
DELETE FROM "user" WHERE id = $1;

-- name: GetCredentialAccount :one
SELECT * FROM account WHERE user_id = $1 AND provider_id = 'credential';

-- name: CreateCredentialAccount :exec
INSERT INTO account (id, account_id, provider_id, user_id, password)
VALUES ($1, $2, 'credential', $2, $3);

-- name: UpdateCredentialPassword :exec
UPDATE account SET password = $2, updated_at = now()
WHERE user_id = $1 AND provider_id = 'credential';

-- name: CreateSession :one
INSERT INTO session (id, token, user_id, expires_at, ip_address, user_agent)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSessionByToken :one
SELECT s.id AS session_id, s.token, s.expires_at, s.updated_at AS session_updated_at,
       u.id AS user_id, u.name, u.email, u.email_verified, u.image,
       u.joined_via_invite, u.created_at AS user_created_at
FROM session s JOIN "user" u ON u.id = s.user_id
WHERE s.token = $1 AND s.expires_at > now();

-- name: TouchSession :exec
UPDATE session SET expires_at = $2, updated_at = now() WHERE id = $1;

-- name: ListSessions :many
SELECT id, token, created_at, updated_at, expires_at, ip_address, user_agent
FROM session WHERE user_id = $1 AND expires_at > now() ORDER BY created_at DESC;

-- name: DeleteSessionByToken :exec
DELETE FROM session WHERE token = $1;

-- name: DeleteSessionByID :exec
DELETE FROM session WHERE id = $1 AND user_id = $2;

-- name: DeleteOtherSessions :exec
DELETE FROM session WHERE user_id = $1 AND token <> $2;

-- name: DeleteSessionsForUser :exec
DELETE FROM session WHERE user_id = $1;

-- name: CreateVerification :exec
INSERT INTO verification (id, identifier, value, expires_at) VALUES ($1, $2, $3, $4);

-- name: GetVerification :one
SELECT * FROM verification WHERE identifier = $1 AND expires_at > now()
ORDER BY created_at DESC LIMIT 1;

-- name: DeleteVerification :exec
DELETE FROM verification WHERE identifier = $1;

-- name: DeleteExpiredVerifications :exec
DELETE FROM verification WHERE expires_at <= now();
