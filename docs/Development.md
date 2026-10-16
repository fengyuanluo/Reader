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

常用环境变量（注释标注 `internal/infra/config/config.go` 中的默认值）：

```bash
export READER_ADDR="127.0.0.1:8080"                 # 默认 127.0.0.1:8080
export READER_DATA_DIR="./data"                     # 默认 data
export READER_SQLITE_DSN="./data/reader.db"         # 默认 data/reader.db
export READER_TOKEN="change-me"                     # 默认为空
export READER_ENV="development"                     # 默认 development，见下方取值约定
export READER_DEFAULT_USER_AGENT="Reader/0.1.0"
export READER_FETCH_TIMEOUT="30s"
export READER_FETCH_MAX_BODY_BYTES="10485760"
export READER_FETCH_MAX_REDIRECTS="5"
export READER_ALLOW_PRIVATE_NETWORK="false"
export READER_GLOBAL_CONCURRENCY="8"
export READER_SOURCE_CONCURRENCY="2"
export READER_SCRIPT_TIMEOUT="10s"
export READER_DEBUG_BODY_LIMIT="32768"
export READER_CONTENT_RESEGMENT="false"
export READER_CONTENT_CHINESE_CONVERTER_TYPE="0"
```

其中 `READER_CONTENT_CHINESE_CONVERTER_TYPE` 取值约定：

- `0`：关闭简繁转换
- `1`：繁体转简体
- `2`：简体转繁体

`READER_DATA_DIR` 与 SQLite 路径的关系：`READER_SQLITE_DSN` 未设置时，数据库默认落在 `READER_DATA_DIR` 下（默认 `data/reader.db`）；显式设置 `READER_SQLITE_DSN` 时以该值为准。相对路径按进程当前工作目录解析，部署时建议用绝对路径。

`READER_ENV` 取值约定：

- `development`：默认值。
- `production`：必须设置 `READER_TOKEN`，否则启动失败。
- `test`：token 为空时会自动填入公开默认值 `reader-test-token`。

监听地址与 token 的关系（对所有环境都生效）：

- `READER_TOKEN` 为空时只允许监听回环地址（`127.0.0.0/8`、`::1`、`localhost`）；监听 `0.0.0.0`、`:8080`、局域网 IP 等非回环地址时 `serve` 拒绝启动并返回退出码 2，且在打开数据库之前就失败。
- `READER_ENV=test` 会在 `config.Load` 里把空 token 填成公开的 `reader-test-token`，因此 test 环境即使监听非回环地址也能启动。这只适合本地/CI，**不要**把 test 环境暴露到不可信网络。
- 不要把真实 token 写入文档、测试 fixture 或提交记录。

`READER_*` 环境变量中只有上面这些项属于应用配置，会被 `config.Load` 读取（并出现在 `serve` 启动日志的 `config.Redacted()` 中，token 已脱敏）；`READER_REAL_E2E_ENABLED`、`READER_REAL_E2E_SOURCE_COLLECTION_URL`、`READER_REAL_E2E_KEYWORD` 等 `READER_REAL_E2E_*` 只由 `test/e2e` 直接读取，不是应用配置。`READER_REGEX_TIMEOUT` 已作为 no-op 配置项删除，不再被读取。

## 本地启动

```bash
mkdir -p data
READER_TOKEN=dev-token \
READER_SQLITE_DSN=./data/reader.db \
go run ./cmd/reader serve
```

`serve` 默认监听 `127.0.0.1:8080`，日志为输出到 **stderr** 的结构化 JSON。token 为空时只允许监听回环地址，监听非回环地址会在建库前拒绝启动（退出码 2）。

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

服务启动时由 `internal/bootstrap` 打开数据库并执行 `readersqlite.Migrate(db)`。迁移步骤是 `internal/infra/sqlite/migrate.go` 内按版本升序排列的列表（`version + name + apply`），每个版本在独立事务内执行并把版本号写入 `schema_migrations`。**已发布版本禁止修改，结构变更只能追加新版本**。

当前版本序列：

- `v1`：initial schema。
- `v2`：补 `check_task_items.failure_class`。
- `v3`：删除不再使用的 `search_books`。
- `v4`：新增 `idx_debug_events_session` 索引。
- `v5`：清空 `namespace='content'` 的旧正文缓存（旧缓存保存的是已应用用户替换规则的正文，需重新抓取）。
- `v6`：补 `books` 的 Legado 元数据列（`custom_tag`、`custom_cover_url`、`custom_intro`、`charset`、`book_group`、`last_check_time`、`last_check_count`、`can_update`、`book_order`）。

`sqlite.Open` 的行为（`internal/infra/sqlite/open.go`）：

- 数据目录以 `0700` 创建，数据库文件强制 `0600`（库内含站点 Cookie 等会话凭证，旧库若是 `0644` 会被收紧）。
- 连接级 PRAGMA 通过 DSN 的 `_pragma` 参数下发，对连接池里每条新连接生效：`busy_timeout(5000)`、`foreign_keys(1)`、`journal_mode(WAL)`；`Migrate` 本身不再设置 PRAGMA。
- `READER_SQLITE_DSN` 是数据库文件路径，不支持 URI 参数或 `:memory:`。
- 启用 WAL 后磁盘上会出现 `reader.db-wal`、`reader.db-shm`。

从空库创建的核心表：

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
- `schema_migrations`

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
go test -race -count=1 ./...
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
go test -count=1 ./test/e2e
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
