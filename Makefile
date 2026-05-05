.PHONY: generate test vet openapi-check generated-check verify security e2e

generate:
	go generate ./...

test:
	go test ./...

vet:
	go vet ./...

openapi-check:
	go test ./test/openapi

generated-check: generate
	git diff --exit-code internal/api/generated

security:
	go test ./test/security

e2e:
	go test ./test/e2e

verify: generate test vet generated-check openapi-check security e2e
