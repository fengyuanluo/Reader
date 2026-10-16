# Go Web 移植 Legado 在线书源核心能力设计文档

## 1. 设计目标

本设计面向一个 Go Web 单体服务，目标是把 Legado 的在线书源核心能力重构为 OpenAPI-first 的服务端系统。设计必须满足以下约束：

- API 契约优先：`api/openapi.yaml` 是唯一接口真相源。
- 代码生成优先：使用 `oapi-codegen` 生成请求/响应类型和 chi server interface。
- 分层清晰：业务层不依赖 HTTP 框架、SQLite、JS runtime、具体 HTTP client。
- SOLID：模块职责单一、接口隔离、依赖倒置、可替换实现。
- 安全默认：Token 鉴权、SSRF 防护、JS 沙箱、正则超时、日志脱敏。
- 一期单体：不拆微服务，不引入多用户租户模型。
- 一期 SQLite：通过 repository 抽象保留未来切换 PostgreSQL 的能力。

## 2. 原实现映射

| 设计模块 | Legado 原实现参考 | Go 重构目标 |
|---|---|---|
| API 契约 | `api.md`、`HttpServer.kt`、`BookSourceController.kt`、`BookController.kt`、`ReplaceRuleController.kt` | 新 `/api/v1` REST API，不兼容旧路由 |
| 书源模型 | `BookSource.kt`、`BaseSource.kt`、规则对象文件 | Domain aggregate + DTO 映射 |
| 书源导入 | `ImportBookSourceViewModel.kt`、`SourceHelp.kt` | Import use case + remote importer |
| 规则执行 | `AnalyzeRule.kt`、`RuleAnalyzer.kt`、`AnalyzeBy*.kt` | RuleAnalyzer interface + adapters |
| URL 抓取 | `AnalyzeUrl.kt`、`CookieStore.kt` | Fetcher interface + HTTP adapter |
| 搜索发现 | `WebBook.kt`、`BookList.kt`、`SearchModel.kt` | Search/Discovery use cases |
| 详情目录正文 | `BookInfo.kt`、`BookChapterList.kt`、`BookContent.kt` | Info/Toc/Content use cases |
| 替换净化 | `ReplaceRule.kt`、`ContentProcessor.kt` | Replace processor + scoped repositories |
| 调试 | `Debug.kt`、`BookSourceDebugWebSocket.kt` | Debug session + event stream |
| 校验 | `CheckSourceService.kt` | Check task runner + persisted task state |
| 持久化 | `AppDatabase.kt`、DAO 文件 | SQLite migrations + repositories |

## 3. 总体架构

```text
┌─────────────────────────────────────────────────────────────┐
│                         HTTP API                            │
│   chi router + oapi-codegen generated server interface       │
└──────────────────────────────┬──────────────────────────────┘
                               │
┌──────────────────────────────▼──────────────────────────────┐
│                      Application Layer                       │
│  import/search/discovery/info/toc/content/debug/check usecase │
└──────────────────────────────┬──────────────────────────────┘
                               │
┌──────────────────────────────▼──────────────────────────────┐
│                         Domain Layer                         │
│  entities, value objects, ports, errors, policies             │
└──────────────────────────────┬──────────────────────────────┘
                               │
┌──────────────────────────────▼──────────────────────────────┐
│                    Infrastructure Layer                      │
│ SQLite repos, HTTP fetcher, rule adapters, goja runtime, logs │
└─────────────────────────────────────────────────────────────┘
```

设计要求：

- `internal/api` 只做 OpenAPI handler 适配，不写业务规则。
- `internal/app` 编排 use case，可以依赖 domain ports。
- `internal/domain` 定义实体、接口、错误和策略，不依赖任何基础设施。
- `internal/infra` 实现 SQLite、HTTP、规则解析、JS 沙箱、缓存等具体能力。
- `internal/worker` 只负责任务调度，不直接解析 HTTP 请求。

HTTP server 超时（`cmd/reader/serve.go`）：`ReadHeaderTimeout` 5s、`ReadTimeout` 60s、`IdleTimeout` 120s；**有意不设全局 `WriteTimeout`**，因为 debug/check 的 SSE 长连接与长时搜索不能被全局写超时打断。日志为 JSON 输出到 **stderr**。

## 4. 推荐目录结构

```text
api/
  openapi.yaml
  examples/
cmd/
  reader/
    main.go
    serve.go
internal/
  api/
    generated/
    handlers/
    middleware/
  app/
    source/
    search/
    discovery/
    book/
    content/
    replace/
    diagnostic/
    debug/
    check/
  bootstrap/
  cli/
  domain/
    source/
    book/
    replace/
    rule/
    fetch/
    task/
    errors/
  infra/
    sqlite/
      repositories/
    fetcher/
    ratelimit/
    concurrency/
    rule/
    script/
    chinese/
    logging/
    config/
  worker/
    check/
test/
  fixtures/
    book_sources/
    html/
    json/
    openapi/
```

目录约束：

- `internal/api/generated` 由 `oapi-codegen` 生成，禁止手工修改。
- `internal/domain` 不允许导入 `database/sql`、`net/http`、`chi`、`goja`。
- `internal/app` 不允许直接拼 SQL，不允许直接 new HTTP client。
- `internal/infra` 可以依赖第三方库，但必须实现 domain ports。
- 没有独立的 `internal/infra/cache/`：进程内缓存由 `internal/infra/script` 提供（脚本 `cache`/`java.put` 与日志缓冲），持久化缓存由 `internal/infra/sqlite/repositories` 通过 `cache_entries` 表提供（章节正文、图片）。
- 没有 `internal/infra/sqlite/migrations/` 目录：schema 迁移改为 `internal/infra/sqlite/migrate.go` 内嵌的版本化列表。
- 没有 `internal/worker/debug/`：调试会话 Runner 位于 `internal/app/debug`；`internal/worker` 只保留 `check/`。

## 5. OpenAPI 设计

### 5.1 基础规则

- 所有业务 API 使用 `/api/v1` 前缀。
- 除 `GET /api/v1/health`、`GET /api/v1/version`、`GET /api/v1/openapi.yaml` 外，所有接口需要 Bearer Token。
- 所有错误使用共享 `Error` schema。
- 所有分页列表使用共享 `PageMeta` schema。
- 所有异步任务使用共享 `TaskStatus` schema。
- 不定义 Legado 旧路由。

### 5.2 API 路径

