# fund-backend

Go backend for Fund (savings planner). Serves the Expo app in `fund-mobile` and shares the Postgres database with the retiring Next.js app.

## Run
```sh
cp .env.example .env   # fill in values
make run
```

Health: `GET /healthz`. Readiness (`/readyz`) arrives with the database layer.

## Develop
`make test`, `make vet`, `make lint`. See `AGENTS.md` for conventions.
