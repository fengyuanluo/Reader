# 已归档：旧版任务拆解

> 状态：已归档 / 已过期
>
> 归档日期：2026-05-05
>
> 过期原因：本文是 Reader 初版按 Phase 展开的全量实现任务文档，适合工程起步阶段，但已经不能准确反映 2026-05-05 审计后的真实兼容缺口、优先级和修复入口。
>
> 替代真相源：
> - `docs/Tasks.md`
> - `docs/ReaderLegadoSourceChainDiffAudit.md`
> - `docs/ReaderHardcodingAndBugAudit.md`
>
> 保留价值：保留项目早期的任务模板、阶段拆解和工程化约束背景，便于回溯“为什么当时这样分 Phase 实施”。
>
> 禁止用途：不得再将本文作为当前兼容缺口、当前优先级排序、当前实现状态或当前 release 修复清单的直接依据。

# Go Web 移植 Legado 在线书源核心能力任务拆解

## 1. 执行原则

本文是后续开发者的颗粒化任务清单。每个任务应尽量对应一个小 PR 和一个 commit，避免跨阶段混做。

硬性原则：

- 先 OpenAPI，后实现。
- 先 domain port，后 infrastructure adapter。
- 先测试 fixture，后业务闭环。
- 生成代码不得手改。
- `internal/domain` 不依赖 HTTP、SQLite、goja。
- 每个 P0 任务必须有测试或可执行验收门禁。
- 所有外部网络、JS、Regex、Cookie、日志能力必须有安全边界。
- 每个任务开始前必须按“任务工作流”探索原实现源码，不能跳过源码事实源直接编码。
- 每个代码任务必须撰写或更新对应单元测试；若任务性质不适合单元测试，必须写明替代的契约测试、集成测试、E2E 测试或人工验收依据。

## 1.1 每个任务强制工作流

后续每个任务，无论任务正文是否重复列出，都必须继承以下工作流：

1. 读取根级 `AGENTS.md`，确认当前目录约束。
2. 执行 `git status --porcelain=v2 --branch`，确认工作树状态，避免覆盖他人变更。
3. 按任务中的“原实现参考”使用 `rg`、`sed`、`nl` 等工具探索 Legado 源码。
4. 记录本任务依赖的源码事实，例如字段、错误语义、流程分支、DAO 查询、调试事件或安全边界。
5. 先新增或更新测试，包括单元测试、契约测试、repository 测试、集成测试、E2E fixture 中至少一种。
6. 再实现代码。
7. 运行任务内“验收门禁”和本文“通用验收命令”。
8. 在提交说明或 PR 描述中列出：源码探索文件、关键事实、测试文件、运行命令、验收结果。

禁止事项：

- 禁止没有阅读任务“原实现参考”就开始实现。
- 禁止只写实现不写测试。
- 禁止用真实外网测试替代 mock 单元/集成测试。
- 禁止修改 `legado/` 上游源码，除非任务明确要求维护上游研究材料；本项目默认只读 `legado/`。

## 2. Commit 命名规则

统一使用 Conventional Commits。每个任务都提供推荐 commit 名称，实际提交时应保持同一语义。

允许 scope：

```text
docs
build
api
domain
db
repo
source
rule
fetcher
script
webbook
replace
bookshelf
debug
check
test
ci
chore
```

## 3. 通用验收命令

后续实现阶段，除非任务另有说明，每个代码任务至少运行：

```bash
go generate ./...
go test ./...
go vet ./...
```

如果项目引入 `staticcheck`，则增加：

```bash
staticcheck ./...
```

OpenAPI 相关任务必须增加：

```bash
oapi-codegen --version
```

并使用项目选定的 OpenAPI validator 校验 `api/openapi.yaml`。

SQLite 相关任务必须增加 migration 测试：

```bash
go test ./internal/infra/sqlite/...
```

## 4. 任务模板

后续任务均按以下字段执行：

```md
### T-编号：任务名称

- Commit 名称：`type(scope): summary`
- 优先级：P0/P1/P2
- 阶段：Phase N
- 开始前源码探索：
  - 必须阅读或搜索本任务列出的原实现参考，并在提交说明中记录关键事实。
- 原实现参考：
  - `legado/...`
- 目标文件/模块：
  - `...`
- 实现内容：
  - ...
- 设计约束：
  - ...
- 单元测试要求：
  - 必须新增或更新本任务对应单元测试；若不适用，必须说明替代测试类型。
- 验收门禁：
  - ...
- 完成定义：
  - ...
```

## Phase 0：文档与工程基线

### T-001：确认需求范围文档

- Commit 名称：`docs(requirements): define source-compatible product scope`
- 优先级：P0
- 阶段：Phase 0
- 原实现参考：
  - `legado/README.md`
  - `legado/api.md`
  - `legado/docs/book-source-chain.md`
- 目标文件/模块：
  - `docs/Requirement.md`
- 实现内容：
  - 明确 Go Web 服务目标。
  - 明确只做在线书源闭环。
  - 明确不兼容旧 API 路由。
  - 明确兼容 Legado 书源 JSON 和规则语义。
  - 明确 P0/P1/P2 需求与验收标准。
- 设计约束：
  - 需求必须能追溯到原实现文件或用户确认。
  - 不得把 RSS、TXT/EPUB、TTS、WebDAV 混入一期 P0。
- 验收门禁：
  - 人工检查每个 P0 需求都有来源依据。
  - 人工检查不做范围清晰。
- 完成定义：
  - `docs/Requirement.md` 存在并可直接指导后续设计。

### T-002：确认架构设计文档

- Commit 名称：`docs(design): define clean architecture boundaries`
- 优先级：P0
- 阶段：Phase 0
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/AppDatabase.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeRule.kt`
- 目标文件/模块：
  - `docs/Design.md`
- 实现内容：
  - 定义 OpenAPI-first。
  - 定义 Go 项目目录结构。
  - 定义 domain/application/infrastructure/API 分层。
  - 定义核心接口和依赖方向。
  - 定义 SQLite、规则引擎、Fetcher、JS 沙箱、调试、校验设计。
- 设计约束：
  - 必须遵循 SOLID。
  - 业务层不得依赖具体基础设施。
- 验收门禁：
  - 人工检查每个核心模块有职责、输入输出、依赖方向。
  - 人工检查安全边界完整。
- 完成定义：
  - `docs/Design.md` 可作为工程架构蓝图。

### T-003：确认颗粒化任务文档

- Commit 名称：`docs(tasks): define granular implementation workflow`
- 优先级：P0
- 阶段：Phase 0
- 原实现参考：
  - 本文档中所有列出的 Legado 源码路径
- 目标文件/模块：
  - `docs/Tasks.md`
- 实现内容：
  - 按阶段拆分任务。
  - 每个任务标明原实现参考。
  - 每个任务标明目标模块、实现内容、验收门禁、完成定义和 commit 名称。
- 设计约束：
  - 一个任务应可由一个开发者在一个小 PR 中完成。
  - 不得出现只有“实现某模块”这种笼统任务。
- 验收门禁：
  - 人工检查每个 P0 任务都有验收门禁。
  - 人工检查任务顺序无明显依赖倒置。
- 完成定义：
  - `docs/Tasks.md` 可直接交给后续开发者执行。

### T-004：初始化 Go module 和目录骨架

- Commit 名称：`build(project): initialize go module and server layout`
- 优先级：P0
- 阶段：Phase 0
- 原实现参考：
  - 无直接 Go 原实现；参考 `docs/Design.md` 目录结构
- 目标文件/模块：
  - `go.mod`
  - `cmd/server/main.go`
  - `internal/api`
  - `internal/app`
  - `internal/domain`
  - `internal/infra`
  - `internal/worker`
  - `api`
  - `test/fixtures`
- 实现内容：
  - 初始化 Go module。
  - 建立目录结构。
  - 添加最小 server main，但不实现业务。
  - 添加基础 README 或开发说明占位。
- 设计约束：
  - 不在初始化任务中写业务逻辑。
  - 不引入未使用的大型依赖。
- 验收门禁：
  - `go test ./...`
  - `go vet ./...`
  - `go list ./...`
- 完成定义：
  - Go 项目可以编译测试。
  - 目录结构与 `Design.md` 一致。

### T-005：建立 OpenAPI 生成流程

- Commit 名称：`build(openapi): add openapi-first generation workflow`
- 优先级：P0
- 阶段：Phase 0
- 原实现参考：
  - `legado/api.md`
  - `legado/app/src/main/java/io/legado/app/web/HttpServer.kt`
- 目标文件/模块：
  - `api/openapi.yaml`
  - `internal/api/generated`
  - `tools.go`
  - `go:generate` 配置
- 实现内容：
  - 引入 `oapi-codegen`。
  - 添加生成配置。
  - 生成 chi server interface 和 types。
  - 添加检查生成代码是否最新的说明或脚本。
- 设计约束：
  - 生成代码目录禁止手改。
  - API handler 必须实现生成 interface。
- 验收门禁：
  - `go generate ./...`
  - `git diff --exit-code internal/api/generated`
  - OpenAPI validator 通过。
- 完成定义：
  - 新增或修改 OpenAPI 后可稳定重新生成代码。

### T-006：添加配置加载

- Commit 名称：`build(config): add runtime configuration and environment loading`
- 优先级：P0
- 阶段：Phase 0
- 原实现参考：
  - `docs/Design.md` 配置设计
- 目标文件/模块：
  - `internal/infra/config`
  - `cmd/server/main.go`
- 实现内容：
  - 支持监听地址、数据目录、SQLite DSN、Token、环境、抓取超时、响应大小、并发、JS 超时、正则超时等配置。
  - 生产模式缺少 Token 时拒绝启动。
  - 测试模式允许默认 token。
- 设计约束：
  - 敏感配置不得打印原文。
  - 配置结构体不得泄露到 domain。
- 验收门禁：
  - `go test ./internal/infra/config/...`
  - 缺少 Token 的生产配置测试失败。
  - 测试配置可启动。
- 完成定义：
  - 所有后续模块通过配置对象读取运行参数。

### T-007：添加结构化日志和 traceId

- Commit 名称：`build(logging): add structured logging and trace ids`
- 优先级：P0
- 阶段：Phase 0
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/Debug.kt`
- 目标文件/模块：
  - `internal/infra/logging`
  - `internal/api/middleware`