```text
GET    /api/v1/health
GET    /api/v1/version
GET    /api/v1/openapi.yaml

POST   /api/v1/book-sources/import
GET    /api/v1/book-sources
POST   /api/v1/book-sources
GET    /api/v1/book-sources/{sourceId}
PUT    /api/v1/book-sources/{sourceId}
DELETE /api/v1/book-sources/{sourceId}
PATCH  /api/v1/book-sources/{sourceId}/enabled
PATCH  /api/v1/book-sources/{sourceId}/explore-enabled
PATCH  /api/v1/book-sources/reorder
GET    /api/v1/book-sources/{sourceId}/login
POST   /api/v1/book-sources/{sourceId}/login
DELETE /api/v1/book-sources/{sourceId}/login

POST   /api/v1/search
POST   /api/v1/discovery
POST   /api/v1/books
GET    /api/v1/books
GET    /api/v1/books/{bookId}
DELETE /api/v1/books/{bookId}
POST   /api/v1/books/{bookId}/refresh-info
POST   /api/v1/books/{bookId}/refresh-toc
GET    /api/v1/books/{bookId}/chapters
GET    /api/v1/books/{bookId}/chapters/{index}/content
PUT    /api/v1/books/{bookId}/progress

GET    /api/v1/replace-rules
POST   /api/v1/replace-rules
PUT    /api/v1/replace-rules/{ruleId}
DELETE /api/v1/replace-rules/{ruleId}
POST   /api/v1/replace-rules/test

POST   /api/v1/debug/book-source
GET    /api/v1/debug/sessions/{sessionId}
GET    /api/v1/debug/sessions/{sessionId}/events
DELETE /api/v1/debug/sessions/{sessionId}

POST   /api/v1/check/book-sources
GET    /api/v1/check/tasks/{taskId}
GET    /api/v1/check/tasks/{taskId}/events
DELETE /api/v1/check/tasks/{taskId}
```

### 5.3 错误模型

OpenAPI 共享错误模型：

```yaml
Error:
  type: object
  required: [code, message, traceId]
  properties:
    code:
      type: string
    message:
      type: string
    details:
      type: object
      additionalProperties: true
    traceId:
      type: string
```

推荐错误码：

| code | 场景 |
|---|---|
| `auth_required` | 未提供 Bearer Token |
| `auth_invalid` | Token 无效 |
| `validation_failed` | 请求参数或 JSON schema 错误 |
| `source_not_found` | 书源不存在 |
| `book_not_found` | 书籍不存在 |
| `chapter_not_found` | 章节不存在 |
| `import_failed` | 导入失败 |
| `rule_parse_failed` | 规则解析失败 |
| `fetch_failed` | HTTP 抓取失败 |
| `fetch_timeout` | HTTP 抓取超时 |
| `fetch_blocked` | SSRF 或安全策略拦截 |
| `script_failed` | JS 执行失败 |
| `script_timeout` | JS 执行超时 |
| `toc_empty` | 目录为空 |
| `content_empty` | 正文为空 |
| `replace_failed` | 替换净化失败 |
| `task_not_found` | 任务不存在 |
| `task_cancelled` | 任务被取消 |
| `internal_error` | 未分类内部错误 |

### 5.4 状态码与会话语义

- 异步资源创建/取消返回 **202** 而不是 200：`POST /check/book-sources`、`DELETE /check/tasks/{taskId}`、`POST /debug/book-source`、`DELETE /debug/sessions/{sessionId}` 返回任务/会话快照，终态由后续 `GET` 轮询或 SSE 事件流确认。
- 未装配的可选服务返回 **501 `not_configured`**（应用 501 表达“本次装配不提供该能力”），不再用 500 掩盖；书源登录走 `loginUi` 无头浏览器流程时返回 **501 `unsupported_login_flow`**。
- **409 `source_conflict`**：`PUT /book-sources/{sourceId}` 修改 `bookSourceUrl` 时，新地址已被别的书源占用，或 body 中的 `sourceId` 与路径不一致。
- **413 `payload_too_large`**：普通 JSON 请求体超过 10 MiB，书源导入请求体超过 32 MiB。
- **400**：`pageSize` 超过 200、缺少 `Content-Type: application/json`（JSON 端点）、`DebugSession.mode` 不在枚举内、`POST /check/book-sources` 指定不存在的 sourceId，均返回 `validation_failed` 或 `source_not_found`。
- `GET /openapi.yaml` 返回 YAML，`Content-Type` 为 `application/yaml`。

事件流（SSE）契约，适用于 `/debug/sessions/{sessionId}/events` 与 `/check/tasks/{taskId}/events`：

- 响应 `Content-Type: text/event-stream`，事件行格式 `id: <eventId>\nevent: <type>\ndata: <json>\n\n`。
- 订阅时先回放历史事件（支持 `Last-Event-ID` 续传），随后实时推送新增事件。
- 会话进入终态后停止推送并关闭响应；订阅不存在的会话立即返回 **404**。
- 连接空闲时周期发送 `: heartbeat` 注释行保活（同时用于检测已收敛的会话）。

## 6. 领域模型设计

### 6.1 BookSource

Go domain 必须保留 Legado 字段语义：

```text
BookSource
  sourceId: string
  bookSourceUrl: string
  bookSourceName: string
  bookSourceGroup: string?
  bookSourceType: int
  bookUrlPattern: string?
  customOrder: int
  enabled: bool
  enabledExplore: bool
  eventListener: bool
  customButton: bool
  jsLib: string?
  enabledCookieJar: bool
  concurrentRate: string?
  header: string?
  loginUrl: string?
  loginUi: string?
  loginCheckJs: string?
  coverDecodeJs: string?
  bookSourceComment: string?
  variableComment: string?
  lastUpdateTime: int64
  respondTime: int64
  weight: int
  exploreUrl: string?
  exploreScreen: string?
  ruleExplore: ExploreRule?
  searchUrl: string?
  ruleSearch: SearchRule?
  ruleBookInfo: BookInfoRule?
  ruleToc: TocRule?
  ruleContent: ContentRule?
  ruleReview: raw json?
```

`sourceId` 推荐使用 `bookSourceUrl` 的稳定 hash，API path 不直接暴露原 URL，避免路径转义问题。数据库仍必须保存原始 `bookSourceUrl` 并设置唯一约束。

### 6.2 Book 与 SearchBook

`SearchBook` 用于搜索结果，`Book` 用于书架。两者需要保持可转换：

- `SearchBook.toBook()` 应保留 `name`、`author`、`bookUrl`、`origin`、`originName`、`type`、`wordCount`、`latestChapterTitle`、`coverUrl`、`intro`、`tocUrl`、`originOrder`、`variable`。
- `Book` 必须保存 `durChapterIndex`、`durChapterPos`、`durChapterTime` 等进度字段。
- `Book` 还必须持久化并在 API 往返中保留一批 Legado 元数据字段（迁移 v6 加列）：`customTag`、`customCoverUrl`、`customIntro`、`charset`、`group`、`latestChapterTime`、`lastCheckTime`、`lastCheckCount`、`totalChapterNum`、`canUpdate`、`order`、`syncTime`。
- `POST` / `PATCH` 书源容忍未知字段：未识别的 Legado 字段原样写入 `raw_json` 并在读取时回写，保证未知字段不丢。

