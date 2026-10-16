# Reader 硬编码、单书源特判与缺陷审计

审计日期：2026-05-05

## 1. 审计目标

本文件回答两个问题：

1. 当前 Reader 生产代码里到底有没有“域名硬编码 / 单书源特判 / 站点白名单式兼容”？
2. 当前项目里有哪些确认为真实实现问题，而不是上游站点波动、书源规则失效或测试夹具现象？

结论先行：

- 生产代码里几乎没有按域名、站点名写死的分支。
- 当前真正的问题主要不是“站点硬编码太多”，而是“通用兼容层还不够完整，导致不得不靠测试夹具做定向验证”。
- 源级特征判断和关键词启发式主要集中在 `test/e2e/real_source_collection_test.go`，不是生产服务路径。

## 2. 生产代码里的硬编码清单

### 2.1 通用常量级硬编码

这些是“固定策略/固定默认值”，但不是单书源特判：

1. 默认抓取 UA：
   - `internal/infra/fetcher/http_fetcher.go`
   - 默认值是 `Reader/0.1.0`

2. 默认 WebView UA 兼容字符串：
   - `internal/infra/script/runtime.go`
   - `defaultWebViewUserAgent` 被 `java.getWebViewUA()` 返回

3. JS 兼容层里的 Android 设备信息：
   - `internal/infra/script/runtime.go`
   - `Packages.android.os.Build.MODEL = "Reader"`
   - `Packages.android.os.Build.MANUFACTURER = "Reader"`

4. 正则输入大小上限：
   - `internal/app/replace/limited_regex.go`
   - `maxRegexInputBytes = 2 * 1024 * 1024`

5. HTTPFetcher 默认重定向上限：
   - `internal/infra/fetcher/http_fetcher.go`
   - 默认 5 次

这些都属于产品/安全策略硬编码，不是单站点特判。

### 2.2 兼容性启发式硬编码

这些是“为了兼容 Legado 书源常见写法”做的通用启发式，不绑定具体域名：

1. 书源 URL suffix 清洗：
   - `internal/app/search/single_source.go`
   - `internal/app/source/script_integration.go`
   - `internal/infra/script/runtime.go`
   - 会去掉 `##`、`#`、全角 `＃`

2. 宽松 URL option / header JSON 解析：
   - `internal/infra/rule/url/url.go`
   - 支持单引号、未加引号 key、中文引号

3. JSONPath 裸路径自动补 `$`：
   - `internal/infra/rule/analyzer.go`

4. JavaImporter / Hutool / Base64 / Cipher 的受限兼容层：
   - `internal/infra/script/runtime.go`

这些属于“规则语言兼容补丁”，不是“某站点专用分支”。

## 3. 生产代码中的单书源特判结论

本轮对 `internal/` 与 `cmd/` 生产路径做了针对性检索，没有发现下面这种模式：

- `if host == "xxx.com"`
- `if strings.Contains(url, "xxx")`
- `switch sourceName`
- `if source.BookSourceURL == "..."`

也就是说：

- 当前生产实现里没有明显的“某域名走专门逻辑”的条件分支。
- 当前问题更像“兼容层没补齐”，而不是“补丁太多、规则太脏”。

## 4. 测试与审计代码中的 source-specific 逻辑

单书源/按类目启发式逻辑主要出现在 `test/e2e/real_source_collection_test.go`，例如：

- 根据书源名或 URL 推断搜索关键词：
  - 故事类 -> `民间故事`
  - 诗词类 -> `蝶恋花` / `李白`
  - 国学古典类 -> `论语` / `三国演义`
  - 轻小说类 -> `刀剑神域`
- 根据规则文本推断候选能力标签：
  - 是否有 `@js:` / `<js>`
  - 是否有 `JavaImporter`
  - 是否请求 WebView

这些逻辑的性质是：

- 仅用于真实 E2E 候选筛选与诊断。
- 不会进入生产 API 请求链。
- 它们是测试基建里的“启发式审计逻辑”，不是线上解析特判。

## 5. 当前最重要的真实 bug 清单

以下问题都来自当前 live 代码，不依赖旧文档推断。

### 5.1 共享 `script.Runtime` 的 `SourceKey` 污染

影响级别：P0

事实：

