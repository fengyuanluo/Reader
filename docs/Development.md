# Reader 本地开发与验收指南

本文面向 Reader Go Web 服务开发者，说明本地启动、配置、OpenAPI 生成、SQLite 迁移和验收命令。

## 基本原则

- 本项目只兼容 Legado 书源 JSON 数据结构和在线书源规则语义，不兼容 Legado 旧 Web API 路由。
- 所有 HTTP API 必须先修改 `api/openapi.yaml`，再运行 `go generate ./...` 生成 `internal/api/generated/server.gen.go`，最后实现 handler。
- `internal/api/generated` 是生成代码，禁止手工修改。
- `legado/` 只作为上游源码事实来源，默认不得修改，也不得纳入本项目版本管理。

## 环境要求

- Go 版本以 `go.mod` 为准。
- SQLite 使用纯 Go driver `modernc.org/sqlite`，本地无需额外安装 SQLite C 库。
- 生成 OpenAPI 代码使用 `github.com/oapi-codegen/oapi-codegen/v2`，已通过 `tools.go` 固定为 Go tool 依赖。

## 配置项

常用环境变量：

```bash
export READER_ADDR="127.0.0.1:8080"
export READER_DATA_DIR="./data"
export READER_SQLITE_DSN="./data/reader.db"
export READER_TOKEN="change-me"
export READER_ENV="dev"
export READER_DEFAULT_USER_AGENT="Reader/0.0.1"
export READER_FETCH_TIMEOUT="30s"
export READER_FETCH_MAX_BODY_BYTES="10485760"
export READER_FETCH_MAX_REDIRECTS="5"
export READER_ALLOW_PRIVATE_NETWORK="false"
export READER_SCRIPT_TIMEOUT="3s"
export READER_REGEX_TIMEOUT="3s"
export READER_CONTENT_RESEGMENT="false"
export READER_CONTENT_CHINESE_CONVERTER_TYPE="0"
```

其中 `READER_CONTENT_CHINESE_CONVERTER_TYPE` 取值约定：

- `0`：关闭简繁转换
- `1`：繁体转简体
- `2`：简体转繁体

生产模式必须设置 `READER_TOKEN`。测试模式允许默认 token，但不要把真实 token 写入文档、测试 fixture 或提交记录。

## 本地启动

```bash
mkdir -p data
READER_TOKEN=dev-token \
READER_SQLITE_DSN=./data/reader.db \
go run ./cmd/server
```

健康检查无需 token：

```bash
curl -s http://127.0.0.1:8080/api/v1/health
```

受保护接口需要 Bearer Token：

```bash
curl -s \
  -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/book-sources
```

## OpenAPI 生成

修改 `api/openapi.yaml` 后必须运行：

```bash
go generate ./...
git diff --exit-code internal/api/generated
```

如果生成代码出现 diff，必须确认 diff 来自 OpenAPI 契约变化，并提交生成结果。不要手工编辑生成代码。

## SQLite 迁移

服务启动时会执行 `readersqlite.Migrate(db)`，从空库创建以下核心表：

- `book_sources`
- `books`
- `chapters`
- `replace_rules`
- `cookies`
- `source_variables`
- `cache_entries`
- `debug_sessions`
- `debug_events`
- `check_tasks`
- `check_task_items`

迁移和 repository 测试：

```bash
go test ./internal/infra/sqlite/...
go test ./internal/infra/sqlite/repositories/...
```

## 常规验收

完整本地门禁：

```bash
./scripts/verify.sh
```

等价命令：

```bash
go generate ./...
go test ./...
go vet ./...
git diff --exit-code internal/api/generated
```

可选 make 入口：

```bash
make verify
```

## E2E

默认 E2E 不访问真实外网：

```bash
go test ./test/e2e
```

真实远程书源集合 release gate 默认跳过。显式启用：

```bash
READER_REAL_E2E_ENABLED=true \
READER_REAL_E2E_KEYWORD="斗破苍穹" \
go test ./test/e2e -run TestRealSourceCollectionE2E -count=1
```

真实 gate 会更新仓库中的最新报告快照：

- `test/reports/real_source_collection_e2e_report.json`
- `test/reports/real_source_collection_e2e_report.md`

这些报告是可重生成的审计快照，但在 `0.0.1` 版本中作为仓库内容保留，便于版本回溯。

真实 gate 依赖外部网络、书源规则和目标站点状态；失败时必须依据报告区分网络不可用、安全策略拦截、书源规则失效、目标站点不可用或实现缺陷。