### 6.3 BookChapter

章节主键建议为 `(book_id, url)`，同时给 `(book_id, index)` 加唯一索引。

必须字段：

- `url`
- `title`
- `isVolume`
- `baseUrl`
- `bookId`
- `bookUrl`
- `index`
- `isVip`
- `isPay`
- `resourceUrl`
- `tag`
- `wordCount`
- `variable`

### 6.4 ReplaceRule

替换规则必须支持：

- 名称、分组、pattern、replacement。
- 标题作用域、正文作用域。
- include scope 和 exclude scope。
- 启用状态。
- 正则开关。
- timeoutMillisecond。
- order。

## 7. SQLite schema 设计

### 7.1 迁移策略

- 不使用外部迁移工具：`internal/infra/sqlite/migrate.go` 内维护按版本升序排列的迁移列表（`version + name + apply`），每个版本在独立事务内执行并写入 `schema_migrations`。
- 已发布版本禁止修改，结构变更只能追加新版本。
- 迁移必须支持空库启动。
- migration tests 必须验证重复运行不会破坏数据。

连接级 PRAGMA（外键、`busy_timeout`、WAL）不由迁移设置，而由 `sqlite.Open` 通过 DSN `_pragma` 参数对连接池里的每条连接生效。

### 7.2 表设计

当前表（不含已在 v3 删除的 `search_books`）：

```text
book_sources
books
chapters
replace_rules
cookies
source_variables
cache_entries
debug_sessions
debug_events
check_tasks
check_task_items
schema_migrations
```

索引：`book_sources(enabled)`、`book_sources(enabled_explore)`、`book_sources(custom_order)`、`debug_events(session_id, event_id)`。

### 7.3 book_sources

关键约束：

- `book_source_url` 唯一。
- `source_id` 唯一。
- `enabled`、`enabled_explore`、`custom_order` 建索引。
- `book_source_group` 可空。
- 规则对象以 JSON TEXT 保存。
- 原始导入 JSON 可选保存到 `raw_json`，便于兼容字段保留。

### 7.4 books 和 chapters

关键约束：

- `books.book_url` 唯一。
- `books.origin` 指向 `book_sources.book_source_url`，但删除书源时不强制 cascade 删除书籍。
- `chapters(book_id, index)` 唯一。
- 删除书籍 cascade 删除章节和章节正文缓存。

### 7.5 cache_entries

`cache_entries` 只承载两类持久化缓存（源变量走独立 `source_variables` 表，脚本缓存是进程内的 `internal/infra/script`）：

| 类型 | namespace | key | 有效期 |
|---|---|---|---|
| 章节正文 | `content` | `content:{bookId}:{chapterIndex}` | 永不过期 |
| 图片 | `image` | `image:{sha256(url)}` | 7 天（`expires_at = datetime('now','+7 days')`） |

缓存语义：

- 图片缓存读取时跳过已过期行；服务启动时删除过期行及 `expires_at` 为空的旧条目（避免引入 TTL 前的残留无限增长）。
- 章节正文缓存只保存**书源 `replaceRegex` 之后、用户替换规则之前**的原文；用户替换规则在每次读取时应用，因此改动替换规则立即生效。
- 迁移 v5 会清空旧的 `namespace='content'` 缓存（旧值保存的是已应用用户规则的正文）；旧库首次启动后正文需重新抓取一次。

缓存表字段：

- `cache_key`
- `namespace`
- `owner_type`
- `owner_id`
- `value`
- `content_type`
- `expires_at`
- `created_at`
- `updated_at`

## 8. 核心接口设计

### 8.1 Repository ports

```go
type BookSourceRepository interface {
    Save(ctx context.Context, source *BookSource) error
    SaveMany(ctx context.Context, sources []*BookSource) error
    FindByID(ctx context.Context, id string) (*BookSource, error)
    FindByURL(ctx context.Context, url string) (*BookSource, error)
    List(ctx context.Context, filter BookSourceFilter) (Page[*BookSource], error)
    Delete(ctx context.Context, id string) error
    UpdateEnabled(ctx context.Context, id string, enabled bool) error
    UpdateExploreEnabled(ctx context.Context, id string, enabled bool) error
    Reorder(ctx context.Context, orders []SourceOrder) error
}
```

```go
type BookRepository interface {
    Save(ctx context.Context, book *Book) error
    FindByID(ctx context.Context, id string) (*Book, error)
    FindByURL(ctx context.Context, bookURL string) (*Book, error)
    List(ctx context.Context, filter BookFilter) (Page[*Book], error)
    Delete(ctx context.Context, id string) error
    SaveProgress(ctx context.Context, id string, progress BookProgress) error
}
```

```go
type ChapterRepository interface {
    ReplaceByBook(ctx context.Context, bookID string, chapters []*BookChapter) error
    ListByBook(ctx context.Context, bookID string) ([]*BookChapter, error)
    FindByIndex(ctx context.Context, bookID string, index int) (*BookChapter, error)
    SaveContent(ctx context.Context, bookID string, chapterIndex int, content string) error
    GetContent(ctx context.Context, bookID string, chapterIndex int) (string, bool, error)
    SaveTitle(ctx context.Context, bookID string, chapterIndex int, title string) error
}
```

```go
type ReplaceRuleRepository interface {
    Save(ctx context.Context, rule *ReplaceRule) error
    FindByID(ctx context.Context, id string) (*ReplaceRule, error)
    List(ctx context.Context, filter ReplaceRuleFilter) (Page[*ReplaceRule], error)
    Delete(ctx context.Context, id string) error
    FindEnabledByTitleScope(ctx context.Context, bookName, origin string) ([]*ReplaceRule, error)
    FindEnabledByContentScope(ctx context.Context, bookName, origin string) ([]*ReplaceRule, error)
}
```

### 8.2 Infrastructure ports

```go
type Fetcher interface {
    Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error)
}
```

```go
type RuleAnalyzer interface {
    SetContent(content any, baseURL string) RuleAnalyzer
    SetRedirectURL(url string) RuleAnalyzer
    GetString(ctx context.Context, rule string, opts RuleOptions) (string, error)
    GetStringList(ctx context.Context, rule string, opts RuleOptions) ([]string, error)
    GetElements(ctx context.Context, rule string) ([]any, error)
}
```

```go
type ScriptRuntime interface {
    Eval(ctx context.Context, script string, bindings ScriptBindings, policy ScriptPolicy) (any, error)
}
```

