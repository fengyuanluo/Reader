# Reader 全项目审阅问题清单（2026-10-15）

> 只读审计产物。审阅对象：`/root/Coding/General/Reader` 当前工作树（`master@a6def37` + 未提交变更：删除 `cmd/server/`，新增 `cmd/reader/`、`internal/cli/`、`internal/bootstrap/`、`docs/CLI.md`、`docs/skills/`，以及 README/docs/content/diagnostic/migrate/E2E 报告的修改）。方法：16 个切片审阅子代理 → 合并去重 → 对全部 P0/P1 及低置信度条目派发 6 个复核子代理（临时测试仅写入 `/tmp`）→ 主代理抽查。未运行真实远程 E2E；对齐现状引用 `test/reports/real_source_collection_e2e_report.*` 现有快照（testTime 2026-10-14T10:05:57Z，未重跑）。

## 1. 概要

### 1.1 基线门禁

| 门禁 | 结果 |
|---|---|
| `go build ./...` | 通过（exit 0） |
| `go vet ./...` | 通过，无输出 |
| `go test ./... -count=1` | 通过：34 包 ok，0 失败；5 包无测试：根包 `reader`、`internal/api/generated`、`internal/bootstrap`、`internal/domain/fetch`、`internal/domain/rule` |
| 生成一致性 | 用 `api/oapi-codegen.yaml` 同配置输出到 `/tmp/reader-gen/server.gen.go`，与 `internal/api/generated/server.gen.go` 逐字节一致 |
| `go test -race`（TestQualityAudit 抽样：search/worker/check/fetcher/script/debug 现有测试） | 通过；但复核阶段用临时并发测试复现了 2 处数据竞争（R 表中 script Runtime、replace Processor） |
| staticcheck / golangci-lint / deadcode | 未安装，未运行 |
| 真实远程 E2E | 未运行（需网络与显式开关） |

### 1.2 严重度 × 维度

| 严重度 | 功能完整性 | 可用性 | Legado对齐 | 代码质量 | 安全 | 性能 | 测试 | 文档一致性 | 架构分层 | 合计 |
|---|---|---|---|---|---|---|---|---|---|---|
| P0 | 0 | 0 | 0 | 0 | 2 | 0 | 0 | 0 | 0 | 2 |
| P1 | 1 | 0 | 11 | 2 | 0 | 0 | 0 | 0 | 0 | 14 |
| P2 | 15 | 7 | 32 | 6 | 4 | 8 | 4 | 4 | 3 | 83 |
| P3 | 5 | 5 | 10 | 11 | 3 | 2 | 1 | 13 | 1 | 51 |
| 合计 | 21 | 12 | 53 | 19 | 9 | 10 | 5 | 17 | 4 | 150 |

严重度定义：P0=安全漏洞/数据损坏/核心链路(导入→搜索→详情→目录→正文)不可用；P1=主流书源功能错误或与 Legado 语义不一致导致失败；P2=边缘场景缺陷、可用性问题、明显代码质量问题；P3=风格/文档小偏差。

### 1.3 切片结论

| 切片 | 范围结论 |
|---|---|
| CliBootstrapAudit | CliBootstrapAudit（cmd/reader、internal/cli、internal/bootstrap、docs/CLI.md、docs/skills/reader-cli）完成。未发现 P0/P1：bootstrap.Open 与旧 newRuntimeServer 逐行等价，serve 的信号/优雅关闭/超时/鉴权行为及旧 main_test 三个用例均保留；Makefile/scripts/verify.sh/ci.yml 无 cmd/server 残留；CLI 与 docs/Requirement.md R-082 边界一致（非交互、不鉴权、退出码 0/1/2、serve 承接 HTTP），三份基线均已同步记录 CLI。发现 1 个 P2 数据丢失缺陷及若干 P2/P3 问题。覆盖：cmd/reader/{main,serve,serve_test}.go、internal/cli/{root,context,import,source,search,book,download,cli_test}.go、internal/bootstrap/bootstrap.go、docs/CLI.md、docs/skills/reader-cli/SKILL.md、Makefile、scripts/verify.sh、.github/workflows/ci.yml、AGENTS.md、docs/Requirement.md(R-082)、docs/Design.md:888、docs/Tasks.md(T-C01)；实际运行 go run ./cmd/reader --help 与子命令 help 核对。无问题文件：Makefile、scripts/verify.sh、ci.yml、cmd/reader/main.go、internal/cli/context.go、docs/skills/reader-cli/SKILL.md、internal/bootstrap/bootstrap.go。附带范围（属于任务但供汇总）：`coverage` 字段已在下方 findings 之外给出。 |
| TestQualityAudit | 测试与门禁体系总体质量较高：真实 E2E 由环境变量门控，未提交修改中的失败分类扩展（tls handshake timeout/EOF/refused）与 diagnosticapp 保持镜像一致且有表驱动测试覆盖；-race 全量运行未发现数据竞争；已删除 cmd/server/main_test.go 的覆盖已原样迁移。主要缺口：CI/verify 缺少 -race 与 -count=1，serve 入口测试落在无路由死代码上导致 newRuntimeServer 装配零 HTTP 覆盖，以及固定 sleep 轮询的 flaky 测试与残留旧路径引用。 |
| ApiContractAudit | API 契约层整体功能完整（spec 中所有端点均已实现，无 501 stub 暴露给运行时，Unimplemented 仅作内嵌默认），错误响应统一含 traceId，鉴权 /health 等豁免与文档一致；但存在 P1 级问题：请求体无大小限制、check/debug 的 201/200 与 spec 202 不一致、check 事件流为伪流式（item 级事件无消费路径），以及若干 P2/P3：hasMore 恒 false、分页上限未强制、Book DTO 字段静默丢弃、handler 层含业务逻辑、docs/API.md 覆盖不全。 |
| PersistenceArchAudit | PersistenceArchAudit 完成：审阅 sqlite 迁移/7 个 repository、domain 全部包、bootstrap 装配与空 cache 目录，发现 15 项问题（P2×9、P3×6，无 P0/P1）：连接级 PRAGMA 失效、迁移未版本化、JSON 反序列化错误吞没、缓存无 TTL、非原子状态机更新、LIKE 误匹配、批量导入整批回滚等；migration 幂等测试通过，books 表 upsert 冲突目标与唯一约束一致（未报）。 |
| BookTocAudit | Reader 的 info/toc 实现与 Legado BookInfo/BookChapterList 主体对齐（init 规则、tocUrl 回退、canReName、`-` 倒序、空 URL 处理、单 next 顺序+去重、formatJS、错误与空目录区分均一致），但存在 8 处偏差：最严重的是 isTruthy 语义与 Legado isTrue 相反导致 isVip/isPay/isVolume 大量误判，以及 preUpdateJS/formatJS 单次失败导致整次刷新失败（Legado 仅记日志继续）；其余为字段格式化缺失、文件书源空链接静默通过、多 next 串行抓取、章节间 @put 变量不共享。 |
| SecurityAudit | 安全面存在两个 P0：fetcher 的 SSRF 校验未绑定实际连接 IP（DNS rebinding 可绕过），以及 development 空 token 时鉴权中间件放行空 Bearer。另有请求体无限制、HTTP server 超时不全、脱敏白名单遗漏、SQLite 权限等 P1/P2 问题。 |
| ScriptRuntimeAudit | 发现 1 个 P1（并发 data race，已用 -race 复现）、2 个 P1 级 API 缺失（java.* 22 个常用 API、source.get/put）、多个 P2/P3（context.Background 脱离超时链、内存无界、ajax 错误当正文等），均有文件:行号与 /tmp overlay 测试复现证据。 |
| SearchDiscoveryAudit | null |
| ContentReplaceAudit | ContentReplaceAudit 切片完成（只读审计，未修改仓库任何文件）。逐行审阅 internal/app/content（content.go 全 567 行、service.go、image_proxy.go、33 个测试）、internal/app/replace（manager/processor/limited_regex/id/resegment）、internal/domain/replace（rule/repository）、api handlers 的 replace CRUD 与错误映射、sqlite replace_rule_repository scope SQL，并与 Legado BookContent.kt、WebBook.kt getContentAwait、ContentProcessor.kt、ReplaceRule.kt、ReplaceRuleDao.kt、AnalyzeRule.getStringList(isUrl) 逐条比对。2 P1 + 4 P2 + 4 P3 见 findings。核对无问题项：未提交的卷章跳过变更（content.go:84-88）与 Legado WebBook.kt:310-312 一致；scope/excludeScope SQL 与 Legado DAO 逐字一致；nextContentUrl visited 去重、正文为空错误可见性、首行重复标题删除、段落缩进均与 Legado 语义一致；htmlFormat 未绑定属切片4 java.* 绑定覆盖范围。 |
| DebugCheckAudit | DebugCheckAudit 切片完成：审阅了 internal/app/debug/、internal/app/check/、internal/app/diagnostic/（含未提交 diff）、internal/worker/check/、internal/worker/debug/（空目录）、internal/api/handlers debug/check mapper 与端点、internal/bootstrap 装配、domain/task 状态机、SQLite 持久化、Legado Debug.kt 与 CheckSourceService.kt 对照、openapi schema、Design.md 章节。共 14 条问题：P1×4（Debug 会话永不更新终态、校验不用 checkKeyWord、content_url 调试模式对未抓取新书必失败且不跳过卷、——前两条为主），P2×6（重启后 running 永挂、SSE 无心跳且订阅无效会话挂起、取消后排队 item 误标 failed、校验超时无默认值、check 事件流不流式且 item 事件未发布、空 searchUrl 无分组语义），P3×4 记录性结论（部分 item 失败提升任务 failed、空 worker/debug 目录、isConnectionEOFMessage 归类正确、Manager 全有或全无语义）。 |
| CrossCuttingQualityAudit | 覆盖维度：①硬编码域名——非测试代码仅 config.go:83 RealE2E 默认 URL 含域名，无站点特判伪造；②context.Background——CLI 全部子命令与后台任务（合理），CLI 无信号处理为缺陷；③错误吞没——processor.go:91 `_ = p.repo.Save` 真实缺陷，其余为轻微；④goroutine——debug/check runner 均有管理但 defer 误删 cancel 有竞态；⑤全局可变状态——sourceCompatState、discovery cache、NamespacedCache、opencc 全局 Once 均无淘汰；⑥锁——Processor 懒加载 opencc 无锁（P1 数据竞态）；⑦性能——regex/css 每次重编译、goja VM 每次重建且 jsLib 重跑；⑧死代码——RedactHeaders、SchemaVersion、checkHostWithResolver、buildSearchURL、Logs 仅测试引用；⑨重复实现——looksLikeFormBody/applyRetryOption 逐字拷贝、scriptAdapter 四处近似、splitGroups 三处不一致、opencc 双实现、配置项 READER_GLOBAL_CONCURRENCY/READER_SOURCE_CONCURRENCY/READER_REGEX_TIMEOUT 从未接线为 no-op。总体结论：incorrect（存在 1 个 P1 数据竞态及多个 P2 系统性内存/性能/接线缺陷）。无问题部分：api/middleware auth 常量时间比较、trace 脱敏；infra/fetcher SSRF/重定向/响应大小防护；infra/logging 脱敏模式；handlers DTO 适配干净；domain 层无禁用依赖；bootstrap SetMaxOpenConns(1)。 |
| SelectorAudit | 四个规则子包整体对齐 Legado 较好，核心索引/提取器/前缀语义经探针逐项验证正确（测试全通过）；但存在两个主流规则整体失败的对齐缺陷（jsonpath 标量错误匹配遗漏 float64/int 类型；filter 表达式 > < \|\| 不支持）及若干 P2/P3 差异（text. 前缀带索引、children.xxx 组合、attr 去重、逗号选择器顺序、step=0 区间、regexp2 firstOnly lookaround 丢上下文）与重复编译性能问题。 |
| UrlFetchAudit | UrlFetchAudit 切片审阅完成（internal/infra/rule/url/、internal/infra/fetcher/ 及 search/book/content/discovery/script runtime 各消费点，Legado 参照 AnalyzeUrl.kt/OkHttpUtils.kt/CookieManager.kt）。发现 2 个 P1：Legado <1,2,3> 分页占位符未实现（占位符被二次编码致 404/空结果）；goja 脚本路径 applyURLOptions 丢弃 JSON 对象 body 与 type/origin 选项。另有 5 个 P2（显式 Content-Type 下仍强制表单编码、无 Content-Type 时跳过响应编码检测、concurrentRate 与全局/源并发配置零消费、proxy/dnsIp 选项硬失败、过期持久化 cookie 仍发送）和 2 个 P3（headers 非字符串值被丢、GET 带 body）。宽松 JSON、GBK/escape 编码（/tmp 探针复现通过）、data URI、SSRF 骨架与 Legado 一致。 |
| DocBaselineAudit | null |
| SourceImportAudit | SourceImportAudit 完成：审阅 internal/domain/source、internal/app/source、internal/api/handlers source 相关文件、sqlite book_source_repository、openapi schema、bootstrap 装配，并逐字段比对 Legado BookSource.kt/BaseSource.kt/rule 实体/ImportBookSourceViewModel/AppPattern/SourceHelp。字段完整性与导入形态总体实现且有测试（四包 go test 通过）。发现 14 项问题（P1×1、P2×9、P3×3、附 summary×1），legadoRef 已嵌入各 finding 正文（legadoRef: 前缀），confidence 以 confidence: confirmed/likely 标注于 body 尾部。其余文件/字段未发现其他问题。 |
| RuleParserAudit | analyzer/parser 骨架（JS 分段、模式判定、@put/@get 主链路、&&/\|\|/%% 交由子选择器）总体成立，但存在 2 个 P1 语义缺陷：literal 误判使含 {{}}/@get 替换段的规则整体变字面量、URL 绝对化缺少 isUrl 门槛导致含点普通文本被改写；另有 ### 替换无匹配语义、实体反转义、@get 特殊键 3 个次要偏差。 |

### 1.4 复核结果

复核 49 条（全部 P0/P1 与置信度 < 0.85 的条目）：confirmed 32，downgrade 12，rejected 5。被驳回条目（已从清单删除）：

| 原条目 | 原标题 | 驳回理由 |
|---|---|---|
| ApiContrac#13 | ReorderBookSources 空 result 时 meta.pageSize=0 违反 PageMeta minimum:1 | 裁决：rejected。声称的空结果路径不可达：空 orders 被 Manager.Reorder 直接报错（manager.go:72-74）；订单指向已删除源时 repo.Reorder 经 execUpdateOne（book_source_repository.go:270-281）返回 ErrNotFound（=source.ErrNotFound，:13）转 404，并非仓库层过滤后为空。Reorder 成功则库中至少一条源，List 无过滤条件（:55-70）必返回至少一条，PageSize=0 无法通过该 handler 产生。 |
| BookTocAud#1 | preUpdateJS 执行失败导致整个目录刷新失败，Legado 仅记日志继续 | [verdict=rejected] Legado 在 getChapterListAwait 中调用 runPreUpdateJs(bookSource, book).getOrThrow()（BookChapterList.kt:60-66）；runPreUpdateJs 内部 runCatching 只记日志（WebBook.kt:211-221），但 getOrThrow 会把失败重新抛出并中止目录抓取。Reader toc.go:45-49 报错即失败与 Legado 错误语义一致（Reader 无 runPerJs 开关属差异，但失败行为相同）。条目对 Legado『仅记日志继续』 |
| BookTocAud#6 | 章节级 @put 变量按章节隔离，与 Legado 章节间共享语义不一致 | [verdict=rejected] Legado AnalyzeRule.put 写入当前 chapter 的 variableMap（AnalyzeRule.kt:740-748：chapter?.putVariable(key,value) ?: book?...），且每章迭代 setChapter 新建 BookChapter（BookChapterList.kt:206-210），下一章 get 读不到上一章 chapter 变量——Legado 章节 @put 本就按章节隔离。临时测试复现 Reader 每章 put 正确落在各自 Variable（chapter[0]={"k": |
| ContentRep#9 | ruleContent.imageStyle 解析后从未使用 | [verdict=rejected] rules.go:71 确解析 ImageStyle，grep 全仓无消费点。但 docs/ReaderLegadoSourceChainDiffAudit.md:66、371 及 Tasks.md T-A11 明确记载 imageStyle/payAction 为客户端阅读/付费 UI 字段，服务端保留兼容字段、不执行，属有意设计且已文档化，非缺陷。 |
| CrossCutti#11 | [跨域] mergeSearchBooks 仅按 name+author 合并，同名同作者的不同书被并成一条 | [verdict=rejected] Legado SearchModel.kt:132-170 对 equalData/containsData/otherData 的合并条件同为 pBook.name==nBook.name && pBook.author==nBook.author（138、150、162 行），不含 bookUrl。Reader multi_source.go:129 与 Legado 语义一致，非偏差；同名同作者不同书被并的问题在 Legado 同样存在。 |

## 2. 问题清单

### R-001 [P0] development 环境空 token 使鉴权形同虚设且默认监听 0.0.0.0

| 字段 | 内容 |
|---|---|
| 维度 | 安全 |
| 严重度 | P0 |
| 相关文件 | `internal/api/middleware/auth.go:24-28`<br>`internal/api/middleware/auth.go:18-24` |
| Legado 参照 | — |
| 问题原因 | config 默认 Env=development，只有 production 环境才强制要求设置 Token，因此缺省时 Token 为空串。auth.go 对 Authorization 头做 Bearer 前缀校验和 TrimSpace，再用 ConstantTimeCompare 比较，两个空串比较结果为 1，鉴权会通过。 |
| 问题后果 | 按默认配置部署时，服务监听 :8080，即对所有网卡开放，对外暴露的是一个实际没有保护的 API。导入、搜索、图片代理等接口都可能被滥用，成为 SSRF 跳板。 |
| 证据 | 相关代码：config.go:63（默认 Addr :8080）、:69（默认 Env=development）、:150-155（仅 production 报错）；auth.go:18-24（前缀校验+TrimSpace+ConstantTimeCompare）。已用 go run /tmp/reader-verify/secapi 通过原始 socket 复现：Bearer 后接尾随 U+00A0 或 U+0085 返回 200，Token 非空时返回 401；不带值的 Bearer 和 Bearer 加尾随 ASCII 空格都会被拒绝，原始记录中写的载荷不准确。 |
| 修复建议 | 非 production 环境也拒绝空 Token（或在 Token 为空时拒绝所有请求），并把默认监听地址改为 127.0.0.1。 |
| 验证结论 | 复核 confirmed；来源切片：SecurityAudit |

### R-002 [P0] SSRF 校验与实际连接未绑定同一 IP（DNS rebinding 可绕过）

| 字段 | 内容 |
|---|---|
| 维度 | 安全 |
| 严重度 | P0 |
| 相关文件 | `internal/infra/fetcher/http_fetcher.go:96-105`<br>`internal/infra/fetcher/http_fetcher.go:160-201` |
| Legado 参照 | — |
| 问题原因 | fetchOnce 先用 f.resolver 校验域名解析结果，随后丢弃该结果，再交给未自定义 Transport 的 http.Client 去请求。默认 Transport 拨号时会重新解析 DNS，且遵循 ProxyFromEnvironment，所以“校验时的 IP”和“实际连接的 IP”不是同一次解析得到的（TOCTOU）。 |
| 问题后果 | 攻击者可以利用 DNS rebinding：校验时让域名解析到公网 IP，实际连接时改为解析到 127.0.0.1、169.254.169.254 等内网地址，从而完全绕过 isBlockedIP 的 SSRF 防护。环境代理变量也可能改变实际的连接目标。 |
| 证据 | 复核确认：http_fetcher.go:34-46 构造的 Client 没有自定义 Transport，:160 校验后直接在 :201 调用 client.Do；bootstrap.go:82-89 的生产接线中也没有自定义 Dialer、Transport 或 Proxy。本结论基于代码结构证据，未做真实 DNS 切换复现。 |
| 修复建议 | 自定义 Transport，在 DialContext 中对实际解析出的 IP 复用 isBlockedIP 校验后再连接，并禁用环境代理（Proxy 设为 nil）。 |
| 验证结论 | 复核 confirmed；来源切片：SecurityAudit |

### R-003 [P1] Debug 会话完成后从不更新状态为 succeeded/failed，永久 running

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P1 |
| 相关文件 | `internal/app/debug/runner.go:34-44`<br>`internal/app/debug/runner.go:40-47`<br>`internal/app/debug/service.go:53`<br>`internal/api/handlers/debug_handler_mapper.go:15` |
| Legado 参照 | `Debug.kt` |
| 问题原因 | Runner 的 goroutine 调用 flow 时忽略了返回的错误，flow 本身只发送 debug.completed/stage_failed 事件。全仓库中 UpdateSessionStatus 的唯一生产调用点是 CancelSession，只会写入 cancelled，succeeded/failed 两种终态从不写入数据库。 |
| 问题后果 | Debug 会话完成或失败后，状态一直停在 running，completedAt 一直为空。服务重启后也没有修复路径，状态会永久卡在 running，与 Legado Debug.kt 在各阶段 onFinish/onError 时收束会话的行为不一致。 |
| 证据 | 复核时全仓 grep 确认：UpdateSessionStatus 只在 CancelSession（service.go:53）中被调用，DebugSessionSucceeded/Failed 两个常量没有任何生产代码使用。runner.go:40-47 的 goroutine 忽略了 flow 的返回错误，会话在 debug_handler_mapper.go:15 以 running 状态写入后再也不会更新。 |
| 修复建议 | 在 runner goroutine 中按 flow 的返回结果调用 UpdateSessionStatus，把状态写为 succeeded 或 failed 并设置 CompletedAt，同时为重启后遗留的 running 会话补一条收敛路径。 |
| 验证结论 | 复核 confirmed；来源切片：DebugCheckAudit |

### R-004 [P1] 校验不使用书源 checkKeyWord，固定用默认关键字“我的”

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/app/check/checker.go:67-70`<br>`internal/app/check/checker.go:68-70`<br>`internal/domain/rule/rules.go:10`<br>`internal/worker/check/runner.go:86`<br>`internal/app/check/diagnostics.go:33-49` |
| Legado 参照 | `BookSource.kt:208-215`, `CheckSourceService.kt:160` |
| 问题原因 | Checker 只在 Keyword 为空时写死默认关键字“我的”，从不读取书源已解析的 RuleSearch.CheckKeyWord。Legado 的 getCheckKeyword 则优先使用书源自身的 checkKeyWord。 |
| 问题后果 | 校验搜索关键字受限的书源时，结果必然为空，书源会被错误归入“搜索失效”分组，并被加上注释，污染书源数据。 |
| 证据 | 复核确认：checker.go:68-70 写死“我的”；rules.go:10 解析出的 CheckKeyWord 在全仓库没有任何读取点，check_mapper.go:12 和 worker/check/runner.go:86 同样不读。失败项经 diagnostics.go:33-49 写入“搜索失效”分组，Legado 对应逻辑见 BookSource.kt:208-215 和 CheckSourceService.kt:160。 |
| 修复建议 | 参照 Legado getCheckKeyword 修改关键字选取：优先用书源的 RuleSearch.CheckKeyWord，为空时才回退到默认关键字“我的”。 |
| 验证结论 | 复核 confirmed；来源切片：DebugCheckAudit |

### R-005 [P1] 正文多页分页缺少『与下一章 URL 相同即停止』条件，会把下一章正文并入本章

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/app/content/content.go:186-219` |
| Legado 参照 | `BookContent.kt:88-91`, `BookContent.kt:86-89` |
| 问题原因 | parseAllPages 的单 next 顺序分页循环只做 seen 去重，没有把 next 链接与 NextChapterURL 做比较。nextChapterURL 只在 JS 绑定中使用。 |
| 问题后果 | 很多书源的本章末页带有『下一章』链接，Reader 会按本章正文规则解析下一章页面，把内容追加进本章，并写入持久化缓存。严重时可能把整本书串进同一章，造成数据污染。 |
| 证据 | 复核临时测试 TestVerifyNextChapterPageMerged 已复现：c1 末页的『下一章』链接指向 c2 时，GetContent 返回 "CH1-para1\nCH2-para1"，结果经 SaveContent 写入缓存。content.go:186-219 的循环里只有 seen 判断；Legado BookContent.kt:86-89 遇到下一页绝对 URL 等于下一章绝对 URL 时会 break。 |
| 修复建议 | 在抓取下一页前，若 nextChapterURL 不为空，且 urlrule.Absolute(redirectURL, next) 等于 urlrule.Absolute(redirectURL, nextChapterURL)，就 break 结束分页。 |
| 验证结论 | 复核 confirmed；来源切片：ContentReplaceAudit |

### R-006 [P1] content_url 调试模式对未抓取新书必失败，且 runFromBook 不跳过卷章节

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/app/debug/flow.go:118-125`<br>`internal/app/debug/flow.go:119-131`<br>`internal/app/debug/flow.go:183-185`<br>`internal/app/content/content.go:82-85` |
| Legado 参照 | `Debug.kt:186-194`, `Debug.kt:270` |
| 问题原因 | content_url 调试分支只用 books.Save 保存书籍行，不创建章节，GetChapterContent 再经 FindByIndex(0) 查空章节表，必然返回 ErrNotFound。runFromBook 的 content 阶段固定用 index 0，没有像 RunKeyword 那样调用 firstContentChapterIndex 跳过卷章节。 |
| 问题后果 | 对还没抓取过目录的新书，content_url 调试模式一定失败。index 0 是卷时，Reader 在卷短路处直接返回 Tag，content 阶段会空跑通过，实际测不到正文规则。 |
| 证据 | 复核确认：flow.go:119-131 的 content_url 分支只落书行（GetChapterContent 在 :127），FindByIndex(chapter_repository.go:62-64) 查空表时经 scanChapter(:95-96) 返回 ErrNotFound；runFromBook 在 flow.go:183-185 固定用 index 0，RunKeyword 则在 flow.go:97 调用 firstContentChapterIndex，卷章节会走 content.go:83-85 的短路分支。Legado 的 Debug.kt:186-194 直接构造 BookChapter，不经数据库；Debug.kt:270 先过滤掉卷章节。 |
| 修复建议 | 参照 Legado：content_url 模式直接在内存中构造章节对象去抓正文，不经数据库查询；runFromBook 的 content 阶段改用 firstContentChapterIndex 跳过卷章节。 |
| 验证结论 | 复核 confirmed；来源切片：DebugCheckAudit |

### R-007 [P1] Legado <1,2,3> 分页占位符模式未实现

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/app/search/single_source.go:422-436`<br>`internal/app/search/single_source.go:422-433` |
| Legado 参照 | `AnalyzeUrl.kt:386-397,820`, `AnalyzeUrl.kt:181-206`, `AnalyzeUrl.kt:670` |
| 问题原因 | buildSearchURLPlaceholders 只处理 {{page}}/{page}，discovery 的 buildPageURL 也一样，全仓没有和 Legado pagePattern（<1,2,3> 按页码取 pages[page-1]，超出时取末位）等价的实现。因此 URL 中的分页占位符会原样保留。 |
| 问题后果 | searchUrl 含 <1,2,3> 时，各页请求的 URL 完全相同，占位符还会被转义残留在 URL 里，可能导致请求 404 或返回空结果。依赖该语法的书源因此无法正常搜索和翻页。 |
| 证据 | 复核复现：page=1/2/3 输出完全相同的 URL，占位符被转义为 %3C1,2,3%3E；问题代码在 single_source.go:422-433 和 discovery.go:327-334。Legado 的对应实现在 AnalyzeUrl.kt:181-206（页码块 196-206）和 670，原记录引用的 386-397/820 行号有误。 |
| 修复建议 | 在 buildSearchURLPlaceholders 和 discovery buildPageURL 中按 Legado pagePattern 语义实现 <a,b,c> 占位符：按当前页码取 pages[page-1]，超出时取末位。 |
| 验证结论 | 复核 confirmed；来源切片：UrlFetchAudit |