- 实现内容：
  - 添加结构化 logger。
  - 为每个 HTTP 请求生成或透传 traceId。
  - 错误响应包含 traceId。
  - 日志脱敏 Authorization、Cookie、Set-Cookie。
- 设计约束：
  - 日志接口注入 application，不让业务依赖具体 logger。
- 验收门禁：
  - `go test ./internal/infra/logging/...`
  - 脱敏测试覆盖敏感 header。
  - API 错误响应包含 traceId。
- 完成定义：
  - 所有 handler 都能访问 traceId。

### T-008：添加 Bearer Token 鉴权中间件

- Commit 名称：`build(auth): add bearer token middleware`
- 优先级：P0
- 阶段：Phase 0
- 原实现参考：
  - 用户确认单用户 + Token
- 目标文件/模块：
  - `internal/api/middleware/auth.go`
- 实现内容：
  - 实现 Bearer Token 校验。
  - 跳过 health、version、openapi。
  - 返回统一错误 schema。
- 设计约束：
  - 不实现多用户。
  - 不在日志中打印 token。
- 验收门禁：
  - 缺失 token 返回 401。
  - 错误 token 返回 401 或 403。
  - 正确 token 通过。
- 完成定义：
  - 所有受保护路由默认启用中间件。

## Phase 1：OpenAPI 契约

### T-020：定义公共 schema 和错误模型

- Commit 名称：`api(openapi): define common schemas and error model`
- 优先级：P0
- 阶段：Phase 1
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/ReturnData.kt`
- 目标文件/模块：
  - `api/openapi.yaml`
- 实现内容：
  - 定义 `Error`、`PageMeta`、`TaskStatus`、`Trace` 等共享 schema。
  - 替换旧 `ReturnData` 包络思路。
  - 为每个错误响应引用共享 `Error`。
- 设计约束：
  - 不使用 `isSuccess/errorMsg/data` 旧包络。
  - 所有错误必须有稳定 `code`。
- 验收门禁：
  - OpenAPI validator 通过。
  - 生成 types 成功。
  - 至少有 400、401、404、500 示例。
- 完成定义：
  - 公共 schema 可被后续所有 API 引用。

### T-021：定义书源管理 OpenAPI

- Commit 名称：`api(openapi): define book source management endpoints`
- 优先级：P0
- 阶段：Phase 1
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/controller/BookSourceController.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/BookSource.kt`
  - `legado/app/src/main/java/io/legado/app/data/dao/BookSourceDao.kt`
- 目标文件/模块：
  - `api/openapi.yaml`
- 实现内容：
  - 定义 `BookSource`、`BookSourceSummary`、规则对象 schema。
  - 定义列表、创建、详情、更新、删除、启用、发现启用、重排 API。
  - 定义关键词、分组、启用状态、分页参数。
- 设计约束：
  - path 使用 `sourceId`，请求/响应保留 `bookSourceUrl`。
  - 不暴露旧路由。
- 验收门禁：
  - OpenAPI validator 通过。
  - `oapi-codegen` 成功。
  - 示例覆盖最小书源和完整规则书源。
- 完成定义：
  - 书源管理 API 可直接生成 handler interface。

### T-022：定义书源导入 OpenAPI

- Commit 名称：`api(openapi): define import source endpoint`
- 优先级：P0
- 阶段：Phase 1
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt`
- 目标文件/模块：
  - `api/openapi.yaml`
  - `api/examples/book-source-import-*.json`
- 实现内容：
  - 定义导入请求，支持 raw JSON、remote URL、导入选项。
  - 定义导入结果，包含新增、更新、跳过、失败明细。
  - 增加 `sourceUrls` 聚合示例。
- 设计约束：
  - 导入接口不直接接受旧 API 包络。
  - 远程 URL 导入必须在 schema 中体现安全错误。
- 验收门禁：
  - 示例可以被 schema 校验。
  - 生成代码成功。
- 完成定义：
  - 导入 API 契约足够支撑单对象、数组、远程 URL、聚合文档。

### T-023：定义搜索和发现 OpenAPI

- Commit 名称：`api(openapi): define search and discovery endpoints`
- 优先级：P0
- 阶段：Phase 1
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/web/socket/BookSearchWebSocket.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/SearchModel.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt`
- 目标文件/模块：
  - `api/openapi.yaml`
- 实现内容：
  - 定义 `POST /api/v1/search`。
  - 定义 `POST /api/v1/discovery`。
  - 定义 `SearchBook` schema。
  - 定义 warnings，用于单源失败但整体成功。
- 设计约束：
  - 搜索可按 sourceIds、group、enabledOnly 控制范围。
  - 发现只使用 `enabledExplore` 书源。
- 验收门禁：
  - OpenAPI 示例覆盖单源和多源搜索。
  - 失败 warning schema 明确。
- 完成定义：
  - 后续搜索 use case 可按 schema 实现。

### T-024：定义书籍、章节和正文 OpenAPI

- Commit 名称：`api(openapi): define book and chapter endpoints`
- 优先级：P0
- 阶段：Phase 1
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/controller/BookController.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/Book.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/BookChapter.kt`
- 目标文件/模块：
  - `api/openapi.yaml`
- 实现内容：
  - 定义保存书籍、列表、详情、删除。
  - 定义刷新详情、刷新目录。
  - 定义章节列表和章节正文。
  - 定义保存阅读进度。
- 设计约束：
  - 不实现 Android 阅读 UI。
  - content endpoint 返回纯文本字段和元信息，不返回任意 HTML 页面。
- 验收门禁：
  - 生成代码成功。
  - 示例覆盖 SearchBook 转 Book。
- 完成定义：
  - 书架和正文 API 契约完整。

### T-025：定义替换规则 OpenAPI

- Commit 名称：`api(openapi): define replace rule endpoints`
- 优先级：P0
- 阶段：Phase 1
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/controller/ReplaceRuleController.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/ReplaceRule.kt`
- 目标文件/模块：
  - `api/openapi.yaml`
- 实现内容：
  - 定义替换规则 CRUD。
  - 定义测试替换接口。
  - 定义正则错误和超时错误。
- 设计约束：
  - 标题作用域和正文作用域必须在 schema 中明确。
- 验收门禁：
  - 示例覆盖 regex 和 plain string。
  - 生成代码成功。
- 完成定义：
  - 替换规则 API 可实现完整净化闭环。

### T-026：定义调试会话 OpenAPI

