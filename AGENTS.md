# fund-backend

Go REST API for Fund, replacing the Next.js backend in `~/dev/fund`. It shares the same Postgres database.

## Stack
stdlib `net/http` (1.22+ mux), pgx, sqlc, goose. REST + OpenAPI. Every mutation returns the fresh fund payload.

## Rules
- Use semver and conventional commits.
- No AI attribution in commits and PRs.
- Always use well-named feature branches; never commit directly on `main` or `staging`.
- Fetch from GitHub before coding so the base is fresh.
- The database schema is frozen at Drizzle migration 0011 until the Next.js app is retired. Go migrations must be backward compatible and never touch the `drizzle` schema.
- Planning maths in `internal/plan` must match `~/dev/fund/src/plan/*` exactly; record any suspected quirk in `PARITY_NOTES.md` instead of fixing it silently.
- Never commit secrets or print env values.

## Commands
`make run | test | vet | lint | build`