### R-008 [P1] literal 判定基于 ## 拆分前整条规则，替换段中的 {{}}/@get 使主规则变字面量

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/infra/rule/analyzer.go:555-561`<br>`internal/infra/rule/analyzer.go:179-185`<br>`internal/infra/rule/analyzer.go:759-798` |
| Legado 参照 | `AnalyzeRule.kt:582-619`, `AnalyzeRule.kt:657-721` |
| 问题原因 | parseSourceRule 先按 ## 拆分，但 literal 却用拆分前的整条 rule 匹配 getRulePattern/jsRulePattern 来判定，所以替换段里只要有 {{}} 或 @get，整条规则就被当成字面量；evalStep 遇到 literal 直接返回主规则原文。另外 makeUpStep 只展开 step.rule，替换段中的 {{}}/@get 一直没有被求值。 |
| 问题后果 | 替换段带 {{}} 或 @get 的 Default 规则会把主规则原文当结果返回，字段采集完全失败。替换段里的模板会被 applyReplace 原样当作替换文本输出，和 Legado 的行为不一致。 |
| 证据 | 复核已确认：临时测试 GetString("class.title@text##【.*】##{{$.pre}}") 返回字面量 "class.title@text"，字段采集失败。代码位置：analyzer.go:552-561 做拆分和判定，179-185 对 literal 提前返回，759-798 只展开 step.rule。 |
| 修复建议 | literal 只对 strings.TrimSpace(parts[0]) 判定；在 evalStep 调用 applyReplace 之前，先按 makeUpStep 的方式展开 step.replaceRegex/step.replacement 里的 @get:{}/{{}}，再做正则替换。 |
| 验证结论 | 复核 confirmed；来源切片：RuleParserAudit |

### R-009 [P1] URL 绝对化缺少 isUrl 门槛，含点普通文本被改写、无点相对路径漏绝对化

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/infra/rule/analyzer.go:79-88`<br>`internal/infra/rule/analyzer.go:1148-1162`<br>`internal/app/book/info.go:88-103` |
| Legado 参照 | `AnalyzeRule.kt:231-241`, `AnalyzeRule.kt:319-324`, `NetworkUtils.kt:174-193` |
| 问题原因 | GetString/GetStringList 对所有结果都无条件调用 shouldNormalizeURL→urlrule.Absolute（analyzer.go:79-88），没有 isUrl 门槛。shouldNormalizeURL（analyzer.go:1148-1162）只要文本无空白且含“.”就判为 URL，而不含点、不以 / 开头的相对路径又会被漏判。 |
| 问题后果 | 书名、作者、字数等普通文本字段里，“1.2万”“3.5分”这类值会被改写成 https://host/... 后写入数据库，造成数据损坏。“catalog/123”这类相对路径则不会被绝对化，与 Legado 对 isUrl 结果一律绝对化的行为不一致。 |
| 证据 | 复核已确认：临时测试在 RedirectURL="https://host/prefix" 下执行 GetString("class.wordcount@text")，“1.2万”返回 "https://host/1.2%E4%B8%87"，GetStringList 对“3.5分”也做了同样的改写。info.go:88-103 无条件用 GetString 采集文本字段。 |
| 修复建议 | 给 GetString/GetStringList 加上 isUrl 语义（或拆出独立的 URL 取值入口），只在 URL 类规则路径上做绝对化；如果保留启发式判断，至少删掉 strings.Contains(value, ".") 这个误判条件，并补上用 / 分隔的相对路径形态。 |
| 验证结论 | 复核 confirmed；来源切片：RuleParserAudit |

### R-010 [P1] css 旧式索引直接附加在 text. 前缀规则上时被当作文本内容

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/infra/rule/css/css.go:129-142`<br>`internal/infra/rule/css/css.go:135-138` |
| Legado 参照 | `AnalyzeByJSoup.kt:305` |
| 问题原因 | extractStep/selectStep 的 text. 分支（css.go:135-138、203-206）把 text. 之后的剩余部分整个当作过滤词，没有剥离 .N 或 [..] 这类旧式索引后缀，也跳过了 parseSelector/parseLegacySelector 中的索引剥离逻辑。Legado 的做法是先用 findIndexSet 剥掉尾随索引，再按 text.关键词 匹配。 |
| 问题后果 | 书源里写 text.关键词.0 或 text.关键词[0] 这类规则时，Reader 会把“关键词.0”当成要匹配的文本，结果找不到节点、返回空。这和 Legado 的行为不一致，导致书源解析失败。 |
| 证据 | 复核已复现：text.关键词.0@text 和 text.关键词[0]@text 都返回空，Elements("text.关键词.0") 同样为空，而 text.关键词@text 正常。Legado 的 AnalyzeByJSoup.kt:305 getElementsSingle 会先调用 findIndexSet 剥离索引，再走 getElementsContainingOwnText 取索引。 |
| 修复建议 | 在 extractStep/selectStep 的 text. 分支里，先按 Legado findIndexSet 的规则剥离尾随的 .N、!N、[...] 索引，再用剩下的关键词做文本匹配，最后按索引取结果。 |
| 验证结论 | 复核 confirmed；来源切片：SelectorAudit |

### R-011 [P1] jsonpath 标量遍历错误匹配遗漏 float64/int 类型导致规则整体失败

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/infra/rule/jsonpath/jsonpath.go:111-120`<br>`internal/infra/rule/jsonpath/jsonpath.go:111-119` |
| Legado 参照 | `AnalyzeByJSonPath.kt:55-66` |
| 问题原因 | isScalarTraversalError 只认 "unsupported value type number for select" 这一种消息，但 PaesslerAG jsonpath 用 %T 输出类型名，实际报的是 "float64" 或 "int"，所以永远匹配不上。结果对标量中间路径做遍历时，本该容错返回空，却直接抛出了 rule_parse_failed。 |
| 问题后果 | 只要 JSON 规则路径在中途碰到数值标量，整条规则就会失败，不会像 Legado 那样返回空值后继续执行。这与 Legado 的语义不一致：AnalyzeByJSonPath.getString 会用 try/catch 吞掉这类异常并返回空。 |
| 证据 | 复现：对 {"x":1} 查询 $.x.a，报错 rule_parse_failed: unsupported value type float64 for select。原因是 isScalarTraversalError（internal/infra/rule/jsonpath/jsonpath.go:111-119）只匹配 "number"，而库在 jsonpath@v0.1.1/selector.go:90 用 %T 输出 float64，这个错误又经 analyzer.go:74-76 上抛。 |
| 修复建议 | 把 isScalarTraversalError 的判断改成匹配 "unsupported value type" 前缀，这样 float64、int 等所有标量类型的遍历错误都能按容错处理返回空。 |
| 验证结论 | 复核 confirmed；来源切片：SelectorAudit |

### R-012 [P1] jsonpath 不支持 Legado 过滤表达式的 > < 与 || 运算符

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/infra/rule/jsonpath/jsonpath.go:31-40`<br>`internal/infra/rule/jsonpath/jsonpath.go:33-45` |
| Legado 参照 | — |
| 问题原因 | PaesslerAG jsonpath 的表达式语言只用了 gval.Base() 加 $/@ 扩展，Base 里只有 ==、!= 和括号；> 和 \|\| 定义在 arithmetic/propositionalLogic 中，没有被纳入。所以带 > 或 \|\| 的过滤表达式在解析阶段就会报错。 |
| 问题后果 | 主流书源里用到数值比较或 \|\| 过滤的 JSON 规则会整体失败，返回 rule_parse_failed。Legado 上游 jayway 支持这些运算符，这类书源迁移到 Reader 后无法使用。 |
| 证据 | 复核复现：$.list[?(@.n>1)].title 报 parsing error "unexpected >"，jsonpath.go:33-45 把这个解析错误包装成 rule_parse_failed 上抛。== 比较、存在过滤 [?(@.t)]、$..、[*]、[0:2]、[1,2] 都能正常使用。 |
| 修复建议 | 构造 jsonpath 语言时把 gval 的 arithmetic 和 propositionalLogic 一起组合进去（也可以换一个支持完整过滤运算符的实现），让 >、<、\|\| 等过滤表达式和 Legado/jayway 保持一致。 |
| 验证结论 | 复核 confirmed；来源切片：SelectorAudit |

### R-013 [P1] 脚本路径 java.ajax/java.post 丢弃 JSON 对象 body 及 type/origin 选项

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/infra/script/runtime.go:1650-1656`<br>`internal/infra/script/runtime.go:1651-1666` |
| Legado 参照 | `AnalyzeUrl.kt:807-825,281-286`, `AnalyzeUrl.kt:686`, `AnalyzeUrl.kt:247` |
| 问题原因 | 脚本路径 parseCustomURL 用 json.Unmarshal 解析选项，对象/数组 body 解析结果为 map[string]any/[]any，但 applyURLOptions 的 case body 只处理 string。该函数也没有 case type 分支，而 validateRuntimeSupportedURLOptions 声明支持全部键。 |
| 问题后果 | java.ajax/java.post 传对象或数组 body 时会被静默丢弃，POST 实际发出的是空 body；传 {"type":"..."} 也会被忽略，返回的不是 hex 文本，与 Legado 行为不一致。 |
| 证据 | 复核已确认：applyURLOptions（runtime.go:1640-1692）中的 case body（1651-1666）只处理 string，且没有 type/origin 分支。Legado 的 UrlOption.body 类型为 Any?（AnalyzeUrl.kt:686），getBody() 会把对象/数组序列化回 JSON（796-800），type 在 247、405-406 被消费并触发 hex；origin 子项不算失配，因为 getOrigin 在全仓没有调用。 |
| 修复建议 | 在脚本路径的 applyURLOptions 中补上 map[string]any/[]any body（序列化为 JSON 发送）和 type 分支，或直接复用 app 层 applySearchURLOptions/buildBookFetchRequest 的共享实现。 |
| 验证结论 | 复核 confirmed；来源切片：UrlFetchAudit |

### R-014 [P1] java.* 约 22 个常用 API 缺失（head/timeFormat/htmlFormat/toNumChapter/strToBytes/cacheFile/webView/getSource/toURL 等）

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P1 |
| 相关文件 | `internal/infra/script/runtime.go:364-457`<br>`internal/infra/script/runtime.go:310-461` |
| Legado 参照 | `JsExtensions.kt:399,523,543,916,440,472,278,170,256,264,981,85,927,813,619,581,322,233`, `JsExtensions.kt`, `AnalyzeByJSoup.kt:72` |
| 问题原因 | javaBindings（runtime.go:310-461）只绑定了约 25 个方法，Legado JsExtensions 里的常用 API 大多没有实现，例如 head、timeFormat、htmlFormat、toNumChapter、strToBytes、cacheFile、webView、getSource、toURL。原始记录里的 getStringList 其实是 AnalyzeByJSoup.kt:72 的内部方法，不属于 JsExtensions API。 |
| 问题后果 | 主流书源脚本一调用这些 API，整条规则就会以 script_failed 失败；如果调用处包在 runCatching 里，会悄无声息地返回空结果，用户拿不到书籍或章节数据。 |
| 证据 | overlay 测试用 typeof 逐个检查，22 个 API 全部是 undefined。实际调用会报 script_failed: TypeError: Object has no member 'timeFormat'，报的是 TypeError，不是 ReferenceError；Legado JsExtensions.kt 中引用的各行号都已核对，确实存在。 |
| 修复建议 | 按调用频率先补齐纯计算类 API（timeFormat、htmlFormat、toNumChapter、strToBytes、bytesToStr、base64DecodeToByteArray），网络类复用 fetcher 实现，系统类至少先提供会明确抛错的 stub。 |
| 验证结论 | 复核 confirmed；来源切片：ScriptRuntimeAudit |

### R-015 [P1] [跨域] replace Processor 懒加载 opencc 无锁，共享实例并发数据竞态

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P1 |
| 相关文件 | `internal/app/replace/processor.go:207-221`<br>`internal/app/replace/processor.go:207-228` |
| Legado 参照 | — |
| 问题原因 | bootstrap.go:98 只创建了一个 contentProcessor，所有 HTTP 请求共用它。convertWithMode 在 openccT2S/openccS2T 为 nil 时直接给指针字段赋值做懒初始化，整个过程没有加锁。 |
| 问题后果 | 当 ChineseConverterType 配置为 1/2 时，如果多个正文请求（GET /books/{id}/content、check 任务）同时第一次触发繁简转换，就会发生数据竞争。可能读到只初始化了一半的指针，导致 panic 或转换结果错乱。 |
| 证据 | 复核用 -race 复现：单个 Processor 实例在 ChineseConverterType=1 下并发调用 Process，报 DATA RACE，位置是 processor.go:209(t2s)/226(s2t) 的无锁懒初始化写入。作为对照，infra/script/runtime.go:55-60 的同类转换用 sync.Once 初始化，是安全的。 |
| 修复建议 | t2s/s2t 转换器的初始化改用 sync.Once（参照 runtime.go:55-60 的写法），或者在启动时预先初始化好。 |
| 验证结论 | 复核 confirmed；来源切片：CrossCuttingQualityAudit |

### R-016 [P1] 共享 Runtime 并发 Eval 存在 data race（cache/cookie 懒初始化无锁读写）

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P1 |
| 相关文件 | `internal/infra/script/runtime.go:271-273`<br>`internal/infra/script/runtime.go:272-302` |
| Legado 参照 | `AnalyzeRule.kt:789-803`, `BaseSource.kt` |
| 问题原因 | 多个服务在 bootstrap.go:90 之后共用同一个 *script.Runtime。installSafeGlobals 对共享字段 r.cache 和 r.cookie 做懒初始化（runtime.go:271-273、301-303），读写都没有加锁。 |
| 问题后果 | 生产路径中 policy.Cache 始终非空，所以并发 Eval 时 cookie 的数据竞争一定会出现；cache 的竞争只在未提供 Cache 时触发。极端情况下 goroutine 会读到初始化到一半的指针，可能导致 panic 或未定义行为。 |
| 证据 | 复核用 go test -race 复现：Runtime 单实例共享、已提供 Cache 时并发 Eval，报 DATA RACE，位置为 runtime.go:301（读）/302（写）；Cache 为 nil 时另有 272/273 处竞争。Legado 每次 eval 都使用独立 scope（BaseSource.kt evalJS），不存在这个问题。 |
| 修复建议 | 在 NewRuntime 中一次性初始化 cache 和 cookie，或者用 sync.Once 保护懒初始化。 |
| 验证结论 | 复核 confirmed；来源切片：ScriptRuntimeAudit |

### R-017 [P2] Book DTO 声明的 customTag/customCoverUrl/customIntro/charset/group/canUpdate/order/syncTime 等字段被静默丢弃

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/book_mapper.go:8-45` |
| Legado 参照 | — |
| 问题原因 | spec 的 Book schema 里声明了 customTag、customCoverUrl、customIntro、charset、group、canUpdate、order、syncTime 等字段，但 book_mapper.go 中的 bookFromDTO/mapBook 一个都没有映射，domain book.Book 也没有这些字段。这些字段在 schema 里有声明，所以不会触发 DisallowUnknownFields 报错。 |
| 问题后果 | 客户端提交这些字段时请求照常成功，但数据被悄悄丢掉，读回时这些字段不见了，往返数据丢失。 |
| 证据 | internal/api/handlers/book_mapper.go:8-45 中，bookFromDTO/mapBook 没有映射 customTag、customCoverUrl、customIntro、charset、group、latestChapterTime、lastCheckTime、lastCheckCount、totalChapterNum、canUpdate、order、syncTime。domain book.Book 也缺少这些字段。 |
| 修复建议 | 给 domain book.Book 补上这些字段，并在 bookFromDTO/mapBook 中双向映射和持久化；如果暂不支持，就从 spec 中删掉这些字段或明确拒绝，避免数据被悄悄丢弃。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-018 [P2] check 任务事件流是伪流式：只写一条合成事件，check.* 事件从未推送

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:143-157`<br>`internal/api/handlers/source_handler.go:152-172`<br>`internal/api/handlers/source_handler.go:152-170` |
| Legado 参照 | — |
| 问题原因 | StreamCheckTaskEvents 只执行一次 Get，写入一条合成事件就结束响应，没有像 StreamDebugSessionEvents 那样订阅事件通道，也没有 Flush 或 Cache-Control。另外 worker/check/runner.go 只调用 SaveItem/UpdateTaskStatus，check.item_started/passed/failed 在仓库内没有任何生产者。 |
| 问题后果 | 订阅 /check/tasks/{id}/events 的客户端拿不到条目级实时进度，只能靠轮询；spec 里声明的 SSE 流式接口和 CheckEvent 枚举实际上没有生效。completed/cancelled 仍会以合成事件推送，并非所有 check 事件都没有推送。 |
| 证据 | 复核确认：source_handler.go:152-170 只有 Get 后写一条合成事件，没有 Subscribe/Flush/Cache-Control；作为对照，StreamDebugSessionEvents（:251-283）有事件订阅。openapi.yaml:1036-1063、2096-2103 声明了流式接口和 item 级枚举，但仓库内没有这些事件的生产者。 |
| 修复建议 | 在 check runner 执行各条目时发布 check.started/item_started/item_passed/item_failed 事件，并参照 StreamDebugSessionEvents 改造 StreamCheckTaskEvents，加上订阅、Flush 和 Cache-Control。 |
| 验证结论 | 复核 downgrade（→P2）；来源切片：ApiContractAudit, DebugCheckAudit |

### R-019 [P2] SearchResponse.hasMore 恒为 false，与分页语义矛盾

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:588-597` |
| Legado 参照 | — |
| 问题原因 | spec 规定 hasMore 是 SearchResponse 的必填字段，但 SearchBooks/DiscoverBooks 把 HasMore 硬编码成 false，没有按实际结果计算。 |
| 问题后果 | MultiSearchInput.Page 实际支持分页，但客户端拿不到“是否还有下一页”的信号，分页约定等于没用，后续页的结果可能拿不到。 |
| 证据 | internal/api/handlers/source_handler.go:588-597 中，SearchBooks/DiscoverBooks 返回的响应把 HasMore 固定写成 false，而 MultiSearchInput.Page 支持分页。 |
| 修复建议 | 根据实际搜索/发现结果（例如本页是否还有后续数据）算出 HasMore 并返回，不要写死为 false。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-020 [P2] GET/PATCH 输出丢失未知字段，RawJSON 保存了但从未用于回写导出

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_mapper.go:47-62` |
| Legado 参照 | — |
| 问题原因 | GET 和 Update 的输出都经过 generated.BookSource 序列化，未知字段在 DTO 解析阶段就被丢掉了。RawJSON 虽然存进了数据库，但从不返回给客户端，也没有任何导出端点用到 raw_json。 |
| 问题后果 | 带有 Legado 新字段或自定义字段的书源，创建后再读取，这些字段就丢了，导出的数据无法和 Legado 完整往返。 |
| 证据 | source_mapper.go:57-58 只写入 RawJSON，从不读取；GET/PATCH 的输出只走 generated.BookSource 的 marshal（confidence: confirmed）。 |
| 修复建议 | GET 时以 raw_json 为底，再叠加当前字段输出，或者新增一个基于 raw_json 的导出端点。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：SourceImportAudit |

### R-021 [P2] [跨域] debug Publisher 非阻塞广播丢事件且 Subscribe 有注册空窗

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/app/debug/publisher.go:24-44` |
| Legado 参照 | — |
| 问题原因 | Publish 对每个订阅者都用非阻塞的 select-default 发送，channel 满了就直接丢弃事件，不报任何错。另外 Subscribe 先调用 ListEvents 读历史，再持锁注册订阅，这两步之间 Publish 的事件既不在历史里，也进不了 channel。 |
| 问题后果 | SSE 消费慢（缓冲只有 16）或建连时机不巧时，调试事件会悄悄丢失，前端看到的调试流程断断续续。事件在 DB 里有，实时流里却缺了。 |
| 证据 | internal/app/debug/publisher.go:24-44：Publish 中写的是 `select { case ch <- event: default: }`；Subscribe 先 ListEvents 读历史，之后才持锁注册订阅者。 |
| 修复建议 | 先持锁注册订阅，再读历史并按序号去重补齐；channel 满时不要静默丢弃，改为标记丢失并通知客户端重新同步，或断开该订阅。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-022 [P2] DebugService.CancelSession 不发布 cancelled 事件，终态事件写库可能被取消

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/app/debug/service.go:48-56`<br>`internal/app/debug/publisher.go:15-27`<br>`internal/infra/sqlite/repositories/debug_repository.go:46` |
| Legado 参照 | — |
| 问题原因 | CancelSession 不发布 DebugEventCancelled，该常量全仓只有定义、没有任何发布点。取消后，flow 内的 publish 仍使用已被取消的 runCtx，AppendEvent 写库失败后，错误被下划线忽略吞掉。 |
| 问题后果 | 取消调试会话后，SSE 订阅者收不到任何收尾事件，终态事件也没有写入数据库。会话状态本身仍会被正常写为 cancelled，影响只在事件流。 |
| 证据 | 复核确认：grep 只在 domain/task/debug.go:37 和生成代码中找到 DebugEventCancelled。runner.go:20 的 runCtx 被 Cancel 后，publish 会调用 AppendEvent，经 ExecContext（debug_repository.go:46）必然得到 context.Canceled，且错误被 `_ =` 忽略。 |
| 修复建议 | 在 CancelSession 中用不受取消影响的 ctx 发布 DebugEventCancelled 事件，并在终态事件写库失败时记录或处理错误，不要直接丢弃。 |
| 验证结论 | 复核 confirmed；来源切片：DebugCheckAudit |

### R-023 [P2] URL 选项白名单接受 origin 但 applySearchURLOptions 无对应处理，静默丢弃

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/app/search/single_source.go:528-573`<br>`internal/app/search/single_source.go:531-541` |
| Legado 参照 | — |
| 问题原因 | buildSearchRequest 的 URL 选项白名单（single_source.go:207）声明支持 origin，但 applySearchURLOptions 的 switch（single_source.go:531-596）没有 origin 分支，导致该选项被静默丢弃。Reader 的 book/content 路径会设置 Origin/Referer，搜索路径却没有，声明与实现不一致。 |
| 问题后果 | 书源在 searchUrl 选项中声明 origin 时，搜索请求不会带 Origin 头。校验 Origin 的站点可能返回 403 或空结果，用户也收不到任何“配置不受支持”的提示。 |
| 证据 | 复核已复现：searchUrl 选项包含 origin 时，搜索请求没有 Origin 头；而 app/book/fetch_request.go:94-99 和 app/content/content.go:350-355 都会设置 Origin/Referer。另外，Legado 的 UrlOption.origin 同样没有被消费（getOrigin 无任何调用），所以这本质上是 Reader 内部的一致性问题。 |
| 修复建议 | 在 applySearchURLOptions 中补上 origin 分支，按 book/content 路径的方式设置 Origin 头；如果不打算支持，就把 origin 从白名单中移除，让配置校验能明确报错。 |
| 验证结论 | 复核 confirmed；来源切片：SearchDiscoveryAudit |

### R-024 [P2] PATCH /book-sources/{id} 修改 bookSourceUrl 触发 UNIQUE 约束失败返回 500

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/app/source/manager.go:36-55`<br>`internal/infra/sqlite/repositories/book_source_repository.go:136-188` |
| Legado 参照 | `BookSourceEditViewModel.kt:58`, `ImportBookSourceViewModel.kt` |
| 问题原因 | Update 允许在 sourceId 不变的情况下修改 bookSourceUrl，但 Save 只用 ON CONFLICT(book_source_url) 处理冲突，因此会撞上 UNIQUE(source_id) 约束，这个错误最后被映射成 500。导入时如果两条源的 URL 不同、source_id 相同，也会触发同一约束错误，SaveMany 会把整批事务回滚。 |
| 问题后果 | 用户修改书源地址一定会收到 500，而且没有任何提示。导入时只要有一条 source_id 冲突，整批导入就会失败并直接返回原始约束错误；Legado 的做法是逐条跳过或覆盖。 |
| 证据 | book_source_repository.go:151 的 Save 只对 book_source_url 做 ON CONFLICT，而 migrate.go:35 定义了 source_id 的 UNIQUE 约束（原始记录 confidence: confirmed）。可对照 Legado 的 BookSourceEditViewModel.kt:58 和 ImportBookSourceViewModel.kt。 |
| 修复建议 | 修改 bookSourceUrl 时，要么返回 400 拒绝，要么在一个事务里删掉旧记录再插入新记录；导入时捕获 UNIQUE(source_id) 冲突，按单条源跳过，或以 source_id 为准确定性覆盖。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：PersistenceArchAudit, SourceImportAudit |