- Commit 名称：`api(openapi): define debug session endpoints`
- 优先级：P0
- 阶段：Phase 1
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/Debug.kt`
  - `legado/app/src/main/java/io/legado/app/web/socket/BookSourceDebugWebSocket.kt`
- 目标文件/模块：
  - `api/openapi.yaml`
- 实现内容：
  - 定义创建调试会话 API。
  - 定义查询调试会话 API。
  - 定义事件流 API。
  - 定义取消调试 API。
  - 定义调试模式：keyword、info_url、toc_url、content_url、discovery_url。
- 设计约束：
  - 推荐 SSE。
  - 不复用 Legado `++`、`--`、`::` 字符串协议。
- 验收门禁：
  - 事件 schema 包含阶段、消息、耗时、traceId。
  - 示例覆盖 keyword 调试。
- 完成定义：
  - 调试系统可按 OpenAPI 开发。

### T-027：定义校验任务 OpenAPI

- Commit 名称：`api(openapi): define check task endpoints`
- 优先级：P0
- 阶段：Phase 1
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/service/CheckSourceService.kt`
  - `legado/app/src/main/java/io/legado/app/model/CheckSource.kt`
- 目标文件/模块：
  - `api/openapi.yaml`
- 实现内容：
  - 定义创建校验任务 API。
  - 定义查询任务状态 API。
  - 定义任务事件流 API。
  - 定义取消任务 API。
  - 定义失败分类枚举。
- 设计约束：
  - 校验任务异步执行。
  - 单源失败不导致任务整体立刻失败。
- 验收门禁：
  - schema 覆盖 timeout、js_failed、toc_failed、content_failed 等分类。
  - 生成代码成功。
- 完成定义：
  - 校验任务契约可支持后续 worker 实现。

## Phase 2：领域模型与 SQLite

### T-040：实现书源领域模型

- Commit 名称：`domain(source): add book source aggregate and rule structs`
- 优先级：P0
- 阶段：Phase 2
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/entities/BookSource.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/SearchRule.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/ExploreRule.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/BookInfoRule.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/TocRule.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/ContentRule.kt`
- 目标文件/模块：
  - `internal/domain/source`
  - `internal/domain/rule`
- 实现内容：
  - 定义 BookSource aggregate。
  - 定义规则结构体。
  - 定义 required field validation。
  - 定义 sourceId 生成策略。
- 设计约束：
  - 保留 Legado 字段语义。
  - 领域模型不包含 JSON tag 以外的 API 适配逻辑。
- 验收门禁：
  - `go test ./internal/domain/source/...`
  - 空 `bookSourceUrl` 或 `bookSourceName` 校验失败。
  - JSON object 和 JSON string 规则解析测试通过。
- 完成定义：
  - 书源领域模型可被导入和 repository 使用。

### T-041：实现书籍、搜索结果和章节领域模型

- Commit 名称：`domain(book): add book search book and chapter entities`
- 优先级：P0
- 阶段：Phase 2
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/entities/Book.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/SearchBook.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/BookChapter.kt`
- 目标文件/模块：
  - `internal/domain/book`
- 实现内容：
  - 定义 Book。
  - 定义 SearchBook。
  - 定义 BookChapter。
  - 实现 SearchBook 转 Book。
  - 定义章节绝对 URL 所需字段。
- 设计约束：
  - 不把 Android UI 配置字段作为一期核心逻辑。
  - 进度字段必须保留。
- 验收门禁：
  - `go test ./internal/domain/book/...`
  - SearchBook 转 Book 保留关键字段。
  - Chapter index 校验覆盖负数。
- 完成定义：
  - 书籍模型可支撑搜索、书架、目录、正文。

### T-042：实现替换规则领域模型

- Commit 名称：`domain(replace): add replace rule entity`
- 优先级：P0
- 阶段：Phase 2
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/entities/ReplaceRule.kt`
- 目标文件/模块：
  - `internal/domain/replace`
- 实现内容：
  - 定义 ReplaceRule。
  - 实现 pattern 校验。
  - 实现 regex/plain 区分。
  - 定义 timeout 默认值。
- 设计约束：
  - 领域层只做校验，不执行具体替换。
- 验收门禁：
  - 空 pattern 校验失败。
  - 非法 regex 校验失败。
  - timeout 小于等于 0 时使用默认值。
- 完成定义：
  - 替换规则可被 repository 和 processor 使用。

### T-043：添加 SQLite migration 基线

- Commit 名称：`db(sqlite): add initial schema migrations`
- 优先级：P0
- 阶段：Phase 2
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/AppDatabase.kt`
- 目标文件/模块：
  - `internal/infra/sqlite/migrations`
  - `internal/infra/sqlite`
- 实现内容：
  - 添加迁移 runner。
  - 创建基础表：book_sources、books、search_books、chapters、replace_rules、cookies、source_variables、cache_entries、debug_sessions、debug_events、check_tasks、check_task_items。
  - 创建关键索引和唯一约束。
- 设计约束：
  - migration 不依赖 application。
  - 表字段保留兼容 JSON。
- 验收门禁：
  - `go test ./internal/infra/sqlite/...`
  - 空库迁移成功。
  - 重复运行迁移无副作用。
  - 关键唯一约束生效。
- 完成定义：
  - SQLite schema 可支撑后续 repository。

### T-044：实现书源 SQLite repository

- Commit 名称：`repo(source): implement sqlite book source repository`
- 优先级：P0
- 阶段：Phase 2
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/dao/BookSourceDao.kt`
  - `legado/app/src/main/java/io/legado/app/help/source/SourceHelp.kt`
- 目标文件/模块：
  - `internal/domain/source` ports
  - `internal/infra/sqlite/repositories`
- 实现内容：
  - 实现保存、批量保存、按 ID/URL 查询、分页列表、启用、发现启用、重排、删除。
  - 实现关键词、分组、启用状态筛选。
  - 删除书源时清理 source_variables 和相关 cache。
- 设计约束：
  - 事务封装在 repository。
  - 不删除已加入书架的书籍，按设计保留来源缺失状态。
- 验收门禁：
  - repository 测试覆盖 CRUD。
  - 筛选测试覆盖 name/group/url/comment。
  - 删除清理测试通过。
- 完成定义：
  - 书源 repository 可被导入和搜索 use case 使用。

### T-045：实现书籍和章节 SQLite repository

- Commit 名称：`repo(book): implement sqlite book and chapter repositories`
- 优先级：P0
- 阶段：Phase 2
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/dao/BookDao.kt`
  - `legado/app/src/main/java/io/legado/app/data/dao/BookChapterDao.kt`
- 目标文件/模块：
  - `internal/domain/book` ports
  - `internal/infra/sqlite/repositories`
- 实现内容：
  - 实现书籍保存、查询、列表、删除、进度保存。
  - 实现章节按书籍替换、列表、按 index 查询。
  - 实现章节正文缓存读取和写入。
- 设计约束：
  - 替换章节列表必须事务执行。
  - 删除书籍必须清理章节和正文缓存。
- 验收门禁：
  - `go test ./internal/infra/sqlite/repositories/...`
  - 章节 index 唯一约束测试。
  - 删除书籍清理测试。
- 完成定义：
  - 书架、目录、正文 use case 可使用 repository。

### T-046：实现替换规则 SQLite repository

- Commit 名称：`repo(replace): implement sqlite replace rule repository`
- 优先级：P0
- 阶段：Phase 2
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/dao/ReplaceRuleDao.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/ReplaceRule.kt`
- 目标文件/模块：
  - `internal/domain/replace` ports
  - `internal/infra/sqlite/repositories`
- 实现内容：
  - 实现替换规则 CRUD。
  - 实现按标题作用域查询启用规则。
  - 实现按正文作用域查询启用规则。
  - 实现排序。
- 设计约束：
  - scope 匹配语义必须文档化并测试。
- 验收门禁：
  - 标题规则查询测试。
  - 正文规则查询测试。
  - 禁用规则不返回。
- 完成定义：
  - 替换净化 processor 可读取规则。

## Phase 3：书源导入与管理

### T-060：实现单个 BookSource JSON 导入

- Commit 名称：`source(import): parse single legado book source json`
- 优先级：P0
- 阶段：Phase 3
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/BookSource.kt`
- 目标文件/模块：
  - `internal/app/source`
  - `test/fixtures/book_sources`
- 实现内容：
  - 解析单个 JSON object。
  - 校验 `bookSourceUrl` 和 `bookSourceName`。
  - 保存到 repository。
  - 返回导入明细。
- 设计约束：
  - 解析逻辑不写在 handler 中。
  - 保留未知兼容字段到 raw JSON 或兼容字段。
- 验收门禁：
  - 单个完整书源 fixture 导入成功。
  - 缺少 URL 返回 item error。
  - 缺少名称返回 item error。
