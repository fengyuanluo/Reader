# Go Web 移植 Legado 在线书源核心能力需求规格

## 1. 背景与目标

本项目位于 `/root/Coding/General/Reader`，当前仓库中 `legado/` 是上游 Android/Kotlin 项目，`docs/` 是本次 Go Web 重构规划文档目录。本次目标不是修改 Android 客户端，而是基于 Legado 的在线书源核心能力，规划一个 OpenAPI-first 的 Go Web API 服务。

目标产品是一个单用户自托管服务：

- 用 Go Web 技术栈提供书源导入、书源管理、搜索、发现、详情、目录、正文、替换净化、调试和校验能力。
- 兼容 Legado 书源 JSON 数据结构和在线书源规则语义。
- 不兼容 Legado 旧 Web API 路由，例如 `/saveBookSource`、`/getBookSources`、`/searchBook`。
- 使用 OpenAPI 文档作为 API 唯一真相源，由 `oapi-codegen` 生成后端接口骨架。
- 一期使用 SQLite 持久化，但通过 repository 抽象保留未来替换 PostgreSQL 的能力。
- 默认部署模型为单体服务，单用户 Bearer Token 认证。

## 2. 原实现事实来源

后续需求、设计和任务拆解必须优先依据以下源码和文档，不得只凭抽象经验设计。

| 能力 | 原实现参考 |
|---|---|
| 项目概览与功能说明 | `legado/README.md` |
| 旧 Web API 与 Content Provider API | `legado/api.md` |
| 书源完整链路研究 | `legado/docs/book-source-chain.md` |
| 书源实体 | `legado/app/src/main/java/io/legado/app/data/entities/BookSource.kt` |
| 通用源能力 | `legado/app/src/main/java/io/legado/app/data/entities/BaseSource.kt` |
| 书籍实体 | `legado/app/src/main/java/io/legado/app/data/entities/Book.kt` |
| 搜索结果实体 | `legado/app/src/main/java/io/legado/app/data/entities/SearchBook.kt` |
| 章节实体 | `legado/app/src/main/java/io/legado/app/data/entities/BookChapter.kt` |
| 替换规则实体 | `legado/app/src/main/java/io/legado/app/data/entities/ReplaceRule.kt` |
| 搜索规则 | `legado/app/src/main/java/io/legado/app/data/entities/rule/SearchRule.kt` |
| 发现规则 | `legado/app/src/main/java/io/legado/app/data/entities/rule/ExploreRule.kt` |
| 详情规则 | `legado/app/src/main/java/io/legado/app/data/entities/rule/BookInfoRule.kt` |
| 目录规则 | `legado/app/src/main/java/io/legado/app/data/entities/rule/TocRule.kt` |
| 正文规则 | `legado/app/src/main/java/io/legado/app/data/entities/rule/ContentRule.kt` |
| 在线书籍门面 | `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt` |
| 搜索/发现列表解析 | `legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt` |
| 详情解析 | `legado/app/src/main/java/io/legado/app/model/webBook/BookInfo.kt` |
| 目录解析 | `legado/app/src/main/java/io/legado/app/model/webBook/BookChapterList.kt` |
| 正文解析 | `legado/app/src/main/java/io/legado/app/model/webBook/BookContent.kt` |
| URL 请求分析 | `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeUrl.kt` |
| 规则执行 | `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeRule.kt` |
| 规则切分 | `legado/app/src/main/java/io/legado/app/model/analyzeRule/RuleAnalyzer.kt` |
| 自定义 URL 属性 | `legado/app/src/main/java/io/legado/app/model/analyzeRule/CustomUrl.kt` |
| 书源导入 | `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt` |
| 书源写入/删除辅助 | `legado/app/src/main/java/io/legado/app/help/source/SourceHelp.kt` |
| 正文后处理 | `legado/app/src/main/java/io/legado/app/help/book/ContentProcessor.kt` |
| 书源旧 API Controller | `legado/app/src/main/java/io/legado/app/api/controller/BookSourceController.kt` |
| 书籍旧 API Controller | `legado/app/src/main/java/io/legado/app/api/controller/BookController.kt` |
| 替换规则旧 API Controller | `legado/app/src/main/java/io/legado/app/api/controller/ReplaceRuleController.kt` |
| 旧 HTTP 服务 | `legado/app/src/main/java/io/legado/app/web/HttpServer.kt` |
| 旧 WebSocket 服务 | `legado/app/src/main/java/io/legado/app/web/WebSocketServer.kt` |
| 搜索 WebSocket | `legado/app/src/main/java/io/legado/app/web/socket/BookSearchWebSocket.kt` |
| 书源调试 WebSocket | `legado/app/src/main/java/io/legado/app/web/socket/BookSourceDebugWebSocket.kt` |
| 批量校验 | `legado/app/src/main/java/io/legado/app/service/CheckSourceService.kt` |
| 调试日志 | `legado/app/src/main/java/io/legado/app/model/Debug.kt` |
| Cookie 存储 | `legado/app/src/main/java/io/legado/app/help/http/CookieStore.kt` |
| Room 数据库 | `legado/app/src/main/java/io/legado/app/data/AppDatabase.kt` |
| 书源 DAO | `legado/app/src/main/java/io/legado/app/data/dao/BookSourceDao.kt` |
| 书籍 DAO | `legado/app/src/main/java/io/legado/app/data/dao/BookDao.kt` |
| 章节 DAO | `legado/app/src/main/java/io/legado/app/data/dao/BookChapterDao.kt` |
| 替换规则 DAO | `legado/app/src/main/java/io/legado/app/data/dao/ReplaceRuleDao.kt` |

