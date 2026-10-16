.PHONY: generate test vet openapi-check generated-check verify security e2e

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