- `internal/bootstrap/bootstrap.go` 只创建一个共享 `scriptRuntime`（由 `cmd/reader serve` 与 CLI 子命令共用）。
- `internal/infra/script/runtime.go` 在首次执行时会把 `policy.SourceKey` 写成当前源。
- 写入后不会在下一次执行前重置。
- `cache.get/put`、`java.get/put` 都依赖这个字段。

结论：

- 这是此前确认过的真实 bug，但当前 live 代码已经修复。
- `Runtime` 现在按每次执行的 bindings/source 计算局部 `sourceKey`，不再把首个书源写回共享 `Runtime.policy`。
- 当前仍保留对应回归测试，因此这条不再属于未修问题。

### 5.2 `enabledCookieJar` 被忽略

影响级别：P0

事实：

- 字段在域模型、仓储、API DTO 中都存在。
- 但 `HTTPFetcher` 只要挂了 `CookieManager` 就对所有请求统一启用 `Load/Save`。
- 抓取链路没有根据当前书源判断是否应启用 CookieJar。

结论：

- 这是此前确认过的真实 bug，但当前 live 代码已经修复。
- `fetch.Request.UseCookieJar` 已在搜索、发现、详情、目录、正文和脚本请求链按书源透传，`HTTPFetcher` 不再对所有请求无条件启用 CookieJar。

### 5.3 登录头 compat state 没接入主请求链

影响级别：P0

事实：

- `internal/infra/script/runtime.go` 已有 `source.putLoginHeader()`、`getLoginHeader()` 等绑定。
- 但主链路 `sourceapp.ScriptIntegration.HeaderMap()` 只读取 `src.Header`。
- 搜索、详情、目录、正文的请求头构造都没有合并登录头缓存。

结论：

- 这是此前确认过的真实 bug，但当前 live 代码已经修复。
- `ScriptIntegration.HeaderMap()` 已合并 compat `loginHeader`，主请求链会真正消费脚本写入的登录头。

### 5.4 `loginCheckJs` 接入方式分叉且不完整

影响级别：P0

事实：

- Reader 已经写了 `ScriptIntegration.LoginCheck()`。
- 但实际业务链路没有统一复用它。
- 搜索、发现、详情手写了一个简化版“如果 JS 返回 string 就替换 body”逻辑。
- 目录和正文抓取后则完全没执行 `loginCheckJs`。

结论：

- 这是此前确认过的真实 bug，但当前 live 代码已经修复。
- 搜索、发现、详情、目录、正文都统一复用了 `ScriptIntegration.LoginCheck()`，不再维持分叉入口。

### 5.5 Cookie 域归并算法错误

影响级别：P0

事实：

- Legado 用 public suffix 数据库求 effective TLD+1。
- Reader 只取 host 最后两段。

结论：

- 这是此前确认过的真实 bug，但当前 live 代码已经修复。
- Cookie 域归并已改为 eTLD+1，不再用“host 最后两段”的错误算法。

### 5.6 搜索、发现、目录的 `+/-` 前缀语义缺失

影响级别：P1

事实：

- Legado 会在 `bookList` / `chapterList` 前缀为 `-` 时反转结果，`+` 时显式保序。
- Reader 搜索、发现、目录此前都没有实现这个语义。

结论：

- 已修复：搜索、发现、目录都按 `+`/`-` 前缀归一化列表规则，`-` 时对结果做 reverse（`normalizeListRule`/`normalizeDiscoveryListRule`/`normalizeTocListRule`），并补了对应回归测试。
- 该差异在当前主链路上已收口，不再作为未修 bug。

### 5.7 `Analyzer` 变量域主链路已收口

影响级别：P1

事实：

- `Analyzer.vars` 仍是实例级 map，但搜索、发现、目录循环现在都会在每个 item 前恢复变量基线。
- 目录阶段现已补上每个章节 item 的 `chapter` carrier，`@put` 不再误写 `book`。
- 章节变量与书籍变量的优先级、污染边界已有 analyzer/service 级回归测试覆盖。

结论：

- 这条差异在当前主链路上已经收口，不再作为未修 bug。

### 5.8 搜索详情 fallback 语义不完整

影响级别：P1

事实：

- `bookUrlPattern` 命中详情页时，Legado 会直接走详情解析。
- 列表为空时，Legado 还会尝试详情页解析。

结论：