## 2.1 文档审阅后修正的问题

本轮对 `Requirement.md`、`Design.md`、`Tasks.md` 做全面审阅后，确认并修正以下潜在问题：

- 原文档只定义 mock E2E，没有把用户指定真实书源集合纳入最终门禁，容易导致实现只对自造样例通过。
- 原任务模板没有强制每个任务开始前先探索 Legado 原实现源码，容易让后续实现者跳过事实源。
- 原任务模板只要求验收门禁，没有把单元测试作为每个任务的强制交付物，容易出现靠人工验证合并的风险。
- 原文档缺少根级 `AGENTS.md` 约束，后续代理可能误修改 `legado/` 上游源码或忽略 OpenAPI-first 规则。
- 当前目录原本不是 git 仓库，且 `legado/` 是上游项目目录；仓库初始化时必须通过忽略规则排除 `legado/`，避免把上游源码纳入本项目版本管理。

这些修正均已在本文、`Design.md`、`Tasks.md` 和根级 `AGENTS.md` 中固化。

## 3. 一期范围

### 3.1 必须实现

- Legado 书源 JSON 导入与管理。
- 书源启用、发现启用、分组、排序、查询、删除。
- 搜索在线书籍。
- 发现分类与发现列表解析。
- 书籍详情解析。
- 章节目录解析。
- 章节正文解析。
- 章节正文缓存。
- 替换净化规则管理与应用。
- 书架保存、删除、列表查询。
- 阅读进度保存。
- 书源调试事件流。
- 书源批量校验任务。
- 书源登录状态查询、登录脚本执行与登录头持久化。
- Token 鉴权。
- OpenAPI 契约、生成代码、契约测试。

### 3.2 明确不做

- 不实现 Android 客户端 UI。
- 不实现旧 Web API 路由兼容。
- 不实现 Android Content Provider。
- 不实现 RSS 订阅源。
- 不实现本地 TXT/EPUB 扫描和阅读。
- 不实现 TTS。
- 不实现主题、阅读排版 UI。
- 不实现 WebDAV、云同步、备份恢复。
- 不实现多用户账号、租户隔离、公开 SaaS 权限模型。
- 不实现完整浏览器 WebView 渲染抓取能力；单纯 `webView:true` 请求降级为普通 HTTP 并记录日志，如遇依赖 WebView 渲染的书源，一期返回明确不支持错误；依赖 DOM 执行的 `webJs`、`serverID`、`webViewDelayTime` 接受但不生效。

## 4. 需求格式说明

本文每条需求使用以下字段：

- 来源依据：对应 Legado 原实现文件。
- 需求描述：必须实现的用户可见行为。
- 输入：API 请求、文件、任务参数或规则。
- 输出：响应、任务状态、缓存数据或错误。
- 边界：明确支持和不支持。
- 验收标准：后续实现必须通过的检查。
- 优先级：P0 为一期必须，P1 为一期建议，P2 为后续扩展。

## 5. 书源数据兼容需求

### R-001：兼容 Legado BookSource JSON 数据模型

- 来源依据：`BookSource.kt`、`SearchRule.kt`、`ExploreRule.kt`、`BookInfoRule.kt`、`TocRule.kt`、`ContentRule.kt`、`legado/docs/book-source-chain.md`
- 需求描述：服务必须能解析并持久化 Legado 书源 JSON 的核心字段，保持字段语义不变。
- 输入：单个 `BookSource` JSON 对象或数组元素。
- 输出：持久化后的书源记录和标准化的 API 响应。
- 边界：必须支持 `bookSourceUrl`、`bookSourceName`、`bookSourceGroup`、`bookSourceType`、`bookUrlPattern`、`customOrder`、`enabled`、`enabledExplore`、`eventListener`、`customButton`、`jsLib`、`enabledCookieJar`、`concurrentRate`、`header`、`loginUrl`、`loginUi`、`loginCheckJs`、`coverDecodeJs`、`bookSourceComment`、`variableComment`、`lastUpdateTime`、`respondTime`、`weight`、`exploreUrl`、`exploreScreen`、`ruleExplore`、`searchUrl`、`ruleSearch`、`ruleBookInfo`、`ruleToc`、`ruleContent`。`eventListener`、`customButton` 和 `ruleReview` 一期只保留字段，不执行 Android 客户端行为。
- 验收标准：导入包含上述字段的 JSON 后，经 OpenAPI、SQLite 和查询接口往返不丢失；规则对象可以是 JSON 对象或 JSON 字符串；缺失 `enabled`、`enabledExplore`、`enabledCookieJar`、`respondTime` 时分别使用 `true`、`true`、`true`、`180000`，显式 `false/0` 保持不变；缺失 `bookSourceUrl` 或 `bookSourceName` 返回结构化 400 错误。
- 优先级：P0

### R-002：兼容 Legado 规则对象结构