### R-025 [P2] download --from 越界时覆盖写空 TXT 导出文件

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/cli/download.go:75-110` |
| Legado 参照 | — |
| 问题原因 | download 只对 --to 做区间夹取（download.go:80-84），--from 没有夹取。--from 超过最后一章索引时 total=0、sections 为空，但 !*noExport 分支仍会调用 exportTXT。 |
| 问题后果 | 导出文件已存在时会被只有书名和作者两行的内容覆盖，之前下载的全部正文都会丢失，用户能直接看到这一数据损失。 |
| 证据 | 触发方式：执行 reader download <id> --from <越界值>，且导出文件已存在。此时循环不执行，download.go:104-110 的 exportTXT 仍通过 os.WriteFile 覆盖目标文件。 |
| 修复建议 | 对 --from 做越界校验或夹取，并在 sections 为空时跳过 exportTXT，不覆盖已有文件。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：CliBootstrapAudit |

### R-026 [P2] [跨域] READER_GLOBAL_CONCURRENCY/READER_SOURCE_CONCURRENCY/READER_REGEX_TIMEOUT 配置项从未接线，纯 no-op

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/config/config.go:30-123`<br>`internal/infra/config/config.go:30-34`<br>`internal/app/search/multi_source.go:53-57` |
| Legado 参照 | — |
| 问题原因 | config.go 会从环境变量加载 GlobalConcurrency、SourceConcurrency、RegexTimeout、DebugBodyLimit，但 bootstrap 没有把它们传给搜索、check 任务或 regex 包，全仓也没有读取它们的地方。实际行为用的都是硬编码值：multi_source 默认并发 4、check 并发 4、regexp2 超时 time.Second。 |
| 问题后果 | 用户设置这些环境变量后行为完全不变，并发上限和正则超时都调不了。日志、/version 和 Design.md 第 18 节却都展示这些配置，用户会误以为它们已经生效。 |
| 证据 | multi_source.go:53-55 只有一处逻辑：concurrency := input.Concurrency，小于 1 时取 4，不读 cfg.SourceConcurrency。check 并发在 source_handler.go:118 写死为 4，regex.go:118 的 regexp2 超时写死为 time.Second；在 internal/ 和 cmd/ 下检索（排除 config.go 和测试），找不到任何读取这些配置的地方。 |
| 修复建议 | 构造 MultiSourceSearcher 时注入 SourceConcurrency/GlobalConcurrency，并接入 discovery/check 的并发上限；同时把 RegexTimeout 接到 regexp2.MatchTimeout，把 DebugBodyLimit 接到调试事件截断。如果暂时不实现，就从 Design 第 18 节和 /version 中移除这些项，或标注为 reserved。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit, DocBaselineAudit |

### R-027 [P2] 过期持久化 Cookie 仍会被发送

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/fetcher/cookies.go:62-78` |
| Legado 参照 | — |
| 问题原因 | CookieManager.Load 会把 repo.ListByDomain 返回的 cookie 全部合并，没有检查 ExpiresAt。CookieRepository.ListByDomain 的查询也没有按 expires_at 过滤。 |
| 问题后果 | 设了过期时间的 cookie 过期后仍会长期注入请求头，站点可能因此返回异常的会话页。cookies 表也会不断堆积失效数据。 |
| 证据 | internal/infra/fetcher/cookies.go:62-78 的 Load 直接合并 ListByDomain 的结果，没有判断 ExpiresAt。internal/infra/sqlite/repositories/cookie_repository.go:36-37 的查询没有 expires_at 条件。 |
| 修复建议 | 在 ListByDomain 查询中加上 expires_at IS NULL OR expires_at > now 的过滤条件，并清理已过期的行。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.85）；来源切片：UrlFetchAudit |

### R-028 [P2] regexp2 路径 firstOnly 替换丢上下文

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/rule/regex/regex.go:155-164` |
| Legado 参照 | — |
| 问题原因 | 在 regexp2 路径的 firstOnly 分支里，regex.go:160 调用 p.re2.Replace(match.String(), ...) 时，只对匹配到的片段做替换，没有处理整段输入。因此匹配位置前后的上下文都被丢掉了。 |
| 问题后果 | 规则里如果用到 lookaround 并开启 firstOnly，替换后只剩匹配片段，原文的其余内容都丢了，用户看到的文本残缺不全。同一规则在 all 模式下结果正确，两种模式表现不一致。 |
| 证据 | 已复现：用模式 (?<=前缀)hello(?=世界) 做 firstOnly 替换，结果只剩 "hello"。同一模式在 all 模式下能正确返回"前缀REPL世界后缀"。 |
| 修复建议 | firstOnly 改成对整段输入只做一次替换：先找到第一个匹配的位置，再把匹配前的文本、替换结果和匹配后的文本拼起来返回。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：SelectorAudit |

### R-029 [P2] JS 错误缺少 sourceId，且全部阶段错误统一报 stage=search

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/script/runtime.go:2137-2139` |
| Legado 参照 | — |
| 问题原因 | bootstrap.go:90 只创建了一个共享的 script.Runtime，Policy.Stage 固定写成 "search"，执行 header_js、rule_js、toc_format_js、login_check_js 等阶段时都不会切换。runtime.go:2137-2139 的 wrapError 只拼接 kind、Stage 和 message，没有带上 sourceId。 |
| 问题后果 | 不管哪个阶段（比如目录 formatJs、正文 loginCheckJs）的 JS 出错，错误消息和调试事件里都显示 stage=search，也没有 sourceId，排障时找不到真正出错的阶段和书源。这不符合 R-034 验收标准中“错误包含 sourceId 和阶段”的要求，也不符合 Design 11.4 中“每次执行带阶段信息”的要求。 |
| 证据 | internal/bootstrap/bootstrap.go:90 共用一个 Runtime，Stage 固定为 "search"；internal/infra/script/runtime.go:2137-2139 的 wrapError 只输出 kind+Stage+message，不包含 sourceId。 |
| 修复建议 | 给 script.Policy 增加只读的 SourceID 字段并在 wrapError 中输出，同时让 EvalWithBindings 允许调用方按阶段用局部变量覆盖 Stage，不写回共享 policy，以免重犯 T-A01 的问题。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DocBaselineAudit |

### R-030 [P2] search_books 表为死表：建表但无任何仓储读写

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/sqlite/migrate.go:105`<br>`internal/infra/sqlite/migrate.go:105-121` |
| Legado 参照 | — |
| 问题原因 | Design 7.2 建议包含 search_books 表，migrate.go:105-121 也建了这张表，但仓储层没有为它实现任何读写，搜索结果只通过 API 返回，不落库。这是设计承诺与实现之间的偏差。 |
| 问题后果 | 每次多源搜索都要重新抓取全部书源，没有可复用的搜索缓存层；这张死表还增加了迁移面积和维护噪音。目前没有功能性错误。 |
| 证据 | 在全仓 grep search_books，只命中 migrate.go 里的 schema 定义和 migrate_test.go:31 的建表断言，没有任何 INSERT 或 SELECT；internal/infra/sqlite/repositories 下也没有相关 SQL。 |
| 修复建议 | 二选一：为 search_books 实现仓储，并在多源搜索中按需复用；或者从 initialSchema 和 Design.md 7.2 中删除这张表，同时更新相关测试。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DocBaselineAudit, PersistenceArchAudit |

### R-031 [P2] check 任务运行中重启后永久 running，无恢复或 stale 清理

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P2 |
| 相关文件 | `internal/worker/check/runner.go:84-86` |
| Legado 参照 | — |
| 问题原因 | check 任务运行状态存放在 check_tasks 表里，但取消句柄放在进程内的 cancels map 中。服务启动时没有 reconcile 或 stale 清理逻辑来处理还没完成的任务。 |
| 问题后果 | 服务重启后，没跑完的 check 任务会一直停在 running，用户既等不到它结束，也取消不了，因为 Cancel 返回 false。 |
| 证据 | internal/worker/check/runner.go:84-86：重启后 cancels map 丢失，Cancel 返回 false，check_tasks 表中未完成的任务一直保持 running。Legado 则由 service 生命周期管理 checkJob。 |
| 修复建议 | 在服务启动时扫描 check_tasks 中处于 running 的任务，把它们标记为失败/中断，或重新调度执行。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DebugCheckAudit |

### R-032 [P2] 导入/更新输入错误被映射为 500，而非 400

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:806-819` |
| Legado 参照 | — |
| 问题原因 | writeSourceError 只单独处理 ErrNotFound 和字段缺失，其余错误一律返回 500。Import 对不支持的格式或空 payload 也返回 500 import_failed。 |
| 问题后果 | 客户端的输入错误（如 sourceId does not match path、格式错误）会收到 5xx 响应，导致监控误报。 |
| 证据 | 问题位于 source_handler.go 第 187 行和第 819 行（writeSourceError 在 806-819 行），记录的置信度为 confirmed。 |
| 修复建议 | 为输入类错误定义 sentinel error，并在 writeSourceError 和 Import 中映射为 400。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：SourceImportAudit |

### R-033 [P2] SSE 事件流无心跳且订阅不存在的会话时静默挂起

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P2 |
| 相关文件 | `internal/app/debug/publisher.go:27-56` |
| Legado 参照 | — |
| 问题原因 | 事件流没有终态检测，也没有心跳。订阅已完成或不存在的会话时，服务端不返回 404，也不主动断开连接。 |
| 问题后果 | 订阅会一直挂起，每个订阅都会占用一个 goroutine 和一个 channel。代理超时后，客户端只会收到原因不明的断流。 |
| 证据 | internal/app/debug/publisher.go:27-56 中的事件流既不检测会话终态，也不发送心跳。订阅已完成或不存在的会话时，连接会无限挂起。 |
| 修复建议 | 订阅时先检查会话是否存在及其状态：会话不存在就返回 404，已完成就主动关闭连接；同时定期发送 SSE 心跳。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DebugCheckAudit |

### R-034 [P2] 数组/URL 导入逐条 Save，无批量事务，中途失败留下部分状态

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P2 |
| 相关文件 | `internal/app/source/importer.go:155-172`<br>`internal/infra/sqlite/repositories/book_source_repository.go:23-35`<br>`internal/app/source/importer.go:155-179`<br>`internal/app/source/importer.go:156-172`<br>`internal/app/source/importer.go:191-194`<br>`internal/infra/sqlite/repositories/book_source_repository.go:30-44`<br>`internal/api/handlers/source_handler.go:186-189` |
| Legado 参照 | `SourceHelp.kt:131-140`, `ImportBookSourceViewModel.kt` |
| 问题原因 | importArray 对每个条目调用 importOne→repo.Save，每次保存都单独自动提交。SaveMany 在仓储层已经实现为事务写入，但生产代码中从未调用，Importer 也没有使用它。另外，Save 一旦失败就直接 return err，前面已写入的条目既不回滚，也不会计入 Items。 |
| 问题后果 | 批量导入时如果中途出现数据库错误，接口直接返回 500，但之前的条目已经写进库里，响应里又没有成功记录，用户无法对账。大批量导入时逐条写入也比较慢。 |
| 证据 | 复核确认调用链为 Import→importArray(importer.go:156-172)→importOne→repo.Save(:192)，SaveMany 全仓库只有测试和 fake 实现在调用。Save 出错时在 importer.go:169-172 直接 return err，source_handler.go:186-189 随即返回 500，result.Items 也随错误一起丢掉。Legado 在 SourceHelp.kt:131-140 中通过一次 bookSourceDao.insert(*sources) 单事务批量落库。 |
| 修复建议 | 建议先收集全部条目，再用 SaveMany 在单个事务内写入（与 Legado 的单事务批量写入一致）；如果不改成事务，至少把 Save 失败的条目记为 failed 后继续处理，并在响应中返回已成功的 Items。 |
| 验证结论 | 复核 confirmed；来源切片：PersistenceArchAudit, SourceImportAudit |

### R-035 [P2] book/source 子命令 --help 无可用子命令提示

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P2 |
| 相关文件 | `internal/cli/book.go:13-30` |
| Legado 参照 | — |
| 问题原因 | book 和 source 子命令的分发逻辑把 --help 当作未知子命令处理，只输出一行报错，不列出可用的子命令。 |
| 问题后果 | 用户执行 `book --help` 或 `source --help` 时拿不到 add\|list\|info\|refresh\|rm 等子命令列表，只能回到顶层 usage 去查。 |
| 证据 | book.go:27-29、source.go:25-27 遇到未知子命令（包括 --help）时只打印 `error: unknown book subcommand "--help"`，没有附带子命令列表。 |
| 修复建议 | 在 book/source 中单独识别 --help/-h，并在遇到未知子命令时打印子命令用法列表（add\|list\|info\|refresh\|rm）。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：CliBootstrapAudit |

### R-036 [P2] CLI 用法错误仍触发 SQLite 建库与迁移副作用

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P2 |
| 相关文件 | `internal/cli/root.go:101-110` |
| Legado 参照 | — |
| 问题原因 | root.go 的 Run 选中合法子命令后，在调用 fn 之前就先执行 openRuntime（config.Load + bootstrap.Open，包括 os.MkdirAll、sql.Open、Migrate），而参数校验写在 fn 里面。所以一定会因参数错误失败的调用，也会先完成建目录、建库和迁移。 |
| 问题后果 | 用户只是输错 CLI 参数（如 toc 缺 bookId、content index 非法），也会生成意外的空数据库文件（默认 data/reader.db）和目录，在文件系统上留下副作用。 |
| 证据 | internal/cli/root.go:101-110 先调用 openRuntime，再调用 fn。toc 缺 bookId、content index 非法等校验都在 fn 内部进行。 |
| 修复建议 | 把参数解析和校验提到 openRuntime 之前，或者改为校验通过后再延迟打开运行时，避免用法错误时建库和跑迁移。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：CliBootstrapAudit |

### R-037 [P2] 顶层 --help 返回 unknown command 错误而非帮助

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P2 |
| 相关文件 | `internal/cli/root.go:80-84` |
| Legado 参照 | — |
| 问题原因 | root.go 的 Run 没有把 -h/--help 当作帮助参数处理，而是当成未知子命令报错。 |
| 问题后果 | 用户或 Agent 用标准的 --help 查看 CLI 用法时，会看到错误提示，命令以失败状态（退出码 1）结束。 |
| 证据 | 运行 go run ./cmd/reader --help，输出 `error: unknown command "--help"` 和 usage，退出码为 1。相关代码在 internal/cli/root.go:80-84。 |
| 修复建议 | 在 root.go 的 Run 中先识别 -h/--help（以及 help），输出帮助信息，并以退出码 0 返回。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：CliBootstrapAudit |

### R-038 [P2] search --limit 负数静默表示不截断，未校验也未在文档声明

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P2 |
| 相关文件 | `internal/cli/search.go:56-58` |
| Legado 参照 | — |
| 问题原因 | search 截断条件为 `*limit >= 0 && len(items) > *limit`，负值会直接跳过截断，而 --limit 没有做范围校验。文档也没有说明这一语义。 |
| 问题后果 | 用户传入负数 --limit 时会静默返回全部结果，没有任何提示。这与 --to -1 表示“最后一个”的负数约定语义不同，容易造成混淆。 |
| 证据 | internal/cli/search.go:56-58 的条件是 `if *limit >= 0 && len(items) > *limit`，负值时不执行截断；该文件中没有对 --limit 的取值做范围校验。 |
| 修复建议 | 为 --limit 加上范围校验，负数直接报错；如果确实要保留“负数表示不限制”的语义，就在帮助和文档里写明。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：CliBootstrapAudit |

### R-039 [P2] POST/PATCH /book-sources 拒绝含未知字段的书源 JSON（Legado 可解析）

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:774-785`<br>`internal/api/handlers/source_handler.go:795-806`<br>`internal/api/handlers/source_handler.go:794-803` |
| Legado 参照 | `BookSource.kt:21`, `BookSourceController.kt:27` |
| 问题原因 | decodeJSON 开启了 DisallowUnknownFields，而 Create/Update 解码用的 generated.BookSource 没有自定义 UnmarshalJSON。因此 POST/PATCH /book-sources 遇到 spec 未声明的字段（如 Legado 新增字段或旧字段）会直接返回 400。 |
| 问题后果 | 书源 JSON 只要带有扩展字段，通过 POST/PATCH /book-sources 创建或更新时就会被拒绝，与 Legado 用 GSON 容忍未知字段的行为不一致。/import 主导入链路不受影响。 |
| 证据 | 复核时把真实生成代码拷到 /tmp 复现：含未知顶层字段的请求会报 unknown field 错误；spec 中的 BookSource 没有设置 additionalProperties:false，AGENTS.md:11 要求兼容 Legado 的 JSON 结构。import 请求体由 union 的自定义 UnmarshalJSON（server.gen.go:1111-1145）解码，单个对象或数组的 payload 含未知字段都能成功，所以"import 也会 400"的说法不成立。 |
| 修复建议 | POST/PATCH /book-sources 解码 BookSource 请求体时，改用不开启 DisallowUnknownFields 的解码方式。 |
| 验证结论 | 复核 downgrade（→P2）；来源切片：ApiContractAudit, SourceImportAudit |

### R-040 [P2] 文件类书源（type=3）下载链接为空时应报错，Reader 静默通过

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/book/info.go:147-162` |
| Legado 参照 | `BookInfo.kt:156-171` |
| 问题原因 | 在 internal/app/book/info.go:147-162 处，文件类书源（type=3）解析出的下载链接为空时，Reader 没有报错，只是保留一个空列表。Legado 的 isWebFile 分支遇到同样情况会抛出“下载链接为空”的异常，并且不解析 tocUrl。 |
| 问题后果 | 书籍信息解析阶段没有提示任何错误，问题要到后续下载链路才暴露为失败，用户难以定位到书源规则的问题。 |
| 证据 | Legado 的 BookInfo.kt:156-171 在 isWebFile 分支中，downloadUrls 为空时抛出 NoStackTraceException("下载链接为空")；Reader 的 info.go:147-162 对空列表直接放行。 |
| 修复建议 | 在 info.go 的文件类书源分支中，下载链接为空时直接返回“下载链接为空”错误，并像 Legado 一样跳过 tocUrl 解析。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：BookTocAudit |

### R-041 [P2] formatJS 单章执行失败导致整个目录更新失败，Legado 仅跳过该章

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/book/toc.go:254-273` |
| Legado 参照 | `BookChapterList.kt:135-146` |
| 问题原因 | Reader 在 formatChapters 中对每章执行 formatJS，只要有一章失败就直接 return err，没有像 Legado 那样逐章容错。 |
| 问题后果 | 只要有一章的 formatJS 出错，RefreshToc 就会整体失败，所有章节都不会落库，用户拿不到更新后的目录。 |
| 证据 | internal/app/book/toc.go:254-273 中，formatChapters 遇到任一章失败就 return err。对照 Legado BookChapterList.kt:135-146，它对每章的 eval 使用 runCatching，失败时只调用 Debug.log 并保留原标题。 |
| 修复建议 | 与 Legado 对齐：formatChapters 中单章 formatJS 失败时记录日志、保留原标题并继续处理后续章节，不再中断整个目录刷新。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：BookTocAudit |

### R-042 [P2] isTruthy 语义与 Legado isTrue 相反，isVip/isPay/isVolume 误判为 false

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/book/toc.go:312-319` |
| Legado 参照 | `StringExtensions.kt:74-79` |
| 问题原因 | Reader 的 isTruthy（toc.go:312-319）只把 true/1/yes/y 当作 true，其余值都判为 false。Legado 的 String.isTrue（StringExtensions.kt:74-79）规则相反：只要非空且不匹配 (?i)^(false\|no\|not\|0)$ 就判为 true。 |
| 问题后果 | 书源的 isVip/isPay 规则返回“VIP”“是”这类文本时会被误判为 false，VIP 和付费章节的标识因此丢失。isVolume 误判后，卷章节 URL 会走 baseURL 分支，而不是用 title+index 合成；这属于边缘书源场景。 |
| 证据 | 复核时用临时对比测试（/tmp/reader-verify/verifier）复现：对“VIP”和“是”，Reader 返回 false，Legado 返回 true；其余取值两边结果一致。 |
| 修复建议 | 把 isTruthy 改成与 Legado isTrue 一致的规则：值非空且不匹配 (?i)^(false\|no\|not\|0)$ 时返回 true，并补充对应的对比测试。 |
| 验证结论 | 复核 downgrade（→P2）；来源切片：BookTocAudit |

### R-043 [P2] 校验默认 timeoutMs 未实现，spec 声明 180000 默认但代码无兜底

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/check/checker.go:71-74` |
| Legado 参照 | — |
| 问题原因 | Options.Timeout 没有默认值，mapper 只在请求显式传入 timeoutMs 时才设置；oapi-codegen 生成的指针字段也不带 spec 里写的 default。 |
| 问题后果 | 请求不传 timeoutMs 时，校验全程没有整体超时，可能一直挂着不结束。这与 spec 声明的默认 180000 不一致，也和 Legado 不同：Legado 默认 180000L，并用 withTimeout 包住整个校验过程。 |
| 证据 | internal/app/check/checker.go:71-74 中 Options.Timeout 没有兜底默认值，mapper 只处理显式传入的情况，oapi-codegen 指针也不带 default。 |
| 修复建议 | 在 mapper 或 checker 中，当 Timeout 未设置时补上默认 180000ms，并像 Legado 的 withTimeout 一样用它包住整个校验流程。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DebugCheckAudit |

### R-044 [P2] 校验不检查空 searchUrl 分组语义，跳过规则与 Legado 不一致

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/check/diagnostics.go:17-35` |
| Legado 参照 | — |
| 问题原因 | 校验逻辑遇到空 searchUrl 时不加任何分组标记，只有 ExploreURL 非空时才执行 CheckDiscovery，也不区分搜索和发现的来源前缀。Legado 则会对空 searchUrl 标记“搜索链接规则为空”，对空 exploreKinds 标记“发现规则为空”。 |
| 问题后果 | 没有搜索链接的书源会被直接标为 passed，用户无法从分组中看出这类书源缺少搜索或发现规则。校验结果和 Legado 的语义不一致。 |
| 证据 | internal/app/check/diagnostics.go:17-35：仅在 ExploreURL 非空时才执行 CheckDiscovery，且没有分组标记，没有搜索链接的书源会被直接标为 passed。 |
| 修复建议 | 参照 Legado，对空 searchUrl 加“搜索链接规则为空”分组，对空发现规则加“发现规则为空”分组，同时区分搜索和发现的来源前缀，不再把这类书源直接标为 passed。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DebugCheckAudit |

### R-045 [P2] 用户替换规则被固化进章节缓存，事后修改规则对已抓取章节无效

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/content/content.go:157-162` |
| Legado 参照 | `BookContent.kt:150-153`, `ContentProcessor.kt:82` |
| 问题原因 | GetContent 先调用 processor.Process，之后才调用 chapters.SaveContent，所以写入缓存的是已经应用了用户规则的内容。缓存命中时直接返回这份内容，不会再经过 Processor。 |
| 问题后果 | 用户新增或修改净化规则后，已抓取的章节不会按新规则重新净化，这和 Legado 的行为不一致。 |
| 证据 | content.go:157-162 中 SaveContent 位于 Process 之后，content.go:88-93 缓存命中时直接 return。Legado 则在 BookContent.kt:150-153 通过 needSave 保存 replaceRegex 之后的原始正文，用户规则在阅读时才应用（ContentProcessor.kt:82）。 |
| 修复建议 | 缓存改为保存净化前的正文，缓存命中后再交给 Processor 应用用户规则。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ContentReplaceAudit |

### R-046 [P2] 正文规则解析出的章节标题未持久化到章节表

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/content/content.go:453-456` |
| Legado 参照 | `BookContent.kt:66-69` |
| 问题原因 | parsePage 解析出标题后只改了内存中的 chapter.Title。ChapterRepository 接口也没有提供更新标题的方法，所以标题无法写回章节表。 |
| 问题后果 | 正文页解析出的标题只在本次响应里有效，重启后会变回目录阶段的标题。之后的会话里，重复标题的删除和展示逻辑也会失效。 |
| 证据 | content.go:453-456 没有任何持久化操作，domain/book/repository.go:27-32 里也没有 SaveTitle。作为对照，Legado 的 BookContent.kt:66-69 会更新 title 并写库。 |
| 修复建议 | 给 ChapterRepository 加一个 SaveTitle 方法，在 parsePage 发现标题变化时调用它把新标题存进章节表。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ContentReplaceAudit |

### R-047 [P2] evalJS 缺少 title/rssArticle 顶层绑定（内容/搜索脚本适配器）

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/content/content.go:557-566` |
| Legado 参照 | `AnalyzeRule.kt:785` |
| 问题原因 | Legado 的 evalJS 会绑定 title=chapter?.title（AnalyzeRule.kt:785），而 Reader 的 content/search/info scriptAdapter 没有提供顶层 title 绑定。rssArticle 绑定则完全没有实现。 |
| 问题后果 | 正文 WebJS 脚本引用 title 时得到 undefined，会走 fallback 逻辑或直接失败，导致依赖该变量的书源与 Legado 行为不一致。 |
| 证据 | content.go:557-566 等处的 scriptAdapter Bindings 中没有 title，Legado 则在 AnalyzeRule.kt:785 绑定了 title=chapter?.title。rssArticle 在 Reader 中未实现。 |
| 修复建议 | 在 scriptAdapter 的 Bindings 中补上顶层 title（取值 chapter.Title），并评估是否实现 rssArticle 绑定。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ScriptRuntimeAudit |

### R-048 [P2] contentRule.sourceRegex 被无条件拒绝，无 webView 提取路径且无前置提示

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/content/content.go:83-85` |
| Legado 参照 | `WebBook.kt:335-337` |
| 问题原因 | 只要 contentRule.sourceRegex 非空，GetContent 就直接返回 ErrUnsupportedContentBrowserFlow，没有 webView 提取路径。报错要到正文请求阶段才出现，内容是 400 unsupported_rule_feature。 |
| 问题后果 | 需要 webView 渲染正文的书源在 Reader 下完全用不了。校书和导入阶段都不提示，用户要到看正文时才发现。 |
| 证据 | 问题代码在 internal/app/content/content.go:83-85，SourceRegex 非空时无条件拒绝。Legado 的做法见 WebBook.kt:335-337：把 sourceRegex 和 webJs 传给 getStrResponseAwait，由 webView 完成提取。 |
| 修复建议 | 在校书阶段遇到 sourceRegex 就提前警告，或在文档中写明不支持这一项及原因。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ContentReplaceAudit |

### R-049 [P2] 发现分类 sync.Map 缓存无 TTL、无源更新失效机制

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/discovery/discovery.go:190-197`<br>`internal/app/discovery/discovery.go:376-382` |
| Legado 参照 | `BookSourceExtensions.kt:100-103` |
| 问题原因 | storeCategoryCache 把 JS 生成的分类写入进程级 sync.Map，键为 bookSourceURL+exploreURL。这份缓存没有 TTL、没有上限，也没有任何清理调用；书源更新（importer 覆盖 exploreUrl）时也不会失效。 |
| 问题后果 | 如果书源更新后 ExploreURL 原文不变，但脚本行为变了（比如依赖 source.getVariable 或远端状态），Categories/Discover 会一直返回旧分类。另外 exploreUrl 可以是任意字符串，旧缓存永远不回收，内存会随书源和分类组合的增加无限上涨。 |
| 证据 | loadCategoryCache 命中缓存后就不再执行脚本（discovery.go:190-197），Service.cache 是进程级 sync.Map（discovery.go:56），写入在 discovery.go:371-395。Legado 则提供 removeExploreKinds 来显式清除缓存（help/source/BookSourceExtensions.kt:100-103）。 |
| 修复建议 | 参照 Legado 的 removeExploreKinds，在书源导入/更新/删除时清除对应 bookSourceURL 的分类缓存，并给缓存加上 TTL 和条目上限。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit, SearchDiscoveryAudit |

