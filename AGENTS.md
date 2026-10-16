# AGENTS.md

本文件约束 `/root/Coding/General/Reader` 及其子目录中的所有后续工作。

## 项目定位

本仓库用于实现一个 Go Web 服务，把 Legado 的在线书源核心能力移植为单用户自托管 API。

兼容边界必须始终保持一致：

- 兼容 Legado 书源 JSON 数据结构和在线规则语义。
- 不兼容 Legado 旧 Web API 路由，例如 `/saveBookSource`、`/getBookSources`、`/searchBook`、`/getBookContent`。
- 一期默认单用户自托管 + Bearer Token。
- 一期默认 Go + `net/http` + `chi` + `oapi-codegen` + SQLite + `goja` 受限脚本沙箱。

`legado/` 是上游 Android/Kotlin 源码镜像，只用于源码研究和事实核对，默认只读。

## 最高优先级规则

- 默认用中文沟通、注释和项目文档。
- 不要修改 `legado/` 内任何文件，除非用户明确要求维护上游研究材料或修改上游源码。
- 不要把 `legado/` 纳入本仓库版本管理；`.gitignore` 已排除 `/legado/`。
- 本项目必须 OpenAPI-first：先改 `api/openapi.yaml`，再运行生成，再实现 handler / app / infra。
- `internal/api/generated/server.gen.go` 是生成代码，禁止手工修改。
- 每个实现任务开始前，必须先探索对应的 Legado 原实现，不能凭印象改 Reader。
- 每个代码任务必须新增或更新测试；如果不适合单元测试，至少提供契约测试、集成测试、E2E 或明确人工验收依据。
- 每个任务完成前必须运行与任务匹配的验收门禁；无法运行时必须说明原因、影响和风险。

## Git 与工作树

- 当前目录是本项目 git 仓库根目录。
- 开始任何任务前先运行：

```bash
git status --porcelain=v2 --branch
```

- 不要执行 `git reset --hard`、`git checkout --` 等破坏性命令，除非用户明确要求。
- 不要覆盖用户未提交变更。
- 不要在用户未要求时自动提交。

## 真相源优先级

### 一级真相源

这些文件决定“要做什么、怎么做、按什么顺序做”：

- `docs/Requirement.md`
- `docs/Design.md`
- `docs/Tasks.md`
- `api/openapi.yaml`

使用规则：

- 如果用户请求实现某项功能，先在 `docs/Requirement.md` 定位需求和非目标，再在 `docs/Design.md` 定位架构边界，最后在 `docs/Tasks.md` 定位最接近的任务。
- 如果三份基线文档之间有冲突，不要硬做；先指出冲突并优先修正文档基线。
- 如果用户新增需求，必须评估是否同步更新 `Requirement.md`、`Design.md`、`Tasks.md`，避免实现与文档脱节。
- 如果源码事实与文档不一致，先回到 Legado 原实现核实，再决定更新文档还是在最终说明中标出偏差。
- 不得只读 `Tasks.md` 就开始编码；`Requirement.md` 和 `Design.md` 同样是必读输入。

### 二级运行与验证真相源

这些文件决定“仓库现在怎么运行、怎么生成、怎么验证”：

- `README.md`
- `docs/Development.md`
- `docs/API.md`
- `generate.go`
- `api/oapi-codegen.yaml`
- `Makefile`
- `scripts/verify.sh`
- `.github/workflows/ci.yml`

### 三级审计与研究真相源

这些文件用于判断 Reader 与 Legado 的对齐状态、真实 E2E 现状和残余问题：

- `docs/LegadoAlignmentReport.md`
- `test/e2e/real_source_collection_test.go`
- `test/reports/real_source_collection_e2e_report.json`
- `test/reports/real_source_collection_e2e_report.md`

使用规则：

- `docs/LegadoAlignmentReport.md` 是持续追加的增量审计日志，内部保留多轮历史快照。
- 如果要引用“当前最新通过率”或“当前剩余 blocker”，必须交叉核对该文档最后一轮更新和最新 `test/reports/real_source_collection_e2e_report.*`，必要时重新运行真实 E2E。
- 不要把审计日志中较早位置的 `fullSuccessCount`、阶段统计或 blocker 描述，当成当前实时状态直接复述。

### 阶段性记录与历史归档

以下内容默认不是当前实现真相源：

- `docs/archive/task_plan-2026-05-03-to-2026-05-05.md`
- `docs/archive/findings-2026-05-03-to-2026-05-05.md`
- `docs/archive/progress-2026-05-03-to-2026-05-05.md`
- `docs/archive/`