- 来源依据：`SearchRule.kt`、`ExploreRule.kt`、`BookInfoRule.kt`、`TocRule.kt`、`ContentRule.kt`
- 需求描述：服务必须按 Legado 规则对象字段建立领域模型，支持搜索、发现、详情、目录、正文解析。
- 输入：书源中的规则字段。
- 输出：可被规则引擎消费的领域对象。
- 边界：一期不实现 `ReviewRule` 行为；`ContentRule.webJs` 与 `imageDecode` 只在设计明确允许的受限 JS / 图片代理范围内执行；`subContent`、`callBackJs`、`imageStyle`、`payAction` 作为兼容字段保留，其中前两者在正文请求时记录未执行诊断，均不阻断主正文规则。
- 验收标准：每个规则对象都有单元测试覆盖 JSON 对象和 JSON 字符串两种导入形式；未知字段不会破坏导入；导出查询保留未知兼容字段。
- 优先级：P0

### R-003：兼容替换规则 ReplaceRule

- 来源依据：`ReplaceRule.kt`、`ReplaceRuleController.kt`、`ContentProcessor.kt`
- 需求描述：服务必须支持 Legado 替换规则的核心字段与标题/正文作用域。
- 输入：替换规则 JSON。
- 输出：替换规则记录、测试替换结果、正文后处理结果。
- 边界：必须支持普通字符串替换和正则替换；必须支持正则超时；正则超时策略为返回错误并按设计禁用或标记规则。
- 验收标准：保存空 `pattern` 返回错误；非法正则返回错误；标题作用域和正文作用域分别生效；测试接口能返回替换后的文本或结构化错误。
- 优先级：P0

## 6. 书源导入与管理需求

### R-010：导入单个书源 JSON 对象

- 来源依据：`ImportBookSourceViewModel.kt`、`BookSourceController.kt`
- 需求描述：用户可以通过 API 上传单个 Legado 书源 JSON 对象。
- 输入：`POST /api/v1/book-sources/import`，body 包含 JSON 对象。
- 输出：导入结果，包括新增数量、更新数量、跳过数量、错误列表。
- 边界：只要 `bookSourceUrl` 和 `bookSourceName` 合法即可导入；规则字段可为空。
- 验收标准：合法对象导入后可通过详情接口查询；非法对象返回 400；重复导入时按 `lastUpdateTime` 策略处理。
- 优先级：P0

### R-011：导入书源 JSON 数组

- 来源依据：`ImportBookSourceViewModel.kt`
- 需求描述：用户可以一次导入多个书源。
- 输入：JSON 数组。
- 输出：批量导入结果。
- 边界：数组中单条失败不得导致全部失败，除非整个 JSON 无法解析。
- 验收标准：混合有效/无效书源时，有效项落库，错误项进入错误列表；响应中包含每条失败原因。
- 优先级：P0

### R-012：导入 sourceUrls 聚合文档

- 来源依据：`ImportBookSourceViewModel.kt`
- 需求描述：兼容包含 `sourceUrls` 的聚合 JSON，服务需要逐个下载 URL 并导入书源数组。
- 输入：`{"sourceUrls":["https://..."]}`。
- 输出：聚合导入结果。
- 边界：远程 URL 请求必须经过 SSRF 防护、超时和响应大小限制。
- 验收标准：mock server 返回书源数组时导入成功；远程下载失败时记录对应 URL 错误，不影响其他 URL；私网地址默认被拦截。
- 优先级：P0

### R-013：支持远程 URL 导入

- 来源依据：`ImportBookSourceViewModel.kt`
- 需求描述：用户可以传入远程 URL，服务下载响应体并解析为书源数组。
- 输入：导入请求中的 URL。
- 输出：导入结果。
- 边界：只接受 `http` 和 `https`；默认禁止访问 loopback、link-local、private IP、metadata IP；支持配置显式放开本地测试。
- 验收标准：mock HTTP URL 导入成功；非法 scheme 返回错误；私网 SSRF 请求被拒绝。
- 优先级：P0

### R-014：真实远程书源集合最终 E2E 验收

- 来源依据：用户指定验收 URL、`ImportBookSourceViewModel.kt`、`WebBook.kt`、`BookList.kt`、`BookInfo.kt`、`BookChapterList.kt`、`BookContent.kt`
- 需求描述：一期最终验收必须引入用户指定真实远程书源集合，验证真实 Legado 书源 JSON 兼容性和完整在线书源链路，而不能只依赖 mock fixtures。
- 输入：`https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361`
- 当前核验事实：2026-05-03 通过 `curl -L` 核验该 URL 返回 HTTP 200，`content-type: application/json`，响应体约 4.85 MB，为 Legado 书源 JSON 数组，共 459 条；前几项包含 `墨辰整理书源系列7.1版`、`起点读书（优+++）`、`番茄小说（优+++）` 等书源，且包含 `searchUrl`、`ruleSearch`、`ruleToc`、`ruleContent` 等核心字段。
- 输出：真实远程集合导入结果、候选书源筛选结果、真实搜索/详情/目录/正文 E2E 报告。
- 边界：真实远程站点可能随时间变化或受网络/反爬影响；该验收作为最终 release gate 或 nightly/manual gate，不替代稳定的 mock E2E；失败时必须区分实现缺陷、书源规则失效、目标站点不可用、网络不可用和安全策略拦截。
- 验收标准：服务能在安全策略允许该域名的情况下下载并解析该集合；至少能导入集合中的有效书源记录；从集合中自动筛选若干启用且具备搜索/详情/目录/正文规则的候选书源；至少 1 个候选书源完成搜索 -> 详情 -> 目录 -> 第一章正文链路，或输出结构化失败分类并保留可审计日志；最终 E2E 报告必须包含测试时间、集合 URL、HTTP 状态、响应大小、解析条数、候选书源、关键词、成功链路或失败分类。
- 优先级：P0