### R-050 [P2] 替换规则 CRUD 不校验 + 单条规则错误中止整个正文处理，一条坏规则打挂全部正文请求

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/replace/processor.go:91-97`<br>`internal/app/replace/processor.go:89-93`<br>`internal/app/replace/processor.go:87-93` |
| Legado 参照 | `ContentProcessor.kt:155-175`, `ContentProcessor.kt:160-180` |
| 问题原因 | Processor.Process 中只要有一条规则在运行时出错，就直接 return ProcessResult{}, err，把整个正文处理一起中止。规则超限时，自动禁用规则所用的 `_ = p.repo.Save` 会把保存错误直接丢掉（这是次要原因）；"保存时不校验"的说法不成立，因为 repo.Save 内部已经调用了 rule.Validate。 |
| 问题后果 | 一条规则在运行时出错，就会让对应正文请求整体失败并返回 500。如果禁用规则时保存失败，这条规则会一直保持启用，这本书每次请求正文都会重复失败，而且没有日志可查。 |
| 证据 | 复核结论：replace_rule_repository.go:19-22 的 Save 会调用 rule.Validate()，所以 CRUD 实际已有校验；问题成立的部分是 processor.go:87-93 遇到单条规则错误就 return ProcessResult{}, err，以及 processor.go:91 的 `_ = p.repo.Save` 吞掉了错误。作为对照，Legado ContentProcessor.kt:160-180 对每条规则单独 try/catch，出错后继续处理（超时则禁用规则后继续，其他异常记日志后继续）。 |
| 修复建议 | 建议对齐 Legado：Process 中单条规则出错时记日志并 continue，只有仓库级错误才中止；同时处理并记录禁用规则时 repo.Save 返回的错误。 |
| 验证结论 | 复核 downgrade（→P2）；来源切片：ContentReplaceAudit, CrossCuttingQualityAudit |

### R-051 [P2] 搜索/发现结果字段未按 Legado 语义格式化（name/author/wordCount/intro/kind）

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/search/single_source.go:126-143`<br>`internal/app/book/info.go:87-126`<br>`internal/app/book/info.go:118-120`<br>`internal/app/search/single_source.go:140-146` |
| Legado 参照 | `BookHelp.kt:477-481`, `BookHelp.kt:486-490`, `StringUtils.kt:252-269`, `BookList.kt:289`, `BookList.kt:231`, `BookHelp.kt:477-491` |
| 问题原因 | Reader 在搜索、发现、详情三处解析结果时，对 name/author/kind/wordCount/intro 只做了 strings.TrimSpace，没有照搬 Legado 的 formatBookName/formatBookAuthor、wordCountFormat 和 HtmlFormatter.format 清洗。另外，搜索的 kind 用的是 GetString，不是 GetStringList，规则命中多个节点时只取第一个。 |
| 问题后果 | 主流书源的结果里，作者会带“作者:”前缀，简介会残留 <br>/<p> 标签，字数显示原始数值，kind 会丢掉多值，展示语义和 Legado 不一致。作者带前缀时，name+author 跨源合并的命中率也会下降，但抓取链路不受阻断。 |
| 证据 | 复核确认，Reader 的 single_source.go:140-146、discovery.go:293-299、info.go:87-126 都只做了 TrimSpace。Legado 的 BookList.kt:220-261 用了 formatBookName/formatBookAuthor（BookHelp.kt:477-490）、getStringList().joinToString(",")（BookList.kt:233）、wordCountFormat（BookList.kt:242）和 HtmlFormatter.format（BookList.kt:260）。 |
| 修复建议 | 建议抽一个公共清洗函数，在搜索、发现、详情三处统一调用，对齐 Legado 的处理：书名和作者按 AppPattern 正则去掉装饰、字数用 wordCountFormat 换算、简介剥离 HTML，kind 改用 GetStringList 后以逗号拼接。 |
| 验证结论 | 复核 downgrade（→P2）；来源切片：BookTocAudit, SearchDiscoveryAudit |

### R-052 [P2] 搜索空列表回退详情解析时未检查 bookUrlPattern 是否为空

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/search/single_source.go:153-163` |
| Legado 参照 | `BookList.kt:100-106` |
| 问题原因 | Legado 只在 collections 为空且 bookSource.bookUrlPattern 为空时才走详情解析回退。Reader 没有检查 bookUrlPattern，只要结果为空就调用 PreviewSearchBookFromInfo。 |
| 问题后果 | 书源配置了 bookUrlPattern、但当前 URL 不匹配的列表页上，本该返回空结果，却会多出一个以列表页 URL 作 bookUrl 的假条目。用户点进去后，详情和目录请求都会打到列表页上而失败。 |
| 证据 | Legado BookList.kt:100-106 的回退条件是 collections.isEmpty() && bookSource.bookUrlPattern.isNullOrEmpty()；Reader 的 single_source.go:153-163 在结果为空时直接尝试 PreviewSearchBookFromInfo。 |
| 修复建议 | 在 single_source.go 的空列表回退里加上 bookUrlPattern 为空的判断，与 Legado 条件保持一致。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：SearchDiscoveryAudit |

### R-053 [P2] body 含 = 或 & 时无视显式 Content-Type 强制表单编码

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/search/single_source.go:548-557` |
| Legado 参照 | `AnalyzeUrl.kt:279-283` |
| 问题原因 | 只要 string body 满足 looksLikeFormBody，就会调用 EncodeFormString，不管书源是否已显式设置了非表单的 Content-Type（如 text/plain）。而且会先用空 charset 编码一次，最后再按 charsetName 重复编码一次；Legado 只在 body 不是 JSON/XML 且 Content-Type 为空时才按表单编码。 |
| 问题后果 | body 原文会被 percent 编码两次，服务器解析请求失败，搜索、书籍信息和正文请求都会受影响。 |
| 证据 | search single_source.go:548-557、book fetch_request.go:75-83、content content.go:332-341 三处都按 looksLikeFormBody 直接调用 EncodeFormString。对照 Legado 的 AnalyzeUrl.kt:279-283，只有 Content-Type 为空时才做表单编码。 |
| 修复建议 | 三处统一改成：只有 Content-Type 为空时才做表单编码和补默认头，并去掉重复编码。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：UrlFetchAudit |

### R-054 [P2] bookUrlPattern 用未锚定的 regexp.MatchString，与 Legado 整串 matches 语义不一致且忽略编译错误

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/search/single_source.go:63-65` |
| Legado 参照 | `BookList.kt:62-66` |
| 问题原因 | single_source.go:64 调用 regexp.MatchString(src.BookURLPattern, resp.URL)。这是子串搜索，不是 Legado 的整串 matches，而且编译错误被 `ok, _ :=` 直接忽略了。 |
| 问题后果 | 像 `book/\d+` 这样的 pattern 也会命中 https://x.com/book/123/other 这类列表页，列表页会被当成详情页直接走 info 解析，搜索列表就丢了。pattern 写错时会静默按“不匹配”处理，书源配置错误不会暴露出来。 |
| 证据 | Go 端代码是 `ok, _ := regexp.MatchString(src.BookURLPattern, resp.URL)`（single_source.go:63-65）。Legado 在 BookList.kt:62-66 用的是 baseUrl.matches(it.toRegex())，Kotlin 的 matches 要求整串匹配，pattern 非法时会抛异常。 |
| 修复建议 | 把 pattern 包成 `^(?:pattern)$` 再编译匹配，实现整串匹配，同时处理编译错误并上报书源配置问题，不要静默当成不匹配。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：SearchDiscoveryAudit |

### R-055 [P2] 默认导入策略在 lastUpdateTime 相等时静默跳过

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/source/importer.go:213-222` |
| Legado 参照 | `ImportBookSourceViewModel.kt:215-222` |
| 问题原因 | shouldSkip 的默认判断是 existing.LastUpdateTime >= incoming.LastUpdateTime。两边 lastUpdateTime 相等时（大量书源默认就是 0），新规则会被静默丢弃。 |
| 问题后果 | 两个 lastUpdateTime 都为 0 的书源快照永远无法互相覆盖。用户只看到结果为 skipped，规则实际没有更新。 |
| 证据 | 问题位于 internal/app/source/importer.go:220 的 shouldSkip 默认比较逻辑，对照 Legado 的 ImportBookSourceViewModel.kt:215-222，记录结论为 confirmed。 |
| 修复建议 | 两边 lastUpdateTime 都为 0 时改为覆盖导入，或者在 spec 中写明这种跳过行为。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：SourceImportAudit |

### R-056 [P2] 分组分隔仅支持英文逗号，未对齐 Legado 的 , ; ，；

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/source/importer.go:255-265` |
| Legado 参照 | `AppPattern.kt:21` |
| 问题原因 | splitGroups 只按英文逗号切分，而 Legado 的 splitGroupRegex 是 [,;，；]。同样是解析书源分组字符串，diagnostics.go 用的 FieldsFunc 支持中英文逗号和分号，两处的行为不一样。 |
| 问题后果 | groupName 里用中文分号或全角逗号分隔时，整串会被写成一个分组，按 , 边界做组过滤也查不到。含中文逗号的分组在导入和校验诊断时切出来的结果也会不同。 |
| 证据 | importer.go:256 使用 strings.Split(value, ",")，diagnostics.go:116 使用 strings.FieldsFunc（支持中英文逗号和分号）；对照的 Legado 定义在 AppPattern.kt:21，该问题已确认（confirmed）。 |
| 修复建议 | 把 splitGroups 统一改成支持 , ; ， ； 四种分隔符的 strings.FieldsFunc 实现，并让导入和诊断共用这一个函数。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：CrossCuttingQualityAudit, SourceImportAudit |

### R-057 [P2] proxy/dnsIp/serverID 等 URL 选项不支持时硬失败

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/app/source/url_option_support.go:11-26` |
| Legado 参照 | `AnalyzeUrl.kt:113,263-265` |
| 问题原因 | ValidateSupportedURLOptions 的白名单里没有 proxy、dnsIp、serverID、webViewDelayTime。书源 URL 只要带了其中任一选项，整个请求就会直接报 unsupported url option 错误，不会降级忽略。 |
| 问题后果 | 依赖代理等选项的主流书源完全无法使用：多源搜索时该源只显示为 warning，单源 API 则直接报错。Legado 原生支持 proxy 和 serverID，两边行为不一致。 |
| 证据 | internal/app/source/url_option_support.go:11-26 的白名单不含上述选项，content_test.go:158-171 已确认 proxy 会触发错误。Legado 对应实现见 AnalyzeUrl.kt:113,263-265（getProxyClient）。 |
| 修复建议 | 建议实现 proxy 支持；若暂不实现，至少返回专门说明“不支持该选项”的明确错误。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：UrlFetchAudit |

### R-058 [P2] 导入未设置 Legado 默认值（enabled/enabledExplore/enabledCookieJar=true、respondTime=180000）

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/domain/source/book_source.go:26-40` |
| Legado 参照 | `BookSource.kt:52-57,77` |
| 问题原因 | 导入或 Create 时没有套用 Legado 的默认值：enabled/enabledExplore/enabledCookieJar 字段缺省时存成 false，而 Legado 默认是 true；save() 又显式写入 RespondTime=0，Legado 默认是 180000。 |
| 问题后果 | 主流书源如果省略这些字段，导入后 cookie jar 是关闭的，书源也默认不能搜索。 |
| 证据 | book_source.go:26-40 的这几个字段直接用 Go 零值，book_source_repository.go:186 显式写入 RespondTime=0；Legado 对应的默认值见 BookSource.kt:52-57,77。该问题已确认。 |
| 修复建议 | 在 ParseBookSource/Normalize 中补上 Legado 默认值：enabled/enabledExplore/enabledCookieJar=true，respondTime=180000。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：SourceImportAudit |

### R-059 [P2] concurrentRate 及全局/源并发配置完全未接线

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/domain/source/book_source.go:31`<br>`internal/domain/source/book_source.go:30-31` |
| Legado 参照 | `AnalyzeUrl.kt:129`, `ConcurrentRateLimiter.kt`, `ConcurrentRateLimiter.kt:8`, `AnalyzeUrl.kt:408`, `BookSource.kt:57` |
| 问题原因 | BookSource.ConcurrentRate 只做了存储和映射（sqlite、mapper），抓取链路里没有任何限流实现。READER_GLOBAL_CONCURRENCY/READER_SOURCE_CONCURRENCY 除 config.go 自身外全仓零引用，多源搜索只用一个默认值为 4 的全局信号量。 |
| 问题后果 | 高并发搜索或校验时，同一源站点没有按源限速，容易触发站点风控或封禁。用户配置的并发参数不起作用；此外 enabledCookieJar 缺省为 false，与 Legado 默认的 true 不一致。 |
| 证据 | 复核用 grep 确认：ConcurrentRate 只在 book_source.go:31、sqlite repo:185/212、mapper:35/84 出现，没有消费点；并发配置只在 config.go 内被引用；multi_source.go:53-57 只有 input.Concurrency 全局信号量。Legado 在 ConcurrentRateLimiter.kt:8 和 AnalyzeUrl.kt:408/490 有按源限流。 |
| 修复建议 | 实现与 ConcurrentRateLimiter 等价的按源限流，在抓取入口解析 concurrentRate，接上全局/源级并发配置，并把 enabledCookieJar 的默认值改为与 Legado 一致的 true。 |
| 验证结论 | 复核 confirmed；来源切片：SourceImportAudit, UrlFetchAudit |

### R-060 [P2] 响应无 Content-Type 时完全跳过编码检测

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/fetcher/http_fetcher.go:233-237` |
| Legado 参照 | `OkHttpUtils.kt:75-95` |
| 问题原因 | fetchOnce 只有在 looksLikeTextContent(Content-Type) 为真时才调用 decodeTextResponseBody。响应缺少 Content-Type 头时该判断返回 false，所以 charset.NewReader 自带的 BOM/meta 嗅探根本不会执行。 |
| 问题后果 | 老旧 GBK 站点常常不返回 Content-Type，这时 GBK 字节会原样传下去，正文显示为乱码。这一行为也和 Legado 不一致：Legado 先用 HTTP 头里的 charset，没有时再用 EncodingDetect 做内容探测。 |
| 证据 | http_fetcher.go:233-237 的外层门禁挡住了 274-291 的解码逻辑；对应的 Legado 实现见 OkHttpUtils.kt:75-95。有 Content-Type 但没有 charset 的情况已由 charset.NewReader 处理，并且有测试。 |
| 修复建议 | Content-Type 为空时也调用 charset.NewReader 做 BOM/meta 嗅探，或者实现一个内容探测的回退方案。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：UrlFetchAudit |

### R-061 [P2] css 逗号多选择器结果顺序与 Legado 文档序不一致

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/rule/css/css.go:259-283`<br>`internal/infra/rule/css/css.go:282-303` |
| Legado 参照 | `AnalyzeByJSoup.kt:320` |
| 问题原因 | findSelection 的逗号分支按选择器分支的书写顺序逐个 AddSelection，没有按文档序合并结果。Legado 用 jsoup 的 select("a,b") 选取，结果是按文档序返回的。 |
| 问题后果 | 书源列表字段如果依赖逗号多选择器的结果顺序，取到的元素顺序会和 Legado 不一样，导致数据错位。只有依赖逗号选择器结果顺序的字段会受影响。 |
| 证据 | 复核已复现：findSelection 对 a,b@text 返回 [a1 b1 b2]，代码位置在 css.go:277-309（清单里写的 259-283 已对不上）。jsoup 的 select("a,b") 返回 [b1 a1 b2]，Legado 在 AnalyzeByJSoup.kt:320 调用 temp.select(beforeRule)，用的就是这个语义。 |
| 修复建议 | 逗号分支把各分支结果合并去重后，按元素的文档序重新排列，与 jsoup select 的行为保持一致。 |
| 验证结论 | 复核 confirmed；来源切片：SelectorAudit |

### R-062 [P2] css children 关键字仅支持裸用，不支持 children.xxx 前缀组合

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/rule/css/css.go:319-337` |
| Legado 参照 | — |
| 问题原因 | selectBase 只在 rule 完全等于 children（EqualFold 比较）时才返回子元素，不识别 children.xxx 这类前缀组合。Legado 的写法是取 beforeRule.split(".")[0]=="children"，所以 children.div.0 这类前缀组合在 Legado 中能用。 |
| 问题后果 | 书源里 body.children.0@text、body.children@text 这类写法会被当作原始 CSS 选择器查找，因为匹配不到而返回空，导致按 Legado 语法写的书源解析失败。 |
| 证据 | internal/infra/rule/css/css.go:319-337 中 selectBase 只对整个 rule 做 EqualFold("children") 判断。已复现：body.children.0@text 和 body.children@text 都返回空。 |
| 修复建议 | 在 selectBase 中把 rule 按 "." 拆开，如果第一段是 children，就先取子元素，再按剩余部分（标签/索引）继续筛选，和 Legado 的处理方式保持一致。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：SelectorAudit |

### R-063 [P2] css 属性提取器不按 Legado 对 attr 值去重

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/rule/css/css.go:737-766` |
| Legado 参照 | `AnalyzeByJSoup.kt:272-276` |
| 问题原因 | Reader 的 extractFromSelection（internal/infra/rule/css/css.go:737-766）把 attr 值直接 append 到结果中，没有去重。Legado 的 getResultLast 默认分支则会用 textS.contains(url) 去重，相同的值只保留第一个。 |
| 问题后果 | 像 a@href 这样的规则遇到重复 href 时会返回重复条目，列表型书源字段因此出现重复项，和 Legado 的结果不一致。 |
| 证据 | 对照 Legado AnalyzeByJSoup.kt:272-276 的去重逻辑，Reader 在 css.go:737-766 中直接 append。原始记录写明该问题“已复现”，但没有附复现命令。 |
| 修复建议 | 在 extractFromSelection 的 attr 默认分支里按 Legado 的方式去重：值已存在就跳过，只保留首次出现的那一条。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：SelectorAudit |

### R-064 [P2] [跨域] ##regex##replacement### 无匹配时返回原文而非空串，非法正则直接报错而非降级字面量替换

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/rule/regex/regex.go:144-153`<br>`internal/infra/rule/regex/regex.go:139-166` |
| Legado 参照 | `AnalyzeRule.kt:441-448`, `AnalyzeRule.kt:449-453` |
| 问题原因 | compiledPattern.Replace 的 firstOnly 分支在没有匹配时直接 return input, nil，regexp2 分支也是这样处理的。compilePattern 编译失败时会返回 ErrInvalidPattern，不像 Legado 那样降级为字面量替换。 |
| 问题后果 | 像 `标题##【.*】###` 这样靠 ### 过滤标题的目录规则，遇到不匹配的标题时 Legado 返回空串（即被过滤），Reader 却保留原标题，两边的目录过滤结果不一致。非法正则在复核实测中没有报错，而是返回原值，但这和 Legado 字面量替换的结果仍有差异。 |
| 证据 | 复核时用临时测试 GetString("class.title@text##【.*】###")，对不匹配的 "普通标题" 返回了原文而不是空串；Legado 的 AnalyzeRule.kt:441-448 在 replaceFirst 无匹配时 return ""。非法正则实测返回原值、没有报错，所以错误分支的影响有所缓解，严重度降级为 P2。 |
| 修复建议 | 建议把 firstOnly 无匹配时的返回值改为 ""；正则编译失败时降级为 strings.Replace 字面量替换，replaceFirst 情况下返回 replacement，与 Legado 的 AnalyzeRule.kt:449-453 保持一致。 |
| 验证结论 | 复核 downgrade（→P2）；来源切片：RuleParserAudit |

### R-065 [P2] source 绑定缺失 BaseSource.get/put 与 enabled/enabledCookieJar 字段

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/script/runtime.go:1777-1866` |
| Legado 参照 | `BaseSource.kt:224` |
| 问题原因 | SourceBindings（runtime.go:1777-1866）没有实现 BaseSource 的 get(key)/put(key,value)，这两个方法在 Legado 中通过 CacheManager 以 v_<key>_ 形式持久化。它也缺少 enabled 和 enabledCookieJar 这两个布尔字段，与 Legado BaseSource.kt:224(put)、232(get)、206(setVariable) 不一致。 |
| 问题后果 | 书源脚本在保存登录态或变量时会抛出 ReferenceError。登录类书源的整条流程都会失败。 |
| 证据 | 实测 typeof source.get、typeof source.put、typeof source.enabledCookieJar 的结果都是 undefined。相关绑定代码位于 internal/infra/script/runtime.go:1777-1866。 |
| 修复建议 | 在 SourceBindings 中补上 get/put（复用 NamespacedCache 做持久化），并加上 enabled 和 enabledCookieJar 布尔字段。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ScriptRuntimeAudit |

### R-066 [P2] [跨域] 脚本触发的网络请求使用 context.Background()，不受 Policy.Timeout 与上层取消控制

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/script/runtime.go:425-450` |
| Legado 参照 | `AnalyzeRule.kt:799-803` |
| 问题原因 | java.get/post/connect/ajax 写死用 context.Background()，installInterrupt 只对 JS 执行起作用。所以脚本发出的网络请求不在 Policy.Timeout 和客户端取消链的控制范围内。 |
| 问题后果 | 客户端断开后抓取还会继续跑。脚本循环调用 ajax 时，总耗时没有上限。 |
| 证据 | runtime.go:425、439、443、450 写死了 context.Background()。Legado 把 coroutineContext 一路传到 ajax/connect（AnalyzeRule.kt:799-803）。 |
| 修复建议 | 把 Eval 收到的 ctx 存进 evalContext，再传给 fetchScriptCall/fetchScriptURL，替换 context.Background()。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ScriptRuntimeAudit |

### R-067 [P2] java.ajax 失败时把错误文本作为正文返回而非 null

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/script/runtime.go:443-448` |
| Legado 参照 | `JsExtensions.kt:93-111` |
| 问题原因 | java.ajax 出错时直接把 err.Error() 当作响应体字符串返回（例如 "script_failed"），而不是返回 null。这与 Legado 的行为不一致：Legado 的 ajax 失败时经 runCatching 返回 null。 |
| 问题后果 | 脚本无法区分网络失败和空内容，错误文本会被当作正文解析，产生脏数据。 |
| 证据 | internal/infra/script/runtime.go:443-448 失败时 return err.Error()；实测 Fetcher 为 nil 时，返回值为 "script_failed"，且 err 为 nil。Legado 的对应实现见 JsExtensions.kt:93-111，失败时返回 null。 |
| 修复建议 | java.ajax 失败时改为返回 goja.Null()，或抛出脚本可捕获的异常，不要再返回错误文本。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ScriptRuntimeAudit |

### R-068 [P2] rewriteJavaCompatScript 对含 JavaImporter 字样的脚本全量改写 function 声明

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/script/runtime.go:841-850`<br>`internal/infra/script/runtime.go:738-744` |
| Legado 参照 | `AnalyzeRule.kt` |
| 问题原因 | rewriteJavaCompatScript 只要发现脚本里出现 "JavaImporter" 字样，就用正则把所有 function name(...) 声明改写成赋值语句。改写时不区分作用域，也不判断这些字样是否在注释或字符串里。 |
| 问题后果 | 改写后函数声明不再提升，原本合法的书源脚本可能报 ReferenceError，或者直接变成 SyntaxError 而无法执行。注释里写到 JavaImporter 也会误触发改写，这与 Legado/Rhino 原生支持 JavaImporter 的行为不一致。 |
| 证据 | 复核确认该函数实际位于 runtime.go:738-744（原报 841-850）：注释含 JavaImporter 时会全量改写函数声明，Eval 报 script_failed: ReferenceError: helper is not defined，原本合法的脚本改写后变成 SyntaxError。httpTTS 型的真实用法仍能通过，jsHelp.md 证实 Rhino 原生支持 JavaImporter。 |
| 修复建议 | 改为只在脚本解析失败时才按需改写，或者先剥离注释和字符串再判断是否需要改写。 |
| 验证结论 | 复核 confirmed；来源切片：ScriptRuntimeAudit |

### R-069 [P2] source_variables 表无写入路径，JS 源级状态仅存进程内存，重启即丢

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/sqlite/migrate.go:172-177`<br>`internal/infra/script/runtime.go:1750-1775`<br>`internal/infra/script/runtime.go:1757-1775` |
| Legado 参照 | `BaseSource.kt` |
| 问题原因 | 迁移里建了 source_variables 表，但全仓没有任何写入它的代码路径。JS 的 source.setVariable/putVariable/getVariable 以及 LoginHeader/LoginInfo 都只读写包级 sync.Map（sourceCompatState），这个 map 只增不删。 |
| 问题后果 | 服务重启后，书源的登录态和脚本写入的变量全部丢失，靠 setVariable 存 token 的书源请求链会失效。另外，删除书源后状态仍留在内存里，内存随书源数量单调增长；删掉再导入同名书源会继承旧的登录态。 |
| 证据 | runtime.go:1758 是包级 `var sourceCompatState sync.Map`。grep source_variables 只命中 migrate.go:172-177 的建表和 book_source_repository.go:102 删除书源时的 DELETE，所以 R-018 验收清理的其实是一张从不写入的表。 |
| 修复建议 | 给 sourceCompatState 加 repository 持久化，写入 source_variables 表、启动时按需加载，删除书源时同时清理内存状态。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.85）；来源切片：CrossCuttingQualityAudit, DocBaselineAudit, SourceImportAudit |

### R-070 [P2] replace 作用域用 LIKE 子串匹配，误命中且与 Legado 语义不符

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/sqlite/repositories/replace_rule_repository.go:82-86` |
| Legado 参照 | `ContentProcessor.kt` |
| 问题原因 | findEnabledByScope 用 scope LIKE '%'\|\|?\|\|'%' 做子串匹配来判断书名或书源是否命中，exclude_scope 的判断方式也一样。Legado 的作用域则是把逗号分隔的每一项逐个匹配（见 ContentProcessor.kt）。 |
| 问题后果 | 替换规则可能被套用到作用域外的书或书源上，例如书名“斗破”会误命中 scope='斗破苍穹'。排除作用域同样会误判，正文替换结果与 Legado 不一致。 |
| 证据 | replace_rule_repository.go:82-86 中 findEnabledByScope 的 scope 和 exclude_scope 条件都用 LIKE 子串匹配。Legado 对应逻辑在 legado/app/src/main/java/io/legado/app/help/book/ContentProcessor.kt，按逗号拆分后逐项匹配。 |
| 修复建议 | 把 SQL 里的 LIKE 子串匹配去掉，改在应用层按逗号拆分 scope/exclude_scope，再逐项精确匹配。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：PersistenceArchAudit |