```go
type DebugPublisher interface {
    Publish(ctx context.Context, sessionID string, event DebugEvent) error
    Close(ctx context.Context, sessionID string, final DebugEvent) error
}
```

### 8.3 Application use case ports

```go
type BookSourceImporter interface {
    Import(ctx context.Context, input ImportInput) (*ImportResult, error)
}
```

```go
type BookSearchUseCase interface {
    Search(ctx context.Context, input SearchInput) (*SearchResult, error)
}
```

```go
type ContentUseCase interface {
    GetContent(ctx context.Context, input ContentInput) (*ContentResult, error)
}
```

```go
type CheckTaskRunner interface {
    Start(ctx context.Context, input CheckInput) (*CheckTask, error)
    Cancel(ctx context.Context, taskID string) error
}
```

## 9. 规则引擎设计

### 9.1 职责拆分

规则引擎拆成以下组件：

- `RuleParser`：负责切分 Legado 规则字符串。
- `RuleAnalyzer`：负责按规则执行并返回结果。
- `CSSAdapter`：处理 JSoup/CSS 类规则。
- `XPathAdapter`：处理 XPath。
- `JSONPathAdapter`：处理 JSONPath。
- `RegexAdapter`：处理 Regex。
- `ScriptAdapter`：处理 JS 规则片段。
- `URLResolver`：处理相对 URL 到绝对 URL。

### 9.2 规则链

Legado 规则支持复杂切分，`RuleAnalyzer.kt` 和 `RuleAnalyzer.kt` 中的 `RuleAnalyzer` 是关键参考。Go 实现必须把规则切分单独封装，避免解析逻辑散落在业务代码中。

具体语义（对齐 Legado `makeUpRule` / `AnalyzeRule`）：

- `##regex##replacement###` 中的末尾 `###` 是 replaceFirst：只替换首个匹配片段，无匹配时返回空串；空替换写作 `##regex#####`；非法正则降级为字面量替换。
- `@get:{...}` / `{{...}}` 模板支持混合与 `@put:{...}`，按 Legado `makeUpRule` 在完整规则上一次展开后再拆分。
- JSONPath 过滤支持 `> < >= <= && || !`。
- CSS 支持 `text.关键词.N` / `[N]`、`children.xxx`、逗号选择器按文档序求并集、属性值去重。
- `@get:{bookName}` / `@get:{title}` 分别取当前书名与章节标题。
- 正则编译、JSONPath `Evaluable`、CSS 选择器解析都有进程内 LRU 缓存。

规则执行必须返回 typed error：

- `RuleSyntaxError`
- `RuleUnsupportedError`
- `RuleNoResultError`
- `RuleExecutionError`
- `ScriptExecutionError`

### 9.3 URL 归一化

URL 归一化必须参考：

- `NetworkUtils.getAbsoluteURL` 使用位置。
- `BookChapter.getAbsoluteURL()`。
- `CustomUrl.kt` 自定义属性。

设计要求：

- 规则提取 URL 时，以 redirect URL 优先作为 base。
- 如果 URL 带自定义属性，属性必须被保留。
- 文件源 `downloadUrls` 只绝对化逗号前的 URL，逗号后的 option 文本逐字保留。
- 空 URL 不应 panic，应返回空值或明确错误，取决于调用场景。
- **只有 URL 类字段**（`bookUrl`、`coverUrl`、`tocUrl`、`nextTocUrl`、`nextContentUrl`、`downloadUrls`）按 Legado `isUrl` 语义做绝对化；普通文本字段不再被绝对化（例如 `1.2万` 不会被改写成 URL）。
- `bookUrlPattern` 为整串匹配（对齐 Legado `baseUrl.matches(pattern)`），编译失败返回明确错误。

## 10. HTTP Fetcher 设计

### 10.1 请求能力

Fetcher 必须支持：

- GET/POST 和书源规则中声明的方法。
- query、body、form、json。
- Header JSON。
- Header JS。
- 默认 User-Agent。
- `#requestWithoutUA` 只用于导入远程书源，不影响普通抓取规则。
- gzip 和常见压缩响应。
- Cookie 持久化与合并。
- 登录 Header。
- `loginCheckJs` 响应处理。
- 统一解析 `method/body/headers/type/charset/origin/retry/js/bodyJs`。
- 代理：只来自书源 `header` 中的 `proxy` 键（Legado 约定）或 URL 选项 `proxy`，**忽略 `HTTP_PROXY`/`HTTPS_PROXY` 环境变量**；支持 `http`/`https`/`socks5`/`socks5h`，凭证支持 Legado 的 `host:port@user@pass` 写法与标准 userinfo 写法；不支持 `socks4`。
- URL 选项 `dnsIp`：直连指定 IP，保留 Host 头与 TLS SNI；该 IP 同样过 SSRF 校验，且不可与 `proxy` 同时使用。
- URL 选项 `charset`：对 query/body 做指定编码。
- GET/HEAD 不发送请求体（对齐 Legado `AnalyzeUrl`：忽略 body 选项）。
- 响应无 `Content-Type` 时，按 BOM 与前 4 KiB 内 `<meta charset>` 嗅探编码（不做 Legado 的统计式编码探测）；有 `Content-Type` 时按 charset 把文本响应解码为 UTF-8。
- `webView:true` 使用普通 HTTP 降级并写告警日志，不做真实渲染。
- URL 选项 `serverID`、`webJs`、`webViewDelayTime` 接受但不生效；未知 URL option 键只有零值才放行，非零值返回明确错误。

### 10.2 安全策略

默认安全策略：

- `http`、`https` 以外 scheme 禁止。
- 私网地址禁止。
- loopback 禁止。
- link-local 禁止。
- metadata 地址禁止。
- DNS 解析后仍需检查 IP。
- redirect 后目标重新检查。
- 最大 redirect 次数默认 5。
- 响应大小默认限制，例如 10 MiB。
- 请求超时默认 30 秒。
- `concurrentRate` 按 `bookSourceUrl` 使用固定窗口限速，等待服从 context 取消；未配置时使用全局策略。

SSRF 校验在**拨号阶段**执行：每次建立连接只解析一次域名，校验全部解析结果后直接连接已校验的 IP，因此 DNS rebinding 与重定向都无法绕过；重定向目标重新拨号，同样过校验。封禁范围除回环/私网/链路本地外，还包含 CGN（`100.64.0.0/10`）、NAT64（`64:ff9b::/96`、`64:ff9b:1::/48`）、6to4（`2002::/16`）、文档段（`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`、`2001:db8::/32`）等特殊用途地址段。

经 `proxy` 抓取时，代理主机允许私网地址；目标主机的校验只做尽力而为（本地解析后校验），无法排除代理侧 DNS 解析结果与本地不同。