### R-015：支持 `#requestWithoutUA`

- 来源依据：`ImportBookSourceViewModel.kt`
- 需求描述：远程导入 URL 以 `#requestWithoutUA` 结尾时，请求不发送默认 User-Agent。
- 输入：`https://example.test/source.json#requestWithoutUA`。
- 输出：导入结果。
- 边界：实际请求 URL 必须去掉后缀。
- 验收标准：mock server 断言请求没有默认 UA；普通 URL 断言请求有默认 UA。
- 优先级：P0

### R-016：书源查询、筛选与分页

- 来源依据：`BookSourceDao.kt`
- 需求描述：用户可以按关键词、分组、启用状态、发现启用状态查询书源列表。
- 输入：列表 API query 参数。
- 输出：分页列表和总数。
- 边界：关键词匹配 `bookSourceName`、`bookSourceGroup`、`bookSourceUrl`、`bookSourceComment`。
- 验收标准：筛选逻辑有 repository 测试；分页参数越界返回结构化错误或按默认值处理；排序稳定。
- 优先级：P0

### R-017：书源启用、发现启用与排序

- 来源依据：`BookSourceDao.kt`、`SourceHelp.kt`
- 需求描述：用户可以独立控制搜索启用、发现启用、手动排序。
- 输入：PATCH 请求。
- 输出：更新后的书源摘要。
- 边界：排序号重复时必须有归一化策略。
- 验收标准：启用状态影响搜索范围；发现启用状态影响发现范围；排序接口能批量更新并保持事务一致。
- 优先级：P0

### R-018：删除书源时清理关联数据

- 来源依据：`SourceHelp.kt`、`CacheDao.kt`
- 需求描述：删除书源时，应清理源变量、临时缓存和相关 Cookie 策略中明确归属该源的数据。
- 输入：删除书源请求。
- 输出：删除结果。
- 边界：已加入书架的书籍是否保留由设计文档明确；一期默认保留书籍但标记来源缺失，避免误删用户书架。
- 验收标准：删除书源后源变量不可再查询；书源列表不再出现；书架记录处理符合设计说明。
- 优先级：P0

## 7. OpenAPI 与 API 需求

### R-020：OpenAPI 是接口唯一真相源

- 来源依据：用户需求、`legado/api.md` 对旧 API 的事实参考
- 需求描述：所有对外 HTTP API 必须先在 OpenAPI 中定义，再通过 `oapi-codegen` 生成 Go 类型和 server interface。
- 输入：`api/openapi.yaml`。
- 输出：生成代码和 API handler 实现。
- 边界：禁止 handler 使用未在 OpenAPI 中声明的请求/响应结构。
- 验收标准：`go generate ./...` 后无未提交 diff；OpenAPI 校验通过；契约测试覆盖主要 2xx 和 4xx 响应。
- 优先级：P0

### R-021：不兼容旧 API 路由

- 来源依据：用户确认：兼容性主要体现在支持导入书源等信息文件，不要求接口层兼容
- 需求描述：新服务使用 `/api/v1` REST API，不复刻旧 `/saveBookSource` 等路由。
- 输入：HTTP 请求。
- 输出：新 API 响应。
- 边界：可在文档中说明旧路由不支持；不得为旧路由引入业务实现。
- 验收标准：OpenAPI 不包含旧路由；文档明确迁移方式；旧 API 兼容不作为验收项。
- 优先级：P0

### R-022：统一错误响应

- 来源依据：`ReturnData.kt` 旧包络仅作为反例参考
- 需求描述：新服务必须使用统一错误 schema，至少包含 `code`、`message`、`details`、`traceId`。
- 输入：所有失败请求。
- 输出：结构化错误响应。
- 边界：不得返回未包装的 panic、堆栈或 HTML 错误页。
- 验收标准：认证失败、参数错误、规则错误、抓取错误、JS 错误、超时错误都有示例和测试。
- 优先级：P0

### R-023：Token 鉴权

- 来源依据：用户确认单用户 + Token
- 需求描述：除健康检查、版本信息、OpenAPI 文档外，所有管理和抓取 API 默认需要 Bearer Token。
- 输入：`Authorization: Bearer <token>`。
- 输出：通过或 401/403。
- 边界：一期不实现多用户，不实现刷新 token，不实现角色权限。
- 验收标准：缺失 token 返回 401；错误 token 返回 403 或 401；日志不打印 token 原文。
- 优先级：P0

## 8. 抓取与规则执行需求

### R-030：URL 请求分析

