# Reader

Reader is a Go Web service project that ports the core online book-source capabilities of Legado.

Reader is OpenAPI-first: business APIs must be defined in `api/openapi.yaml`, generated with `oapi-codegen`, and then implemented in handlers. Generated code under `internal/api/generated` must not be edited by hand.

The service targets single-user self-hosting with Bearer Token authentication, SQLite persistence, a restricted JavaScript runtime, safe HTTP fetching, debug sessions, check tasks, replacement rules, bookshelf/progress APIs, image proxying, and mock/real E2E gates.

Compatibility boundary:

- Compatible with Legado book source JSON and online rule semantics.
- Not compatible with legacy Legado Web API routes such as `/saveBookSource`, `/getBookSources`, `/searchBook`, or `/getBookContent`.

## Documentation

- `docs/Requirement.md`: product scope, priorities, acceptance gates.
- `docs/Design.md`: architecture, module boundaries, ports, security design.
- `docs/Tasks.md`: task breakdown and required workflow.
- `docs/Development.md`: local development, configuration, generation, verification.
- `docs/API.md`: API usage examples.
- `docs/LegadoAlignmentReport.md`: longitudinal Legado-vs-Reader alignment audit; use together with the latest real E2E report when citing current status.
- `docs/Release-0.0.1.md`: first release snapshot and publication notes.
- `docs/archive/`: archived historical documents; not a current source of truth unless you are explicitly tracing history.

## Local Run

```bash
mkdir -p data
READER_TOKEN=dev-token \
READER_SQLITE_DSN=./data/reader.db \
READER_CONTENT_RESEGMENT=false \
READER_CONTENT_CHINESE_CONVERTER_TYPE=0 \
go run ./cmd/server
```

Health check:

```bash
curl -s http://127.0.0.1:8080/api/v1/health
```

Authenticated request:

```bash
curl -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/book-sources
```

## Verification

```bash
go generate ./...
go test ./...
go vet ./...
git diff --exit-code internal/api/generated
```

Or:

```bash
./scripts/verify.sh
make verify
```

Default E2E does not access real external sources:

```bash
go test ./test/e2e
```

The real remote source collection release gate is opt-in:

```bash
READER_REAL_E2E_ENABLED=true \
go test ./test/e2e -run TestRealSourceCollectionE2E -count=1
```