### 10.3 Cookie 策略

参考 `CookieStore.kt`：

- Cookie 按域保存。
- 持久 Cookie 和 session Cookie 合并。
- Cookie 长度过大时必须有清理策略。
- 调试日志不得打印 Cookie 原文。
- 过期或 `expires_at` 损坏的持久化 Cookie 不再发送，并从存储中删除；`Max-Age <= 0` 视为删除该 Cookie（`Max-Age` 优先于 `Expires`）。
- `enabledCookieJar` 按书源生效，由 `fetch.Request.UseCookieJar` 透传。

Go 实现建议：

- `CookieRepository` 持久化 domain cookie。
- `SessionCookieStore` 保存在进程内。
- `CookiePolicy` 负责合并、截断和删除。

## 11. JS 沙箱设计

### 11.1 Runtime 选择

一期默认使用 `goja`。设计目标不是完全复刻 Rhino，而是支持常用 Legado 书源 JS 场景。

### 11.2 允许能力

允许暴露：

- `source`：只读书源字段和受控变量 API（含 `setVariable`/`getVariable`、`putLoginHeader`/`getLoginHeader`）。
- `book`：当前书籍上下文（含 `getVariable`/`putVariable`）。
- `chapter`：当前章节上下文（含 `getVariable`/`putVariable`）；Reader 无 RSS 文章链路，不提供 `rssArticle`，`title` 绑定为当前章节标题。
- `result`：当前响应对象。
- `cache.get`、`cache.put`：受命名空间限制的键值缓存。
- `log`：调试日志输出。
- `url.resolve`：URL 解析。
- `java.ajax` / `ajaxTestAll` / `getCookie` 等网络与 Cookie helper：只能经受控 Fetcher/CookieStore 执行。
- `timeFormat`、`toNumChapter`、`getStringList`、`getElement`、`setContent`、摘要/base64/User-Agent 等纯 helper。
- `JSON`、`String`、`Math`、`Date` 等 JS 内建能力。
- 兼容补齐的 Legado helper：`java.ajax`、`java.head`、`java.timeFormat`、`java.htmlFormat`、`java.toNumChapter`、`java.strToBytes`/`java.bytesToStr`、`java.base64DecodeToByteArray`、`java.hexDecodeToByteArray`、`java.md5Encode16`、`java.getCookie`、`java.cacheFile`、`java.importScript`、`java.getSource`、`java.toURL`、`androidId`、`java.logType`、`java.queryTTF`/`java.queryBase64TTF`/`java.replaceFont`。
- 本地文件 API（`java.readFile`/`java.writeFile`/`java.downloadFile`、zip 解压等）限定在 `<DataDir>/script-files` 沙箱内；未配置沙箱根时抛 `script_unsupported`。
- `java.ajax` 失败时返回错误文本（与 Legado `JsExtensions.kt` 一致，非 null）。

### 11.3 禁止能力

禁止暴露：

- 任意文件系统访问（仅允许上述沙箱内文件 API）。
- 进程执行。
- 绕过受控 Fetcher 的任意网络请求。
- Go 指针或反射。
- 环境变量。
- 未脱敏 Cookie 和 Token。
- 无限循环。

浏览器类 API（`webView*`、`startBrowser*`、`getVerificationCode`、`openUrl`）与 7z/rar 解压抛 `script_unsupported`（脚本可 try/catch）。

### 11.4 执行限制

每次 JS 执行必须有：

- 上下文 deadline。
- 最大执行时间。
- 最大调用次数或 interrupt 机制。
- panic recover。
- 错误包装。
- 阶段信息。

阶段由 app 层通过 ctx 标注，不再有全局 Stage。阶段取值：`search`、`info`、`toc`、`content`、`image_decode`、`discovery`，以及子阶段 `toc.format_js`、`toc.pre_update_js`、`*.header_js`、`*.login_check_js`。脚本错误信息格式为 `<kind>: stage=<stage> source=<sourceId>: msg`。

其他执行约束：

- 书源级脚本状态（`sourceVariable`/`loginHeader`/`loginInfo`/`v_<key>`）持久化到 `source_variables` 表，每次读写直接走仓库，无跨请求内存缓存；删除书源时随事务清理。
- 脚本 `cache` 有界（条目数、总字节、单值大小上限 + TTL），日志缓冲有界。
- 书源 `jsLib` 按内容哈希缓存 `goja.Compile` 结果，每次 `Eval` 仍使用独立 VM，状态不跨执行共享。

## 12. 业务用例设计

### 12.1 书源导入

流程：

```text
Import request
  -> detect input type
  -> parse local JSON or fetch remote URL
  -> parse BookSource array
  -> validate required fields
  -> compare existing source by bookSourceUrl
  -> apply import options
  -> normalize order
  -> save in transaction
  -> return itemized result
```

导入选项：

- `keepExistingName`
- `keepExistingGroup`
- `keepExistingEnabled`
- `groupMode`: `replace` 或 `append`
- `groupName`
- `updatePolicy`: 默认按 `lastUpdateTime`

落库语义：

- 数组导入在单事务内落库：任一条写库失败整批回滚，不留部分导入状态；解析失败的单条记为 failed 并继续处理其余条目。
- 同批次内重复 URL 视为“已存在”，仍受 `updatePolicy` 约束。
- 默认 `lastUpdateTime` 策略在两者 `lastUpdateTime` 相等时跳过（与 Legado `ImportBookSourceViewModel` 一致），需要覆盖时用 `updatePolicy=always`。
- 书源关键字/分组过滤会转义 LIKE 通配符（`ESCAPE '\'`）。
- 书源组分隔符对齐 Legado `AppPattern.splitGroupRegex`：`[,;，；]`。
- 导入或覆盖书源后会使该源的发现分类缓存失效。

### 12.2 搜索

单源搜索流程：

```text
load source
  -> validate enabled or debug override
  -> build AnalyzeUrl from searchUrl
  -> run request js
  -> fetch response
  -> run bodyJs
  -> run loginCheckJs if configured
  -> check redirect/direct detail pattern
  -> parse BookList
  -> return SearchBook list
```

多源搜索流程：

```text
resolve source scope
  -> run single source search with concurrency limit
  -> collect successes and warnings
  -> merge duplicate name-author results
  -> sort by match quality and origin count
  -> return result
```

并发与容错：