- 完成定义：
  - API 层可调用 import use case 导入单对象。

### T-061：实现 BookSource 数组导入

- Commit 名称：`source(import): parse legado book source array json`
- 优先级：P0
- 阶段：Phase 3
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt`
- 目标文件/模块：
  - `internal/app/source`
  - `test/fixtures/book_sources/array.json`
- 实现内容：
  - 解析 JSON array。
  - 单条失败不影响其他条目。
  - 返回新增、更新、跳过、失败统计。
- 设计约束：
  - 批量保存使用事务，但允许预校验先拆分有效/无效。
- 验收门禁：
  - 混合有效和无效项测试通过。
  - 有效项落库。
  - 无效项返回错误明细。
- 完成定义：
  - 批量导入可用于真实书源文件。

### T-062：实现 sourceUrls 聚合导入

- Commit 名称：`source(import): parse sourceUrls aggregate import document`
- 优先级：P0
- 阶段：Phase 3
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt`
- 目标文件/模块：
  - `internal/app/source`
  - `internal/infra/fetcher`
- 实现内容：
  - 识别 JSON object 中的 `sourceUrls`。
  - 逐个 URL 下载并解析书源数组。
  - 每个 URL 独立记录成功或失败。
- 设计约束：
  - 所有远程 URL 经 Fetcher 安全策略。
- 验收门禁：
  - mock server 两个 URL，一个成功一个失败，结果正确。
  - 私网 URL 被拒绝。
- 完成定义：
  - 聚合文档可导入。

### T-063：实现远程 URL 导入和 `#requestWithoutUA`

- Commit 名称：`source(import): support remote url and requestWithoutUA imports`
- 优先级：P0
- 阶段：Phase 3
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt`
- 目标文件/模块：
  - `internal/app/source`
  - `internal/infra/fetcher`
- 实现内容：
  - 支持用户直接传入远程 URL。
  - URL 以 `#requestWithoutUA` 结尾时移除默认 UA。
  - 下载结果按书源数组解析。
- 设计约束：
  - 不绕过 Fetcher SSRF、timeout、size limit。
- 验收门禁：
  - mock server 断言普通 URL 有 UA。
  - mock server 断言 `#requestWithoutUA` 无默认 UA。
  - 非 JSON 响应返回结构化错误。
- 完成定义：
  - 远程导入行为与 Legado 兼容。

### T-064：实现导入更新策略和分组选项

- Commit 名称：`source(import): apply update policy and group options`
- 优先级：P0
- 阶段：Phase 3
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt`
  - `legado/app/src/main/java/io/legado/app/help/source/SourceHelp.kt`
- 目标文件/模块：
  - `internal/app/source`
- 实现内容：
  - 按 `lastUpdateTime` 判断默认选中更新。
  - 支持保留本地名称。
  - 支持保留本地分组。
  - 支持保留本地启用状态。
  - 支持追加或覆盖导入分组。
  - 归一化 `customOrder`。
- 设计约束：
  - 更新策略必须在 API 请求中显式可配置，并有默认值。
- 验收门禁：
  - 本地较新时默认跳过。
  - 远程较新时默认更新。
  - keepName/keepGroup/keepEnabled 均有测试。
- 完成定义：
  - 导入行为适合真实书源升级场景。

### T-065：实现书源管理 API handler

- Commit 名称：`source(api): implement source management handlers`
- 优先级：P0
- 阶段：Phase 3
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/controller/BookSourceController.kt`
  - `legado/app/src/main/java/io/legado/app/data/dao/BookSourceDao.kt`
- 目标文件/模块：
  - `internal/api/handlers`
  - `internal/app/source`
- 实现内容：
  - 接入生成的 OpenAPI interface。
  - 实现列表、创建、详情、更新、删除、启用、发现启用、重排。
  - 把 domain error 映射为 API error。
- 设计约束：
  - Handler 不直接访问 repository。
  - DTO 转换集中在 mapper。
- 验收门禁：
  - API 契约测试通过。
  - 401、404、400、200 响应测试覆盖。
- 完成定义：
  - 书源管理 API 可用。

## Phase 4：规则解析基础

### T-080：实现规则切分器

- Commit 名称：`rule(parser): implement source rule splitting`
- 优先级：P0
- 阶段：Phase 4
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/RuleAnalyzer.kt`
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeRule.kt`
- 目标文件/模块：
  - `internal/infra/rule/parser`
- 实现内容：
  - 支持常见规则链切分。
  - 处理括号、方括号、引号中的分隔符。
  - 输出结构化 rule steps。
- 设计约束：
  - 切分器不能直接执行规则。
- 验收门禁：
  - golden tests 覆盖 `@`、`&&`、`||`、`%%`、括号嵌套、引号。
  - 不平衡规则返回 syntax error。
- 完成定义：
  - 后续 CSS/XPath/JSONPath/Regex/JS adapter 可消费 rule steps。

### T-081：实现 CSS/JSoup 类选择器 adapter

- Commit 名称：`rule(css): implement css selector extraction adapter`
- 优先级：P0
- 阶段：Phase 4
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeByJSoup.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt`
- 目标文件/模块：
  - `internal/infra/rule/css`
- 实现内容：
  - 解析 HTML。
  - 支持文本、HTML、属性提取。
  - 支持元素列表。
- 设计约束：
  - adapter 不知道 BookSource。
- 验收门禁：
  - 搜索列表 HTML fixture 可提取 name、author、bookUrl。
  - 空选择器返回空结果或 typed error。
- 完成定义：
  - 常见 HTML 书源可被解析。

### T-082：实现 XPath adapter

- Commit 名称：`rule(xpath): implement xpath extraction adapter`
- 优先级：P0
- 阶段：Phase 4
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeByXPath.kt`
- 目标文件/模块：
  - `internal/infra/rule/xpath`
- 实现内容：
  - 支持 XPath 字符串和节点列表提取。
  - 支持 HTML 文档容错解析。
- 设计约束：
  - XPath 库选择需写入 go.mod，并有测试。
- 验收门禁：
  - XPath fixture 可提取目录列表。
  - 非法 XPath 返回 rule_parse_failed。
- 完成定义：
  - XPath 书源基本可用。

### T-083：实现 JSONPath adapter

- Commit 名称：`rule(jsonpath): implement jsonpath extraction adapter`
- 优先级：P0
- 阶段：Phase 4
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeByJSonPath.kt`
- 目标文件/模块：
  - `internal/infra/rule/jsonpath`
- 实现内容：
  - 支持 JSON 字符串解析。
  - 支持字符串和列表提取。
  - 支持非法 JSON 错误。
- 设计约束：
  - JSONPath 失败不得 panic。
- 验收门禁：
  - JSON 搜索 fixture 可提取列表。
  - 非法 JSON 和非法 JSONPath 都有测试。
- 完成定义：
  - JSON API 类书源基本可用。

### T-084：实现 Regex adapter 和 URL 归一化

- Commit 名称：`rule(regex): implement regex extraction and url normalization`
- 优先级：P0
- 阶段：Phase 4
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeByRegex.kt`
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/CustomUrl.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/BookChapter.kt`
- 目标文件/模块：
  - `internal/infra/rule/regex`
  - `internal/infra/rule/url`
- 实现内容：
  - 实现正则提取和替换片段。
  - 实现相对 URL 转绝对 URL。
  - 实现自定义 URL 属性保留。
- 设计约束：
  - 正则执行需限制输入大小或超时策略。
- 验收门禁：
  - 相对 URL、绝对 URL、自定义属性 URL 测试通过。
  - Regex 提取测试通过。
- 完成定义：
  - 正则和 URL 能支撑目录/正文解析。

### T-085：实现 RuleAnalyzer 组合入口

- Commit 名称：`rule(engine): compose rule analyzer interface`
- 优先级：P0
- 阶段：Phase 4
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeRule.kt`
- 目标文件/模块：
  - `internal/domain/rule`
  - `internal/infra/rule`
- 实现内容：
  - 实现 `GetString`、`GetStringList`、`GetElements`。
  - 按 rule step 调用 CSS/XPath/JSONPath/Regex/JS adapter。
  - 支持 baseUrl、redirectUrl、content 上下文。
- 设计约束：
  - RuleAnalyzer 依赖 ScriptRuntime port，而不是具体 goja。
- 验收门禁：
  - HTML、JSON、XPath、Regex、链式规则 golden tests 通过。
  - 错误类型稳定。