### R-071 [P2] debug 会话 mode 未按 DebugMode 枚举校验，任意字符串可建会话

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:172-190` |
| Legado 参照 | — |
| 问题原因 | CreateDebugSession 只对 generated.Keyword 做了特判，要求必须有 keyword。其他 mode（包括拼错的）全部进入 else 分支，只检查 url 非空，然后原样传给 debugSvc.CreateSession，没有按 DebugMode 枚举做校验。 |
| 问题后果 | 传入非法 mode 也会返回 201 并创建会话，但这个会话永远无法被正确执行。调用方以为创建成功，实际调试不可用。 |
| 证据 | internal/api/handlers/source_handler.go:172-190 中，非 keyword 的 mode 都走 else 分支，只要求 url 非空，mode 原样透传。spec 中 DebugMode 枚举只有 keyword/info_url/toc_url/content_url/discovery_url 这几个值。 |
| 修复建议 | 在 CreateDebugSession 里先按 DebugMode 枚举白名单校验 mode，不在枚举内的直接返回 400，然后再按 mode 检查 keyword 或 url 参数。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-072 [P2] [跨域] debug Runner defer 无条件按 sessionID 删除，重启同 ID 误删新运行的 cancel

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P2 |
| 相关文件 | `internal/app/debug/runner.go:28-31` |
| Legado 参照 | — |
| 问题原因 | debug Runner 的 Start 遇到相同 sessionID 时，会先 cancel 旧 goroutine，再注册新的 cancel。旧 goroutine 的 defer 不检查 map 里存的还是不是自己的 cancel，直接按 sessionID 删除，可能在新 Start 注册后把新运行的 cancelFunc 删掉。 |
| 问题后果 | 复用同一 ID 或重跑 check 任务后，再调用 Cancel 会因找不到 cancel 而返回 false。正在运行的任务无法取消，只能等它自然结束，取消操作会静默失效。 |
| 证据 | internal/app/debug/runner.go:28-31 的 defer 无条件执行 `delete(r.runs, session.SessionID)`。worker/check/runner.go:109 的 `delete(r.cancels, checkTask.TaskID)` 同样不加判断。 |
| 修复建议 | defer 删除前先确认 map 中当前的条目仍属于本次运行（例如用唯一运行标识或 token 比对），一致时才删除；check runner 也按同样方式修复。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-073 [P2] foreign_keys PRAGMA 仅 Migrate 时生效，FK 级联删除可能失效

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/sqlite/migrate.go:10-13` |
| Legado 参照 | — |
| 问题原因 | chapters 表声明了 FK ON DELETE CASCADE，但 foreign_keys=ON 只在 Migrate() 里用 db.Exec 设置了一次，属于连接级设置。连接重建后该设置默认为 OFF。 |
| 问题后果 | 在新连接上执行 DELETE FROM books 不会级联删除 chapters。book Delete 只显式清理了 cache_entries，没有兜底删除章节，可能留下孤儿章节数据。 |
| 证据 | internal/infra/sqlite/migrate.go:10-13 只在 Migrate() 内用 db.Exec 设置一次 foreign_keys=ON。book Delete 只显式删除 cache_entries，没有删除 chapters 的逻辑。 |
| 修复建议 | 把 foreign_keys 参数写进 DSN，保证每个连接都开启外键约束。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：PersistenceArchAudit |

### R-074 [P2] 书源规则 JSON 列反序列化失败被静默吞没

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/sqlite/repositories/book_source_repository.go:191-195` |
| Legado 参照 | — |
| 问题原因 | scanBookSource 对 ruleExplore、ruleSearch、ruleBookInfo、ruleToc、ruleContent 五个 JSON 列做 json.Unmarshal 时都用 '_ =' 丢掉了错误。 |
| 问题后果 | 列里存的 JSON 非法时，书源会悄悄变成"没有规则"，没有任何报错提示数据已损坏，用户和开发者都难以排查。 |
| 证据 | book_source_repository.go:191-195 的五处 json.Unmarshal 全部用 '_ =' 忽略错误；遇到非法 JSON 时 Flexible.Value 为 nil，Raw 也为空。 |
| 修复建议 | 反序列化失败时返回错误，至少也要记一条日志，标明是哪个书源的哪个规则列数据损坏。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：PersistenceArchAudit |

### R-075 [P2] UpdateTaskStatus 读-验-写非原子，状态机竞态

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/sqlite/repositories/check_repository.go:41-50` |
| Legado 参照 | — |
| 问题原因 | UpdateTaskStatus 的流程是：先调 FindTask 读出状态（其中还会额外执行一次 ListItems 查询），再在应用层按状态机做校验，最后单独执行 UPDATE。这三步没有放在同一个事务里，所以整体不是原子操作。 |
| 问题后果 | 如果 worker 完成任务和用户取消任务同时发生，两边可能都读到 running 并都通过校验。最终状态由最后写入的一方决定，状态机的约束因此失效。 |
| 证据 | check_repository.go:41-50 中的读取、校验、写入三步之间没有事务包裹，状态检查和写入是分开执行的。 |
| 修复建议 | 把读取、校验和写入合并成一条带条件的 UPDATE（WHERE status=from），再根据 RowsAffected 判断状态转换是否成功。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：PersistenceArchAudit |

### R-076 [P2] check 并发不限 goroutine 创建，取消后排队 item 被记为 failed 而非 cancelled

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P2 |
| 相关文件 | `internal/worker/check/runner.go:80-93` |
| Legado 参照 | — |
| 问题原因 | 每个 source 都直接 wg.Add(1)+go func，sem 只在 goroutine 内部阻塞，所以 goroutine 会一次性全部创建，数量等于源数。取消后，正在 sem 上等待的 goroutine 拿到信号量就立即失败、状态变成 failed，而 runner.go:89 只在状态不是 failed 时才改为 cancelled。 |
| 问题后果 | 源数量多时会同时建出大量 goroutine。取消后，还在排队的 item 被记成 failed 而不是 cancelled，和状态机里 cancelled 的语义对不上，用户看到的检查结果状态也会出错。 |
| 证据 | internal/worker/check/runner.go:80-93 中每个 source 都直接 wg.Add(1)+go func，sem 只在 goroutine 内阻塞。runner.go:89 只在非 failed 时改为 cancelled，已经是 failed 的排队 item 会保持 failed。 |
| 修复建议 | 把获取 sem 的步骤移到创建 goroutine 之前，并在取消时把尚未执行或因取消而失败的 item 统一标为 cancelled。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DebugCheckAudit |

### R-077 [P2] HTTP server 缺少 ReadTimeout/IdleTimeout/WriteTimeout，body 阶段可被 slowloris 挂住

| 字段 | 内容 |
|---|---|
| 维度 | 安全 |
| 严重度 | P2 |
| 相关文件 | `cmd/reader/serve.go:105-111`<br>`cmd/reader/serve.go:106-111` |
| Legado 参照 | — |
| 问题原因 | newRuntimeServer 和 newServerWithToken 构造 HTTP server 时只设置了 ReadHeaderTimeout: 5s，没有设置 ReadTimeout、WriteTimeout、IdleTimeout。 |
| 问题后果 | 客户端慢速发送请求体时，能长期占用连接和 goroutine（slowloris），空闲连接也不会被回收。在自托管单用户部署下，低成本的连接耗尽攻击就可能把服务打满，影响可用性。 |
| 证据 | 复核确认，serve.go:106-111 的 newRuntimeServer 和 :89-95 的 newServerWithToken 都只设置了 ReadHeaderTimeout 5s。由于这是自托管单用户服务的可用性加固问题，严重度从 P1 降为 P2。 |
| 修复建议 | 在两个 server 构造函数中补上 ReadTimeout、WriteTimeout 和 IdleTimeout。 |
| 验证结论 | 复核 downgrade（→P2）；来源切片：SecurityAudit |

### R-078 [P2] HTTP 请求体无大小限制，导入大 JSON 可耗尽内存

| 字段 | 内容 |
|---|---|
| 维度 | 安全 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:795-802`<br>`internal/api/handlers/source_handler.go:795-806`<br>`internal/api/handlers/source_handler.go:794-803` |
| Legado 参照 | — |
| 问题原因 | decodeJSON 直接用 json.NewDecoder(r.Body) 解码请求体，没有用 http.MaxBytesReader 或 LimitReader 限制大小。http.Server 也没有设置请求体上限，所以所有 POST/PUT 请求体都没有大小限制。 |
| 问题后果 | 攻击者或误用的客户端可以发送任意大的 JSON（例如向 /book-sources/import 提交超大书源数组），请求体会被完整读入内存，最终导致内存耗尽和拒绝服务。这些接口需要鉴权，但默认空 token 部署时不受此保护。 |
| 证据 | 复核确认 decodeJSON（source_handler.go:794-803）直接解码 r.Body，全仓 grep 找不到 MaxBytesReader，http.Server 也没有请求体上限。根因成立，严重度按标尺从 P1 降为 P2。 |
| 修复建议 | 在 decodeJSON 或 middleware 中用 http.MaxBytesReader 限制请求体大小，上限可复用 READER_FETCH_MAX_BODY_BYTES 或新增一个配置项。 |
| 验证结论 | 复核 downgrade（→P2）；来源切片：ApiContractAudit, SecurityAudit |

### R-079 [P2] SQLite 数据目录 0755、数据库文件权限未收紧

| 字段 | 内容 |
|---|---|
| 维度 | 安全 |
| 严重度 | P2 |
| 相关文件 | `internal/bootstrap/bootstrap.go:54-55` |
| Legado 参照 | — |
| 问题原因 | Bootstrap.Open 用 0o755 创建数据目录。SQLite 数据库文件由 modernc.org/sqlite 按进程默认 umask 创建，通常是 0644，权限没有收紧。 |
| 问题后果 | 数据库里存有目标站点的持久化 Cookie，也就是会话凭证。同一台机器上的其他用户可以直接读到。 |
| 证据 | internal/bootstrap/bootstrap.go:54 以 0o755 创建数据目录。Cookie 由 internal/infra/sqlite/repositories/cookie_repository.go 持久化到该数据库。 |
| 修复建议 | 数据目录改用 0o700 创建，打开数据库后把数据库文件 chmod 为 0600。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：SecurityAudit |

### R-080 [P2] 脱敏白名单遗漏常见敏感头，URL query 凭证未覆盖

| 字段 | 内容 |
|---|---|
| 维度 | 安全 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/logging/redact.go:12-23` |
| Legado 参照 | — |
| 问题原因 | RedactHeaders 的 sensitiveHeaders 白名单只有 authorization、cookie、set-cookie 三个键。RedactString 的敏感模式只匹配 authorization:、cookie:、set-cookie:、token= 前缀，不处理 x-api-key: 等头，也不处理 URL query 里的 key/password 类参数。 |
| 问题后果 | X-Api-Key、X-Auth-Token、Proxy-Authorization 等头，以及带 key 的 URL，出现在调试输出或错误消息中时会原样写进 debug 事件。这些事件会持久化到 SQLite，并推送给 SSE 订阅者，造成凭证泄露。 |
| 证据 | internal/infra/logging/redact.go:12-16 的白名单只有这三个头，redact.go:18-23 的模式不含 x-api-key: 和 query 参数。未脱敏内容经 internal/app/debug/publisher.go 的 Publish->AppendEvent 写入存储并推送。 |
| 修复建议 | 把 X-Api-Key、X-Auth-Token、Proxy-Authorization 等常见敏感头加入名单和匹配模式，同时对 URL query 中的 key、apikey、password、token 参数做脱敏。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：SecurityAudit |

### R-081 [P2] 分页 pageSize 无上限校验，spec maximum: 200 未被服务端强制

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:703-713` |
| Legado 参照 | — |
| 问题原因 | spec 为 PageSize 声明了 maximum: 200，但 oapi-codegen 不会校验这一约束。ListBooks 和 ListBookSources 只检查 page<1\|\|pageSize<1，没有检查上限。 |
| 问题后果 | 客户端传入超大 pageSize（如 1000000）就能一次拉取全表，服务端行为与 spec 约定不一致。 |
| 证据 | internal/api/handlers/source_handler.go:703-713 中只有 page<1\|\|pageSize<1 的下限校验，ListBookSources 和 ListBooks 都没有 pageSize 上限校验。 |
| 修复建议 | 在 ListBooks 和 ListBookSources 中统一加上 pageSize>200 的校验（拒绝或截断到 200），让服务端行为与 spec 一致。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-082 [P2] 多 next 并发分页被实现为顺序抓取

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P2 |
| 相关文件 | `internal/app/content/content.go:224-252`<br>`internal/app/book/toc.go:131-156` |
| Legado 参照 | `BookContent.kt:103-130`, `BookChapterList.kt:101-113` |
| 问题原因 | 书源返回多个 nextContentUrl 或 nextTocUrl 时，正文和目录的多分页逻辑都用普通 for 循环逐个抓取、解析每个 next URL。目录循环体内也不检查 ctx 取消。 |
| 问题后果 | 一次返回多个分页 URL 的书源，章节抓取和大目录刷新的耗时会随页数线性增加。 |
| 证据 | content.go:224-252 和 toc.go:131-156 都在 for 循环里串行 Fetch。Legado 对应位置 BookContent.kt:103-130 和 BookChapterList.kt:101-113 都用 flow.mapAsync(AppConfig.threadCount) 并发抓取。 |
| 修复建议 | 正文和目录的多分页都改为受控并发抓取（限定 worker 数量、响应 ctx 取消），抓完后按原顺序合并结果。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：BookTocAudit, ContentReplaceAudit |

### R-083 [P2] 多源发现完全串行执行，无并发上限

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P2 |
| 相关文件 | `internal/app/discovery/discovery.go:97-112` |
| Legado 参照 | — |
| 问题原因 | Discover 对 resolveSources 返回的每个源逐一串行调用 discoverSource，每次调用都要做一次网络抓取加解析，中间没有任何并发。多源搜索侧已经用 semaphore 做了并发，发现侧没有同样的实现。 |
| 问题后果 | 启用发现的书源一多，接口延迟会随源数线性增加，容易触发客户端或网关超时。 |
| 证据 | discovery.go:97-112 用循环串行调用 discoverSource；对照 multi_source.go:53-88，多源搜索用 semaphore 实现了并发。 |
| 修复建议 | 参照 multi_source.go 的 semaphore 做法，让 Discover 在有并发上限的前提下并发执行各源的 discoverSource。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：SearchDiscoveryAudit |

### R-084 [P2] 未启用 WAL 且 busy_timeout 为连接级 PRAGMA，仅 Migrate 时单连接生效

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P2 |
| 相关文件 | `internal/bootstrap/bootstrap.go:56-62` |
| Legado 参照 | — |
| 问题原因 | 全仓没有设置 journal_mode/WAL。busy_timeout=5000 只在 Migrate() 里用 db.Exec 设置了一次，而它是连接级 PRAGMA，只对当时池里那一条连接生效，连接池之后新建的连接都拿不到这个设置。 |
| 问题后果 | 现在只能靠 SetMaxOpenConns(1) 来避开锁冲突，结果数据库访问全部串行，没有并发能力。如果以后放开连接数，新连接既没有 busy_timeout 也没有 WAL，容易出现锁冲突。 |
| 证据 | 全仓 grep 搜不到 journal_mode/WAL。internal/bootstrap/bootstrap.go:56-62 只在 Migrate() 中执行一次 busy_timeout=5000，同时用 SetMaxOpenConns(1) 来避免锁冲突。 |
| 修复建议 | 把 journal_mode=WAL 和 busy_timeout 写进 DSN（modernc 支持 _pragma 参数），或者用连接 hook 设置，让每条新连接都生效。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：PersistenceArchAudit |

### R-085 [P2] 每次调用重复编译正则与解析选择器，无缓存

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/rule/regex/regex.go:94-108`<br>`internal/infra/rule/regex/regex.go:30-38` |
| Legado 参照 | `AnalyzeRule.kt:475-480` |
| 问题原因 | compilePattern/compileAttrRegex 每次 Element/Elements/Replace 调用都会重新编译正则，css 每一步 findSelection→parseSelector 也会重新解析选择器，全程没有缓存。analyzer.evalStep 对每个元素都新建 regex.Adapter 并重新编译 pattern（regexp.Compile 失败后再用 regexp2.Compile），single_source.go:64 的 regexp.MatchString 每次搜索也会重新编译 bookUrlPattern。 |
| 问题后果 | 搜索 100 条结果、每条 7 个字段时，相同的 pattern 字符串要重复编译几百次；目录页有几千章时 CPU 开销会明显放大，高并发抓取时浪费 CPU。 |
| 证据 | regex.go:94-108 和 regex.go:30-38 每次调用都重新编译，analyzer.go:826 对每个元素都调用 regex.Replace，所以会重复编译。Legado 在 AnalyzeRule.kt:475-480 用 stringRuleCache/splitSourceRuleCacheString 做了缓存。 |
| 修复建议 | 为正则 pattern（包括 bookUrlPattern 和 @attr~=regex）和 CSS 选择器增加带锁的编译/解析结果缓存，按 pattern 字符串复用。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit, SelectorAudit |

### R-086 [P2] cache.put 与日志缓冲无内存上限，可被脚本撑爆内存

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/script/runtime.go:2153-2172`<br>`internal/infra/script/runtime.go:2158-2164`<br>`internal/infra/script/runtime.go:267-2143`<br>`internal/infra/script/runtime.go:2149-2172` |
| Legado 参照 | `CacheManager.kt`, `CacheManager.kt:57-61` |
| 问题原因 | 全局单实例 NamespacedCache（bootstrap.go:81 创建）用内存 map 按书源+key 写入，只提供 Put/Get，没有 TTL、条目上限、淘汰或删除。log 绑定会无限追加到 r.logs，也没有截断逻辑。 |
| 问题后果 | 恶意或低质书源可以通过 cache.put/java.put 写入任意 key 和大体积 value，长驻服务的内存会只增不减，最终可能 OOM。日志无界增长目前触发不到，因为 AllowLog 全仓都没有设为 true，Logs() 也没有生产调用方。 |
| 证据 | 复核实测：执行 5 万次 cache.put 后，50000 条全部留在内存中（runtime.go:2149-2172）；单次 Eval 内执行 20 万次 log 后，Logs() 仍有 200000 条。runtime.go:454 的 java.log 已有 AllowLog 判断，Legado CacheManager 采用磁盘存储并按 saveTime 过期（CacheManager.kt:57-61）。 |
| 修复建议 | 给 NamespacedCache 加上条目数上限、单个值大小上限、TTL 和淘汰机制，并把 r.logs 改成有上限的环形缓冲。 |
| 验证结论 | 复核 confirmed；来源切片：CrossCuttingQualityAudit, ScriptRuntimeAudit |

### R-087 [P2] [跨域] 每次脚本 Eval 新建 goja VM 并重复执行书源 jsLib

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/script/runtime.go:98-252` |
| Legado 参照 | — |
| 问题原因 | EvalWithBindings 每次调用都会执行 goja.New() 新建 VM 并调用 installSafeGlobals；书源带 jsLib 时，loadSourceJSLib 还会在每次 Eval 时把 src.JSLib 完整重跑一遍。 |
| 问题后果 | 搜索列表有 100 个元素、每个元素又有多个 JS 字段时，jsLib 会被重复执行数百次。使用公共 jsLib 的主流书源在搜索和解析时会明显变慢。 |
| 证据 | internal/infra/script/runtime.go:98-252 中，EvalWithBindings 每次都调用 goja.New() 和 installSafeGlobals，每个规则字段的 JS 片段执行时都会经 loadSourceJSLib 重新执行 src.JSLib。 |
| 修复建议 | 按书源缓存或复用已加载 jsLib 的 VM（例如用 VM 池，或对 jsLib 预编译），避免每次 Eval 都重建 VM 并重跑 jsLib。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-088 [P2] cache_entries.expires_at 从不写入也不检查，缓存无 TTL 无限增长

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/sqlite/repositories/image_cache_repository.go:34-38`<br>`internal/infra/sqlite/repositories/image_cache_repository.go:34-40` |
| Legado 参照 | — |
| 问题原因 | cache_entries 表虽建了 expires_at 列，但 SaveContent/SaveImage 写入时都不设置它，GetContent/GetImage 读取时也不检查；全仓也没有针对 cache_entries 的过期清理。image 命名空间的 owner_id 是图片 URL 本身，所以删除书源或书籍时的级联清理永远删不到这些记录。 |
| 问题后果 | 图片 BLOB 以 sha256(url) 为 key 不断累积，经图片代理浏览得越多，SQLite 磁盘占用就越大，而且永远不会回收。缓存内容也不会过期，会一直沿用旧数据。 |
| 证据 | image_cache_repository.go:34-40 中，SaveImage 用 ON CONFLICT UPSERT 写入 cache_entries(namespace='image', owner_id=imageURL)，不带过期时间。现有的级联删除只在 book_repository.go:102 和 book_source_repository.go:105 按 owner_id 执行，覆盖不到以 URL 为 owner_id 的图片记录。 |
| 修复建议 | 写入缓存时设置 expires_at TTL，读取时过滤掉已过期的条目，并增加定期清理过期 cache_entries 的任务。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit, PersistenceArchAudit |

### R-089 [P2] serve 测试迁移后落在无路由死代码路径，newRuntimeServer 无 HTTP 集成测试

| 字段 | 内容 |
|---|---|
| 维度 | 测试 |
| 严重度 | P2 |
| 相关文件 | `cmd/reader/serve_test.go:14-47` |
| Legado 参照 | — |
| 问题原因 | cmd/server/main_test.go 被原样复制成 cmd/reader/serve_test.go，测试对象 newServer/newServerWithToken 只会创建一个没有注册路由的空 http.ServeMux，除测试外也没有调用方。生产路径 runServe → newRuntimeServer 用的是 chi 路由和全部业务 handler，却没有对应的测试。 |
| 问题后果 | 现有测试覆盖不到生产 HTTP 服务器，health 路由或业务路由的 token 鉴权出了回归，测试也拦不住。断言实际是空转的，造成"已覆盖"的假象。 |
| 证据 | TestNewServerSetsBaselineHTTPServerOptions 请求 /api/v1/health 实际返回 404，但测试只断言 Trace 中间件的 header 不为空，所以照样通过。用 grep 确认过，newServer/newServerWithToken 只在 serve_test.go 中被引用。 |
| 修复建议 | 删除死代码 newServer/newServerWithToken，再用 httptest 给 newRuntimeServer 补集成测试，至少断言 health 返回 200、业务路由在 token 鉴权下返回 401。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：TestQualityAudit |

### R-090 [P2] check 任务轮询测试依赖固定 sleep 预算，高负载下易 flaky

| 字段 | 内容 |
|---|---|
| 维度 | 测试 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler_test.go:855-862` |
| Legado 参照 | — |
| 问题原因 | TestCheckTaskHandlersCreateGetCancelAndEvents 轮询异步 check 任务时只用了固定 20 次 × 10ms 的等待，总共约 200ms。runner_test.go 和 e2e 测试也是用 sleep 轮询，只有 e2e 测试设了 3s deadline，情况稍好。 |
| 问题后果 | CI 负载高或出现 GC 停顿时，runner goroutine 可能 200ms 内还没跑完，got.Status 仍是 running，Finished=0 的断言就会失败。结果是测试时过时不过，CI 不稳定。 |
| 证据 | 固定次数的 sleep 轮询出现在 internal/api/handlers/source_handler_test.go:855-862，internal/worker/check/runner_test.go:153-155 也有；test/e2e/mock_online_source_test.go:294-304 同样用 sleep 轮询，但带 3s deadline。 |
| 修复建议 | 把这些轮询统一换成一个 deadline 不少于 5s 的 waitForCondition helper，或者注入一个同步完成的 fake runner。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：TestQualityAudit |

### R-091 [P2] internal/bootstrap 无任何测试（基线 5 个无测试包之一）

| 字段 | 内容 |
|---|---|
| 维度 | 测试 |
| 严重度 | P2 |
| 相关文件 | `internal/bootstrap/bootstrap.go:53-132`<br>`internal/bootstrap/bootstrap.go:47-116` |
| Legado 参照 | — |
| 问题原因 | serve 和所有 CLI 子命令都只通过 bootstrap.Open 装配依赖，而 internal/bootstrap 是本次未提交变更新增的包，没有任何 *_test.go。serve_test 和 cli_test 只测 CLI 输出与 HTTP 行为，不检查 Runtime 字段是否装配正确。 |
| 问题后果 | 装配顺序和选项传递（WithTimeout、WithCookieManager、WithProcessor 等）一旦回归，没有测试会拦住；Sources/Books/Content/Check 等服务注入混用也不会被发现。这类注入错误要到运行时或真实 E2E 才会暴露。 |
| 证据 | internal/bootstrap（132 行，映射 16 个服务装配字段）下没有 _test.go，Tasks T-C01 备注和基线门禁都确认该包无测试。它是基线中 5 个无测试包之一。 |
| 修复建议 | 补一个最小装配测试：用临时 SQLite DSN 调 bootstrap.Open，断言 Runtime 各字段非 nil 且没有混用，cleanup 后连接已关闭；同时覆盖生产模式缺 READER_TOKEN 时 serve 拒绝启动。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：CliBootstrapAudit, DocBaselineAudit |

### R-092 [P2] verify.sh 与 CI 均未运行 -race 与 -count=1

| 字段 | 内容 |
|---|---|
| 维度 | 测试 |
| 严重度 | P2 |
| 相关文件 | `scripts/verify.sh:1-4` |
| Legado 参照 | — |
| 问题原因 | make verify（scripts/verify.sh）和 CI 的 Test 步骤都只跑裸的 `go test ./...` 和 `go vet ./...`，既没有加 `-race`，也没有用 `-count=1` 跳过测试缓存。 |
| 问题后果 | worker/check runner、debug runner、multi-source search、check handler 异步任务这些并发代码里的数据竞争，在 CI 上永远查不出来。测试步骤还可能命中 Go 测试缓存，导致回归漏检。 |
| 证据 | scripts/verify.sh:4 和 ci.yml 的 Test 步骤都是裸的 `go test ./...`。本次审计运行 `go test -race -count=1 ./internal/app/search/... ./internal/worker/check/... ./internal/infra/fetcher/... ./internal/infra/script/... ./internal/app/debug/...`，结果全部 ok，说明加上 -race 的开销可以接受。 |
| 修复建议 | 把 verify.sh 和 CI 的测试命令都改成 `go test -race -count=1 ./...`。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：TestQualityAudit |

