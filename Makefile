.PHONY: run build test vet lint tidy

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
