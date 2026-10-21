.PHONY: build generate test vet openapi-check generated-check verify security e2e staticcheck govulncheck

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -trimpath -ldflags "-s -w -X reader/internal/api/handlers.version=$(VERSION)" -o bin/reader ./cmd/reader

generate:
	go generate ./...

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

openapi-check:
	go test -count=1 ./test/openapi

generated-check: generate
	git diff --exit-code internal/api/generated

security:
	go test -count=1 ./test/security

e2e:
	go test -count=1 ./test/e2e

verify: generate test vet generated-check openapi-check security e2e

staticcheck:
	go install honnef.co/go/tools/cmd/staticcheck@latest
	staticcheck ./...

govulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...
