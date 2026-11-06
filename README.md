# fund-backend

Go backend for Fund (savings planner). It replaces the Next.js backend in `~/dev/fund`, serves the Expo app in
`fund-mobile`, and shares the same Postgres database (schema frozen at Drizzle migration 0011).

## Run

```sh
cp .env.example .env      # fill in values
make run                  # or: make build && ./bin/fundd
```

Health: `GET /healthz` (liveness), `GET /readyz` (database ping). API description: `GET /openapi.yaml`.

### Configuration (environment)

| Variable | Purpose |
|---|---|
| `DATABASE_URL` (required) | Postgres connection string |
| `AUTH_SECRET` (required, 16+ chars) | Signs email-link tokens and unsubscribe links. **Reuse the web app's `BETTER_AUTH_SECRET`** so links already in inboxes keep working |
| `PUBLIC_URL` | The client app; links in emails point here (`/verify-email`, `/reset-password`, `/invite/<token>`, `/contributions?new=1`) |
| `API_URL` | This API's public URL; reminder unsubscribe links point here (defaults to `PUBLIC_URL`) |
| `RESEND_API_KEY`, `EMAIL_FROM` | Email delivery. With no key, emails are logged instead of sent |
| `CRON_SECRET` (16+ chars) | Enables `POST/GET /cron/reminders` (`Authorization: Bearer <secret>`); unset = 404 |
| `CORS_ORIGINS` | Comma-separated browser origins allowed to call the API (Expo web) |
| `TRUST_PROXY` | Read client IP from `X-Forwarded-For`/`X-Real-IP` (default true; set false when not behind a proxy) |
| `RATE_LIMIT` | Per-IP auth route limits (default true) |
| `MIGRATE_ON_START` | Apply migrations at boot (default false, see Cutover) |
| `PORT`, `LOG_LEVEL` | Defaults `4000`, `info` |

Call the reminder cron **hourly**; each fund is emailed at most once per fund-local day, from 08:00 local on.

## API in one paragraph

Bearer sessions (`Authorization: Bearer <token>` from sign-in/sign-up). Funds are addressed by id
(`/funds/{fundId}/...`). Every fund mutation answers `{ok:true, payload}` with the fresh fund payload; failures are
`{ok:false, error, status}` with the same HTTP status. Contribution creates are idempotent by a client UUID `id`.
See `internal/api/openapi.yaml` (a test fails if it drifts from the routes).

## Develop

```sh
make test      # needs TEST_DATABASE_URL (a Postgres server; each test makes its own database)
make vet lint  # gofmt + go vet
make sqlc      # regenerate internal/db/dbgen after editing internal/db/queries/*.sql
```

Local Postgres for tests: `docker run -d --name fund-pg -e POSTGRES_USER=fund -e POSTGRES_PASSWORD=fund -p 55432:5432 postgres:18`
then `TEST_DATABASE_URL=postgres://fund:fund@localhost:55432/postgres?sslmode=disable make test`.

### Parity with the web app

The Go port is checked against the web app's real code (needs `~/dev/fund` with `node_modules`):

| Target | What it proves |
|---|---|
| `make parity` | Regenerates golden vectors from the real TypeScript: planning maths (700 cases), money/formatting, CSV/ICS/zip exports, email copy and unsubscribe tokens |
| `make parity-auth` | Password hashes made by Go verify in better-auth |
| `make parity-db` | The web app's real server functions seed a database; Go's payload for the owner and a viewer is identical to the web app's `loadFund()` |
| `make parity-db-go` | Go seeds a database; the web app's real code reads it identically, mutates it, and Go reads those changes |

The parity targets expect the Postgres container from the Develop section (override the name with `PG_CONTAINER=...`).

Known, deliberate differences are in `PARITY_NOTES.md`.

## Cutover from the Next.js app

The schema is frozen, so both apps can run against the same database during the move.

1. Point a staging copy at a database created by the web app and run `DATABASE_URL=... go run ./cmd/fund-migrate baseline`
   (records the baseline without running it; refuses an empty database). Use `fund-migrate up` on fresh databases.
2. Deploy `fundd` with the same `DATABASE_URL`, `AUTH_SECRET` = the web app's `BETTER_AUTH_SECRET`, and `RESEND_*`.
3. Move the hourly cron to `POST /cron/reminders` and stop the Next.js cron. Point clients at the Go API.
4. Existing passwords keep working (same scrypt format). Old browser cookies are not accepted: people sign in once.
5. Retire the Next.js app, then new migrations can be added with goose (`internal/db/migrations`).

In-process state (rate limits, invite cooldowns, daily invite cap) resets on restart and is per instance, like the
web app; run one instance or add a shared store before scaling out.
