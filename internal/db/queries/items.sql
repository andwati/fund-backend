-- name: ListItems :many
SELECT id, name, price, deadline::text AS deadline, plan_start::text AS plan_start,
       "position", archived_at, actual_price, COALESCE(purchased_on::text, '')::text AS purchased_on, created_at
FROM item WHERE fund_id = $1 ORDER BY "position", created_at, id;

-- name: GetItem :one
SELECT id, fund_id, name, price, deadline::text AS deadline, plan_start::text AS plan_start,
       "position", archived_at, actual_price, COALESCE(purchased_on::text, '')::text AS purchased_on, created_at
FROM item WHERE id = $1 AND fund_id = $2;

-- name: MaxItemPosition :one
-- Web parity: new position = max(0, max over ALL items) + 1, so the first item gets 1.
SELECT COALESCE(max("position"), 0)::int FROM item WHERE fund_id = $1;

-- name: ListActiveItemIDs :many
SELECT id FROM item WHERE fund_id = $1 AND archived_at IS NULL ORDER BY "position", created_at, id;

-- name: UpdateItemsPlanStart :exec
-- A new payday schedule applies from today: active items start their plan now.
UPDATE item SET plan_start = sqlc.arg(today)::date
WHERE fund_id = $1 AND archived_at IS NULL
  AND plan_start < sqlc.arg(today)::date AND deadline >= sqlc.arg(today)::date;

-- name: ListLedgerLinesForItems :many
SELECT c.id AS contribution_id, a.item_id, c.date::text AS date, c.kind, a.amount, c.created_at
FROM allocation a JOIN contribution c ON c.id = a.contribution_id
WHERE c.fund_id = $1 AND a.item_id = ANY(sqlc.arg(item_ids)::text[]);

-- name: ListFundItemsForContribution :many
SELECT id, name, (archived_at IS NOT NULL)::bool AS archived FROM item WHERE fund_id = $1;

-- name: ListActiveItemNames :many
SELECT id, name FROM item WHERE fund_id = $1 AND archived_at IS NULL ORDER BY "position", created_at, id;

-- name: GetItemName :one
SELECT name FROM item WHERE id = $1;

-- name: InsertItem :exec
INSERT INTO item (id, fund_id, name, price, deadline, plan_start, "position")
VALUES ($1, $2, $3, $4, sqlc.arg(deadline)::date, sqlc.arg(plan_start)::date, $5);

-- name: UpdateItem :exec
UPDATE item SET name = $3, price = $4, deadline = sqlc.arg(deadline)::date,
    plan_start = sqlc.arg(plan_start)::date
WHERE id = $1 AND fund_id = $2;

-- name: ArchiveItem :exec
UPDATE item SET archived_at = now(), actual_price = $3, purchased_on = sqlc.arg(purchased_on)::date
WHERE id = $1 AND fund_id = $2;

-- name: UnarchiveItem :exec
UPDATE item SET archived_at = NULL, actual_price = NULL, purchased_on = NULL
WHERE id = $1 AND fund_id = $2;

-- name: SetItemPosition :exec
UPDATE item SET "position" = $3 WHERE id = $1 AND fund_id = $2;

-- name: DeleteItem :exec
DELETE FROM item WHERE id = $1 AND fund_id = $2;

-- name: CountItemAllocations :one
SELECT count(*)::int FROM allocation WHERE item_id = $1;

-- name: ListContributions :many
SELECT id, date::text AS date, note, kind, created_at
FROM contribution WHERE fund_id = $1 ORDER BY date, created_at, id;

-- name: ListAllocationsForFund :many
SELECT a.id, a.contribution_id, a.item_id, a.amount
FROM allocation a JOIN contribution c ON c.id = a.contribution_id
WHERE c.fund_id = $1 ORDER BY a.contribution_id, a.id;

-- name: GetContribution :one
SELECT id, fund_id, date::text AS date, note, kind, created_at
FROM contribution WHERE id = $1;

-- name: ListContributionAllocations :many
SELECT id, contribution_id, item_id, amount FROM allocation
WHERE contribution_id = $1 ORDER BY id;

-- name: InsertContribution :exec
INSERT INTO contribution (id, fund_id, date, note, kind)
VALUES ($1, $2, sqlc.arg(date)::date, $3, $4);

-- name: UpdateContributionHeader :exec
UPDATE contribution SET date = sqlc.arg(date)::date, note = $3, kind = $4
WHERE id = $1 AND fund_id = $2;

-- name: DeleteContribution :exec
DELETE FROM contribution WHERE id = $1 AND fund_id = $2;

-- name: InsertAllocation :exec
INSERT INTO allocation (id, contribution_id, item_id, amount) VALUES ($1, $2, $3, $4);

-- name: DeleteAllocationsFor :exec
DELETE FROM allocation WHERE contribution_id = $1;

-- name: ListLedgerLines :many
SELECT c.id AS contribution_id, a.item_id, c.date::text AS date, c.kind,
       a.amount, c.created_at
FROM allocation a JOIN contribution c ON c.id = a.contribution_id
WHERE c.fund_id = $1;

-- name: PurgeFundAllocations :exec
DELETE FROM allocation WHERE contribution_id IN (SELECT id FROM contribution WHERE fund_id = $1);

-- name: PurgeFundContributions :exec
DELETE FROM contribution WHERE fund_id = $1;

-- name: PurgeFundItems :exec
DELETE FROM item WHERE fund_id = $1;

-- name: PurgeFundInvites :exec
DELETE FROM fund_invite WHERE fund_id = $1;

-- name: PurgeFundMembers :exec
DELETE FROM fund_member WHERE fund_id = $1;
