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

## Docker

Pull and run the latest image:

```bash
docker pull ghcr.io/fengyuanluo/reader:latest
docker run -d \
  --name reader \
  --restart unless-stopped \
  -p 8080:8080 \
  -e READER_ENV=production \
  -e READER_TOKEN=replace-with-a-secret \
  -v reader-data:/data \
  ghcr.io/fengyuanluo/reader:latest
```

Or use Compose without storing the token in `compose.yaml`:

```bash
READER_TOKEN=replace-with-a-secret docker compose up -d
```

The container listens on port 8080, stores SQLite data under `/data`, and uses `/tmp` only for temporary TXT exports. The image sets `READER_DATA_DIR=/data` and `READER_SQLITE_DSN=/data/reader.db`; the named `reader-data` volume preserves the database across container replacement. Production mode requires a non-empty `READER_TOKEN`.

Published image tags include `latest`, SemVer tags such as `0.1.0` and `0.1`, and immutable commit tags such as `sha-abcdef0`.

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

## License

Reader is licensed under `AGPL-3.0-only`.
