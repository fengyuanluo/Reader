# Reader 当前任务表

## 1. 说明

本文件取代旧版按 Phase 展开的 `Tasks.md`，只保留当前最需要跟进的任务清单。

编写原则：

- 只基于 live 代码与最新审计文档维护。
- 不再重复旧版的大段模板、Phase、commit 建议和通用工作流。
- 每条任务尽量直接对应一个明确问题域，而不是抽象“实现某模块”。

旧版任务文档已归档到：

- `docs/archive/Tasks-2026-05-05-pre-audit-table-rewrite.md`

当前主要问题来源：

- `docs/ReaderLegadoSourceChainDiffAudit.md`
- `docs/ReaderHardcodingAndBugAudit.md`

## 2. 状态约定

| 状态 | 含义 |
| --- | --- |
| `待办` | 问题已确认，当前还没有可靠实现闭环 |
| `部分完成` | 已有部分实现，但语义、覆盖面或一致性仍不足 |
| `待设计` | 问题已经明确，但需要先定服务端边界或产品决策 |
| `观察项` | 当前已知差异存在，但暂不建议直接进入开发主线 |

## 3. 当前任务

| ID | 任务 | 状态 | 优先级 | 备注 | 关联文档 | 关联代码 |
| --- | --- | --- | --- | --- | --- | --- |
| `T-A01` | 修复共享 `script.Runtime` 的 `SourceKey` 串扰 | `完成` | `P0` | 已改为每次执行按 bindings/source 计算局部 `sourceKey`，不再写回共享 `Runtime.policy`；新增共享 runtime 多 source 回归测试 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 5.4 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 5.1 节 | `cmd/server/main.go`<br>`internal/infra/script/runtime.go`<br>`internal/app/search/multi_source.go`<br>`internal/infra/script/runtime_test.go` |
| `T-A02` | 让 `enabledCookieJar` 真正按书源生效，并修正 Cookie 域归并算法 | `完成` | `P0` | 已为 `fetch.Request` 增加 `UseCookieJar` 并由搜索/发现/详情/目录/正文/脚本请求链按书源透传；`cookieDomain()` 已切到 eTLD+1 归并并补测试 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 4.3-4.5 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 5.2、5.5 节 | `internal/infra/fetcher/http_fetcher.go`<br>`internal/infra/fetcher/cookies.go`<br>`cmd/server/main.go`<br>`internal/domain/source/book_source.go` |
| `T-A03` | 打通登录状态闭环：统一接入 `loginHeader` / `loginInfo` / `loginCheckJs` | `完成` | `P0` | 已把 compat `loginHeader` 合回 `HeaderMap()`，搜索/发现/详情/目录/正文统一复用 `ScriptIntegration.LoginCheck()`；`loginInfo` 当前按 Legado 事实保持为登录表单/脚本状态存储，不额外强行并回主请求头 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 4.4、6.2、9.2、10.2 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 5.3、5.4 节 | `internal/app/source/script_integration.go`<br>`internal/app/search/single_source.go`<br>`internal/app/discovery/discovery.go`<br>`internal/app/book/info.go`<br>`internal/app/book/toc.go`<br>`internal/app/content/content.go`<br>`internal/infra/script/runtime.go` |
| `T-A04` | 补齐或显式收口 `AnalyzeUrl` 高级请求 option 兼容 | `完成` | `P1` | 搜索、发现、详情、目录、正文和 JS 网络 helper 已统一复用 `requestoption`，支持 `method/body/headers/type/charset/origin/retry/js/bodyJs`；`webView:true` 作带日志的 HTTP 降级，`webJs`、代理、DNS 与其他浏览器专属 option 明确拒绝；`concurrentRate` 已按书源固定窗口限速 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 4.2 节 | `internal/infra/requestoption/request.go`<br>`internal/infra/fetcher/rate_limiter.go`<br>`internal/app/search/single_source.go`<br>`internal/app/book/fetch_request.go`<br>`internal/app/content/content.go`<br>`internal/infra/script/runtime.go` |
| `T-A05` | 对齐列表链路语义：搜索、发现、目录的 `+/-` 前缀、详情 fallback、宽松 header JS 结果 | `完成` | `P1` | 已补齐搜索/发现/目录 `+/-` 前缀 reverse、搜索头部 JS 结果宽松 JSON 解析，以及搜索/发现在直达详情页或列表为空时的详情 fallback；对应回归测试已补齐 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 6.2、7.2、9.2 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 5.6、5.8 节 | `internal/app/search/single_source.go`<br>`internal/app/discovery/discovery.go`<br>`internal/app/book/toc.go`<br>`internal/app/source/script_integration.go` |
| `T-A06` | 重构 `Analyzer` 变量域生命周期，消除跨条目污染 | `完成` | `P0` | 已补齐搜索/发现/目录 item 级变量基线恢复、显式 chapter/book/source carriers、混合 `@get`/`{{...}}` 模板优先级，并修正目录阶段未挂 `chapter` carrier 导致 `@put` 误写 `book` 的剩余 bug；当前已有 analyzer/service 级回归测试覆盖 chapter -> book -> source 读取顺序与章节变量不污染书籍变量 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 5.3 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 5.7 节 | `internal/infra/rule/analyzer.go`<br>`internal/app/search/single_source.go`<br>`internal/app/discovery/discovery.go`<br>`internal/app/book/toc.go`<br>`internal/app/book/info.go`<br>`internal/app/content/content.go` |
| `T-A07` | 补齐详情链与文件型书源能力：`downloadUrls`、`infoHtml/tocHtml` 复用、`coverDecodeJs` | `完成` | `P1` | 已为 `Book/SearchBook` 与 SQLite 仓储补上运行态 `downloadUrls / infoHtml / tocHtml`，打通 HTML 复用链并接入 `coverDecodeJs`；文件型书源可提取 TXT 等地址，且只绝对化 URL、原样保留逗号后的请求 option；当前不扩展为文件下载器 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 3.2、8.2 节 | `internal/domain/book/book.go`<br>`internal/app/book/info.go`<br>`internal/infra/rule/url/url.go`<br>`internal/app/content/image_proxy.go`<br>`internal/api/generated/server.gen.go` |
| `T-A08` | 补齐正文高级字段与正文请求语义 | `完成` | `P1` | 正文链已覆盖 `content/title/nextContentUrl/replaceRegex`、`nextChapterUrl`、受限 `webJs`、`imageDecode` 和图片保留语义；`subContent/callBackJs` 已可导入并在请求时记录未执行诊断但不阻断主正文，`sourceRegex` 仍按浏览器流边界明确拒绝，`imageStyle/payAction` 保留不执行 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 10.2 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 5.9 节 | `internal/app/content/content.go`<br>`internal/app/book/fetch_request.go`<br>`internal/infra/script/runtime.go`<br>`internal/domain/rule/rules.go` |
| `T-A09` | 提升替换净化与 Legado 的行为一致性 | `完成` | `P2` | 已实现标题/正文替换、重复标题移除、正文缓存前处理、regex limit/timeout 后自动禁用规则并落库，并补齐服务端可配置的 `reSegment()` 与简繁转换链；当前保留的差异只剩 Android 侧随机断段与异常模型的非字节级差别，服务端改为确定性断段并已有稳定性回归测试，不再视为一期 blocker | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 11.2 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 5.11 节 | `internal/app/replace/processor.go`<br>`internal/app/replace/resegment.go`<br>`internal/infra/config/config.go`<br>`cmd/server/main.go` |
| `T-A10` | 细化校验与调试失败分类，避免过度折叠为 `website_failed` | `完成` | `P2` | 已为 check/debug 建立统一失败诊断器，保留标准 `failureCode` 的同时新增持久化/事件级 `failureClass`，并稳定区分 DNS/TLS/超时、认证失败、上游空 JSON/非 JSON、脚本语法错误等；最新真实 E2E 报告中的失败聚类已不再以 `website_failed` / `implementation_or_rule_failed` 为主，剩余主要是源级或网络级外部失败 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 12.2 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 5.12 节 | `internal/app/diagnostic/failure.go`<br>`internal/app/check/checker.go`<br>`internal/app/debug/flow.go`<br>`internal/infra/sqlite/repositories/check_repository.go`<br>`api/openapi.yaml` |
| `T-A11` | 明确并文档化服务端边界字段：`loginUrl/loginUi`、`exploreScreen`、`ruleReview` | `完成` | `P2` | 当前基线已明确：`loginUrl` 保留供脚本/登录状态使用；`loginUi`、`eventListener`、`customButton`、`exploreScreen`、`ruleReview` 及客户端字段 `imageStyle/payAction` 可导入持久化，但不执行对应 Android UI/事件行为 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 3.2、7.2、10.2 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 5 节相关条目 | `internal/domain/source/book_source.go`<br>`internal/api/handlers/source_mapper.go`<br>`internal/infra/sqlite/repositories/book_source_repository.go` |
| `T-A12` | 维护 JS 兼容白名单与非目标清单，避免 runtime 无边界膨胀 | `完成` | `P1` | Reader 继续采用受限 goja 子集；已按实际书源补 `timeFormat/toNumChapter/getStringList/getElement/setContent`、Cookie、摘要/base64、User-Agent、`head/ajaxTestAll/androidId` 等 helper，网络 helper 统一经过受控 Fetcher；浏览器 DOM/WebView 专属能力仍是非目标 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 5.2 节<br>`docs/ReaderHardcodingAndBugAudit.md` 第 2.2 节 | `internal/infra/script/runtime.go`<br>`internal/infra/script/runtime_test.go`<br>`internal/infra/requestoption/request.go` |
| `T-A13` | 补齐发现分类 `exploreKinds()` 缓存层 | `完成` | `P2` | 已为 JS 生成的发现分类增加 service 内缓存，并补回归测试确保同一源不会重复执行分类脚本 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 7.2 节 | `internal/app/discovery/discovery.go`<br>`internal/infra/script/runtime.go`<br>`internal/infra/script/runtime_test.go` |
| `T-A14` | 明确导入入口是否要覆盖本地 URI / `content://` 场景 | `完成` | `P2` | 当前需求基线已明确 Reader 服务端一期只覆盖 JSON payload、远程 URL 与 `sourceUrls` 聚合；`content://` / 文件 URI 属于产品边界差异，不纳入当前实现范围 | `docs/ReaderLegadoSourceChainDiffAudit.md` 第 3.3 节 | `internal/app/source/importer.go`<br>`internal/api/handlers/source_handler.go`<br>`internal/domain/source/book_source.go` |
| `T-A15` | 修复 Legado 书源校验假阴性与任务状态问题 | `完成` | `P0` | 已保留 warning 原文、优先 `checkKeyWord`、落实 180 秒默认超时；搜索/发现独立记录后续阶段，文件源只校验详情/`downloadUrls`；进度逐项单调持久化并修复 runner race；HTTP 202 与 SSE 状态已对齐 | `docs/LegadoSourceFailureAttributionReport.md` | `internal/app/check/checker.go`<br>`internal/app/diagnostic/failure.go`<br>`internal/worker/check/runner.go`<br>`internal/infra/sqlite/repositories/check_repository.go`<br>`internal/api/handlers/source_handler.go` |
| `T-A16` | 修复真实集合暴露的非浏览器规则/runtime 兼容缺口 | `完成` | `P1` | 已按真实错误补受限 `org.jsoup.Jsoup.parse`、`md5Encode16`、JSON `toArray()`、`aesEncodeToBase64String`，修正 JSON/CSS 错误回退、JSONPath 缺字段、`||` 分支与可选 `nextTocUrl`；复验相关错误均为 0，`initUrl` 因该样本立即进入 WebView 链路按边界排除 | `docs/LegadoSourceFailureAttributionReport.md` | `internal/infra/script/runtime.go`<br>`internal/infra/rule/analyzer.go`<br>`internal/infra/rule/jsonpath/jsonpath.go`<br>`internal/app/book/toc.go` |
| `T-A17` | 全书 UTF-8 TXT 导出与真实搜索到导出链路 | `完成` | `P1` | 新增 `GET /books/{bookId}/export.txt`；按目录顺序串行复用正文缓存，原子生成临时文件，章节失败不返回残缺文件；真实 438 章链路验收通过 | `docs/TxtExportE2EReport.md` | `internal/app/content/service.go`<br>`internal/api/handlers/source_handler.go`<br>`api/openapi.yaml` |