- 来源依据：`AnalyzeUrl.kt`、`CustomUrl.kt`
- 需求描述：服务必须将书源中的 URL 规则解析为实际 HTTP 请求，包括关键词、页码、自定义属性、Header、Body 和响应后处理。
- 输入：书源 URL 规则、关键词、页码、书籍/章节上下文。
- 输出：可执行的 fetch request。
- 边界：统一解析 `method/body/headers/type/charset/origin/retry/js/bodyJs`；`proxy`（书源 `header` 的 `proxy` 键或 URL 选项，支持 `http`/`https`/`socks5`/`socks5h`，不支持 `socks4`）与 `dnsIp`（直连指定 IP，保留 Host 头与 TLS SNI，不可与 `proxy` 同用）在抓取链真实生效；`serverID`、`webJs`、`webViewDelayTime` 接受但不生效；`webView: true` 只作带日志的普通 HTTP 降级，不承诺渲染，其余浏览器专属 option 返回明确错误。
- 支持的 URL option：`proxy`、`dnsIp`、`charset`（query/body 编码）。
- 验收标准：关键词、页码、自定义属性、相对 URL、`js` 请求变换和 `bodyJs` 响应变换有单元测试；搜索、发现、详情、目录、正文及 JS 网络 helper 共用同一 option 语义。
- 优先级：P0

### R-031：HTTP 抓取安全控制

- 来源依据：`AnalyzeUrl.kt`、`CookieStore.kt`
- 需求描述：所有外部请求必须有超时、并发、响应大小、重定向和 SSRF 防护。
- 输入：抓取请求。
- 输出：响应体或结构化抓取错误。
- 边界：默认禁止私网、回环、链路本地、metadata 地址，以及 CGN、NAT64、6to4、文档段等特殊用途地址段；SSRF 校验在拨号阶段对每个解析结果执行，可防 DNS rebinding 与重定向绕过；忽略 `HTTP_PROXY`/`HTTPS_PROXY` 环境变量，代理只来自书源配置，经代理抓取时目标校验为尽力而为；重定向后目标也必须检查；`concurrentRate` 按书源键应用固定窗口限速，等待可被 context 取消。
- 验收标准：mock 测试覆盖超时、过大响应、过多重定向、私网地址、Cookie 合并和每源限速。
- 优先级：P0

### R-032：Header、Cookie 与登录检测

- 来源依据：`BaseSource.kt`、`CookieStore.kt`、`WebBook.kt`
- 需求描述：服务必须支持书源 Header、Header JS、Cookie Jar、登录 Header、`loginCheckJs` 的受限实现。
- 输入：书源配置和 HTTP 响应。
- 输出：带 Header/Cookie 的请求，或被 `loginCheckJs` 转换后的响应。
- 边界：不实现 Android WebView 登录 UI；`loginUi` 只作为兼容字段和后续扩展。Cookie 过期或 `expires_at` 损坏时不再发送并删除；`Max-Age <= 0` 视为删除；`enabledCookieJar` 按书源生效。
- 验收标准：Header JSON 生效；Header JS 返回 JSON 生效；Cookie 按域保存和合并；`loginCheckJs` 能在沙箱内运行并处理响应。
- 优先级：P0

### R-033：规则解析基础能力

- 来源依据：`AnalyzeRule.kt`、`RuleAnalyzer.kt`、`AnalyzeByJSoup.kt`、`AnalyzeByXPath.kt`、`AnalyzeByJSonPath.kt`、`AnalyzeByRegex.kt`
- 需求描述：规则引擎必须支持常用 CSS/JSoup 类规则、XPath、JSONPath、Regex、JS 片段和规则链式组合。
- 输入：规则字符串、HTML/JSON/文本内容、上下文对象。
- 输出：字符串、字符串列表、元素集合或结构化规则错误。
- 边界：完全兼容 Legado 所有边缘规则不是一期承诺；必须记录不兼容规则并返回明确错误。
- 验收标准：黄金测试覆盖 HTML、JSON、XPath、Regex、链式规则、替换片段、空结果、非法规则。
- 优先级：P0

### R-034：受限 JS 沙箱

- 来源依据：`BaseSource.kt`、`AnalyzeRule.kt`、`modules/rhino/src/main/java/com/script/*`
- 需求描述：服务必须提供受限 JS 执行能力，用于 Header JS、规则 JS、`loginCheckJs`、部分格式化 JS。
- 输入：JS 代码、绑定变量、执行阶段。
- 输出：JS 返回值或结构化 JS 错误。
- 边界：禁止进程、任意网络、宿主反射、无限循环；文件 API 只允许 `<DataDir>/script-files` 沙箱（越界路径与压缩包越界条目被拒绝，单次解压总量上限 64 MiB/4096 条，无沙箱根时抛 `script_unsupported`）；浏览器类 API（`webView*`、`startBrowser*`、`getVerificationCode`、`openUrl`）与 7z/rar 抛 `script_unsupported`，脚本可 try/catch；不提供 RSS 文章链路相关的 `rssArticle`。
- 验收标准：无限循环被超时中断；沙箱外文件不可访问；错误包含 sourceId 和阶段（`<kind>: stage=<stage> source=<sourceId>: msg`）；敏感信息脱敏。
- 优先级：P0

### R-035：书源登录状态与登录脚本

- 来源依据：`BaseSource.kt`、`SourceLoginViewModel.kt`
- 需求描述：服务必须提供书源登录能力：查询/提交/清除登录状态；提交时先把表单值持久化为 `loginInfo`，再执行 `loginUrl` 的 login 脚本，脚本写入的登录头持久化为 `loginHeader`，并用于后续请求链。
- 输入：书源 ID、可选的 `values` 表单键值。
- 输出：`BookSourceLoginState{sourceId,hasLoginUrl,hasLoginUi,loggedIn,loginInfo,loginHeader}`。
- 边界：不引入无头浏览器；`loginUi` 为空且 `loginUrl` 非脚本时返回 501 `unsupported_login_flow`；提交空 `values` 等价于只清除 `loginInfo`。
- 验收标准：书源不存在 404；表单值非法 400；脚本失败 502；`DELETE` 清除后状态为未登录并可 204 返回。
- 优先级：P1