使用规则：

- `docs/archive/task_plan-2026-05-03-to-2026-05-05.md`、`docs/archive/findings-2026-05-03-to-2026-05-05.md`、`docs/archive/progress-2026-05-03-to-2026-05-05.md` 是阶段性执行记录，可用于追溯决策和中断恢复，但不能替代 `docs/Requirement.md`、`docs/Design.md`、`docs/Tasks.md`。
- `docs/archive/` 只存放历史或过期文档。除非用户明确要求追溯历史判断，否则不要用归档文档指导当前实现。
- 如果归档文档与当前 `docs/` 基线、当前代码或当前测试结果冲突，以当前基线和最新代码/测试结果为准。

## 仓库结构与职责

当前仓库主要目录职责如下：

- `api/`
  - `openapi.yaml` 是接口唯一真相源。
  - `oapi-codegen.yaml` 定义生成配置。
  - `examples/` 存放契约示例。
- `cmd/reader/`
  - 单二进制入口：`serve` 子命令的 HTTP 路由装配、监听与 token 校验、优雅关闭。
- `internal/cli/`
  - 非交互 CLI 子命令（import/source/search/book/toc/content/download），直连服务层与 SQLite。
- `internal/bootstrap/`
  - 由配置装配运行时（SQLite、迁移、仓储、服务），serve 与 CLI 共用。
- `internal/api/`
  - `generated/`：生成代码，禁止手改。
  - `handlers/`：HTTP handler、DTO 映射。
  - `middleware/`：鉴权、traceId 等 HTTP 中间件。
- `internal/app/`
  - use case 编排，按 `source/search/discovery/book/content/replace/debug/check` 分模块。
- `internal/domain/`
  - 领域模型、ports、错误与策略；不得依赖 `net/http`、`database/sql`、`chi`、`goja`。
- `internal/infra/`
  - `config/`、`fetcher/`、`logging/`、`rule/`、`script/`、`sqlite/` 等具体实现。
- `internal/worker/`
  - 长任务执行器；当前主要是 `check/`。
- `test/`
  - `openapi/`：契约与示例校验。
  - `e2e/`：mock 与真实远程集合 E2E。
  - `security/`：SSRF、JS 超时、日志脱敏等安全测试。
  - `reports/`：真实 E2E 生成的报告，当前被 `.gitignore` 排除，可重生成。
- `scripts/verify.sh`
  - 本地快速完整门禁入口。

## 通用工作流

执行任一实现、重构、审计或文档任务时，至少遵循：

1. 读取本 `AGENTS.md`。
2. 运行 `git status --porcelain=v2 --branch`，确认工作树状态。
3. 读取相关一级真相源：`Requirement.md`、`Design.md`、`Tasks.md`。
4. 按任务需要探索对应 Legado 原实现并记录关键源码事实。
5. 先新增或更新测试，再实现代码；纯文档任务则先核对 live repo 结构、命令和引用。
6. 运行与任务匹配的验收门禁。
7. 在最终说明中列出：改动内容、所用事实来源、测试命令、验收结果、未完成项与风险。

## 按任务类型执行规则

### OpenAPI 与 HTTP API 任务

- 必须先修改 `api/openapi.yaml`。
- 然后运行：

```bash
go generate ./...
git diff --exit-code internal/api/generated
go test ./test/openapi
```

- 之后再修改 `internal/api/handlers`、`internal/app` 和必要的 `internal/domain` / `internal/infra`。
- 禁止在 handler 中堆业务规则；handler 只负责 HTTP / OpenAPI DTO 适配。

### 书源导入、搜索、详情、目录、正文任务

先阅读或检索这些 Legado 入口：

