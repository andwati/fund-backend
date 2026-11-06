.PHONY: run build test vet lint tidy parity parity-auth sqlc parity-db parity-db-go

run:
	go run ./cmd/fundd

build:
	go build -o bin/fundd ./cmd/fundd

test:
	go test ./...

vet:
	go vet ./...

# golangci-lint gets pinned as a `go tool` dependency in a later phase.
lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l .; echo 'gofmt needed'; exit 1)
	go vet ./...

tidy:
	go mod tidy

# Regenerate golden vectors from the web app's real planning code (needs ~/dev/fund with node_modules).
parity:
	cd tools/parity && OUT=$(CURDIR)/internal/plan/testdata/parity.json.gz /home/ian/dev/fund/node_modules/.bin/vitest run --root $(CURDIR)/tools/parity gen.test
	cd tools/parity && OUT=$(CURDIR)/internal/export/testdata/lib.json.gz /home/ian/dev/fund/node_modules/.bin/vitest run --root $(CURDIR)/tools/parity gen-lib
	cd tools/parity && OUT=$(CURDIR)/internal/money/currencies_gen.go /home/ian/dev/fund/node_modules/.bin/vitest run --root $(CURDIR)/tools/parity gen-currencies
	cd tools/parity && OUT=$(CURDIR)/internal/mail/testdata/mail.json.gz /home/ian/dev/fund/node_modules/.bin/vitest run --root $(CURDIR)/tools/parity gen-mail

sqlc:
	go tool sqlc generate

# Go-made password hashes must verify in better-auth (Node).
parity-auth:
	GO_HASHES_OUT=$(CURDIR)/tmp/gohashes.json go test ./internal/auth -run WriteGoHashes -count=1
	cd tools/parity && GO_HASHES=$(CURDIR)/tmp/gohashes.json OUT=/dev/null /home/ian/dev/fund/node_modules/.bin/vitest run --root $(CURDIR)/tools/parity gen-auth

# Cross-implementation check on one database: the web app's real code seeds it, Go must read the
# same payloads (Node -> Go). Needs Docker Postgres from `make test-db` and ~/dev/fund node_modules.
PARITY_DB ?= postgres://fund:fund@localhost:55432
parity-db:
	docker exec fund-pg-throwaway psql -U fund -d postgres -qc "DROP DATABASE IF EXISTS fund_parity" -c "CREATE DATABASE fund_parity"
	DATABASE_URL="$(PARITY_DB)/fund_parity?sslmode=disable" go run ./cmd/fund-migrate up
	cd tools/parity && PARITY_DATABASE_URL="$(PARITY_DB)/fund_parity?sslmode=disable" OUT=$(CURDIR)/tmp/payloads.json /home/ian/dev/fund/node_modules/.bin/vitest run --root $(CURDIR)/tools/parity seed-node
	PARITY_DATABASE_URL="$(PARITY_DB)/fund_parity?sslmode=disable" PARITY_PAYLOADS=$(CURDIR)/tmp/payloads.json go test -count=1 -v -run TestPayloadMatchesWebAppOnWebAppData ./internal/fund

# Go -> Node -> Go on a second database: Go seeds it, the web app's real code must read it identically
# and may then mutate it; Go must read the web app's changes.
parity-db-go:
	docker exec fund-pg-throwaway psql -U fund -d postgres -qc "DROP DATABASE IF EXISTS fund_parity_go" -c "CREATE DATABASE fund_parity_go"
	DATABASE_URL="$(PARITY_DB)/fund_parity_go?sslmode=disable" go run ./cmd/fund-migrate up
	GO_SEED_DATABASE_URL="$(PARITY_DB)/fund_parity_go?sslmode=disable" GO_PAYLOADS_OUT=$(CURDIR)/tmp/go-payloads.json go test -count=1 -run TestSeedScenarioForNode ./internal/fund
	cd tools/parity && PARITY_DATABASE_URL="$(PARITY_DB)/fund_parity_go?sslmode=disable" GO_PAYLOADS=$(CURDIR)/tmp/go-payloads.json OUT_AFTER=$(CURDIR)/tmp/after.json /home/ian/dev/fund/node_modules/.bin/vitest run --root $(CURDIR)/tools/parity read-go
	GO_SEED_DATABASE_URL="$(PARITY_DB)/fund_parity_go?sslmode=disable" PARITY_PAYLOADS_AFTER=$(CURDIR)/tmp/after.json go test -count=1 -v -run TestPayloadMatchesWebAppAfterItMutatedGoData ./internal/fund