- `READER_GLOBAL_CONCURRENCY` 是全局抓取并发上限，同时作为多源搜索与发现的并发度；`READER_SOURCE_CONCURRENCY` 是单源并发上限。
- 书源 `concurrentRate`（`n` 为同一源两次访问的最小毫秒间隔，`n/ms` 为窗口内次数）按 Legado `ConcurrentRateLimiter` 语义按源限流，统一由 fetcher 承担（由 `fetch.Request` 的 `SourceKey`/`ConcurrentRate`/`SkipRateLimit` 驱动），搜索、发现、详情、目录、正文与脚本请求共用同一实现，避免同一请求被双重限速；`internal/infra/ratelimit.Limiter` 只保留 `READER_GLOBAL_CONCURRENCY`/`READER_SOURCE_CONCURRENCY` 并发上限。
- 每个源的 goroutine 有 panic 隔离：单源 panic 只转成该源的 Warning，其余源结果照常返回。
- 无效 sourceId 转成 Warning（`source_unavailable`），不使整个请求 500。
- 搜索结果按 Legado 语义清洗：书名去装饰、作者去前后缀、`wordCount`、`intro` 去 HTML。

`SearchResponse.hasMore` 语义：本页有结果即可继续翻页（Legado 书源分页无总数信息，空页即结束）。

### 12.3 发现

发现与搜索共用列表解析能力。差异：

- 输入为发现 URL 或分类。
- 规则优先使用 `ruleExplore`，为空时可按 Legado 行为回退到 `ruleSearch`。
- 只使用 `enabledExplore = true` 且有 `exploreUrl` 的书源。
- 发现分类（`exploreKinds()`）结果按源缓存，带 TTL（默认 10 分钟）与条目上限（默认 256），并支持按源失效（导入/编辑书源后失效）。

### 12.4 详情

流程：

```text
load source and book
  -> fetch bookUrl unless infoHtml exists
  -> run loginCheckJs
  -> run BookInfoRule.init
  -> extract fields
  -> normalize coverUrl and tocUrl
  -> for type=3, extract downloadUrls while preserving option text
  -> save/update book
```

### 12.5 目录

流程：

```text
load source and book
  -> run preUpdateJs if configured
  -> fetch tocUrl unless tocHtml exists
  -> parse chapter list
  -> parse nextTocUrl
  -> fetch serial or parallel next pages
  -> dedupe
  -> normalize order and indexes
  -> run formatJs if configured
  -> replace chapters transactionally
```

### 12.6 正文

流程：

```text
check chapter content cache
  -> load source, book, chapter
  -> build/fetch chapter request and run js/bodyJs
  -> parse content and nextContentUrl
  -> fetch next content pages
  -> apply contentRule.replaceRegex
  -> save raw content to cache (SkipCache=false)
  -> write parsed chapter title back (SaveTitle)
  -> apply ContentProcessor
  -> return content
```

要点：

- 缓存保存的是 `replaceRegex` 之后、用户替换规则之前的原文；缓存命中时也要重新应用 `ContentProcessor`（改动替换规则立即生效）。
- 多 `nextContentUrl` 分页有界并发抓取（上限 8），并按规则顺序拼接；遇到“下一页链接等于下一章 URL”即停止。
- 正文规则解析出的章节标题会写回章节表（`ChapterRepository.SaveTitle`）。
- `contentRule.sourceRegex` 不支持：Legado 仅通过 BackstageWebView 资源嗅探实现（属 WebView-only），Reader 在校验阶段提前给出明确失败项，抓取阶段返回 `ErrUnsupportedContentBrowserFlow`。
- 正文图片经安全代理获取时复用书源 `coverDecodeJs` / `imageDecode` 上下文（`image_decode` 阶段）。

`subContent` 与 `callBackJs` 可导入和持久化，但当前不执行；检测到配置时写内部诊断日志并继续主 `content` 规则。`sourceRegex` 仍因需要浏览器流而明确返回不支持。

### 12.7 书源登录

openapi-first 的登录能力（`GET/POST/DELETE /book-sources/{sourceId}/login`）：

- `POST` 请求体为 `BookSourceLoginRequest{values?: map<string,string>}`：先把提交的表单值持久化为 `loginInfo`，再执行 `loginUrl` 中的 login 脚本，脚本通过 `source.putLoginHeader` 写入的登录头会持久化到 `loginHeader`；提交空 `values` 等价于只清除 `loginInfo`。
- 状态响应 `BookSourceLoginState{sourceId,hasLoginUrl,hasLoginUi,loggedIn,loginInfo,loginHeader}`。
- `loginUi` 为空且 `loginUrl` 非脚本时返回 501 `unsupported_login_flow`（不引入无头浏览器）；书源不存在 404，未声明 `loginUrl` 或表单值非法 400，脚本失败 502。
- `DELETE` 清除持久化的 `loginInfo` 与 `loginHeader`（204）。
- 状态存储与书源级脚本状态共用 `source_variables` 表（见 11.4）。

### 12.8 全书 TXT 导出

```text
load book and ordered chapters
  -> create temporary UTF-8 text file
  -> write book metadata
  -> for each chapter in order:
       volume: write volume title
       content: reuse cache or execute GetChapterContent
       write chapter title and normalized body
  -> on any error: discard temporary file
  -> on success: return attachment and remove temporary file after response
```

导出按目录顺序串行执行，避免改变书源脚本、Cookie 和限速语义；正文失败时不发送部分结果，已成功章节仍保留正文缓存。

## 13. 替换净化设计

替换净化由 `ContentProcessor` 负责：

- 加载标题作用域规则。
- 加载正文作用域规则。
- 去除重复标题。
- 应用标题替换。
- 应用正文替换。
- 对 regex 设置超时。
- 记录实际命中的替换规则。

替换语义：

- 规则内的 `##regex##replacement###` 末尾 `###` 表示 replaceFirst（只替换首个匹配片段，无匹配返回空串）；空替换写作 `##regex#####`；非法正则降级为字面量替换。
- regex 命中 limit/timeout 时会自动禁用该规则并落库。
- 正文缓存保存净化前正文，用户替换规则在每次读取时应用。

正则安全：

- Go 标准库 regexp 没有灾难性回溯，但仍必须限制输入大小和执行上下文。
- 如果引入 PCRE 类库，必须有严格 timeout。
- 设计文档和任务必须要求 timeout 测试，不得省略。

## 14. 调试系统设计

### 14.1 事件流协议

一期推荐 SSE：

```text
GET /api/v1/debug/sessions/{sessionId}/events
```

事件类型：

```text
debug.started
debug.stage_started
debug.log
debug.stage_completed
debug.stage_failed
debug.completed
debug.cancelled
```

事件字段：

- `sessionId`
- `sourceId`
- `stage`
- `level`
- `message`
- `elapsedMs`
- `traceId`
- `data`

连接语义：