- 已修复：`bookUrlPattern` 命中时用详情解析（`PreviewSearchBookFromInfo`）生成 `SearchBook`，解析失败才退回最小 `SearchBook`。
- 已修复：仅在未配置 `bookUrlPattern` 时，列表为空会按详情页回退解析（搜索与发现共用同一语义）。
- 该差异在当前主链路上已收口，不再作为未修 bug。

### 5.9 正文高级字段主链路已收口，剩余为服务端边界

影响级别：P1

补充说明：

- `webJs` 当前主正文链已接入。
- `imageDecode` 当前已接入图片代理，并支持书源/书籍上下文。
- 正文 HTML 规范化当前已补上接近 `HtmlFormatter.formatKeepImg()` 的图片保留与块级转行语义。
- `sourceRegex` 已按 Requirement 3.2 的浏览器流非目标显式收口为不支持，而不是静默忽略。
- `imageStyle`、`payAction` 已在需求基线中明确为客户端阅读 / 付费 UI 兼容字段，服务端只保留字段，不执行。

结论：

- 这条差异不再表示“正文主链路缺实现”。
- 当前剩余的是浏览器流非目标边界，而不是服务端正文解析链仍然漏字段。

### 5.10 文件型书源详情能力已补到运行态与基础对外表达

影响级别：P1

事实：

- Legado `BookInfo.kt` 对文件型书源走 `downloadUrls`。
- Reader 当前 `Book` 运行态字段和详情解析链已经支持 `downloadUrls`。
- 当前 OpenAPI `Book` schema 和 DTO 已补出 `downloadUrls`，因此“运行态兼容已补，外部 API 表达缺失”这条差异已不再成立。

结论：

- 文件型书源在一期范围内需要的兼容字段与基础对外表达已经补齐；由于 Requirement 仍明确“不实现文件下载站闭环”，后续不应再把这条当成未修 bug。

### 5.11 替换净化主链路已闭环，剩余为异常模型差异

影响级别：P2

事实：

- 当前 live `Processor` 在命中 `ErrRegexLimitExceeded` 且规则为 regex 时，已经会禁用规则并落库。
- 但 Reader 的触发条件仍是服务端受限 regex/上下文超限路径，不是对 Legado `RegexTimeoutException` 的逐语义复刻。

结论：

- “超限后自动禁用规则”这部分已经不再缺失。
- 剩余差异主要在于超时/限额边界和异常模型仍非逐字等价，不再是一期主链路 blocker。

### 5.12 校验失败分类已基本收口

影响级别：P2

事实：

- `check/debug` 当前都已接入统一失败诊断器。
- `CheckTaskItem` 和调试事件里已经持久化/输出 `failureClass`。
- 真实 E2E 报告中的失败聚类也已经以 DNS/TLS/timeout、上游 HTTP、授权、空 JSON 等具体类别为主，不再大量折叠成 `website_failed`。

结论：

- “失败分类仍然明显偏粗”这条结论已经不再成立。
- 剩余工作更多是是否继续扩更细的对外契约，而不是主链路仍然缺少分类能力。

## 6. 当前没有发现的坏味道

以下问题本轮没有在生产代码里发现：

- 按域名写死 header。
- 某站点特定 XPath/CSS 分支。
- 某个 sourceId 的白名单逻辑。
- “如果名字包含七猫/番茄/得奇就走特殊实现”这种线上分支。

这意味着当前 Reader 的技术债主要不是“生产逻辑已经被站点补丁污染”，而是“兼容子系统还没补完整”。

## 7. 优先级建议

如果后续要继续推进兼容，不建议先做更多单源修补，建议按下面顺序：

1. 继续把审计中仍保留的浏览器流字段明确划分为“已收口非目标”与“未来阶段能力”。
2. 评估是否需要继续逼近 Android `RegexTimeoutException` 的异常模型。
3. 不要再把 `imageStyle/payAction` 这类客户端 UI 字段当成服务端实现缺口重复审计。
4. 后续若有真实 E2E 新 blocker，优先判断是浏览器流边界、源规则失效还是站点变化。

## 8. 结论

当前 Reader 生产代码的主要问题不是“写了很多脏的单站点特判”，而是：

- 规则兼容框架已经搭起来了。
- 当前剩余问题已经明显收敛到浏览器流高级属性和少量异常模型，而不再是变量域或正文高级字段主链路仍未闭环。

从维护性角度看，这是好消息：

- 说明当前代码基线仍然相对干净。
- 后续最有价值的工作是继续补通用兼容层，而不是先做大规模去特判清理。
