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
      migrations/
      repositories/
    fetcher/
    rule/
    script/
    cache/
    logging/
    config/
  worker/
    check/
    debug/
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

- 使用迁移工具管理 SQL，例如 `goose`。
- 所有 schema 变更必须写迁移。
- 迁移必须支持空库启动。
- migration tests 必须验证重复运行不会破坏数据。

### 7.2 表设计

建议表：

```text
book_sources
books
search_books
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

缓存 key 设计：

| 类型 | key 建议 |
|---|---|
| 章节正文 | `content:{bookId}:{chapterIndex}` |
| 源变量 | `source_variable:{bookSourceUrl}:{name}` |
| JS 缓存 | `script_cache:{bookSourceUrl}:{key}` |
| 图片缓存 | `image:{sha256(url)}` |
| 抓取缓存 | `fetch:{sha256(request)}` |

缓存表字段：

- `key`
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
- 空 URL 不应 panic，应返回空值或明确错误，取决于调用场景。

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
- 每源并发默认由 `concurrentRate` 或全局配置控制。

### 10.3 Cookie 策略

参考 `CookieStore.kt`：

- Cookie 按域保存。
- 持久 Cookie 和 session Cookie 合并。
- Cookie 长度过大时必须有清理策略。
- 调试日志不得打印 Cookie 原文。

Go 实现建议：

- `CookieRepository` 持久化 domain cookie。
- `SessionCookieStore` 保存在进程内。
- `CookiePolicy` 负责合并、截断和删除。

## 11. JS 沙箱设计

### 11.1 Runtime 选择

一期默认使用 `goja`。设计目标不是完全复刻 Rhino，而是支持常用 Legado 书源 JS 场景。

### 11.2 允许能力

允许暴露：

- `source`：只读书源字段和受控变量 API。
- `book`：当前书籍上下文。
- `chapter`：当前章节上下文。
- `result`：当前响应对象。
- `cache.get`、`cache.put`：受命名空间限制的键值缓存。
- `log`：调试日志输出。
- `url.resolve`：URL 解析。
- `JSON`、`String`、`Math`、`Date` 等 JS 内建能力。

### 11.3 禁止能力

禁止暴露：

- 文件系统。
- 进程执行。
- 任意网络请求。
- Go 指针或反射。
- 环境变量。
- 未脱敏 Cookie 和 Token。
- 无限循环。

### 11.4 执行限制

每次 JS 执行必须有：

- 上下文 deadline。
- 最大执行时间。
- 最大调用次数或 interrupt 机制。
- panic recover。
- 错误包装。
- 阶段信息，例如 `header_js`、`login_check_js`、`rule_js`、`toc_format_js`。

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

### 12.2 搜索

单源搜索流程：

```text
load source
  -> validate enabled or debug override
  -> build AnalyzeUrl from searchUrl
  -> fetch response
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

### 12.3 发现

发现与搜索共用列表解析能力。差异：

- 输入为发现 URL 或分类。
- 规则优先使用 `ruleExplore`，为空时可按 Legado 行为回退到 `ruleSearch`。
- 只使用 `enabledExplore = true` 且有 `exploreUrl` 的书源。

### 12.4 详情

流程：

```text
load source and book
  -> fetch bookUrl unless infoHtml exists
  -> run loginCheckJs
  -> run BookInfoRule.init
  -> extract fields
  -> normalize coverUrl and tocUrl
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
  -> fetch chapter URL
  -> parse content and nextContentUrl
  -> fetch next content pages
  -> apply contentRule.replaceRegex
  -> apply ContentProcessor
  -> save cache
  -> return content
```

## 13. 替换净化设计

替换净化由 `ContentProcessor` 负责：

- 加载标题作用域规则。
- 加载正文作用域规则。
- 去除重复标题。
- 应用标题替换。
- 应用正文替换。
- 对 regex 设置超时。
- 记录实际命中的替换规则。

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

### 14.2 调试模式

支持：

- `keyword`：搜索 -> 详情 -> 目录 -> 正文。
- `info_url`：详情 -> 目录 -> 正文。
- `toc_url`：目录 -> 正文。
- `content_url`：正文。
- `discovery_url`：发现 -> 详情 -> 目录 -> 正文。

这对应 `Debug.startDebug()` 中对关键词、绝对 URL、`::`、`++`、`--` 的分支，但新 API 不复用这些字符串协议，而使用显式字段。

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

### 15.2 校验流程

每个书源：

```text
start checking
  -> remove invalid groups and stale error comments equivalent
  -> search check if enabled
  -> discovery check if enabled and configured
  -> info check
  -> toc check
  -> content check
  -> classify failures
  -> record respondTime
  -> persist result
```

失败分类：

| 分类 | 触发 |
|---|---|
| `timeout` | context deadline exceeded |
| `js_failed` | JS runtime error |
| `website_failed` | fetch error or unclassified runtime error |
| `search_failed` | 搜索空结果 |
| `discovery_failed` | 发现空结果 |
| `toc_failed` | 目录为空或目录解析失败 |
| `content_failed` | 正文为空或正文解析失败 |

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

建议配置项：

```text
READER_ADDR
READER_DATA_DIR
READER_SQLITE_DSN
READER_TOKEN
READER_ENV
READER_DEFAULT_USER_AGENT
READER_FETCH_TIMEOUT
READER_FETCH_MAX_BODY_BYTES
READER_FETCH_MAX_REDIRECTS
READER_ALLOW_PRIVATE_NETWORK
READER_GLOBAL_CONCURRENCY
READER_SOURCE_CONCURRENCY
READER_SCRIPT_TIMEOUT
READER_REGEX_TIMEOUT
READER_DEBUG_BODY_LIMIT
READER_CONTENT_RESEGMENT
READER_CONTENT_CHINESE_CONVERTER_TYPE
READER_REAL_E2E_ENABLED
READER_REAL_E2E_SOURCE_COLLECTION_URL
READER_REAL_E2E_KEYWORD
READER_REAL_E2E_MAX_CANDIDATES
```

生产模式缺少 `READER_TOKEN` 必须拒绝启动。

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