- 完成定义：
  - WebBook 链路可调用统一规则入口。

## Phase 5：HTTP 抓取与 JS 沙箱

### T-100：实现 FetchRequest 构建与 HTTP 抓取

- Commit 名称：`fetcher(http): implement request builder and http fetcher`
- 优先级：P0
- 阶段：Phase 5
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeUrl.kt`
- 目标文件/模块：
  - `internal/domain/fetch`
  - `internal/infra/fetcher`
- 实现内容：
  - 定义 FetchRequest/FetchResponse。
  - 实现 URL、method、headers、body、query 构建。
  - 实现 gzip 等常见响应处理。
- 设计约束：
  - Fetcher 不知道具体业务 use case。
- 验收门禁：
  - mock server 验证 method、query、body、headers。
  - 压缩响应可读取。
- 完成定义：
  - 规则链路可通过 Fetcher 获取响应。

### T-101：实现 Fetcher 安全边界

- Commit 名称：`fetcher(security): enforce timeout size redirect and ssrf policy`
- 优先级：P0
- 阶段：Phase 5
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeUrl.kt`
- 目标文件/模块：
  - `internal/infra/fetcher`
- 实现内容：
  - 请求超时。
  - 响应大小限制。
  - redirect 次数限制。
  - redirect 目标安全检查。
  - SSRF 私网/回环/link-local/metadata 地址拦截。
- 设计约束：
  - 安全策略默认开启。
  - 测试环境可配置允许本地 mock server。
- 验收门禁：
  - timeout 测试。
  - oversized response 测试。
  - private IP blocked 测试。
  - redirect to private IP blocked 测试。
- 完成定义：
  - 外部抓取不会默认成为 SSRF 通道。

### T-102：实现 Cookie 存储和合并

- Commit 名称：`fetcher(cookie): implement source scoped cookie jar`
- 优先级：P0
- 阶段：Phase 5
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/help/http/CookieStore.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/Cookie.kt`
- 目标文件/模块：
  - `internal/domain/fetch`
  - `internal/infra/fetcher`
  - `internal/infra/sqlite/repositories`
- 实现内容：
  - 按 domain 保存 Cookie。
  - 合并持久 Cookie 和 session Cookie。
  - 写入和读取 Cookie repository。
  - 日志脱敏。
- 设计约束：
  - Cookie API 不暴露明文到普通业务响应。
- 验收门禁：
  - Set-Cookie 保存测试。
  - Cookie 合并测试。
  - 日志脱敏测试。
- 完成定义：
  - 登录态相关书源可复用 Cookie。

### T-103：实现受限 goja runtime

- Commit 名称：`script(runtime): add goja runtime wrapper`
- 优先级：P0
- 阶段：Phase 5
- 原实现参考：
  - `legado/modules/rhino/src/main/java/com/script/ScriptEngine.kt`
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeRule.kt`
- 目标文件/模块：
  - `internal/domain/rule`
  - `internal/infra/script`
- 实现内容：
  - 封装 goja runtime。
  - 定义 ScriptBindings。
  - 定义 ScriptPolicy。
  - 实现 Eval。
- 设计约束：
  - 不暴露文件系统、进程、任意网络。
  - Runtime 每次执行隔离或清理状态。
- 验收门禁：
  - 简单 JS 返回值测试。
  - panic recover 测试。
  - 禁止访问宿主能力测试。
- 完成定义：
  - JS adapter 可调用 runtime。

### T-104：实现 JS 超时与安全绑定

- Commit 名称：`script(security): enforce timeout and safe bindings`
- 优先级：P0
- 阶段：Phase 5
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/entities/BaseSource.kt`
  - `legado/app/src/main/java/io/legado/app/model/Debug.kt`
- 目标文件/模块：
  - `internal/infra/script`
- 实现内容：
  - 支持 context deadline。
  - 支持无限循环中断。
  - 暴露 source/book/chapter/result/cache/log/url.resolve 等白名单 API。
  - 错误携带执行阶段。
- 设计约束：
  - cache API 受 source namespace 限制。
  - log 输出走 DebugPublisher 或 logger。
- 验收门禁：
  - 无限循环被中断。
  - cache get/put 只访问当前 source namespace。
  - 错误脱敏。
- 完成定义：
  - Header JS、loginCheckJs、rule JS 可安全执行。

### T-105：实现 Header JS 和 loginCheckJs

- Commit 名称：`script(integration): support header js and login check js`
- 优先级：P0
- 阶段：Phase 5
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/entities/BaseSource.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
- 目标文件/模块：
  - `internal/app/source`
  - `internal/infra/fetcher`
  - `internal/infra/script`
- 实现内容：
  - Header 字段为 JSON 时直接解析。
  - Header 字段为 JS 时执行并要求返回 JSON。
  - 抓取响应后执行 `loginCheckJs`。
  - 允许 `loginCheckJs` 转换响应或返回错误。
- 设计约束：
  - JS 返回结构必须文档化。
- 验收门禁：
  - Header JSON 测试。
  - Header JS 测试。
  - loginCheckJs 修改响应体测试。
  - loginCheckJs 超时测试。
- 完成定义：
  - 常见登录检测类书源可运行。

## Phase 6：搜索与发现链路

### T-120：实现单源搜索 use case

- Commit 名称：`webbook(search): implement single source search use case`
- 优先级：P0
- 阶段：Phase 6
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt`
- 目标文件/模块：
  - `internal/app/search`
  - `internal/app/webbook`
- 实现内容：
  - 加载书源。
  - 构建搜索 URL。
  - Fetch。
  - 执行 loginCheckJs。
  - 解析 SearchBook 列表。
  - 支持 bookUrlPattern 直接详情页。
- 设计约束：
  - use case 不直接访问 HTTP client。
- 验收门禁：
  - mock HTML 搜索源返回结果。
  - bookUrlPattern 直接详情测试。
  - 空搜索 URL 返回 validation error。
- 完成定义：
  - 单源搜索可被 API 和调试使用。

### T-121：实现多源搜索编排

- Commit 名称：`webbook(search): implement multi source search orchestration`
- 优先级：P0
- 阶段：Phase 6
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/webBook/SearchModel.kt`
- 目标文件/模块：
  - `internal/app/search`
- 实现内容：
  - 根据 sourceIds/group/enabledOnly 解析搜索范围。
  - 并发调用单源搜索。
  - 单源失败变成 warning。
  - 按书名/作者合并重复结果。
  - 支持精准搜索。
- 设计约束：
  - 全局并发和每源并发均受配置限制。
- 验收门禁：
  - 两个源返回同名同作者时合并。
  - 一个源失败时整体仍成功并包含 warning。
  - 禁用源默认不参与。
- 完成定义：
  - 多源搜索 API 可实现。

### T-122：实现搜索 API handler

- Commit 名称：`webbook(api): implement search endpoint`
- 优先级：P0
- 阶段：Phase 6
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/web/socket/BookSearchWebSocket.kt`
- 目标文件/模块：
  - `internal/api/handlers`
  - `internal/app/search`
- 实现内容：
  - 接入 `POST /api/v1/search`。
  - 参数校验。
  - 返回搜索结果和 warnings。
- 设计约束：
  - Handler 不直接调用 Fetcher。
- 验收门禁：
  - API contract test 通过。
  - 缺少 keyword 返回 400。
  - 正常搜索返回 200。
- 完成定义：
  - 搜索 API 对外可用。

### T-123：实现发现分类和发现列表

- Commit 名称：`webbook(discovery): implement discovery categories and list use case`
- 优先级：P1
- 阶段：Phase 6
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/ExploreRule.kt`
- 目标文件/模块：
  - `internal/app/discovery`
- 实现内容：
  - 解析 `exploreUrl` 分类。
  - 抓取发现 URL。
  - 使用 `ruleExplore` 或回退 `ruleSearch` 解析列表。
- 设计约束：
  - `exploreScreen` 一期只保留字段，不做复杂筛选 UI。
- 验收门禁：
  - mock 发现源返回结果。
  - 无 exploreUrl 返回明确错误。
  - disabled explore 源不参与默认发现。
- 完成定义：
  - 发现 API 可实现。

## Phase 7：详情、目录、正文链路

### T-140：实现详情解析 use case

- Commit 名称：`webbook(info): implement book info parsing`
- 优先级：P0
- 阶段：Phase 7
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookInfo.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/BookInfoRule.kt`
- 目标文件/模块：
  - `internal/app/book`
  - `internal/app/webbook`