- 响应 `Content-Type: text/event-stream`，每行事件为 `id: <eventId>\nevent: <type>\ndata: <json>\n\n`。
- 订阅时先回放历史事件（支持 `Last-Event-ID` 续传），随后实时推送新增事件。
- 会话进入终态（`succeeded`/`failed`/`cancelled`，并写 `CompletedAt`）后停止推送并关闭响应；订阅不存在的会话立即返回 404。
- 空闲时周期发送 `: heartbeat` 注释行保活，并顺带检测已收敛的会话。

### 14.2 调试模式

支持：

- `keyword`：搜索 -> 详情 -> 目录 -> 正文。
- `info_url`：详情 -> 目录 -> 正文。
- `toc_url`：目录 -> 正文。
- `content_url`：正文。
- `discovery_url`：发现 -> 详情 -> 目录 -> 正文。

这对应 `Debug.startDebug()` 中对关键词、绝对 URL、`::`、`++`、`--` 的分支，但新 API 不复用这些字符串协议，而使用显式字段。`mode` 不在上述枚举内时创建会话返回 400。

### 14.3 脱敏

必须脱敏：

- `Authorization`
- `Cookie`
- `Set-Cookie`
- Token。
- 登录表单中的密码字段。

长响应体默认截断，例如最多 32 KiB。

## 15. 校验系统设计

### 15.1 状态机

`CheckTask` 状态：

```text
pending -> running -> succeeded
pending -> running -> failed
pending -> running -> cancelled
```

`CheckTaskItem` 状态：

```text
pending
running
passed
failed
skipped
cancelled
```

终态会写入 `CompletedAt`；进程重启后遗留的 `running` 任务/会话无法再被推进，启动时统一置为 `failed`（interrupted）。

### 15.2 校验流程

每个书源：

```text
start checking
  -> choose valid ruleSearch.checkKeyWord or request/default fallback
  -> apply the effective 180s default timeout
  -> search check if enabled -> its own info/toc/content stages
  -> discovery check if enabled and configured -> its own info/toc/content stages
  -> for file source: info/downloadUrls only; toc/content not applicable
  -> classify failures
  -> persist item, keywordUsed, timeoutMs and stage results
  -> monotonically persist task progress
```

校验关键字（对齐 Legado `BookSource.getCheckKeyword`）：书源自身的 `RuleSearch.CheckKeyWord` 优先，其次请求指定的 keyword，最后回退默认关键字“我的”；缺少搜索规则或地址时直接给出明确失败项。

任务级状态规则：任一源失败 → 任务 failed，但每个源的独立结果仍完整保留。

搜索或发现服务返回 per-source warning 时，校验器使用 warning 原文；只有没有 warning 且列表确实为空时才生成空结果。搜索和发现即使同时启用也分别执行后续阶段，不能用一条链路的结果替代另一条。

`failureCode` 保留阶段兼容值，`failureClass` 表达根因：

| 分类 | 触发 |
|---|---|
| `source_*_empty*` | 明确的搜索、发现、目录、正文或文件下载地址为空 |
| `source_upstream_*` | 上游 HTTP、空 JSON 或非 JSON 响应 |
| `network_*` | DNS、TLS、超时、重置和瞬态传输失败 |
| `source_rule_*` | 规则脚本语法或执行失败 |
| `unsupported_runtime_api` | 受限 JS runtime 尚未支持的确定性 API |
| `unsupported_browser_capability` | WebView/浏览器专属能力 |
| `implementation_or_rule_failed` | 证据不足，保留原文等待同请求 A/B |

### 15.3 任务与诊断持久化

- `finished` 使用仓储层单调更新，不能被乱序并发写回较小值。
- worker 的失败状态与计数在同一同步域内更新，必须通过 race detector。
- 只有确定性的源空结果或规则失败可更新“失效”分组和 `// Error:`；网络、HTTP、安全策略、浏览器边界和未确认错误只保留任务诊断。
- 创建/取消异步任务返回 202；pending/running SSE 使用 `check.progress`，终态才使用 completed/cancelled 事件。

在上述标准 `failureCode` 之外，check/debug 还通过统一诊断器写入更细的 `failureClass`（持久化到 `check_task_items.failure_class` 与调试事件），用于区分安全策略拦截、授权失败、DNS/TLS/超时/连接重置、上游 4xx/5xx、上游空 JSON/非 JSON、脚本语法错误等，避免一律折叠成 `website_failed`。

## 16. SOLID 设计约束

### 16.1 单一职责

- Handler 只做 HTTP 适配。
- Use case 只编排业务流程。
- Repository 只做持久化。
- Fetcher 只做网络请求。
- RuleAnalyzer 只做规则解析。
- ScriptRuntime 只做 JS 执行。

### 16.2 开闭原则

- 新增规则类型时新增 adapter，不修改搜索/目录/正文 use case。
- 新增数据库实现时新增 repository 实现，不修改业务层。
- 新增事件传输方式时新增 publisher，不修改调试业务流程。

### 16.3 里氏替换

- Mock repository、SQLite repository、未来 PostgreSQL repository 必须满足同一接口语义。
- Mock fetcher 和 real fetcher 必须使用同一错误模型。

### 16.4 接口隔离

- 不提供“大而全”的 `Repository`。
- 搜索 use case 不应依赖替换规则 repository。
- 导入 use case 不应依赖章节 repository。

### 16.5 依赖倒置

- `internal/app` 依赖 domain ports。
- `internal/infra` 实现 domain ports。
- 依赖注入在 composition root 完成：`internal/bootstrap` 装配共享 Runtime，`cmd/reader serve` 与 CLI 子命令共同复用。

## 17. 测试策略

### 17.0 任务工作流测试约束

后续每个开发任务都必须遵循同一工作流，不能只按任务标题直接编码：

1. 开始前读取根级 `AGENTS.md`。
2. 执行 `git status --porcelain=v2 --branch`，确认当前工作树状态。
3. 按任务中的“原实现参考”阅读或搜索 Legado 源码，记录关键事实。
4. 先补充或更新该任务对应的单元测试、契约测试、集成测试或 E2E fixture。
5. 再实现代码。
6. 运行任务内验收门禁和通用验收命令。
7. 在 PR 或提交说明中列出源码探索文件、测试命令和验收结果。

如任务确实不需要代码实现，例如纯文档任务，也必须说明已审阅的源码或需求依据，并进行 Markdown 链接和内容一致性检查。

### 17.1 单元测试

必须覆盖：

- 领域模型校验。
- 规则切分。
- URL 归一化。
- 替换净化。
- JS 沙箱。
- 文件源 `downloadUrls` option 原文保留。
- 错误分类。

### 17.2 Repository 测试

必须覆盖：

- migration。
- insert/update/delete。
- 查询筛选。
- 事务。
- cascade 或手动清理策略。

### 17.3 OpenAPI 契约测试

必须覆盖：