### R-093 [P2] Release-0.0.1.md 将 2026-05-05 旧快照标注为“最新快照”，与现有 E2E 报告数字冲突

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P2 |
| 相关文件 | `docs/Release-0.0.1.md:43-52` |
| Legado 参照 | — |
| 问题原因 | 工作树已更新 test/reports 下的真实远程集合 E2E 报告，但 Release-0.0.1.md 没有同步改，仍把 2026-05-05 的旧数据写成“最新快照”。 |
| 问题后果 | 读者按发布文档引用的“最新通过率”是 51/100，与仓库报告的 39/100 对不上。做版本审计时无法判断该以哪份数字为准。 |
| 证据 | docs/Release-0.0.1.md:43-52 写的是“真实远程集合 gate 最新快照”，时间 2026-05-05T07:20:28Z，fullSuccessCount=51。test/reports/real_source_collection_e2e_report.{json,md} 中 testTime 为 2026-10-14T10:05:57Z，fullSuccessCount=39（search 46/54、info 43/3、toc 41/2、content 39/2）。 |
| 修复建议 | 把快照段更新为当前仓库报告的 testTime 和统计数字；或者明确标注这是历史快照，并指向 test/reports 下的最新报告文件。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DocBaselineAudit |

### R-094 [P2] check/debug 创建与取消的状态码与 spec 声明不一致（201/200 vs 202）

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:108-116`<br>`internal/api/handlers/source_handler.go:123-248` |
| Legado 参照 | — |
| 问题原因 | openapi.yaml 中书源检测与调试会话的创建、取消接口都只声明了 202 作为成功码，但 handler 实际返回 201（创建）和 200（取消），实现与契约不一致。 |
| 问题后果 | 严格按 spec 生成的客户端会把 201/200 当作未声明的响应处理，导致检测/调试任务的创建或取消被误判。不阻断书源主流程，宽松客户端感知不到。 |
| 证据 | 复核确认：spec 在 openapi.yaml:971/1024（check 创建/取消）和 :862/917（debug 创建/取消）只声明了 202；handler 在 source_handler.go:123、:222 写 http.StatusCreated，在 :149、:248 写 http.StatusOK。 |
| 修复建议 | 统一这四个接口的成功状态码：可以把 handler 改成返回 202，也可以修改 openapi.yaml，改为声明实际返回的 201/200。 |
| 验证结论 | 复核 downgrade（→P2）；来源切片：ApiContractAudit |

### R-095 [P2] POST /check/book-sources 对不存在的 sourceId 返回 spec 未声明的 404

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:879-885` |
| Legado 参照 | — |
| 问题原因 | writeCheckError 会把 source.ErrNotFound 映射成 404，错误码用的是 task_not_found。但 spec 中 POST /check/book-sources 只声明了 202/400/401/500，没有 404。 |
| 问题后果 | sourceId 不存在时，客户端会收到 spec 没有声明的 404，按 spec 编写的客户端无法正确处理。错误码 task_not_found 的语义也不对，实际不存在的是 sourceId 而不是 task，会误导排查。 |
| 证据 | internal/api/handlers/source_handler.go:879-885 中，writeCheckError 把 source.ErrNotFound 映射为 404 task_not_found；而 spec 中 POST /check/book-sources 只有 202/400/401/500 四种响应。 |
| 修复建议 | sourceId 不存在时改为返回 spec 已声明的状态码（如 400），并换用能表明 sourceId 不存在的错误码；如果确实要保留 404，就同步在 spec 中补充声明。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-096 [P2] GET /openapi.yaml 返回 JSON 且 Content-Type: application/json，spec 声明 application/yaml

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/system_handler.go:15-26` |
| Legado 参照 | — |
| 问题原因 | GetOpenAPI 调用 spec.MarshalJSON() 输出 JSON，并把 Content-Type 设为 application/json。而 spec 中 /openapi.yaml 的 200 响应 content 声明为 application/yaml。 |
| 问题后果 | 照文档 curl /openapi.yaml 拿到的是 JSON 而不是 YAML，与 spec 声明和"下载 YAML 文档"的语义不符，按 YAML 处理的客户端会出错。 |
| 证据 | internal/api/handlers/system_handler.go:15-26 中，GetOpenAPI 用 spec.MarshalJSON() 生成响应，并设置 Content-Type: application/json；spec 中该路径的 operationId 为 getOpenAPI，响应 content 为 application/yaml。 |
| 修复建议 | 将 GetOpenAPI 改为输出 YAML 并设置 Content-Type: application/yaml；如果要保留 JSON，就改用另一个路径（如 /openapi.json），并同步修改 spec 声明。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-097 [P2] handler 层承载业务逻辑：ID 生成、会话状态赋值、并发上限硬编码

| 字段 | 内容 |
|---|---|
| 维度 | 架构分层 |
| 严重度 | P2 |
| 相关文件 | `internal/api/handlers/source_handler.go:99-113` |
| Legado 参照 | — |
| 问题原因 | SourceHandler 除了做 DTO 适配，还自己生成任务和会话 ID（"check-"/"debug-" 加时间戳），把调试会话的 Status 预先设为 Running，并在 handler 里写死并发上限 4。这些 ID、状态和并发策略本该放在 app 层。 |
| 问题后果 | 这违反了 AGENTS.md 中“handler 只做 DTO 适配，不写业务规则”的约定。ID 生成、状态和并发策略散落在 handler 层，其他入口复用不了，也没法统一调整。 |
| 证据 | internal/api/handlers/source_handler.go:99-113：check 任务 ID 由 "check-"+time.Now()… 生成；debugSessionFromCreateRequest 生成 "debug-"+timestamp 并预置 Status 为 Running；调用 check.Start(r.Context(), checkTask, body.SourceIds, ..., 4) 时写死并发常量 4。 |
| 修复建议 | 把任务/会话 ID 生成、初始状态赋值和并发上限都移到 app 层服务里，handler 只负责请求和响应 DTO 的转换。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-098 [P2] [跨域] domain 层 import net/http，违反硬性架构约束

| 字段 | 内容 |
|---|---|
| 维度 | 架构分层 |
| 严重度 | P2 |
| 相关文件 | `internal/domain/fetch/fetch.go:5-30`<br>`internal/domain/fetch/fetch.go:1-30` |
| Legado 参照 | — |
| 问题原因 | internal/domain/fetch/fetch.go 导入了 net/http，Response.Headers 字段直接使用 http.Header 类型。这违反了 Design.md 第 4 节「internal/domain 不允许导入 database/sql、net/http、chi、goja」的硬性约束。 |
| 问题后果 | domain 层与 HTTP 传输细节耦合，违背了「业务层不依赖 HTTP 框架」的设计约定。以后替换 fetcher 实现或更换 HTTP 库时，domain 类型也得跟着改。 |
| 证据 | fetch.go:6 有 import "net/http"，Response.Headers 的类型是 http.Header；执行 go list -deps ./internal/domain/fetch 可确认依赖中包含 net/http。其余 domain 包（book/source/replace/task/rule）没有发现 database/sql、chi、goja 导入。 |
| 修复建议 | 把 Response.Headers 改成 domain 自定义的 map[string][]string 类型，在 fetcher 适配层完成与 http.Header 的转换。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DocBaselineAudit, PersistenceArchAudit |

### R-099 [P2] 迁移未版本化：schema_migrations 恒为 1，ensureColumn 旁路版本机制且 schema 执行无事务

| 字段 | 内容 |
|---|---|
| 维度 | 架构分层 |
| 严重度 | P2 |
| 相关文件 | `internal/infra/sqlite/migrate.go:10-17` |
| Legado 参照 | — |
| 问题原因 | schema_migrations 始终写入 version=1，导出的 SchemaVersion() 没有任何调用方用它决定迁移路径。后续的 failure_class 列通过探测式 ensureColumn 添加，没有注册成版本化迁移。 |
| 问题后果 | initialSchema 里每条 CREATE 各自跑在独立的隐式事务中，中途失败时不会回滚，也不留状态记录，数据库可能停在半迁移状态，只能靠 IF NOT EXISTS 重跑来补齐。因为版本号不反映真实结构，以后的 schema 变更也无法可靠追踪和排序。 |
| 证据 | internal/infra/sqlite/migrate.go:10-17：schema_migrations 恒写 version=1，failure_class 通过 ensureColumn 探测后添加，initialSchema 的 CREATE 语句没有包在同一个事务里。 |
| 修复建议 | 给每一步迁移注册版本号，并在同一个事务里依次完成版本检查、执行 DDL、写入版本记录。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：PersistenceArchAudit |

### R-100 [P3] SourceWarning.traceId 字段从未填充

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P3 |
| 相关文件 | `internal/api/handlers/source_mapper.go:232-244` |
| Legado 参照 | — |
| 问题原因 | spec 中的 SourceWarning 定义了 traceId 属性，但 mapSourceWarnings/mapDiscoveryWarnings 构造 DTO 时没有设置 TraceId。 |
| 问题后果 | 搜索和发现接口返回的逐源警告始终不带 traceId，没法和对应请求的 trace 关联起来排查问题。 |
| 证据 | source_mapper.go:232-258 中的 mapSourceWarnings/mapDiscoveryWarnings 构造 SourceWarning DTO 时都没有给 TraceId 赋值。 |
| 修复建议 | 在 mapSourceWarnings/mapDiscoveryWarnings 构造 DTO 时，把当前请求的 traceId 写入 TraceId 字段。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-101 [P3] UpdateReplaceRule 对不存在的 ID 执行 upsert 而非返回 404

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P3 |
| 相关文件 | `internal/app/replace/manager.go:30-40` |
| Legado 参照 | — |
| 问题原因 | Update 把 path 参数赋给 rule.ID 后直接调用 Save，而 Save 是 upsert；ID 不存在时会新建一条记录并返回 200。 |
| 问题后果 | 对不存在的规则发 PUT 请求不会返回 404，而是悄悄新建一条规则，客户端发现不了 ID 拼错，数据也会意外变多。 |
| 证据 | manager.go:30-40 的 Update 直接调用 Save；Save 的实现是 INSERT ON CONFLICT DO UPDATE（replace_rule_repository.go:23-26）。 |
| 修复建议 | Update 前先调用 FindByID，找不到时返回 ErrNotFound（对应 404）。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ContentReplaceAudit |

### R-102 [P3] 多源搜索固定 PageSize 10000 拉源，源数超过后静默截断

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P3 |
| 相关文件 | `internal/app/search/multi_source.go:105-115` |
| Legado 参照 | — |
| 问题原因 | 多源搜索和发现功能都只用固定的 PageSize:10000 一次性拉取书源，不分页，也不检查是否还有剩余书源。书源超过 10000 个时，多出的部分会被直接丢掉。 |
| 问题后果 | 书源超过 10000 个的部署里，超出的书源不会参与搜索和发现，结果覆盖不全，而且没有任何告警。目前仓库的书源数量远少于这个数，暂时不会触发。 |
| 证据 | 复核确认 multi_source.go:105-115 和 discovery.go:110 都用 source.BookSourceFilter{Page: 1, PageSize: 10000} 一次性拉取书源。仓储层的 LIMIT 本身没有上限，所以截断只发生在书源超过 10000 个的部署上。 |
| 修复建议 | 改为分页循环拉取直到全部取完，或者提供不分页的全量查询接口；如果确实要保留上限，截断时至少记一条告警日志。 |
| 验证结论 | 复核 confirmed；来源切片：SearchDiscoveryAudit |

### R-103 [P3] loginUrl/loginUi 无执行入口，依赖登录的书源流程不可用

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/script/runtime.go:1782-1795`<br>`internal/infra/script/runtime.go:1791`<br>`internal/app/source/script_integration.go:91-95`<br>`api/openapi.yaml:9-1039` |
| Legado 参照 | `BookSource.kt:60-63` |
| 问题原因 | loginCheckJs 已在抓取链路中使用，但 loginUrl/loginUi 只作为 JS 绑定暴露（runtime.go:1791）。登录头和登录态只存放在进程内的 sync.Map 中（runtime.go:1751-1773），既不持久化，也没有执行登录或注入登录态的端点。 |
| 问题后果 | 需要交互式登录的书源无法通过 API 完成登录，相关的搜索、详情、目录和正文流程都用不了。登录态只在进程内存中，没有持久化。 |
| 证据 | 复核确认 loginCheckJs 在 book/info.go:66、book/toc.go:63、content/content.go:148、search/single_source.go:58、discovery/discovery.go:149 中被使用。openapi.yaml 的全部路径（:9-:1039）中没有 login 或设置登录态的端点。 |
| 修复建议 | 提供执行 loginUrl/loginUi/loginCheckJs 以及设置、持久化登录态的 API 端点；如果暂不实现，就在文档中说明替代用法。 |
| 验证结论 | 复核 confirmed；来源切片：SourceImportAudit |

### R-104 [P3] 书源关键字/分组过滤未转义 LIKE 通配符

| 字段 | 内容 |
|---|---|
| 维度 | 功能完整性 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/sqlite/repositories/book_source_repository.go:258-273`<br>`internal/infra/sqlite/repositories/book_source_repository.go:240-253` |
| Legado 参照 | — |
| 问题原因 | buildBookSourceWhere 把用户输入的 keyword 直接拼成 '%'+kw+'%'，group 也直接用在四个 LIKE 条件里。拼接前没有转义 % 和 _，也没有 ESCAPE 子句，所以这两个字符会被当作通配符处理。 |
| 问题后果 | 关键字里有 % 或分组名里有下划线时会误匹配，过滤结果中会出现不该出现的书源。 |
| 证据 | book_source_repository.go:244-247 中，keyword 和 group 的 LIKE 参数直接拼接用户输入，没有转义和 ESCAPE 子句（已确认）。 |
| 修复建议 | 在拼接 LIKE 参数前转义 %、_ 和转义符本身，并在 SQL 中加上 ESCAPE 子句。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：PersistenceArchAudit, SourceImportAudit |

### R-105 [P3] writeNotConfigured 对未装配服务返回 500 internal_error，掩盖配置错误语义

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P3 |
| 相关文件 | `internal/api/handlers/source_handler.go:888-893` |
| Legado 参照 | — |
| 问题原因 | importer、check、debugSvc、images 等依赖为 nil 时，各 handler 统一调用 writeNotConfigured，返回 500 internal_error，消息为 'X service is not configured'。这种情况没有使用 501 这类能表达未实现/未配置的语义状态码。 |
| 问题后果 | 服务装配不完整时，调用方拿到的 500 和真实内部错误分不开。排障时无法判断是配置遗漏还是代码 bug。 |
| 证据 | internal/api/handlers/source_handler.go:888-893 的 writeNotConfigured 对未装配的服务固定返回 500 internal_error。生成代码中的 Unimplemented 本可以返回 501。 |
| 修复建议 | 让 writeNotConfigured 改用 501 或其他专用状态码，并配上独立的错误码，例如 not_configured，以便和真实内部错误区分开。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-106 [P3] Authorization scheme 大小写敏感，不符合 RFC 6750 bearer 语义

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P3 |
| 相关文件 | `internal/api/middleware/auth.go:20-24` |
| Legado 参照 | — |
| 问题原因 | middleware.Auth 用 strings.HasPrefix(auth, "Bearer ") 精确匹配 scheme，没有按 RFC 7235/6750 做大小写不敏感处理。 |
| 问题后果 | 客户端发送 'bearer <token>' 等非标准大小写的合法请求头时，会被拒绝并返回 auth_required，与 spec 中声明的 http bearer 语义不一致。 |
| 证据 | internal/api/middleware/auth.go:20-24 使用 strings.HasPrefix(auth, "Bearer ") 判断；spec 的 securitySchemes 声明为 http bearer。 |
| 修复建议 | 解析 Authorization 头时按空格拆出 scheme，再用 strings.EqualFold 与 "Bearer" 做大小写不敏感比较。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-107 [P3] 单源 searchUrl 为空被跳过但 resolveSources 中单个无效 ID 中止整个请求

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P3 |
| 相关文件 | `internal/app/search/multi_source.go:94-102`<br>`internal/app/search/multi_source.go:94-98` |
| Legado 参照 | — |
| 问题原因 | resolveSources 按 SourceIDs 逐个调用 FindByID，只要有一个出错就直接返回错误，整个多源搜索随之中止。这与单源搜索失败时转为 Warning 的处理方式不一致。 |
| 问题后果 | 用户传入的 ID 列表里只要混有一个无效 sourceId，整个多源搜索就会返回 500，有效源的结果也拿不到。 |
| 证据 | 复核确认：在 multi_source.go:94-98 中，FindByID 一出错就整体返回错误；而第 66-88 行对单源失败的处理是转为 Warning，两者不一致。 |
| 修复建议 | 在 resolveSources 中，把单个 sourceId 查询失败改为跳过该源并记录 Warning，其余有效源照常搜索，与单源失败的处理保持一致。 |
| 验证结论 | 复核 confirmed；来源切片：SearchDiscoveryAudit |

### R-108 [P3] 导入不支持带 BOM 的 JSON

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P3 |
| 相关文件 | `internal/app/source/importer.go:76-92`<br>`internal/app/source/importer.go:77`<br>`internal/app/source/importer.go:83-92`<br>`internal/cli/import.go:73-79` |
| Legado 参照 | `ImportBookSourceViewModel.kt:107`, `StringExtensions.kt:56-59` |
| 问题原因 | Import() 不会去掉开头的 UTF-8 BOM。带 BOM 的数组或对象在 importer.go:77 调用 json.Unmarshal 时就会报错，:83-92 的 raw[0] switch 和其中的 default 分支实际执行不到。 |
| 问题后果 | 在 Windows 上导出的带 BOM 文件，无论走 CLI 文件、stdin 还是 remoteUrl 导入都会失败。报错信息也没有提到 BOM，用户很难找到原因。 |
| 证据 | 复核时用临时程序 /tmp/reader-verify/reviewer/bom.go 实测：带 BOM 的数组和对象在 importer.go:77 的 json.Unmarshal 处报 invalid character 起始错误。cli/import.go:73-79 读文件、stdin 和 remoteUrl（importer.go:111-114）都会把 BOM 原样传进去。 |
| 修复建议 | 在 Import() 解析 JSON 之前，先去掉开头的 UTF-8 BOM，再去除首尾空白。 |
| 验证结论 | 复核 confirmed；来源切片：SourceImportAudit |

### R-109 [P3] [跨域] CLI 全部子命令使用 context.Background() 且无信号处理，长下载不可中断

| 字段 | 内容 |
|---|---|
| 维度 | 可用性 |
| 严重度 | P3 |
| 相关文件 | `internal/cli/download.go:47` |
| Legado 参照 | — |
| 问题原因 | CLI 的 download 等子命令发起请求时都用 context.Background()，进程也没有监听 SIGINT/SIGTERM，因此运行中的请求无法被取消。 |
| 问题后果 | 全本下载要串行抓取数千章，用户只能用 kill -9 强行结束。已抓取的章节可以靠缓存续传，但正在抓的那一章状态可能不一致；SQLite 单写，不会损坏数据，只是体验差。 |
| 证据 | internal/cli/download.go:47 处所有请求都用 context.Background()，进程中没有 SIGINT/SIGTERM 信号处理。 |
| 修复建议 | 在 cli 的 context.go 里用 signal.NotifyContext 创建根 ctx，再传给各子命令，替换掉 context.Background()。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-110 [P3] URL 选项 headers 中非字符串值被静默丢弃

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/app/book/fetch_request.go:175-188` |
| Legado 参照 | `AnalyzeUrl.kt:268-270` |
| 问题原因 | mergeBookHeaders/mergeSearchHeaders/mergeContentHeaders 处理 map[string]any 分支时，只拷贝 value.(string) 类型的值，其他类型直接跳过。这与 method/retry 采用的类型宽化策略不一致。 |
| 问题后果 | 书源 URL 选项中的数字或布尔类型 header（如 {"X-Count":3}）会被静默丢弃，不会随请求发出，与 Legado 的行为不一致。 |
| 证据 | internal/app/book/fetch_request.go:175-188 和 internal/app/search/single_source.go:628-645 只接受 string 类型的值；Legado 的 AnalyzeUrl.kt:268-270 用 entry.value.toString() 把所有值统一转成字符串。 |
| 修复建议 | 非字符串的 header 值先用 fmt.Sprint 转成字符串再写入，与 Legado 保持一致。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：UrlFetchAudit |

### R-111 [P3] check Manager.Start 任一 source 不存在即整体失败，无部分跳过语义

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/app/check/manager.go:17-41` |
| Legado 参照 | — |
| 问题原因 | check Manager.Start 会对每个 source 逐个调用 FindByID，只要有一个不存在，整个检查就失败，不支持跳过不存在的 source。Legado 的做法是静默跳过不存在的 source，两边行为不一致。 |
| 问题后果 | 只要批量检查里混入一个不存在的 source ID，整批检查都无法启动，其余有效 source 也不会被检查。不过原始记录认为 Reader 这种显式失败的语义是合理的，所以只作为差异记录。 |
| 证据 | internal/app/check/manager.go:17-41 逐个调用 FindByID，任一 source 不存在就返回整体失败；Legado 在同一场景下会静默跳过不存在的 source。 |
| 修复建议 | 在文档中说明这一差异，并决定是保留显式失败，还是改为跳过不存在的 source、同时返回被跳过的 ID 列表。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DebugCheckAudit |

### R-112 [P3] nextChapterUrl 缺少回退到第 0 章的语义

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/app/content/service.go:31-34` |
| Legado 参照 | `BookContent.kt:52-56` |
| 问题原因 | AppService.GetChapterContent 在 index+1 章节不存在时直接把 nextChapterURL 置为空串，缺少 Legado 回退到第 0 章的逻辑。 |
| 问题后果 | 读到最后一章时，JS 规则里的 nextChapterUrl 绑定为空串，依赖它构造参数的正文接口规则会在末章失效。 |
| 证据 | internal/app/content/service.go:31-34 没有回退逻辑；Legado 的 BookContent.kt:52-56 写法是 getChapter(index+1)?.url ?: getChapter(0)?.url。 |
| 修复建议 | index+1 章节不存在时改用 FindByIndex(0) 的章节 URL 作为 nextChapterURL，与 Legado 保持一致。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ContentReplaceAudit |

### R-113 [P3] type 选项任意非空字符串都触发 hex 编码，与 Legado 二进制响应语义仅部分对齐

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/app/search/single_source.go:556-560`<br>`internal/app/search/single_source.go:564-567` |
| Legado 参照 | `AnalyzeUrl.kt:406-408`, `AnalyzeUrl.kt:405-406` |
| 问题原因 | applySearchURLOptions 的 type 分支只看 type 是否为非空字符串，是就置 EncodeBodyAsHex=true，不用其具体值。Legado 的判断是 type != null，所以 type 为空串或非字符串值时，两边的判断条件不一致。 |
| 问题后果 | 书源的 type 为空串或非字符串值时，Legado 会把响应按 hex 编码的二进制处理，Reader 则忽略该选项，响应解析结果不一致。属于边缘场景，只影响使用这类非常规 type 值的书源。 |
| 证据 | 复核已复现：type 为任意非空字符串（如 "x"）都会置 EncodeBodyAsHex=true（single_source.go:564-567），Legado 则是 type != null 就走 hex（AnalyzeUrl.kt:405-406）。Legado 同样不限定 type 取值，原记录所说"Reader 未限定取值与 Legado 不同"不成立，实际差异只剩空串和非字符串值这两种情况。 |
| 修复建议 | 把 type 分支的判断改成与 Legado 一致的"type 键存在且值非 null"，空串和非字符串值也启用 EncodeBodyAsHex。 |
| 验证结论 | 复核 confirmed；来源切片：SearchDiscoveryAudit |

### R-114 [P3] GET 请求携带 body 选项时仍发送请求体

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/fetcher/http_fetcher.go:161-171` |
| Legado 参照 | `AnalyzeUrl.kt:298-308,404-408` |
| 问题原因 | Reader 的 HTTP 抓取器遇到 GET 请求且带 body 选项时，仍会构造带请求体的请求。Legado 的 GET 分支只发送 urlNoQuery+encodedQuery，会忽略 body 选项。 |
| 问题后果 | 部分服务器或网关收到带 body 的 GET 请求时会返回 400，或表现不一致，导致书源请求失败。这也与 Legado 的行为不一致。 |
| 证据 | internal/infra/fetcher/http_fetcher.go:161-171 在 GET+body 时仍构造带 body 的请求。对照 Legado 的 AnalyzeUrl.kt:298-308 和 404-408，GET 分支只拼接 URL 与查询串，不处理 body。 |
| 修复建议 | method 为 GET 时忽略 body 选项，或记录一条调试警告。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.85）；来源切片：UrlFetchAudit |

### R-115 [P3] GetString 结果缺少 HTML 实体反转义，regex/JS 提取文本原样输出实体编码

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/rule/analyzer.go:884-893`<br>`internal/infra/rule/analyzer.go:878-893` |
| Legado 参照 | `AnalyzeRule.kt:312-317` |
| 问题原因 | Legado 的 getString 会对最终结果统一执行 unescapeHtml4（仅在结果含 & 时）。Go 版 GetString 靠 toStringList 收尾，但只有 default 分支调用了 html.UnescapeString，case string 和 case []string 分支都原样返回。 |
| 问题后果 | 用 regex 模式或 JS 提取的文本如果含 &amp;、&nbsp; 等实体，GetString 输出会保留实体编码，正文或标题会显示成 "A&amp;B"，和 Legado 行为不一致。默认 CSS 模式不受影响。 |
| 证据 | 在 analyzer.go:878-893 的 toStringList 中，default 分支做了反转义，string/[]string 分支没有做；对照 Legado AnalyzeRule.kt:312-317。复核用临时测试确认：默认 CSS 模式下 "A&amp;B" 会正确返回 "A&B"，只有 regex/JS 等非默认模式会残留实体。 |
| 修复建议 | 在 GetString 返回前，如果合并后的最终字符串含 &，对它执行一次 html.UnescapeString；也可以在 toStringList 的 string/[]string 分支补上反转义，同时注明 GetStringList 不反转义的差异。 |
| 验证结论 | 复核 downgrade（→P3）；来源切片：RuleParserAudit |

### R-116 [P3] @get:{bookName}/@get:{title} 缺少特殊键处理，恒返回空串

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/rule/analyzer.go:920-927` |
| Legado 参照 | `AnalyzeRule.kt:754-764`, `AnalyzeRule.kt:754` |
| 问题原因 | lookupVariable（analyzer.go:920-927）只依次查找 input 字段、carrier 的 variable 载荷和 a.vars，没有像 Legado AnalyzeRule.kt:754-764 那样先特判 bookName 和 title。因此这两个键无法从书籍或章节中取值。 |
| 问题后果 | 书源里常见的 @get:{bookName}、@get:{title} 在 Reader 中取不到书名或章节标题，依赖这些变量的规则链会产出错误数据。复核发现 token 会原样残留，并非原始记录所说的返回空串。 |
| 证据 | 复核用临时测试执行 GetString("class.t@text##(.*)##pre-@get:{bookName}")，返回 "pre-@get:{bookName}"，即 token 原样残留。analyzer.go:920-927 和 decodeVariableEntry（983-997）中都没有 bookName/title 特判。 |
| 修复建议 | 在 lookupVariable 开头参照 evalTemplateExpr 对 source.bookSourceUrl 的特判：key 为 "bookName" 时从 a.ctx.Book 取 name，key 为 "title" 时从 a.ctx.Chapter 取 title，取到非空值才返回。 |
| 验证结论 | 复核 downgrade（→P3）；来源切片：RuleParserAudit |