- 实现内容：
  - 支持 `infoHtml` 直接解析。
  - 无 `infoHtml` 时抓取 `bookUrl`。
  - 执行 `BookInfoRule.init`。
  - 提取 name、author、kind、wordCount、latestChapter、intro、coverUrl、tocUrl。
  - 相对 URL 转绝对 URL。
- 设计约束：
  - 文件类书源 downloadUrls 一期保留字段，不实现下载闭环。
- 验收门禁：
  - mock 详情页解析字段完整。
  - coverUrl/tocUrl 相对 URL 正确转换。
  - init rule 测试通过。
- 完成定义：
  - 详情刷新 API 可调用。

### T-141：实现目录解析 use case

- Commit 名称：`webbook(toc): implement chapter list parsing`
- 优先级：P0
- 阶段：Phase 7
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookChapterList.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/TocRule.kt`
- 目标文件/模块：
  - `internal/app/book`
  - `internal/app/webbook`
- 实现内容：
  - 支持 `preUpdateJs`。
  - 抓取 tocUrl 或使用 tocHtml。
  - 解析 chapterList、chapterName、chapterUrl、isVolume、isVip、isPay、updateTime。
  - 支持 nextTocUrl。
  - 去重、排序、重建 index。
  - 支持 formatJs。
  - 事务替换章节列表。
- 设计约束：
  - 空目录返回 `toc_empty`。
  - formatJs 走受限 JS runtime。
- 验收门禁：
  - 单页目录测试。
  - 多页目录测试。
  - 卷标测试。
  - formatJs 测试。
- 完成定义：
  - 章节目录可刷新并持久化。

### T-142：实现正文解析 use case

- Commit 名称：`webbook(content): implement chapter content parsing`
- 优先级：P0
- 阶段：Phase 7
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookContent.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/ContentRule.kt`
- 目标文件/模块：
  - `internal/app/content`
  - `internal/app/webbook`
- 实现内容：
  - 先查章节正文缓存。
  - 抓取章节 URL。
  - 解析正文。
  - 支持 title rule 更新章节标题。
  - 支持 nextContentUrl。
  - 支持多页正文合并。
  - 支持 `replaceRegex`。
  - 保存正文缓存。
- 设计约束：
  - 非卷章节正文为空返回 `content_empty`。
  - 多页正文顺序必须确定。
- 验收门禁：
  - 单页正文测试。
  - 多页正文测试。
  - 空正文错误测试。
  - 缓存命中避免二次网络请求。
- 完成定义：
  - 正文 API 可返回处理前或处理后的内容。

### T-143：实现书籍、章节、正文 API handler

- Commit 名称：`webbook(api): implement book chapter and content endpoints`
- 优先级：P0
- 阶段：Phase 7
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/controller/BookController.kt`
- 目标文件/模块：
  - `internal/api/handlers`
  - `internal/app/book`
  - `internal/app/content`
- 实现内容：
  - 保存书籍。
  - 查询书架。
  - 删除书籍。
  - 刷新详情。
  - 刷新目录。
  - 查询章节列表。
  - 获取章节正文。
  - 保存阅读进度。
- 设计约束：
  - Handler 只做 DTO 转换。
- 验收门禁：
  - API contract tests。
  - 书籍生命周期测试。
  - 内容缓存测试。
- 完成定义：
  - 在线阅读核心 API 闭环可用。

## Phase 8：替换净化

### T-160：实现替换规则 CRUD API

- Commit 名称：`replace(api): implement replace rule crud endpoints`
- 优先级：P0
- 阶段：Phase 8
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/controller/ReplaceRuleController.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/ReplaceRule.kt`
- 目标文件/模块：
  - `internal/app/replace`
  - `internal/api/handlers`
- 实现内容：
  - 实现列表、创建、更新、删除。
  - 校验 pattern 和 regex。
  - 保持 order。
- 设计约束：
  - API 错误使用统一 Error schema。
- 验收门禁：
  - CRUD contract tests。
  - 非法 regex 返回 400。
- 完成定义：
  - 替换规则可管理。

### T-161：实现替换测试接口

- Commit 名称：`replace(api): implement replace rule test endpoint`
- 优先级：P0
- 阶段：Phase 8
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/controller/ReplaceRuleController.kt`
- 目标文件/模块：
  - `internal/app/replace`
  - `internal/api/handlers`
- 实现内容：
  - 输入规则和文本。
  - 返回替换后的文本。
  - 返回 regex 错误或超时错误。
- 设计约束：
  - 不保存测试规则。
- 验收门禁：
  - plain replace 测试。
  - regex replace 测试。
  - invalid regex 测试。
- 完成定义：
  - 用户可调试替换规则。

### T-162：实现 ContentProcessor

- Commit 名称：`replace(processor): implement content post processor`
- 优先级：P0
- 阶段：Phase 8
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/help/book/ContentProcessor.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/BookChapter.kt`
- 目标文件/模块：
  - `internal/app/replace`
  - `internal/app/content`
- 实现内容：
  - 去除重复标题。
  - 应用标题替换规则。
  - 应用正文替换规则。
  - 整理段落。
  - 返回命中规则列表。
- 设计约束：
  - 处理器依赖 ReplaceRuleRepository port。
- 验收门禁：
  - 重复标题移除测试。
  - 标题规则和正文规则分别测试。
  - 正文 endpoint 集成测试覆盖替换。
- 完成定义：
  - 获取正文时可返回净化后的内容。

### T-163：实现正则超时/限制策略

- Commit 名称：`replace(security): enforce regex execution limits`
- 优先级：P0
- 阶段：Phase 8
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/entities/ReplaceRule.kt`
  - `legado/app/src/main/java/io/legado/app/help/book/ContentProcessor.kt`
- 目标文件/模块：
  - `internal/app/replace`
- 实现内容：
  - 对正则执行添加输入大小限制或 timeout 策略。
  - 超时后返回稳定错误。
  - 按设计禁用或标记失败规则。
- 设计约束：
  - 不允许长时间阻塞 worker。
- 验收门禁：
  - 慢正则或大输入测试不会卡死。
  - 超时结果可观测。
- 完成定义：
  - 替换净化具备 DoS 防护。

## Phase 9：调试系统

### T-180：实现调试会话模型和 repository

- Commit 名称：`debug(model): define debug session and event persistence`
- 优先级：P0
- 阶段：Phase 9
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/Debug.kt`
- 目标文件/模块：
  - `internal/domain/task`
  - `internal/app/debug`
  - `internal/infra/sqlite/repositories`
- 实现内容：
  - 定义 DebugSession。
  - 定义 DebugEvent。
  - 持久化 session 和 events。
- 设计约束：
  - 事件消息必须可脱敏。
- 验收门禁：
  - session 创建、追加事件、查询事件测试。
- 完成定义：
  - 调试事件可被 API 查询和流式消费。

### T-181：实现调试事件发布器

- Commit 名称：`debug(publisher): implement sse event publisher`
- 优先级：P0
- 阶段：Phase 9
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/web/socket/BookSourceDebugWebSocket.kt`
- 目标文件/模块：
  - `internal/app/debug`
  - `internal/api/handlers`
- 实现内容：
  - 实现 SSE 事件流。
  - 支持历史事件补发。
  - 支持 session 关闭。
- 设计约束：
  - SSE handler 不执行业务，只订阅事件。
- 验收门禁：
  - SSE 测试收到 started/log/completed。
  - 断开连接不影响后台任务。
- 完成定义：
  - 前端或调用方可实时观察调试。

### T-182：实现关键词调试链路

- Commit 名称：`debug(flow): implement keyword source debug flow`
- 优先级：P0
- 阶段：Phase 9
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/Debug.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
- 目标文件/模块：
  - `internal/app/debug`
  - `internal/app/search`
  - `internal/app/book`
  - `internal/app/content`
- 实现内容：
  - keyword 模式执行搜索、详情、目录、正文。
  - 每阶段发出 started/completed/failed。
  - 日志包含耗时和阶段。
- 设计约束：
  - 调试使用同一业务 use case，不复制解析逻辑。
- 验收门禁：
  - mock 书源调试输出完整阶段。
  - 搜索空结果输出 failed。
- 完成定义：
  - 关键词调试可替代 Legado 书源调试主路径。

### T-183：实现 URL 阶段调试和取消

- Commit 名称：`debug(flow): implement url debug modes and cancellation`
- 优先级：P0
- 阶段：Phase 9
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/Debug.kt`
- 目标文件/模块：
  - `internal/app/debug`
  - `internal/api/handlers`