## 9. 在线书源业务闭环需求

### R-040：单源搜索

- 来源依据：`WebBook.kt`、`BookList.kt`、`SearchModel.kt`
- 需求描述：给定书源和关键词，服务能解析搜索 URL，抓取响应并提取 `SearchBook` 列表。
- 输入：书源 ID、关键词、页码。
- 输出：搜索结果列表。
- 边界：未启用书源默认不能参与全局搜索，但单源调试可显式指定。
- 验收标准：mock 书源完成搜索；空结果返回空列表而非 500；规则错误返回结构化错误。
- 优先级：P0

### R-041：多源搜索与结果合并

- 来源依据：`SearchModel.kt`
- 需求描述：服务能对多个启用书源并发搜索，并按书名/作者合并重复结果。
- 输入：关键词、分组或书源范围、页码、精准搜索开关。
- 输出：合并后的搜索结果。
- 边界：并发数受全局和每源限制；某个书源失败不导致整个搜索失败。
- 验收标准：多个 mock 书源中重复书籍合并；失败书源进入 warnings；禁用书源不参与。
- 优先级：P0

### R-042：发现分类与发现列表

- 来源依据：`BookList.kt`、`ExploreRule.kt`、`SourceHelp.exploreKinds` 相关链路
- 需求描述：服务能解析 `exploreUrl` 中的发现入口，并抓取发现列表。
- 输入：书源 ID、发现 URL 或分类标识、页码。
- 输出：发现结果列表。
- 边界：`exploreScreen` 一期保留字段，不承诺完整筛选 UI 行为。
- 验收标准：有发现 URL 的书源可返回分类；发现列表解析与搜索列表共用解析模型；空发现分类返回明确错误。
- 优先级：P1

### R-043：详情解析

- 来源依据：`BookInfo.kt`、`BookInfoRule.kt`
- 需求描述：服务能根据搜索结果或书籍 URL 获取书名、作者、分类、字数、最新章节、简介、封面、目录 URL。
- 输入：书源、书籍 URL、可选 `infoHtml`。
- 输出：标准化 Book。
- 边界：`downloadUrls` 用于文件类书源，可指向 TXT 等文件；一期提取并返回地址，不实现文件下载器。
- 验收标准：规则 `init` 生效；`canReName` 行为按设计实现；相对封面 URL 转绝对 URL；文件地址只绝对化 URL 部分，逗号后的请求 option 原文保持不变。
- 优先级：P0

### R-044：目录解析

- 来源依据：`BookChapterList.kt`、`TocRule.kt`
- 需求描述：服务能解析章节列表、卷标、VIP、付费、更新时间、下一页目录。
- 输入：书源、Book、目录 URL 或 `tocHtml`。
- 输出：章节列表。
- 边界：必须支持串行下一页；并行下一页按设计实现；章节去重和顺序必须稳定。
- 验收标准：空目录返回 `TocEmpty` 类型错误；下一页目录合并后索引连续；卷标章节不要求正文。
- 优先级：P0

### R-045：正文解析

- 来源依据：`BookContent.kt`、`ContentRule.kt`
- 需求描述：服务能解析章节正文、正文标题、下一页正文、正文替换规则、图片标签保留或转换。
- 输入：书源、Book、BookChapter。
- 输出：章节正文字符串和缓存记录。
- 边界：图片章节和音频章节一期不作为核心验收；正文图片代理作为 P1；依赖 WebView 浏览器流的 `sourceRegex` 一期返回明确不支持错误；`subContent/callBackJs` 保留并记录未执行诊断，不影响主正文。
- 验收标准：空的非卷章节正文返回 `ContentEmpty` 类型错误；多页正文按顺序合并；缓存命中时不重复抓取。
- 优先级：P0

### R-045A：全书 TXT 导出

- 需求描述：用户可以将已建立目录的文本书籍按目录顺序导出为完整 UTF-8 TXT。
- 输入：书籍 ID、已保存目录；正文按需抓取并复用章节缓存。
- 输出：带书名、作者、简介、卷名/章节标题和正文的 TXT 附件。
- 边界：同步生成临时文件，全部正文章成功后才发送；文件型书源仍只提供 `downloadUrls`，不在此接口下载；卷标不请求正文。
- 验收标准：搜索 -> 保存书籍 -> 详情 -> 目录 -> 全部章节 -> TXT 下载链路成功；任一正文章失败不返回残缺文件，成功章节可在重试时命中缓存。
- 优先级：P1

### R-046：书架与阅读进度

- 来源依据：`Book.kt`、`BookController.kt`、`BookDao.kt`
- 需求描述：用户可以把搜索结果加入书架，查询书架，删除书籍，保存阅读进度。
- 输入：Book 或 SearchBook 转换结果、进度字段。
- 输出：书架记录和进度状态。
- 边界：不实现 Android 阅读 UI；只保存 API 层进度字段。
- 验收标准：添加书籍后可刷新目录；进度保存后查询书籍详情可见；删除书籍删除章节和正文缓存。
- 优先级：P0

## 10. 替换净化需求

### R-050：正文后处理