### R-117 [P3] css 步长为 0 的区间 [start:end:0] 展开语义与 Legado 不一致

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/rule/css/css.go:440-462` |
| Legado 参照 | — |
| 问题原因 | expandSelectorIndexes 处理 stepX=0 时，因为 0 既不满足 >0，也不满足 >=length，step 保持为 1。Legado 的处理是：-stepX<len 成立，所以 step=len。 |
| 问题后果 | 书源写了 [start:end:0] 这种区间时，本实现会选出整段连续元素，Legado 只取区间的第一个元素，两边解析结果不一样。这是边缘场景，主流书源很少用到。 |
| 证据 | 问题位于 internal/infra/rule/css/css.go:440-462。[0:2:0] 在本实现中展开为连续序列，在 Legado 中只取首位，已复现。 |
| 修复建议 | 在 expandSelectorIndexes 中按 Legado 的逻辑处理 stepX=0：-stepX<len 成立时令 step=len，让区间只取首位。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：SelectorAudit |

### R-118 [P3] java.get 单参启发式：形似 URL 的缓存键被误当抓取请求

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/script/runtime.go:408-424` |
| Legado 参照 | `BaseSource.kt:232-236`, `JsExtensions.kt:378`, `BaseSource.kt:232` |
| 问题原因 | java.get 单参数调用会用 looksLikeURL 启发式判断，值以 "/" 开头或含 "://" 时就当作 URL 去抓取。Legado 的 get(key) 只读缓存，get(urlStr, headers) 要传两个参数才会抓取。 |
| 问题后果 | 书源用形似 URL 的字符串作缓存键时会意外发起 GET 请求，拿到的是响应对象而不是缓存值，行为和 Legado 不一致。这种情况属于边缘场景。 |
| 证据 | 复核复现：java.get('/cached/bookkey') 单参调用实际发起了 GET，stub fetcher 记录到 1 次请求并返回 code=200 的响应对象；含 :// 的键同样会抓取，纯文本键不会。get 的分派逻辑在 runtime.go:408-424，looksLikeURL 在 1600-1604；Legado 对应代码为 BaseSource.kt:232（只读 CacheManager）和 JsExtensions.kt:378（两参才抓取）。 |
| 修复建议 | 把单参数 java.get 改为只读变量/缓存，只有双参数调用或显式调用 ajax/connect 时才走 URL 抓取语义。 |
| 验证结论 | 复核 confirmed；来源切片：ScriptRuntimeAudit |

### R-119 [P3] check 任务级 status 把部分 item 失败提升为任务 failed

| 字段 | 内容 |
|---|---|
| 维度 | Legado对齐 |
| 严重度 | P3 |
| 相关文件 | `internal/worker/check/runner.go:111-118` |
| Legado 参照 | — |
| 问题原因 | check runner 只要有一个书源失败，就把整个任务标成 failed。Legado 没有任务级 failed 的概念，只记录每个源各自的结果。 |
| 问题后果 | 一部分源失败，整个校验任务就显示为失败，和 Legado 按源独立呈现结果的做法不一致，用户可能会误以为整批都失败了。这一点也许是有意为之，但文档里没有说明。 |
| 证据 | internal/worker/check/runner.go:111-118 中，单个 item 失败会被提升为任务级 failed 状态。 |
| 修复建议 | 先确定这是不是有意设计：如果是，就在文档中写明任务级状态怎么判定；如果不是，就改成按源独立出结果，或者给部分失败单独设一个状态。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DebugCheckAudit |

### R-120 [P3] decodeJSON 不校验 Content-Type，任何 Content-Type 的 body 都按 JSON 解析

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/api/handlers/source_handler.go:795-806` |
| Legado 参照 | — |
| 问题原因 | decodeJSON 没有检查请求头的 Content-Type 是否为 application/json，所以 text/plain 等其他类型的 body 也会被当作 JSON 解析。 |
| 问题后果 | 这种处理方式和 OpenAPI requestBody 里声明的 content 类型不一致。客户端漏设 Content-Type 时，拿到的是 decode 错误，看不出缺的是 Content-Type。 |
| 证据 | internal/api/handlers/source_handler.go:795-806 中的 decodeJSON 没有校验 r.Header 的 Content-Type，直接解析 body。 |
| 修复建议 | 在 decodeJSON 里先校验 Content-Type 是否为 application/json，不符合时返回明确的错误提示。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-121 [P3] [跨域] looksLikeFormBody/applyRetryOption 在 app/book 与 app/content 逐字拷贝

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/app/content/content.go:372-403` |
| Legado 参照 | — |
| 问题原因 | app/book 和 app/content 各写了一份 looksLikeFormBody（6 行）和 applyRetryOption（20 行 switch），两份代码逐行相同，没有共用同一个实现。 |
| 问题后果 | 以后只改其中一份的话，两边的表单请求体判断和重试选项处理就会对不上，书籍和内容两条请求链路的行为会出现差别。 |
| 证据 | internal/app/content/content.go:372-403 里的 looksLikeFormBody 和 applyRetryOption，与 app/book 中同名的实现逐行一致。 |
| 修复建议 | 把这两个函数挪到共享包（例如 internal/infra/rule/url），只保留一份实现，app/book 和 app/content 都改为调用它。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-122 [P3] [跨域] image_proxy 用 strings.Contains(err.Error(),"not found") 做错误分类

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/app/content/image_proxy.go:130-134` |
| Legado 参照 | — |
| 问题原因 | decodeImage 判断 bookRepo.FindByID 的错误是不是 NotFound 时，比对的是错误消息字符串，没有用 sentinel error。仓库其他地方都用 sentinel error。 |
| 问题后果 | 错误消息措辞一旦变化（比如本地化或被包裹后），NotFound 就会被误判成其他错误并直接返回错误，导致行为漂移。 |
| 证据 | internal/app/content/image_proxy.go:130-134 用 `strings.Contains(strings.ToLower(err.Error()), "not found")` 做错误分类，没有用 errors.Is。 |
| 修复建议 | 改用 errors.Is(err, book.ErrNotFound) 判断，并让 repository 用 %w 包裹错误。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-123 [P3] nextRuleID 基于 UnixNano，并发创建可能主键碰撞

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/app/replace/id.go:5-7` |
| Legado 参照 | — |
| 问题原因 | nextRuleID 用 time.Now().UnixNano() 生成主键，两个并发 Create 落在同一纳秒就会拿到相同 ID。Save 以该值为主键执行 upsert（ON CONFLICT(id)），而且没有自增主键兜底。 |
| 问题后果 | 并发创建时两条替换规则可能被静默合并成一条，造成数据丢失。这种情况概率极低，但不是零。 |
| 证据 | 复核确认：id.go:5-7 确实使用 time.Now().UnixNano()，replace_rule_repository.go:25-26 的 Save 用该 ID 做 ON CONFLICT(id) upsert。 |
| 修复建议 | 把规则 ID 改为 SQLite 自增 rowid 生成，或在 ID 中加入随机分量，避免并发创建时主键碰撞。 |
| 验证结论 | 复核 confirmed；来源切片：ContentReplaceAudit |

### R-124 [P3] 多源搜索并发 goroutine 无 panic recover

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/app/search/multi_source.go:66-88` |
| Legado 参照 | — |
| 问题原因 | Search 为每个源启动的 goroutine（multi_source.go:66-88）调用 SingleSourceSearcher.Search 时没有 panic recover，缺少单源失败隔离；Legado 用协程异常隔离把单源失败转成 onError。 |
| 问题后果 | 如果单个源的规则引擎或解析路径发生 panic，可能导致整个搜索请求返回 500 甚至进程崩溃，其他正常源的结果也会一起丢失。不过复核没有找到可实际触发的 panic 点，所以这是防御性风险。 |
| 证据 | 复核确认 multi_source.go:66-88 的 goroutine 没有 recover，但 script.Runtime 内部已经用 runtime.go:115-119 的 recover 覆盖了 goja 执行。reverseSearchBooks、mergeSearchBooks 等处的索引都是安全的，目前没有找到可实际触发 panic 的路径。 |
| 修复建议 | 在每个源的 goroutine 开头加 defer recover，把 panic 转成该源的错误结果，这样其他源的结果仍能正常返回。 |
| 验证结论 | 复核 downgrade（→P3）；来源切片：SearchDiscoveryAudit |

### R-125 [P3] [跨域] scriptAdapter 在 search/content/discovery/book 四处近似重复实现

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/app/search/single_source.go:650-671` |
| Legado 参照 | — |
| 问题原因 | search、content、discovery、book 四个包各自定义了 struct{runtime,baseURL,source,...}+Eval 形式的 scriptAdapter。四份实现的核心逻辑相同（nil 检查，再调用 EvalWithBindings 绑定 result/src/baseUrl/source/__source/book），只在 chapter/nextChapterUrl 等绑定上略有不同。 |
| 问题后果 | 新增或修改绑定时要同时改四处，容易出现不一致，导致不同流程中脚本能拿到的变量不同。 |
| 证据 | internal/app/search/single_source.go:650-671 是其中一份实现。另外三份分别在 content、discovery、book 包中，绑定集略有差异，核心逻辑重复了四次。 |
| 修复建议 | 提供一个可以传入可选绑定的共享 adapter，用它替换四个包各自的实现。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-126 [P3] [跨域] 硬编码外部域名作为 RealE2E 默认值落在非测试代码 config.go

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/config/config.go:82-83` |
| Legado 参照 | — |
| 问题原因 | RealE2ESourceCollection 的默认值直接写死了外部域名 https://shuyuan-api.yiove.com/...，而且写在生产配置代码 config.go 里，不在测试文件里。这和“严禁域名硬编码”的约束有冲突，不过这个值只在真实 E2E 场景使用，默认是关闭的。 |
| 问题后果 | 外部域名失效后，默认配置会指向一个死链接。 |
| 证据 | internal/infra/config/config.go:82-83 把 RealE2ESourceCollection 的默认值硬编码成了 https://shuyuan-api.yiove.com/...。 |
| 修复建议 | 默认值改为留空；当 RealE2EEnabled=true 而 URL 为空时直接报错。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-127 [P3] [跨域] logging.RedactHeaders 导出函数无生产调用方（死代码）

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/logging/redact.go:24-36` |
| Legado 参照 | — |
| 问题原因 | 导出函数 logging.RedactHeaders 只在测试中被调用。fetcher 和 handler 都不记录请求头，所以这个脱敏入口在运行期没有任何执行路径。 |
| 问题后果 | 这段死代码会让后续开发者误以为请求头日志已经做过脱敏。实际上运行期只走 RedactString。 |
| 证据 | internal/infra/logging/redact.go:24-36 定义了 RedactHeaders，但只有 redact_test.go 调用它；运行期的脱敏只经过 RedactString。 |
| 修复建议 | 删除 RedactHeaders；如果要保留到以后 fetcher 接入请求头日志时使用，需注明它目前还没有生产调用方。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-128 [P3] [跨域] opencc 繁简转换双实现：script/runtime.go 与 replace/processor.go

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/script/runtime.go:2011-2052` |
| Legado 参照 | — |
| 问题原因 | opencc 繁简转换写了两份：runtime.go 用包级 sync.Once 全局缓存 converter，processor.go 则用实例字段懒加载，两边的 t2s/s2t 分支和错误处理几乎一模一样。 |
| 问题后果 | 转换失败时两处都不报错，直接返回原文，用户察觉不到转换没生效。processor.go 的懒加载方式就是 P1 竞态的出处，重复实现也让修复和维护要做两遍。 |
| 证据 | internal/infra/script/runtime.go:2011-2052 用包级 sync.Once 缓存 converter；replace/processor.go 用实例字段懒加载，两者的 t2s/s2t 分支和吞掉错误的逻辑基本相同。 |
| 修复建议 | 抽一个基于 sync.Once 的公共繁简转换 helper，让 runtime.go 和 processor.go 都调用它，同时修掉 processor.go 的 P1 竞态。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-129 [P3] [跨域] SchemaVersion/InitialSchemaVersion、checkHostWithResolver、buildSearchURL 为仅测试引用的死代码

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/sqlite/migrate.go:246-258` |
| Legado 参照 | — |
| 问题原因 | SchemaVersion、InitialSchemaVersion、checkHostWithResolver、buildSearchURL 这四个函数在生产代码中都没有调用方，只有测试在用。实际的迁移流程是首次建表加 ensureColumn，并没有版本化逻辑。 |
| 问题后果 | SchemaVersion 让人以为存在一套版本化迁移机制，实际上没有，会误导维护者。死代码也会增加维护负担。 |
| 证据 | 逐个 grep 确认这四个函数在生产代码中没有调用。buildSearchURL 只被测试调用，而且内部使用的是 context.Background。 |
| 修复建议 | 删除 SchemaVersion、InitialSchemaVersion 和 buildSearchURL，checkHostWithResolver 改为在测试内部构造。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：CrossCuttingQualityAudit |

### R-130 [P3] cookie expires_at 解析失败被静默忽略，视为永不过期

| 字段 | 内容 |
|---|---|
| 维度 | 代码质量 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/sqlite/repositories/cookie_repository.go:49-53` |
| Legado 参照 | — |
| 问题原因 | ListByDomain 解析 expires_at 失败时直接跳过赋值，没有报错，cookie.ExpiresAt 保持为 nil。 |
| 问题后果 | 时间戳损坏的 cookie 会被当作“永不过期”，在请求里一直被带上。 |
| 证据 | internal/infra/sqlite/repositories/cookie_repository.go:49-53 中，expires_at 解析失败时既没有赋值，也没有返回错误，ExpiresAt 停留在 nil。 |
| 修复建议 | expires_at 解析失败时，跳过这条 cookie 或者返回错误，不要静默保留为永不过期。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：PersistenceArchAudit |

### R-131 [P3] isBlockedIP 对 IPv4-mapped IPv6 与非常规写法覆盖已足够，仅 NAT64 段未封禁

| 字段 | 内容 |
|---|---|
| 维度 | 安全 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/fetcher/http_fetcher.go:342-374` |
| Legado 参照 | — |
| 问题原因 | isBlockedIP 已经能拦住 0.0.0.0、十进制/八进制字面量 host、IPv4-mapped IPv6，以及内网、环回、link-local、组播和保留段，唯一没有显式封禁的是 64:ff9b::/96 这个 NAT64 映射段。 |
| 问题后果 | 发往 NAT64 映射段的请求不会被拦截，不过这个段对应的公网地址本来就能访问，所以实际影响较低。 |
| 证据 | internal/infra/fetcher/http_fetcher.go:342-374 里，0.0.0.0 由 IsUnspecified 和 ip4[0]==0 拦截；字面量 host 经 net.LookupIP/ParseIP 规范化后会命中判断分支；IPv4-mapped IPv6 由 To4 覆盖。审计没有发现绕过路径，只是 64:ff9b::/96 没有列入封禁。 |
| 修复建议 | 在 isBlockedIP 里显式封禁 64:ff9b::/96 这个 NAT64 映射段，补齐覆盖范围。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：SecurityAudit |

### R-132 [P3] __source 原始 Go 指针暴露给脚本，可反射遍历内部字段

| 字段 | 内容 |
|---|---|
| 维度 | 安全 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/script/runtime.go:127-224` |
| Legado 参照 | — |
| 问题原因 | EvalWithBindings 把 bindings 原样 vm.Set 进脚本环境，isForbiddenBinding 只拦截 8 个名字。prepareBindingValue 只对 selection/book/chapter 做白名单转换，__source 对应的 *BookSource 指针没有经过转换就暴露给了 JS。 |
| 问题后果 | 书源脚本能通过反射遍历 BookSource 的内部字段（含 Header、JSLib、LoginURL），这违背了受限沙箱和白名单绑定的设计原则。 |
| 证据 | internal/infra/script/runtime.go:127-135 中 EvalWithBindings 直接对 bindings 调用 vm.Set；prepareBindingValue 的白名单只覆盖 selection/book/chapter，不包括 __source。 |
| 修复建议 | 把 __source 加进 prepareBindingValue 白名单，只转换出需要的字段，或者干脆不让 JS 看到这个绑定。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ScriptRuntimeAudit |

### R-133 [P3] Policy.Timeout 为 0 且上层无 deadline 时脚本无超时保护

| 字段 | 内容 |
|---|---|
| 维度 | 安全 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/script/runtime.go:2098-2103` |
| Legado 参照 | — |
| 问题原因 | installInterrupt 在 Policy.Timeout=0 时只调用 WithCancel(ctx)，没有加超时上限。如果上层 ctx 也没有 deadline，脚本执行就完全不受时间限制。 |
| 问题后果 | 脚本库被直接复用、且上层 ctx 没有 deadline 时，死循环脚本会一直占着资源。生产路径默认有 10s 超时，不受这个问题影响。 |
| 证据 | runtime.go:2098-2103 在 Timeout=0 时只调用 WithCancel(ctx)。生产路径的 cfg.ScriptTimeout 默认 10s（config.go:77、bootstrap.go:90），所以有保护。 |
| 修复建议 | 在 NewRuntime 里，当 Timeout<=0 时给它设一个默认上限（比如 30s）。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ScriptRuntimeAudit |

### R-134 [P3] 每次 Eval 启动 2 个超时中断 goroutine，高并发下无谓开销

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/script/runtime.go:2097-2113` |
| Legado 参照 | — |
| 问题原因 | EvalWithBindings 和 loadSourceJSLib 各自调用一次 installInterrupt，每次都会起一个 select goroutine，所以一次 Eval 要启动 2 个超时中断 goroutine。 |
| 问题后果 | 几十个书源并发时，每个请求会产生上百个瞬时 goroutine，带来不必要的调度开销；有 defer close(done) 保证退出，泄漏风险低。 |
| 证据 | internal/infra/script/runtime.go:2097-2113 中，EvalWithBindings 与 loadSourceJSLib 都各自 installInterrupt 一次，各启动一个 select goroutine。 |
| 修复建议 | 让 EvalWithBindings 和 loadSourceJSLib 共用同一个 cancelInterrupt，一次 Eval 只启动一个中断 goroutine。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ScriptRuntimeAudit |

### R-135 [P3] debug_events 缺 session_id 索引，ListEvents 全表扫描

| 字段 | 内容 |
|---|---|
| 维度 | 性能 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/sqlite/migrate.go:193-207` |
| Legado 参照 | — |
| 问题原因 | debug_events 表没有建 session_id 索引，SQLite 也不会给外键自动建索引。 |
| 问题后果 | ListEvents 每次查询都要全表扫描，SSE 或轮询频繁拉取事件时性能会变差。 |
| 证据 | internal/infra/sqlite/migrate.go:193-207 中的 debug_events 建表语句没有 session_id 索引，而 ListEvents 按 session_id + event_id>? 条件查询。 |
| 修复建议 | 在迁移中加上 CREATE INDEX idx_debug_events_session ON debug_events(session_id, event_id)。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：PersistenceArchAudit |

### R-136 [P3] getVariableFromStruct 对非 JSON variable 字段静默失败（兜底分支无测试）