- 实现内容：
  - 支持 info_url、toc_url、content_url、discovery_url。
  - 实现取消 session。
  - 取消时中断 fetch 和 JS。
- 设计约束：
  - 新 API 使用显式 mode，不使用 `++`、`--`、`::`。
- 验收门禁：
  - 每个 mode 有测试。
  - cancel 后任务状态为 cancelled。
- 完成定义：
  - 调试入口覆盖 Legado 原有分支能力。

### T-184：实现调试 API handler

- Commit 名称：`debug(api): implement debug session endpoints`
- 优先级：P0
- 阶段：Phase 9
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/web/socket/BookSourceDebugWebSocket.kt`
- 目标文件/模块：
  - `internal/api/handlers`
- 实现内容：
  - 创建 session。
  - 查询 session。
  - 事件流。
  - 取消 session。
- 设计约束：
  - 所有输出日志脱敏。
- 验收门禁：
  - contract tests。
  - 未授权请求被拒绝。
- 完成定义：
  - 调试 API 对外可用。

## Phase 10：书源批量校验

### T-200：实现校验任务模型和 repository

- Commit 名称：`check(model): define check task status machine`
- 优先级：P0
- 阶段：Phase 10
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/service/CheckSourceService.kt`
- 目标文件/模块：
  - `internal/domain/task`
  - `internal/app/check`
  - `internal/infra/sqlite/repositories`
- 实现内容：
  - 定义 CheckTask。
  - 定义 CheckTaskItem。
  - 定义状态机。
  - 持久化任务和单项结果。
- 设计约束：
  - 状态转换必须受控。
- 验收门禁：
  - 状态转换测试。
  - 非法状态转换失败。
- 完成定义：
  - 校验 worker 可记录进度。

### T-201：实现单书源校验逻辑

- Commit 名称：`check(worker): implement single source validation flow`
- 优先级：P0
- 阶段：Phase 10
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/service/CheckSourceService.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
- 目标文件/模块：
  - `internal/app/check`
- 实现内容：
  - 校验搜索。
  - 校验发现。
  - 校验详情。
  - 校验目录。
  - 校验正文。
  - 记录 respondTime。
- 设计约束：
  - 复用已有 search/info/toc/content use case。
- 验收门禁：
  - mock 正常源通过。
  - 搜索空结果 classified search_failed。
  - 空目录 classified toc_failed。
  - 空正文 classified content_failed。
- 完成定义：
  - 单源可独立校验。

### T-202：实现批量校验 worker

- Commit 名称：`check(worker): implement concurrent check task runner`
- 优先级：P0
- 阶段：Phase 10
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/service/CheckSourceService.kt`
- 目标文件/模块：
  - `internal/worker/check`
  - `internal/app/check`
- 实现内容：
  - 并发校验多个书源。
  - 支持取消。
  - 支持超时。
  - 记录任务进度。
- 设计约束：
  - 并发数受配置限制。
  - 单项失败不终止整个任务。
- 验收门禁：
  - 多源并发测试。
  - cancel 测试。
  - timeout 测试。
- 完成定义：
  - 批量校验任务可后台运行。

### T-203：实现失败分类和书源状态更新

- Commit 名称：`check(worker): classify failures and update source diagnostics`
- 优先级：P0
- 阶段：Phase 10
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/service/CheckSourceService.kt`
  - `legado/app/src/main/java/io/legado/app/model/Debug.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/BookSource.kt`
- 目标文件/模块：
  - `internal/app/check`
  - `internal/app/source`
- 实现内容：
  - 分类 timeout、js_failed、website_failed、search_failed、discovery_failed、toc_failed、content_failed。
  - 更新书源 respondTime。
  - 更新错误摘要。
  - 清理旧失效标记等价能力。
- 设计约束：
  - 不把中文分组作为唯一状态源，使用结构化 code。
- 验收门禁：
  - 每种分类有测试。
  - respondTime 更新测试。
- 完成定义：
  - 校验结果可用于书源维护。

### T-204：实现校验 API handler

- Commit 名称：`check(api): implement check task endpoints`
- 优先级：P0
- 阶段：Phase 10
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/service/CheckSourceService.kt`
- 目标文件/模块：
  - `internal/api/handlers`
  - `internal/app/check`
- 实现内容：
  - 创建任务。
  - 查询任务。
  - 事件流。
  - 取消任务。
- 设计约束：
  - Handler 不直接启动 goroutine，交给 runner。
- 验收门禁：
  - contract tests。
  - 创建任务返回 taskId。
  - 查询任务返回进度。
  - 取消任务生效。
- 完成定义：
  - 校验 API 对外可用。

## Phase 11：书架、进度、图片代理

### T-220：实现书架生命周期

- Commit 名称：`bookshelf(api): implement book lifecycle endpoints`
- 优先级：P0
- 阶段：Phase 11
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/controller/BookController.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/Book.kt`
- 目标文件/模块：
  - `internal/app/book`
  - `internal/api/handlers`
- 实现内容：
  - 保存书籍。
  - 查询书架列表。
  - 查询书籍详情。
  - 删除书籍。
- 设计约束：
  - SearchBook 转 Book 在 application 层完成。
- 验收门禁：
  - 保存后可查询。
  - 删除后章节和正文缓存清理。
- 完成定义：
  - 基础书架可用。

### T-221：实现阅读进度保存

- Commit 名称：`bookshelf(progress): implement reading progress persistence`
- 优先级：P0
- 阶段：Phase 11
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/data/entities/Book.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/BookProgress.kt`
  - `legado/app/src/main/java/io/legado/app/api/controller/BookController.kt`
- 目标文件/模块：
  - `internal/app/book`
  - `internal/api/handlers`
- 实现内容：
  - 保存章节 index。
  - 保存章节位置。
  - 更新阅读时间。
- 设计约束：
  - 不实现 UI 阅读状态。
- 验收门禁：
  - 保存后查询书籍详情可见进度。
  - 非法章节 index 返回 400 或 404。
- 完成定义：
  - API 调用方可同步阅读进度。

### T-222：实现封面和正文图片代理

- Commit 名称：`bookshelf(image): implement cover and content image proxy`
- 优先级：P1
- 阶段：Phase 11
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/api/controller/BookController.kt`
  - `legado/app/src/main/java/io/legado/app/model/ImageProvider.kt`
  - `legado/app/src/main/java/io/legado/app/model/BookCover.kt`
- 目标文件/模块：
  - `internal/app/content`
  - `internal/infra/fetcher`
  - `internal/api/handlers`
- 实现内容：
  - 根据 URL 代理封面。
  - 根据章节图片 URL 代理正文图片。
  - 缓存图片。
  - 应用 Fetcher 安全策略。
- 设计约束：
  - 图片 URL 也必须经过 SSRF 防护。
  - 不实现 Android Glide 行为。
- 验收门禁：
  - mock 图片可代理。
  - 私网图片 URL 被拒绝。
  - 超大图片被拒绝。
- 完成定义：
  - 文本正文中的图片可通过服务安全访问。

## Phase 12：系统质量与端到端

### T-240：添加 OpenAPI 契约测试

- Commit 名称：`test(contract): add generated api contract tests`
- 优先级：P0
- 阶段：Phase 12
- 原实现参考：
  - `legado/api.md`
- 目标文件/模块：
  - `test/openapi`
  - `internal/api/handlers`
- 实现内容：
  - 校验 OpenAPI 文件。
  - 校验生成代码。
  - 校验 handler 响应符合 schema。
- 设计约束：
  - CI 中必须运行。
- 验收门禁：
  - OpenAPI validator 通过。
  - `go generate ./...` 无 diff。
  - 主要接口 contract tests 通过。
- 完成定义：
  - API 契约不会无意漂移。

### T-241：添加 mock 书源端到端测试

- Commit 名称：`test(e2e): add full mock online source flow`
- 优先级：P0
- 阶段：Phase 12
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookInfo.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookChapterList.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookContent.kt`
- 目标文件/模块：
  - `test/e2e`
  - `test/fixtures`
- 实现内容：
  - 启动 mock HTTP 书源。
  - 导入书源。
  - 搜索。
  - 保存书籍。
  - 刷新详情。
  - 刷新目录。
  - 获取正文。
  - 应用替换。
  - 保存进度。
- 设计约束：
  - 不访问真实外网。
- 验收门禁：
  - e2e 测试稳定通过。
  - 网络请求次数可断言缓存命中。
- 完成定义：
  - 核心闭环有自动化保障。

### T-242：添加真实远程书源集合 E2E 门禁

- Commit 名称：`test(e2e): add real source collection release gate`
- 优先级：P0
- 阶段：Phase 12
- 开始前源码探索：
  - 阅读 Legado 导入链路和在线书源链路，确认真实集合导入、候选书源筛选、搜索、详情、目录、正文阶段的原实现语义。
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt`
  - `legado/app/src/main/java/io/legado/app/help/source/SourceHelp.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookInfo.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookChapterList.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookContent.kt`