- 来源依据：`ContentProcessor.kt`、`BookChapter.kt`
- 需求描述：获取正文时必须支持去除重复标题、标题替换、正文替换、段落整理。
- 输入：Book、BookChapter、原始正文、替换规则。
- 输出：处理后的正文。
- 边界：简繁转换一期可作为 P2；Android 版本差异处理不需要迁移。
- 验收标准：重复标题被移除；标题规则只影响标题；正文规则只影响正文；替换前后有测试。
- 优先级：P0

### R-051：正则超时保护

- 来源依据：`ReplaceRule.kt`、`ContentProcessor.kt`
- 需求描述：替换规则执行必须可超时，避免灾难性回溯阻塞服务。
- 输入：正则替换规则和正文。
- 输出：替换结果或超时错误。
- 边界：超时策略必须写入设计；不能无上限执行 Go 正则或第三方正则。
- 验收标准：构造慢正则测试能被限制；超时结果可观测；不会导致进程崩溃。
- 优先级：P0

## 11. 调试需求

### R-060：书源调试会话

- 来源依据：`Debug.kt`、`BookSourceDebugWebSocket.kt`
- 需求描述：用户可以启动书源调试，按关键词、详情 URL、目录 URL 或正文 URL 进入不同调试路径，并获取阶段日志。
- 输入：书源 ID、调试模式、关键词或 URL。
- 输出：调试 session ID 和事件流。
- 边界：事件流可用 SSE 或 WebSocket，需在设计中固定；一期推荐 SSE，便于 OpenAPI 文档描述。
- 验收标准：关键词调试按搜索、详情、目录、正文输出事件；URL 调试可从指定阶段开始；取消调试会中断抓取和 JS。
- 优先级：P0

### R-061：调试日志脱敏

- 来源依据：`Debug.kt`
- 需求描述：调试日志必须有阶段、耗时、消息、错误；不得泄露 Token、Cookie、Authorization、登录凭据。
- 输入：调试运行时日志。
- 输出：脱敏事件。
- 边界：原始 HTML 可按配置截断输出；默认不得输出超大响应体。
- 验收标准：日志脱敏测试覆盖 Authorization、Cookie、Set-Cookie；长响应被截断。
- 优先级：P0

## 12. 书源校验需求

### R-070：批量校验任务

- 来源依据：`CheckSourceService.kt`、`CheckSource.kt`
- 需求描述：用户可以启动批量校验任务，对多个书源并发执行搜索、发现、详情、目录、正文检查。
- 输入：书源 ID 列表、校验选项、超时；关键词为空或仅作为 fallback 时优先使用合法的 `ruleSearch.checkKeyWord`。
- 输出：任务 ID、单调进度、每源结果、实际 `keywordUsed/timeoutMs` 及按搜索/发现区分的阶段结果。
- 边界：校验任务可取消；某个书源失败不影响其他书源；搜索与发现分别执行后续详情/目录/正文；`bookSourceType=3` 只校验详情和 `downloadUrls`，目录/正文为不适用，无搜索规则可跳过。
- 验收标准：并发执行和 `-race` 有测试；任务状态从 pending/running/cancelled/succeeded/failed 可查询；每完成一项即持久化单调进度；默认超时为 180 秒；创建和取消异步任务均返回 202；运行中 SSE 使用 `check.progress`。
- 优先级：P0

### R-071：校验失败分类

- 来源依据：`CheckSourceService.kt`
- 需求描述：校验必须保留书源 warning 原文，并在标准阶段 code 之外区分空结果、上游 HTTP/响应、网络、脚本/规则、运行时 API、浏览器边界和内部错误。
- 输入：校验过程中的异常和空结果。
- 输出：标准 `failureCode`、细分 `failureClass` 和未改写的用户可读 `message`。
- 边界：只有明确的 `empty search/discovery result`、`empty toc|toc_empty`、`empty content|content_empty` 才归为空结果；网络、HTTP、浏览器能力和其他通用错误不得因所在阶段被改写为空。网络等非确定性失败不写入永久“失效”分组或注释。
- 验收标准：每种失败都有最小测试；warning 原文、阶段、书源响应时间和错误摘要被持久化；`EOF` 与 TLS handshake timeout 等传输错误分类稳定。
- 优先级：P0

## 13. 数据与持久化需求

### R-080：SQLite 一期持久化

- 来源依据：`AppDatabase.kt`
- 需求描述：一期使用 SQLite 存储书源、书籍、章节、替换规则、Cookie、缓存、任务、调试日志。
- 输入：应用配置中的 SQLite DSN（数据库文件路径）。
- 输出：可迁移的数据库。
- 边界：业务层不得依赖 SQLite 具体实现；必须通过 repository 接口访问。数据目录以 `0700` 创建、数据库文件强制 `0600`；连接级 PRAGMA（`foreign_keys(1)`、`busy_timeout(5000)`、`journal_mode(WAL)`）由 `sqlite.Open` 通过 DSN `_pragma` 对每条连接生效；schema 变更用 `internal/infra/sqlite/migrate.go` 内嵌的版本化迁移列表，已发布版本禁止修改，结构变更只追加新版本。
- 验收标准：空库迁移成功；重复迁移不破坏数据；repository 测试覆盖事务。
- 优先级：P0

### R-081：缓存策略