| 字段 | 内容 |
|---|---|
| 维度 | 测试 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/script/runtime.go:1254-1281`<br>`internal/infra/script/runtime.go:1232-1265` |
| Legado 参照 | `BaseSource.kt:217` |
| 问题原因 | getVariableFromStruct（runtime.go:1232-1256）和 compatGetVariable（1258-1265）在 variable 字段不是 JSON 时，反序列化会失败，失败后不报错，直接返回空值。这两个函数是自己加的兼容分支，Legado 只有 BaseSource.kt:217 的 getVariable()，它返回原始字符串，没有 getVariable(key)。现有测试只覆盖了 map 形态的成功路径，这条兜底分支没有测试。 |
| 问题后果 | 书源的 variable 是纯文本时，按 key 取变量会拿到空值，但不会有任何报错，脚本行为可能和预期不一样，排查起来也难。 |
| 证据 | 复核复现结果：Variable 为纯文本时，两个函数都静默返回空；JSON 形态时正常。所有 *_test.go 中都没有这两个函数的直接单测。 |
| 修复建议 | 为非 JSON variable 分支补单元测试，必要时兼容非 JSON 的 variable 字符串，或者至少记录这次解析失败。 |
| 验证结论 | 复核 confirmed；来源切片：ScriptRuntimeAudit |

### R-137 [P3] AGENTS.md 仓库结构仍声明已删除的 cmd/server/ 为服务装配目录

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `AGENTS.md:112-113`<br>`docs/Tasks.md:35-43`<br>`internal/bootstrap/bootstrap.go:52`<br>`docs/CLI.md:38` |
| Legado 参照 | — |
| 问题原因 | cmd/server/ 目录已经删掉（变更尚未提交），职责迁到了 cmd/reader/ 和 internal/bootstrap/，但 AGENTS.md:112-113 的仓库结构里仍把 cmd/server/ 写成服务装配、配置加载、HTTP server 启动的目录。docs/Tasks.md:35,36,43（T-A01/A02/A09）的相关文件列也没改，还写着 cmd/server/main.go。 |
| 问题后果 | 按 Tasks.md 关联代码用 IDE 或 rg 跳转时，会落到不存在的路径。以"当前文件列表为准"的自动化审计也会因此产生噪音。AGENTS.md 和 docs/Design.md:888 的说法互相矛盾。 |
| 证据 | AGENTS.md:112-113 写的是 `- cmd/server/`"服务装配、配置加载、HTTP server 启动"；docs/Tasks.md:35,36,43 写的是 cmd/server/main.go。internal/bootstrap/bootstrap.go:52 的注释"按 cmd/server 原装配顺序"和 docs/CLI.md:38 的"与原 cmd/server 等价"也还引用旧路径，不过这两处都是历史对照，可以接受。 |
| 修复建议 | 把 AGENTS.md 结构清单改成 cmd/reader/ 和 internal/bootstrap/，Tasks.md 关联代码列改指 internal/bootstrap/bootstrap.go（或注明已迁移）；bootstrap 注释可以顺手改成"按原 serve 装配顺序"。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：CliBootstrapAudit, DocBaselineAudit, TestQualityAudit |

### R-138 [P3] serve 日志从 stdout 改为 stderr，为未记录的行为变化

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `cmd/reader/serve.go:48` |
| Legado 参照 | — |
| 问题原因 | serve 命令把结构化 JSON 日志从旧版的 stdout 改到了 stderr，但 docs/CLI.md 没有说明这项变化。 |
| 问题后果 | 现有脚本如果用 `reader serve 1>app.log` 收集日志，得到的会是空文件，日志等于丢了。 |
| 证据 | 旧的 cmd/server/main.go 用的是 slog.NewJSONHandler(os.Stdout)，新的 cmd/reader/serve.go:48 改成了 stderr。docs/CLI.md 没有写明 serve 的日志现在输出到 stderr。 |
| 修复建议 | 在 docs/CLI.md 里写明 serve 的 JSON 日志改为输出到 stderr，并提示迁移方式；如果要保持兼容，就把日志改回 stdout。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：CliBootstrapAudit |

### R-139 [P3] docs/API.md 与 spec 漂移：大量已实现端点未在文档覆盖

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `docs/API.md:1-149` |
| Legado 参照 | — |
| 问题原因 | docs/API.md 自称以 api/openapi.yaml 为唯一真相源，但正文只示例了少数端点，没有随已实现的接口同步更新。 |
| 问题后果 | 新接入方只看文档的话，不知道系统的全部能力，会漏掉发现页、书源管理、替换规则管理等已有接口。 |
| 证据 | docs/API.md:1-149 只覆盖导入、搜索、books 创建/进度、替换测试、debug、check、图片代理。/discovery、/book-sources 的 CRUD 及 enabled/explore-enabled/reorder、/replace-rules CRUD、GET /books、DELETE 等已实现端点在文档中都没有提到。 |
| 修复建议 | 按 api/openapi.yaml 补全 docs/API.md 的端点清单和示例，或明确写出正文只是部分示例、完整列表以 spec 为准。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-140 [P3] docs/CLI.md 称 source show 输出六行，实际为七行

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `docs/CLI.md:82` |
| Legado 参照 | — |
| 问题原因 | docs/CLI.md:82 列出了 name/url/group/enabled/enabledExplore/searchUrl/lastUpdateTime 共 7 个字段，却写成"六行"，和实现对不上。 |
| 问题后果 | 如果脚本或自动化工具按文档说的行数解析 source show 的输出，结果会出错。 |
| 证据 | docs/CLI.md:82 写的是"六行"；source.go 的 runSourceShow（:131-138）实际输出 7 个字段，一共 7 行。 |
| 修复建议 | 把 docs/CLI.md:82 的"六行"改成"七行"，和 runSourceShow 的实际输出保持一致。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：CliBootstrapAudit |

### R-141 [P3] Design 5.2 API 路径清单缺少已实现并声明的 /images/proxy 端点

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `docs/Design.md:130-157` |
| Legado 参照 | — |
| 问题原因 | Design 5.2 的 /api/v1 路径清单没有收录 /images/proxy，但 openapi 已定义该端点并生成了 handler，因此 Design 与 openapi（唯一真相源）的路径集合出现了漂移。 |
| 问题后果 | 把 Design 5.2 当作接口全景来读的人会漏掉图片代理端点，而 README 和 Release 文档都已将它列为已实现能力，各文档说法互相矛盾。 |
| 证据 | docs/Design.md:130-157 的路径清单中没有 /images/proxy；api/openapi.yaml:630 定义了 GET /images/proxy，internal/api/generated/server.gen.go 也已生成对应的 handler。 |
| 修复建议 | 在 Design 5.2 的路径清单中补上一行 GET /api/v1/images/proxy。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DocBaselineAudit |

### R-142 [P3] 迁移实现为单文件 initialSchema，与 Design“使用迁移工具（如 goose）”表述不符

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `docs/Design.md:310-315` |
| Legado 参照 | — |
| 问题原因 | Design 7.1 写的是"使用迁移工具管理 SQL，例如 goose"。实际实现是 migrate.go 里内嵌 initialSchema 常量，加上 schema_migrations(version=1) 的手写幂等迁移，没有引入 goose。 |
| 问题后果 | 读者照 Design 去找 goose 迁移文件会找不到。以后改 schema 时也没有版本化路径可循，因为现有实现只支持单版本。 |
| 证据 | docs/Design.md:310-315 写的是 goose 迁移工具，而实际是 migrate.go 内嵌 initialSchema 并用 schema_migrations 记录 version=1。internal/infra/sqlite/migrations/ 目录是空的，项目也没有引入 goose。 |
| 修复建议 | 把 Design 7.1 改为"单文件幂等迁移 + schema_migrations 版本表，后续变更递增 version 并追加迁移块"，或者真正引入 goose。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DocBaselineAudit |

### R-143 [P3] LegadoAlignmentReport 最后一段“修复前/后”数字与现有快照不完全对应

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `docs/LegadoAlignmentReport.md:337-341`<br>`test/reports/real_source_collection_e2e_report.md:14-30`<br>`test/reports/real_source_collection_e2e_report.json:2062-2079` |
| Legado 参照 | — |
| 问题原因 | LegadoAlignmentReport 最后一段 triage 写的“network_timeout 仅 2、修复前 search 49/51、info 47/2、toc 44/3、content 39/5”，和仓库中唯一纳入版本管理的快照对不上，可能来自同一天另一次已被覆盖的 run。报告里也没注明这些数字取自哪个快照文件、哪个 testTime。 |
| 问题后果 | 如果把这一段当作“当前最新通过率”引用，各阶段计数会和快照文件差 2-3 条，报告作为审计快照的对应关系因此变弱。代码行为不受影响。 |
| 证据 | 复核确认：快照 real_source_collection_e2e_report.md（testTime 2026-10-14T10:05:57Z，JSON :2062-2079 数值相同）为 search 46/54、info 43/3、toc 41/2、content 39/2、network_timeout=3（md :19-30）。报告 :337-341 中只有修复后的 content 39/2 和 implementation_or_rule_failed 清零这两项与快照一致。 |
| 修复建议 | 在报告这一段标明对应的快照文件和 testTime，或者复核数字来源后改成与快照一致；仓库里找不到对应文件的数字，不要再用“本轮 gate”的说法。 |
| 验证结论 | 复核 confirmed；来源切片：DocBaselineAudit |

### R-144 [P3] ReaderHardcodingAndBugAudit 5.6/5.8 小节结论未回写，与当前代码状态矛盾

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `docs/ReaderHardcodingAndBugAudit.md:149-160` |
| Legado 参照 | — |
| 问题原因 | 审计文档 5.6（+/- 前缀）和 5.8（搜索详情 fallback）两节仍用现在时写“Reader 没有实现”。但 Tasks T-A05 已标记完成，代码也已实现，这两节的结论却没有回写。 |
| 问题后果 | 读者按该审计文档查找“未修 bug”时，会把已修复的项当成 open gap，导致重复审计。 |
| 证据 | 代码已经实现：single_source.go:63-80 的 bookUrlPattern 直跳、152-166 空列表时走 PreviewSearchBookFromInfo fallback，另见 reverseSearchBooks:152-153 和 discovery.go:242-244,305。同一文档的 5.1-5.5、5.7、5.9-5.12 都补了“已修复/已收口”结论，只有 5.6/5.8 没补。 |
| 修复建议 | 参照 5.1-5.5 的写法，给 5.6 和 5.8 补上“已修复”结论，并附上证据所在的文件和行号。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DocBaselineAudit |

### R-145 [P3] BookProgress spec 必填 name/author 但 handler 完全忽略

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `internal/api/handlers/book_mapper.go:116-131` |
| Legado 参照 | — |
| 问题原因 | spec 中 BookProgress 把 name、author 列为必填，但 progressFromDTO 只读取 durChapter* 四个字段。name/author 被静默丢弃，spec 的必填约束与服务端实际语义不一致。 |
| 问题后果 | 客户端按 spec 必须提交 name/author，但服务端既不校验也不使用这两个字段。调用方可能误以为这些字段会生效或参与匹配。 |
| 证据 | internal/api/handlers/book_mapper.go:116-131 的 progressFromDTO 只取 durChapter* 四个字段，没有处理 name、author。 |
| 修复建议 | 二选一：要么从 spec 的 BookProgress 中去掉 name/author 的必填约束，要么在 handler 中校验并使用这两个字段。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：ApiContractAudit |

### R-146 [P3] --update 导入策略合法取值未在文档说明，非法值静默降级为默认行为

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `internal/app/source/importer.go:208-216` |
| Legado 参照 | — |
| 问题原因 | 文档 docs/CLI.md:44 只写了 [--update <policy>]，没有列出合法取值。shouldSkip 只认 always 和 skipExisting，其他值（包括拼写错误）都不校验，直接按 lastUpdateTime 比较。 |
| 问题后果 | 用户不知道可以填哪些取值。输错策略（比如写成 Always）时没有任何报错，导入会悄悄按默认的更新时间比较来执行，结果可能和预期不一样。 |
| 证据 | docs/CLI.md:44 只有 [--update <policy>]，没有取值说明。importer.go:208-216 的 shouldSkip 只识别 always/skipExisting，其余值都会降级为 lastUpdateTime 比较。 |
| 修复建议 | 在 CLI.md 里列出 --update 的合法取值，并在解析参数时校验，遇到非法值直接报错退出。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.90）；来源切片：CliBootstrapAudit |

### R-147 [P3] 子命令 --help 输出为 Go flag 默认单横线格式，与文档双横线写法不一致

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `internal/cli/import.go:29-41` |
| Legado 参照 | — |
| 问题原因 | 子命令使用标准库 flag 默认帮助输出，参数以单横线风格展示；docs/CLI.md 则全部写成双横线。 |
| 问题后果 | 标准库 flag 下单横线和双横线等价，功能不受影响。但帮助输出和文档写法不一致，用户读起来容易困惑。 |
| 证据 | 运行 `reader import --help`，输出为 `Usage of import:` 和 `-from int` 这样的单横线风格（internal/cli/import.go:29-41）。docs/CLI.md 中写的是 `--from`。 |
| 修复建议 | 为子命令自定义 flag.Usage，按双横线格式输出参数说明，与 docs/CLI.md 保持一致。 |
| 验证结论 | 未单独复核（切片审阅置信度 0.95）；来源切片：CliBootstrapAudit |

### R-148 [P3] internal/infra/cache 空目录与 Design.md 目录结构不一致

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `internal/infra/cache/.gitkeep:104` |
| Legado 参照 | — |
| 问题原因 | Design.md:104 的目录结构里列出了 infra/cache/，但 internal/infra/cache 下只有 .gitkeep，没有实际代码。缓存实际实现在 internal/infra/script（NewNamespacedCache）和 sqlite/repositories 中。 |
| 问题后果 | 开发者按设计文档去找缓存实现时会找到空目录，增加理解和维护成本。 |
| 证据 | internal/infra/cache 目录下只有 .gitkeep，没有任何代码；Design.md:104 的目录结构中列有 infra/cache/。 |
| 修复建议 | 删除 internal/infra/cache 空目录并更新 Design.md，写明缓存实际所在位置；如果要保留目录，就在文档中标注为预留。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：PersistenceArchAudit |

### R-149 [P3] 真实 E2E 报告未包含 Design 17.6 要求的 traceId 字段

| 字段 | 内容 |
|---|---|
| 维度 | 文档一致性 |
| 严重度 | P3 |
| 相关文件 | `test/reports/real_source_collection_e2e_report.json:1-10` |
| Legado 参照 | — |
| 问题原因 | Design 17.6 规定报告必须包含"失败分类和 traceId"，但真实 E2E 报告的 stageResults 每条只有 failureClass 和 message 两个字段。check/debug 仓储本身有 trace_id 列，只是 E2E 报告的结构里没有这个字段。 |
| 问题后果 | 失败条目没法和日志或请求 trace 关联，回溯单条失败时只能靠 sourceId 加 message 人工对齐。这与 Design 的承诺不一致，但不影响功能。 |
| 证据 | 在 test/reports/real_source_collection_e2e_report.json 中 grep traceId，结果为零命中。check_repository.go:54 和 debug_repository.go:47 已有 trace_id 列。 |
| 修复建议 | 在 realE2EReport 的 stageResult/failureSummary 结构里补上 traceId 字段，值可以用 E2E 请求上下文生成的 trace，或者留空并注明原因。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DocBaselineAudit |

### R-150 [P3] internal/worker/debug/ 目录为空，仅 .gitkeep

| 字段 | 内容 |
|---|---|
| 维度 | 架构分层 |
| 严重度 | P3 |
| 相关文件 | `internal/worker/debug/.gitkeep:1` |
| Legado 参照 | — |
| 问题原因 | internal/worker/debug/ 目录下只有一个 .gitkeep，没有任何 Go 文件。调试 Runner 实际写在 app/debug/runner.go，所以这个目录用不上，只是多出来的占位。 |
| 问题后果 | 它和 worker/check 的结构不对称，读代码的人可能以为调试相关逻辑在 worker 层，被误导。这是纯粹多余的目录，没有功能上的作用。 |
| 证据 | internal/worker/debug/ 下只有 .gitkeep，没有 Go 文件；调试 Runner 在 app/debug/runner.go。 |
| 修复建议 | 删掉空目录 internal/worker/debug/，或者把调试 Runner 移到这里，让它和 worker/check 的结构对齐。 |
| 验证结论 | 未单独复核（切片审阅置信度 1.00）；来源切片：DebugCheckAudit |

## 3. 按模块索引

| 模块 | 问题编号 |
|---|---|
| `AGENTS.md` | R-137 |
| `api` | R-103 |
| `cmd/reader` | R-077, R-089, R-138 |
| `docs` | R-093, R-137, R-139, R-140, R-141, R-142, R-143, R-144 |
| `internal/api/handlers` | R-003, R-017, R-018, R-019, R-020, R-032, R-034, R-039, R-071, R-078, R-081, R-090, R-094, R-095, R-096, R-097, R-100, R-105, R-120, R-145 |
| `internal/api/middleware` | R-001, R-106 |
| `internal/app/book` | R-009, R-040, R-041, R-042, R-051, R-082, R-110 |
| `internal/app/check` | R-004, R-043, R-044, R-111 |
| `internal/app/content` | R-005, R-006, R-045, R-046, R-047, R-048, R-082, R-112, R-121, R-122 |
| `internal/app/debug` | R-003, R-006, R-021, R-022, R-033, R-072 |
| `internal/app/discovery` | R-049, R-083 |
| `internal/app/replace` | R-015, R-050, R-101, R-123 |
| `internal/app/search` | R-007, R-023, R-026, R-051, R-052, R-053, R-054, R-102, R-107, R-113, R-124, R-125 |
| `internal/app/source` | R-024, R-034, R-055, R-056, R-057, R-103, R-108, R-146 |
| `internal/bootstrap/bootstrap.go` | R-079, R-084, R-091, R-137 |
| `internal/cli/book.go` | R-035 |
| `internal/cli/download.go` | R-025, R-109 |
| `internal/cli/import.go` | R-108, R-147 |
| `internal/cli/root.go` | R-036, R-037 |
| `internal/cli/search.go` | R-038 |
| `internal/domain/fetch` | R-098 |
| `internal/domain/rule` | R-004 |
| `internal/domain/source` | R-058, R-059 |
| `internal/infra/cache` | R-148 |
| `internal/infra/config` | R-026, R-126 |
| `internal/infra/fetcher` | R-002, R-027, R-060, R-114, R-131 |
| `internal/infra/logging` | R-080, R-127 |
| `internal/infra/rule` | R-008, R-009, R-115, R-116 |
| `internal/infra/rule/css` | R-010, R-061, R-062, R-063, R-117 |
| `internal/infra/rule/jsonpath` | R-011, R-012 |
| `internal/infra/rule/regex` | R-028, R-064, R-085 |
| `internal/infra/script` | R-013, R-014, R-016, R-029, R-065, R-066, R-067, R-068, R-069, R-086, R-087, R-103, R-118, R-128, R-132, R-133, R-134, R-136 |
| `internal/infra/sqlite` | R-022, R-024, R-030, R-034, R-069, R-070, R-073, R-074, R-075, R-088, R-099, R-104, R-129, R-130, R-135 |
| `internal/worker/check` | R-004, R-031, R-076, R-119 |
| `internal/worker/debug` | R-150 |
| `scripts` | R-092 |
| `test/reports` | R-143, R-149 |

## 4. 旧审计文档 blocker 核对

来源：DocBaselineAudit 逐条核对 `docs/ReaderHardcodingAndBugAudit.md`、`docs/ReaderLegadoSourceChainDiffAudit.md`、`docs/LegadoAlignmentReport.md` 最后一轮 triage。“仍存在”且属于 Reader 缺陷的条目已并入第 2 节；属于一期声明边界或站点侧问题的仅在此记录。

| 原文档 | 条目编号/小节 | 条目摘要 | 已修复/仍存在 | 证据(文件:行) |
|---|---|---|---|---|
| ReaderHardcodingAndBugAudit.md | §5.1 (P0) | 共享 script.Runtime 的 SourceKey 写回共享 policy，跨书源缓存/变量串扰 | 已修复 | internal/infra/script/runtime.go:100（runCtx.sourceKey = sourceKeyForBindings 局部计算，不写回 r.policy）；runtime.go:277-280 缓存读写用 runCtx.sourceKey |
| ReaderHardcodingAndBugAudit.md | §5.2 (P0) | enabledCookieJar 被忽略，HTTPFetcher 对所有请求统一启用 CookieJar | 已修复 | internal/infra/fetcher/http_fetcher.go:184,244（仅 req.UseCookieJar && cookies != nil 才 Load/Save） |
| ReaderHardcodingAndBugAudit.md | §5.3 (P0) | 登录头 compat state 未接入主请求链（HeaderMap 只读 src.Header） | 已修复 | internal/app/source/script_integration.go:32-76（HeaderMap 解析并合并 compat loginHeader） |
| ReaderHardcodingAndBugAudit.md | §5.4 (P0) | loginCheckJs 接入分叉：搜索/发现/详情手写简化版，目录/正文完全不执行 | 已修复 | internal/app/content/content.go:148,205,239；internal/app/book/toc.go:63,119,148；internal/app/book/info.go:66 均统一走 ScriptIntegration.LoginCheck |
| ReaderHardcodingAndBugAudit.md | §5.5 (P0) | Cookie 域归并用“host 最后两段”，未用 eTLD+1 | 已修复 | internal/infra/fetcher/cookies.go:12（golang.org/x/net/publicsuffix）,135（EffectiveTLDPlusOne） |
| ReaderHardcodingAndBugAudit.md | §5.6 (P1) | 搜索/发现/目录的 +/- 前缀语义缺失，目录剥前缀但不 reverse | 已修复（代码），审计小节结论未回写 | internal/app/search/single_source.go:88,152-153,610-619；internal/app/book/toc.go:95,294-306；internal/app/discovery/discovery.go:242,305,402-404 |
| ReaderHardcodingAndBugAudit.md | §5.7 (P1) | Analyzer 变量域跨条目污染、目录 @put 误写 book | 已修复 | internal/app/book/toc.go:190-206（每章节 item 新建 Analyzer 挂 Chapter carrier 并 SetVariables(baseVars)）；single_source.go:100-101（item 级 baseVars 恢复） |
| ReaderHardcodingAndBugAudit.md | §5.8 (P1) | bookUrlPattern 直跳只返回最小 SearchBook；列表为空无详情 fallback | 已修复（代码），审计小节结论未回写 | internal/app/search/single_source.go:63-80（bookUrlPattern 命中 PreviewSearchBookFromInfo）、152-166（空列表详情 fallback） |
| ReaderHardcodingAndBugAudit.md | §5.9 (P1) | 正文高级字段（webJs/imageDecode/formatKeepImg/sourceRegex）缺失 | 已收口（一期边界） | internal/app/content/content.go:27（sourceRegex 显式 ErrUnsupportedContentBrowserFlow）；webJs/imageDecode 已接入（LegadoAlignmentReport §10.2 声明 + content.go 图片代理链） |
| ReaderHardcodingAndBugAudit.md | §5.10 (P1) | 文件型书源 downloadUrls 运行态/DTO 缺失 | 已修复 | internal/domain/book/book.go:31-33（DownloadURLs/InfoHTML/TocHTML）；internal/api/handlers/book_mapper.go:24,68-69 |
| ReaderHardcodingAndBugAudit.md | §5.11 (P2) | regex 超限后自动禁用规则未落库 | 已收口 | internal/app/replace/limited_regex.go:14-33（maxRegexInputBytes=2MiB、ErrRegexLimitExceeded）；Tasks.md T-A09 复核 processor 禁用落库链 |
| ReaderHardcodingAndBugAudit.md | §5.12 (P2) | 校验失败分类过度折叠为 website_failed | 已收口 | internal/app/diagnostic/failure.go:14-152（DNS/TLS/timeout/EOF/授权/空 JSON 等分类）；internal/infra/sqlite/repositories/check_repository.go:54（failure_class 持久化） |
| ReaderHardcodingAndBugAudit.md | §2.1 | 常量级硬编码清单（默认 UA Reader/0.0.1、maxRegexInputBytes 2MiB、重定向 5、WebView UA、Build.MODEL=Reader） | 仍存在（确认为产品/安全策略硬编码，非单站特判，与审计结论一致） | internal/infra/fetcher/http_fetcher.go:45（默认 UA）,重定向上限；internal/app/replace/limited_regex.go:14；internal/infra/script/runtime.go:52 |
| ReaderHardcodingAndBugAudit.md | §2.2/§3 | 兼容启发式（URL suffix 清洗、宽松 JSON、裸路径补 $、JavaImporter/Hutool 兼容）与“无按域名/sourceId 特判”结论 | 仍成立 | internal/infra/rule/url/url.go（宽松解析）；internal/app/source/url_option_support.go:11（白名单按 option key 而非域名）；rg 全仓生产代码无 if host=="..."/sourceName 分支 |
| ReaderLegadoSourceChainDiffAudit.md | §3.2-2 | Book 缺 downloadUrls/infoHtml/tocHtml 运行态字段，文件型书源与 HTML 复用被弱化 | 已修复 | internal/domain/book/book.go:31-33；internal/app/book/info.go:52,145,166（infoHtml/tocHtml 复用链） |
| ReaderLegadoSourceChainDiffAudit.md | §3.3 | 导入入口缺 content://文件 URI | 仍存在（确认为一期产品边界，Requirement R-082/审计均已声明非目标，非缺陷） | internal/app/source/importer.go 仅 payload/remoteUrl/sourceUrls |
| ReaderLegadoSourceChainDiffAudit.md | §4.2 | AnalyzeUrl 高级 option（retry/proxy/useWebView/webJs/sourceRegex/serverID/webViewDelayTime）未覆盖 | 已按一期边界收口：retry 已实现，webJs 已接入正文链，proxy/useWebView/serverID/webViewDelayTime/sourceRegex 显式 unsupported | internal/app/source/url_option_support.go:11-20（method/body/headers/type/charset/origin/retry 白名单，zeroLike 跳过）；single_source.go:207、internal/app/book/fetch_request.go:47、internal/app/content/content.go:303、internal/infra/script/runtime.go:1507,2360（unsupported url option） |
| ReaderLegadoSourceChainDiffAudit.md | §4.3 | enabledCookieJar 未按书源生效 | 已修复 | 同 Hardcoding §5.2：http_fetcher.go:184,244 |
| ReaderLegadoSourceChainDiffAudit.md | §4.4 | 登录头/登录信息状态未打通主链 | 已修复 | 同 §5.3：script_integration.go:32-76；discovery.go:139,149、info.go:66、toc.go:63 均消费 HeaderMap/LoginCheck |
| ReaderLegadoSourceChainDiffAudit.md | §4.5 | Cookie 域归并算法错误 | 已修复 | 同 §5.5：cookies.go:135 |
| ReaderLegadoSourceChainDiffAudit.md | §5.2 | JS 运行时为受限兼容层，非 Rhino 等价 | 仍存在（设计性差异，审计明示为非 bug 边界；Tasks T-A12 已文档化白名单） | internal/infra/script/runtime.go（受限绑定 + isForbiddenBinding） |
| ReaderLegadoSourceChainDiffAudit.md | §5.3 | 变量域与 Legado chapter→book→source 顺序不一致 | 已修复 | 同 Hardcoding §5.7：toc.go:190-206 |
| ReaderLegadoSourceChainDiffAudit.md | §5.4 | 共享 Runtime SourceKey 串扰 | 已修复 | 同 Hardcoding §5.1：runtime.go:100 |
| ReaderLegadoSourceChainDiffAudit.md | §6.2-1/2 | bookUrlPattern 直跳不全、空列表无详情 fallback | 已修复 | single_source.go:63-80,152-166 |
| ReaderLegadoSourceChainDiffAudit.md | §6.2-3 | bookList +/- 前缀 reverse 缺失 | 已修复 | single_source.go:88,152-153,610-619 |
| ReaderLegadoSourceChainDiffAudit.md | §6.2-4 | 搜索头部 JS 结果只接受严格 JSON | 已修复（T-A05 声明宽松解析） | internal/app/search/single_source.go:495,520,644（urlrule.ParseAttributeObject 宽松解析统一入口） |
| ReaderLegadoSourceChainDiffAudit.md | §7.2-1 | exploreKinds() 无缓存 | 已修复 | internal/app/discovery/discovery.go:56,167-168,200,215,360（sync.Map 分类缓存） |
| ReaderLegadoSourceChainDiffAudit.md | §7.2-2 | exploreScreen 只落库不消费 | 仍存在（确认为一期声明边界：Requirement R-042/T-A11“只保留字段”，非缺陷） | internal/api/handlers/source_mapper.go:37,86；discovery.go 无 exploreScreen 引用 |
| ReaderLegadoSourceChainDiffAudit.md | §7.2-3/4 | 发现链 +/- 前缀与空列表详情 fallback 缺失 | 已修复 | discovery.go:242,305,402-404（reverse）；T-A05 声明 + discovery 列表解析与搜索共用 parseBookList |
| ReaderLegadoSourceChainDiffAudit.md | §8.2 | coverDecodeJs 仅图片代理上下文、文件源 API 表达缺失 | 已修复/已收口 | internal/app/content/image_proxy.go（coverDecodeJs 接入 imageCacheKey/decodeScriptForSource）；book_mapper.go:24,68 |
| ReaderLegadoSourceChainDiffAudit.md | §9.2-1 | 目录 +/- 前缀无 reverse | 已修复 | toc.go:95,294-306,306-310（reverseChapters） |
| ReaderLegadoSourceChainDiffAudit.md | §9.2-3 | 未复刻章节词数补全、最新章节元数据刷新、reverseToc 用户配置 | 仍存在（确认为能力弱化声明，非单点 bug；Legado BookChapterList.kt 附加语义未迁移） | internal/app/book/toc.go 无 wordCount 补全/reverseToc 配置链 |
| ReaderLegadoSourceChainDiffAudit.md | §9.2-4 | 目录项变量跨章节污染 | 已修复 | 同 §5.7：toc.go:190-206 |
| ReaderLegadoSourceChainDiffAudit.md | §10.2 | 正文链 sourceRegex 未做 WebView 等价；webJs/nextChapterUrl/imageDecode/formatKeepImg 缺失 | 已收口：sourceRegex 显式不支持；webJs/nextChapterUrl/imageDecode/formatKeepImg 语义已接入 | internal/app/content/content.go:27；content.go:148-205（webJs/replaceRegex/nextContentUrl 链）；T-A08 复核 |
| ReaderLegadoSourceChainDiffAudit.md | §11.2 | reSegment/简繁转换缺失、regex 异常模型不等价 RegexTimeoutException | 前两者已修复，异常模型差异仍存在（审计声明为非一期 blocker） | internal/app/replace/resegment.go:12-15（确定性断段）；internal/infra/rule/regex/regex.go:116（regexp2.MatchTimeout=1s，非逐语义复刻） |
| ReaderLegadoSourceChainDiffAudit.md | §13 | 剩余差异清单（浏览器流 option、sourceRegex、regex 异常模型、文件型源） | 均已按一期边界显式收口，与当前代码一致 | url_option_support.go、content.go:27、regex.go:116、book.go:31-33 |
| LegadoAlignmentReport.md | §7 最后一轮（2026-10-14）- 通用实现缺口 | 卷章节 content 短路缺失（小米阅读/小米书城 invalid json: invalid character '<'） | 已修复（该轮 triage 声称 + 代码核实） | internal/app/content/content.go:104（c.IsVolume && strings.HasPrefix(c.URL, c.Title) 返回 chapter.Tag，不抓取不缓存）；test/e2e/real_source_collection_test.go:549 复核 |
| LegadoAlignmentReport.md | §7 最后一轮 - 站点/源侧 | m.zym888.com WAF 403 join TypeError、炫动小说 script_timeout、fq-book.netsite.cc 宕机 | 仍存在（确认为站点/源侧，非 Reader 缺陷；报告分类与其一致） | test/reports/real_source_collection_e2e_report.md failureClass=source_rule_script_failed（src_ae48dc2981afcd5dbeea4255）、script_timeout（炫动小说） |
| LegadoAlignmentReport.md | §7 最后一轮 - 失败分类器 | TLS handshake timeout、裸 EOF/unexpected EOF 漏配网络类 | 已修复（代码核实） | internal/app/diagnostic/failure.go:134-152（i/o timeout、client.timeout exceeded、tls handshake timeout 网络类）；test/e2e/real_source_collection_test.go:280-296 表驱动回归测试 |
| LegadoAlignmentReport.md | §7 最后一轮 - gate 数字 | 修复前 fullSuccessCount=39，search 49/51、info 47/2、toc 44/3、content 39/5；修复后 content 39/2 | 部分对应：快照 fullSuccess=39、content 39/2 一致；但快照 search 46/54、info 43/3、toc 41/2、network_timeout=3 与文中“49/51、47/2、44/3、仅 2”不对应（疑同日不同 run） | test/reports/real_source_collection_e2e_report.json（testTime=2026-10-14T10:05:57Z, fullSuccessCount=39, stageSummary, failureSummary.network_timeout=3） |

备注：docs/archive/ 下的归档文档（findings/progress/Tasks-2026-05-05）属历史记录不参与核对；Release-0.0.1.md 与 LegadoAlignmentReport §3 的“51/2026-05-05”数字与现有快照的冲突已在切片结果 R-001/R-002 单独报告，此处不重复。