- 目标文件/模块：
  - `test/e2e`
  - `test/e2e/real_source_collection_test.go`
  - `test/reports`
  - `internal/app/source`
  - `internal/app/search`
  - `internal/app/book`
  - `internal/app/content`
- 实现内容：
  - 增加 manual 或 nightly E2E，用真实 URL 下载书源集合。
  - URL 固定为 `https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361`，同时允许通过 `READER_REAL_E2E_SOURCE_COLLECTION_URL` 覆盖。
  - 解析集合并统计条数、导入成功、更新、跳过和失败数量。
  - 自动筛选候选书源：`enabled = true`、`bookSourceType = 0`、具备 `searchUrl`、`ruleSearch`、`ruleBookInfo`、`ruleToc`、`ruleContent`，优先 HTTP/HTTPS `bookSourceUrl`。
  - 使用配置关键词执行搜索，默认关键词应选择稳定通用词，例如 `斗破苍穹` 或测试配置中的 `READER_REAL_E2E_KEYWORD`。
  - 对候选源按顺序尝试搜索 -> 详情 -> 目录 -> 第一章正文，直到至少一个成功或候选耗尽。
  - 输出 `real_source_collection_e2e_report.json` 和 `real_source_collection_e2e_report.md`。
- 设计约束：
  - 该测试默认不进入普通 PR CI，必须由 `READER_REAL_E2E_ENABLED=true` 显式开启。
  - 真实外网测试不能替代 mock E2E 和单元测试。
  - 网络失败、集合不可用、目标站点不可用、规则失效、安全策略拦截、实现缺陷必须分类清楚。
  - 实现缺陷分类不得放行 release。
- 单元测试要求：
  - 候选书源筛选逻辑必须有纯单元测试，使用本地 fixture。
  - 真实集合报告生成器必须有单元测试。
  - 失败分类逻辑必须有单元测试。
- 验收门禁：
  - `go test ./test/e2e -run TestRealSourceCollectionE2E` 在显式启用环境变量时可运行。
  - 未设置 `READER_REAL_E2E_ENABLED=true` 时测试应 skip，而不是失败。
  - 下载集合后能解析为 Legado 书源数组。
  - 报告包含测试时间、集合 URL、HTTP 状态、响应大小、解析条数、候选源、关键词、阶段结果和失败分类。
  - 至少 1 个候选源完成搜索 -> 详情 -> 目录 -> 正文，或报告明确标记 release 不可放行的实现缺陷。
- 完成定义：
  - 最终 release gate 能真实验证用户指定集合，不再只依赖 mock 数据。

### T-243：添加安全测试

- Commit 名称：`test(security): add ssrf script and regex safety tests`
- 优先级：P0
- 阶段：Phase 12
- 原实现参考：
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeUrl.kt`
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeRule.kt`
  - `legado/app/src/main/java/io/legado/app/help/book/ContentProcessor.kt`
- 目标文件/模块：
  - `test/security`
- 实现内容：
  - SSRF 私网拦截测试。
  - redirect SSRF 测试。
  - JS 无限循环测试。
  - JS 宿主逃逸测试。
  - Regex 限制测试。
  - 日志脱敏测试。
- 设计约束：
  - 安全测试必须进入 CI。
- 验收门禁：
  - 所有安全测试通过。
  - 失败时不产生敏感日志。
- 完成定义：
  - 核心安全边界可回归验证。

### T-244：添加 CI 门禁

- Commit 名称：`ci: add lint test generate and migration gates`
- 优先级：P0
- 阶段：Phase 12
- 原实现参考：
  - 无直接原实现
- 目标文件/模块：
  - `.github/workflows`
  - `Makefile`
  - `scripts`
- 实现内容：
  - 运行 go generate。
  - 运行 go test。
  - 运行 go vet。
  - 运行 staticcheck，如果引入。
  - 运行 OpenAPI validator。
  - 运行 SQLite migration tests。
- 设计约束：
  - CI 不访问真实外网。
- 验收门禁：
  - CI 在干净 checkout 通过。
  - 生成代码漂移会失败。
- 完成定义：
  - 后续 PR 有自动质量门禁。

### T-245：添加开发和部署说明

- Commit 名称：`docs(dev): add local development and verification guide`
- 优先级：P1
- 阶段：Phase 12
- 原实现参考：
  - `docs/Requirement.md`
  - `docs/Design.md`
  - `docs/Tasks.md`
- 目标文件/模块：
  - `docs/Development.md`
  - `docs/API.md`
  - `README.md`
- 实现内容：
  - 本地启动说明。
  - 配置说明。
  - OpenAPI 生成说明。
  - SQLite 迁移说明。
  - 常见调试流程。
  - 示例 curl。
- 设计约束：
  - 不把真实 token 写入文档。
- 验收门禁：
  - 按文档命令可启动服务。
  - curl 示例可对 mock 环境运行。
- 完成定义：
  - 新开发者可按文档完成本地开发。

## 5. 阶段性完成标准

### Phase 0 完成标准

- 文档齐全。
- Go 项目骨架可测试。
- OpenAPI 生成流程可运行。
- 配置、日志、鉴权基础可用。

### Phase 1 完成标准

- `api/openapi.yaml` 定义所有一期 API。
- `oapi-codegen` 生成代码成功。
- 所有 endpoint 有 request/response 示例。
- 旧路由不出现在 OpenAPI 中。

### Phase 2 完成标准

- domain 模型完整。
- SQLite migration 可用。
- repository CRUD 通过测试。
- 删除和事务策略明确。

### Phase 3 完成标准

- 单对象、数组、sourceUrls、远程 URL、`#requestWithoutUA` 均可导入。
- 书源管理 API 可用。
- 导入结果可逐项解释。

### Phase 4 完成标准

- CSS/XPath/JSONPath/Regex/URL 规则基础能力可用。
- golden tests 覆盖常见规则。
- 规则错误不会 panic。

### Phase 5 完成标准

- Fetcher 安全策略生效。
- Cookie 可保存和合并。
- JS 沙箱有超时和白名单绑定。
- Header JS 和 loginCheckJs 可用。

### Phase 6 完成标准

- 单源搜索可用。
- 多源搜索可用。
- 搜索结果可合并。
- 发现列表 P1 可用或明确延期。

### Phase 7 完成标准

- 搜索 -> 详情 -> 目录 -> 正文链路可用。
- 章节缓存生效。
- 空目录和空正文错误分类稳定。

### Phase 8 完成标准

- 替换规则 CRUD 可用。
- 替换测试接口可用。
- 正文后处理集成到正文获取。
- Regex 限制有测试。

### Phase 9 完成标准

- 调试 session 可创建、查询、取消。
- 事件流可订阅。
- 关键词调试能跑完整链路。
- 日志脱敏通过测试。

### Phase 10 完成标准

- 批量校验任务可创建、查询、取消。
- 每源结果可分类。
- 进度可查询或订阅。
- 失败分类有测试。

### Phase 11 完成标准

- 书架生命周期可用。
- 阅读进度可保存。
- 图片代理 P1 可用或明确延期。

### Phase 12 完成标准

- `go generate ./...` 通过。
- `go test ./...` 通过。
- `go vet ./...` 通过。
- OpenAPI 校验通过。
- migration 测试通过。
- e2e mock source 全链路通过。
- 安全测试通过。

## 6. 最终交付门禁

一期最终合并前必须满足：

- OpenAPI 是最新的，生成代码无漂移。
- 所有 P0 API 已实现。
- 所有 P0 repository 已测试。
- mock 书源 e2e 覆盖导入、搜索、详情、目录、正文、替换、书架、进度。
- 真实远程书源集合 E2E release gate 支持 `https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361`，并能生成完整报告。
- 调试和校验至少覆盖主路径和失败路径。
- SSRF、JS timeout、Regex 限制、日志脱敏均有自动化测试。
- 文档说明旧 API 路由不兼容，数据格式兼容。
- SQLite 数据库可从空库迁移启动。
- 所有 P0 任务 commit 名称遵循本文建议或保持同等清晰语义。
