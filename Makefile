.PHONY: run build test vet lint tidy parity sqlc

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

sqlc:
	go tool sqlc generate