- 来源依据：`BookController.kt`、`ContentProcessor.kt`、`CookieStore.kt`
- 需求描述：服务必须缓存章节正文、源变量、Cookie、任务/调试状态。
- 输入：抓取和解析产物。
- 输出：缓存记录。
- 边界：缓存必须可按书源或书籍清理（删除书源/书籍时随事务清理）；缓存命中策略必须可测试。图片缓存有效期 7 天，读取时跳过过期行，服务启动时清理过期行；章节正文缓存不过期，且只保存“书源 `replaceRegex` 之后、用户替换规则之前”的原文，用户替换规则在每次读取时应用（改规则立即生效）。脚本 `cache` 为进程内有界缓存，不落库。
- 验收标准：章节正文缓存命中避免二次网络请求；修改替换规则后读取正文立即生效；删除书籍清理章节正文缓存；删除书源清理源变量。
- 优先级：P0

### R-082：CLI 模式

- 来源依据：用户确认的单二进制 CLI 决策
- 需求描述：单二进制 `reader`，`serve` 子命令承接现有 HTTP 服务；`import`/`source`/`search`/`book`/`toc`/`content`/`download` 子命令直连服务层与 SQLite，不启动 server、不需要 token、不产生 HTTP 依赖。覆盖书源导入、书源列表/详情/启停、搜索、加书架、详情、目录、单章正文、整本下载（SQLite 缓存 + 默认 TXT 导出，`--no-export` 关闭）。
- 输入：`READER_*` 环境变量与命令行参数。
- 输出：默认 tab 分隔行式输出；`--json` 机读 JSON；进度与错误到 stderr。
- 边界：全部子命令非交互；CLI 不鉴权（直连数据库，`READER_TOKEN` 仅 `serve` 需要）；退出码 0=成功、1=用法/参数错误、2=运行失败；发现/调试/校验/替换规则的 CLI 子命令不在一期范围。
- 验收标准：import → source list → search → book add → book refresh → toc → download 全链可在不启动 server 的情况下完成；`--json` 输出可被 jq 解析；`serve` 与原 HTTP 服务行为等价。
- 优先级：P1

## 14. 安全与运维需求

### R-090：服务配置

- 来源依据：用户确认技术栈和部署模型
- 需求描述：服务必须通过配置控制监听地址、SQLite 路径、Token、超时、并发、响应大小、是否允许私网抓取。
- 输入：环境变量或配置文件。
- 输出：运行时配置。
- 边界：敏感配置不得打印原文。默认监听 `127.0.0.1:8080`（回环）；`READER_TOKEN` 为空时只允许监听回环地址（`127.0.0.0/8`、`::1`、`localhost`），监听非回环地址时拒绝启动（退出码 2，不建库），该规则对所有环境生效；`READER_ENV=production` 无论监听哪里都必须设置 token；`READER_ENV=test` 且 token 为空时会填入公开默认 token `reader-test-token`（本地/CI 便利，不可暴露到不可信网络）。
- 验收标准：缺少 Token 时生产模式拒绝启动；空 token 监听非回环地址拒绝启动；测试模式可使用默认 token；配置错误返回启动错误。
- 优先级：P0

### R-091：可观测性

- 来源依据：`Debug.kt`、`CheckSourceService.kt`
- 需求描述：服务必须有结构化日志、traceId、错误码、任务状态和基础健康检查。
- 输入：运行时事件。
- 输出：日志和 API 状态。
- 边界：一期不强制接入 Prometheus，但设计应预留 metrics 接口。
- 验收标准：每个 API 响应有 traceId；抓取和 JS 错误能关联 sourceId；健康检查不需要认证。
- 优先级：P0

### R-092：容器化发布

- 来源依据：用户确认的 GitHub 与 GHCR 发布模型。
- 需求描述：项目提供非 root、双架构的 OCI 镜像，通过 GHCR 发布并支持 Docker Compose 部署。
- 输入：`READER_TOKEN` 及其他可选环境变量。
- 输出：`linux/amd64`、`linux/arm64` 镜像和可持久化 SQLite 的单服务部署。
- 边界：数据固定持久化到 `/data`，临时 TXT 使用 `/tmp`；不提交凭据，不增加自动清理、签名或额外发布系统。
- 验收标准：容器健康检查通过；Bearer 鉴权有效；SQLite 写入在容器重启后保留；公开 GHCR 标签可匿名拉取。
- 优先级：P0

## 15. 总体验收标准

一期完成时，必须通过以下端到端验收：

- 能从 Legado 书源 JSON 数组导入至少 3 个 mock 书源。
- 能从 `https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361` 下载并解析真实远程书源集合，生成真实集合 E2E 报告。
- 能对启用书源执行关键词搜索并返回标准 SearchBook。
- 能从搜索结果解析详情、目录和第一章正文。
- mock 文本书源能在请求 `js/bodyJs` 生效后完成搜索到正文；文件型书源能提取 TXT `downloadUrls` 并保持其请求 option 原文。
- 能把书籍加入书架并保存阅读进度。
- 能应用标题替换和正文替换规则。
- 能启动调试 session 并看到搜索、详情、目录、正文阶段事件。
- 能启动批量校验任务并得到每个书源的分类结果。
- 能通过 Docker 或 Compose 以非 root 用户运行，并在 `/data` 持久化 SQLite。
- 能通过 `go generate ./...`、`go test -race -count=1 ./...`、`go vet ./...`、OpenAPI 校验和 SQLite migration 测试。