- `oapi-codegen` 生成。
- 请求 validation。
- 响应 schema。
- 错误 schema。

### 17.4 Fetcher 集成测试

使用 mock HTTP server 覆盖：

- Header。
- Cookie。
- redirect。
- compression。
- timeout。
- response size。
- SSRF block。

### 17.5 端到端测试

必须有 mock 书源完成稳定自动化 E2E：

```text
import source
  -> search
  -> save book
  -> refresh info
  -> refresh toc
  -> get content
  -> apply replace
  -> save progress
  -> debug
  -> check
```

mock E2E 的目的，是在不访问真实外网的情况下稳定覆盖导入、搜索、详情、目录、正文、替换、书架、进度、调试和校验闭环。mock E2E 必须进入默认 CI。

另有轻量兼容关键路径：文本书源的搜索和正文请求执行 `js/bodyJs` 后完成搜索 -> 详情 -> 目录 -> 正文；文件型书源从详情规则提取 TXT `downloadUrls` 并保留其请求 option 原文。

### 17.6 真实远程书源集合 E2E

除 mock E2E 外，最终 release gate 必须支持用户指定真实远程书源集合：

```text
https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361
```

当前核验事实：

- 核验日期：2026-05-03。
- HTTP 状态：200。
- Content-Type：`application/json`。
- 响应体大小：约 4.85 MB。
- JSON 形态：Legado 书源数组。
- 数组条数：459。
- 首批样例：`墨辰整理书源系列7.1版`、`起点读书（优+++）`、`番茄小说（优+++）`、`阅读助手（优+++）`、`天天小说（优+++）`。

真实远程集合 E2E 设计为 manual 或 nightly gate，不放入每次 PR 默认 CI，原因是外部网络、书源规则、目标站点和反爬策略可能漂移。该 gate 必须输出机器可读和人工可读报告：

```text
real_source_collection_e2e_report.json
real_source_collection_e2e_report.md
```

报告必须包含：

- 测试时间。
- 集合 URL。
- HTTP 状态。
- Content-Type。
- 响应大小。
- JSON 解析条数。
- 导入成功、更新、跳过、失败数量。
- 候选书源筛选规则。
- 被选中的候选书源。
- 搜索关键词。
- 每个候选源的搜索、详情、目录、正文阶段结果。
- 成功样例正文长度。
- 失败分类和 traceId。

候选书源筛选策略：

- 仅选择 `enabled = true` 的书源。
- 必须具备 `searchUrl`、`ruleSearch`、`ruleBookInfo`、`ruleToc`、`ruleContent`。
- 优先选择 `bookSourceType = 0` 文本类书源。
- 优先选择 `bookSourceUrl` 为 HTTP/HTTPS URL 的书源；非 URL 类型聚合/说明书源只参与导入兼容，不作为搜索候选。
- 最多尝试前 N 个候选，N 由测试配置控制，默认 10。

通过标准：

- 集合可下载。
- JSON 可解析。
- 至少导入 1 条有效书源。
- 至少 1 个候选源完成搜索 -> 详情 -> 目录 -> 正文链路。
- 如果没有候选源成功，报告必须证明失败是外部源不可用、目标站点不可用、规则失效、安全策略拦截或实现缺陷中的哪一类；实现缺陷分类不得放行 release。

## 18. 配置设计

应用配置项（`config.Load` 读取，含默认值）：

```text
READER_ADDR                         默认 127.0.0.1:8080
READER_DATA_DIR                     默认 data
READER_SQLITE_DSN                   默认 data/reader.db
READER_TOKEN                        默认空
READER_ENV                          默认 development
READER_DEFAULT_USER_AGENT           默认 Reader/0.1.0
READER_FETCH_TIMEOUT                默认 30s
READER_FETCH_MAX_BODY_BYTES         默认 10 MiB
READER_FETCH_MAX_REDIRECTS          默认 5
READER_ALLOW_PRIVATE_NETWORK        默认 false
READER_GLOBAL_CONCURRENCY           默认 8
READER_SOURCE_CONCURRENCY           默认 2
READER_SCRIPT_TIMEOUT               默认 10s
READER_DEBUG_BODY_LIMIT             默认 32768
READER_CONTENT_RESEGMENT            默认 false
READER_CONTENT_CHINESE_CONVERTER_TYPE 默认 0
```

监听与 token：

- `READER_ADDR` 默认 `127.0.0.1:8080`（回环）。
- `READER_TOKEN` 为空时只允许监听回环地址（`127.0.0.0/8`、`::1`、`localhost`）；监听非回环地址时 `serve` 拒绝启动并返回退出码 2，且在打开数据库之前失败。该规则对所有环境生效。
- `READER_ENV=production` 无论监听哪里都必须设置 `READER_TOKEN`。
- `READER_ENV=test` 且 token 为空时，`config.Load` 会填入公开默认 token `reader-test-token`，因此 test 环境在非回环地址也能启动——这是本地/CI 便利，不是可暴露到不可信网络的安全配置。

已移除的配置项：

- `READER_REGEX_TIMEOUT`：纯 no-op，已从 config 与 `config.Redacted()` 输出删除。

非应用配置：

- `READER_REAL_E2E_ENABLED`、`READER_REAL_E2E_SOURCE_COLLECTION_URL`、`READER_REAL_E2E_KEYWORD`、`READER_REAL_E2E_MAX_CANDIDATES` 等 `READER_REAL_E2E_*` 不再是应用配置：`config.Load` 不读取，只由 `test/e2e` 直接读环境变量。

### 18.1 容器与发布

- 构建阶段使用 Go 1.26 Alpine，通过 `CGO_ENABLED=0` 交叉构建静态二进制。
- 运行阶段使用 Alpine，安装 CA 证书和时区数据，以非 root `reader` 用户启动。
- 容器监听 8080，SQLite DSN 固定为 `/data/reader.db`，`/data` 由命名卷持久化，TXT 导出临时文件使用 `/tmp`。
- GitHub Action 在 `master`、`v*` 和手动触发时先运行测试与 vet，再向 GHCR 发布 `linux/amd64`、`linux/arm64` 镜像。
- `master` 生成 `latest`，版本标签生成完整 SemVer 和 `major.minor`，所有发布生成短提交 SHA 标签。
- 发布权限仅包含读取仓库和写入 Packages；不使用额外 PAT，不启用自动清理或签名。

## 19. 未来扩展

以下能力不进入一期，但设计应避免阻塞：

- PostgreSQL repository。
- 多用户账号和租户隔离。
- RSS。
- TXT/EPUB 本地书籍。
- TTS。
- WebDAV 同步。
- Web 管理前端。
- WebView/浏览器渲染抓取 worker。
- 分布式抓取 worker。