- `legado/docs/book-source-chain.md`
- `legado/app/src/main/java/io/legado/app/data/entities/BookSource.kt`
- `legado/app/src/main/java/io/legado/app/data/entities/BaseSource.kt`
- `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
- `legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt`
- `legado/app/src/main/java/io/legado/app/model/webBook/BookInfo.kt`
- `legado/app/src/main/java/io/legado/app/model/webBook/BookChapterList.kt`
- `legado/app/src/main/java/io/legado/app/model/webBook/BookContent.kt`
- `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt`

优先关注 Reader 当前对应实现：

- `internal/app/source/`
- `internal/app/search/`
- `internal/app/discovery/`
- `internal/app/book/`
- `internal/app/content/`
- `internal/api/handlers/source_handler.go`

### 规则引擎、抓取器、脚本兼容任务

先阅读或检索这些 Legado 入口：

- `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeUrl.kt`
- `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeRule.kt`
- `legado/app/src/main/java/io/legado/app/model/analyzeRule/RuleAnalyzer.kt`
- `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeByJSoup.kt`

优先关注 Reader 当前对应实现：

- `internal/infra/rule/parser/`
- `internal/infra/rule/css/`
- `internal/infra/rule/xpath/`
- `internal/infra/rule/jsonpath/`
- `internal/infra/rule/regex/`
- `internal/infra/rule/url/`
- `internal/infra/rule/analyzer.go`
- `internal/infra/script/runtime.go`
- `internal/infra/fetcher/`

额外规则：

- 任何兼容修复都必须说明命中了哪类真实失败，而不是只说“更像 Legado”。
- 严禁通过域名硬编码、站点特判或放宽安全边界来伪造兼容。
- 必须区分 Reader 通用实现缺口、书源规则失效、目标站点状态变化、网络环境问题。

### SQLite、repository、缓存与状态任务

- schema 变化必须带 migration。
- repository 变化必须带 repository tests。
- 优先检查：
  - `internal/infra/sqlite/migrate.go`
  - `internal/infra/sqlite/repositories/`
  - `internal/domain/*/repository.go`

### 调试与校验任务

先阅读或检索这些 Legado 入口：

- `legado/app/src/main/java/io/legado/app/service/CheckSourceService.kt`
- `legado/app/src/main/java/io/legado/app/model/Debug.kt`

优先关注 Reader 当前对应实现：

- `internal/app/debug/`
- `internal/app/check/`
- `internal/worker/check/`
- `internal/api/handlers/debug_*`
- `internal/api/handlers/check_*`

### 文档、README、AGENTS 和审计任务

- 先核对 live repo 结构、真实命令、真实脚本、真实测试入口，再写文档。
- 不要把阶段性记录、过期审计或旧快照包装成当前结论。
- 纯文档任务可以不跑全量产品测试，但必须验证路径、命令、引用和真相源层级是否准确。
- 如果只是改文档，不要声称“产品行为已验证”或“通过率已更新”，除非你真的重跑了对应门禁。

## Go 实现约束

- `internal/domain` 不得依赖 `net/http`、`database/sql`、`chi`、`goja`。
- `internal/api` 只做 HTTP/OpenAPI DTO 适配，不写业务规则。
- `internal/app` 负责编排 use case，依赖 domain ports。
- `internal/infra` 负责实现 SQLite、HTTP Fetcher、规则适配器、脚本运行时、缓存、日志等基础设施。
- 所有外部抓取必须经过超时、响应大小限制、重定向限制和 SSRF 防护。
- 所有 JS 执行必须经过受限沙箱、超时和白名单绑定。
- 所有日志必须脱敏 `Authorization`、`Cookie`、`Set-Cookie` 和 token。
- SQLite 是一期默认持久化，但 repository 抽象必须允许未来替换 PostgreSQL。

## 工具与搜索习惯

优先使用：

```bash
rg
rg --files
sed -n
nl -ba
```

如果需要快速确认当前仓库结构，优先查看：

```bash
rg --files cmd internal scripts test api docs
```

## 验收门禁

常规代码任务至少运行：

```bash
go generate ./...
go test -race -count=1 ./...
go vet ./...
```

OpenAPI 相关任务至少补跑：

```bash
go test ./test/openapi
git diff --exit-code internal/api/generated
```

SQLite 相关任务至少补跑：

```bash
go test ./internal/infra/sqlite/...
go test ./internal/infra/sqlite/repositories/...
```

安全边界相关任务至少补跑：

```bash
go test ./test/security
```

默认 E2E：

```bash
go test ./test/e2e
```

本地完整门禁快捷入口：

```bash
./scripts/verify.sh
make verify
```

真实远程书源集合 release/manual gate 默认不进入普通 PR CI，必须显式启用：

```bash
READER_REAL_E2E_ENABLED=true \
go test ./test/e2e -run TestRealSourceCollectionE2E -count=1
```

最终真实 release gate URL 必须保持一致：

- `https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361`

如果运行真实 gate：

- 必须区分实现缺陷、书源规则失效、目标站点不可用、网络不可用和安全策略拦截。
- 必须说明 `test/reports/real_source_collection_e2e_report.json` 与 `test/reports/real_source_collection_e2e_report.md` 是本地可重生成、且当前作为最新审计快照被版本化跟踪的报告。
