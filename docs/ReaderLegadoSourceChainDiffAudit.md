# Reader 与 Legado 书源链路差异审计

审计日期：2026-05-05

## 1. 审计范围与方法

本审计只以当前仓库 live 代码为准，文档仅作为辅助定位，不作为结论真相源。

本次逐项核对了以下两类源码：

- Reader 当前实现：
  - `internal/domain/source/book_source.go`
  - `internal/domain/rule/rules.go`
  - `internal/infra/rule/analyzer.go`
  - `internal/infra/rule/css/css.go`
  - `internal/infra/rule/url/url.go`
  - `internal/infra/fetcher/http_fetcher.go`
  - `internal/infra/fetcher/cookies.go`
  - `internal/infra/script/runtime.go`
  - `internal/app/source/importer.go`
  - `internal/app/source/script_integration.go`
  - `internal/app/search/single_source.go`
  - `internal/app/search/multi_source.go`
  - `internal/app/discovery/discovery.go`
  - `internal/app/book/fetch_request.go`
  - `internal/app/book/info.go`
  - `internal/app/book/toc.go`
  - `internal/app/content/content.go`
  - `internal/app/replace/processor.go`
  - `internal/app/replace/limited_regex.go`
  - `internal/app/debug/flow.go`
  - `internal/app/check/checker.go`
  - `internal/bootstrap/bootstrap.go`
- Legado 对照实现：
  - `legado/app/src/main/java/io/legado/app/data/entities/BookSource.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/BaseSource.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/SearchRule.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/ExploreRule.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/BookInfoRule.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/TocRule.kt`
  - `legado/app/src/main/java/io/legado/app/data/entities/rule/ContentRule.kt`
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeUrl.kt`
  - `legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeRule.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookInfo.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookChapterList.kt`
  - `legado/app/src/main/java/io/legado/app/model/webBook/BookContent.kt`
  - `legado/app/src/main/java/io/legado/app/help/book/ContentProcessor.kt`
  - `legado/app/src/main/java/io/legado/app/help/http/CookieStore.kt`
  - `legado/app/src/main/java/io/legado/app/help/source/BookSourceExtensions.kt`
  - `legado/app/src/main/java/io/legado/app/ui/association/ImportBookSourceViewModel.kt`

## 2. 总体结论

Reader 当前已经具备了 Legado 在线书源主链路的基础骨架：

- 书源 JSON 对象与规则对象可以导入、落库、再输出。
- 基础规则引擎支持 CSS、XPath、JSONPath、Regex、`@js:`/`<js>...</js>`、`@put/@get/{{...}}`。
- 搜索、发现、详情、目录、正文、替换规则、调试、校验都有独立 use case。

但从“与 Legado 在线书源语义完整对齐”的角度看，当前仍存在一批结构性差异，主要集中在以下几类：

- 请求构造和 `AnalyzeUrl` 兼容仍是子集实现。
- JS 运行时做了受限兼容，但不是 Legado/Rhino 行为等价替身。
- `sourceRegex` 这类依赖浏览器流的字段，当前仍与 Legado 存在明确边界差异；`imageStyle`、`payAction` 则已明确归类为客户端阅读 / 付费 UI 字段，由服务端保留但不执行。
- 搜索/发现/目录链路中部分 Legado 规则语义被弱化或遗漏。
- 正文链路对 `sourceRegex` 仍未做 WebView 等价实现；其中 `sourceRegex` 已按一期非目标显式收口，`imageDecode` 已接入图片链，`imageStyle` / `payAction` 也已明确为客户端 UI 边界字段。

下面按链路展开。

## 3. 数据模型与导入

### 3.1 已对齐部分

- `BookSource` 核心字段在 Reader 域模型中基本都保留了，包括 `jsLib`、`enabledCookieJar`、`header`、`loginUrl`、`loginUi`、`loginCheckJs`、`coverDecodeJs`、`exploreUrl`、`exploreScreen`、`ruleExplore`、`searchUrl`、`ruleSearch`、`ruleBookInfo`、`ruleToc`、`ruleContent`、`ruleReview`。
- Reader 规则对象 `SearchRule` / `ExploreRule` / `BookInfoRule` / `TocRule` / `ContentRule` 的字段集合与 Legado 当前实体基本一致。
- Reader 的 `Flexible[T]` 兼容了 Legado 常见的“规则字段既可能是 JSON 对象，也可能是 JSON 字符串”两种输入形式。
- `#requestWithoutUA` 已按 Legado 导入语义实现。

### 3.2 结构性差异

1. Reader 用 `sourceId = sha256(bookSourceUrl)` 作为 API 主键，而 Legado 直接用 `bookSourceUrl` 作为主键。
   - 这是服务端设计差异，不是兼容 bug。
   - 影响是 API 层引用方式不同，但不影响规则执行语义。

2. Reader 的 `Book` 域模型没有 `downloadUrls`、`infoHtml`、`tocHtml` 这些 Legado 在详情/目录/正文链路中会真实使用的字段。
   - Legado `BookInfo.kt` 在文件源模式下依赖 `downloadUrls`。
   - Legado `BookInfo.kt` / `BookChapterList.kt` / `WebBook.kt` 会把详情 HTML、目录 HTML 作为后续链路缓存输入。
   - Reader `internal/domain/book/book.go` 当前没有这些字段，因此文件型书源和“详情页=目录页缓存复用”能力被弱化。

3. `ruleReview` 在两边都不是主链路有效能力，但状态不同。
   - Legado `BookSource.kt` 里 `ReviewRule` 类型转换器直接返回 `null` / `"null"`，编辑 UI 相关入口也被注释，等价于“字段存在但主链路未启用”。
   - Reader 只是把 `ruleReview` 当原始 JSON 保留和回传，没有行为实现。
   - 结论：这是“字段保留对齐”，不是“行为对齐”。

### 3.3 导入链路差异

1. Legado 导入入口支持字符串、JSON 对象、JSON 数组、远程 URL、`content://`/文件 URI。
2. Reader 服务端导入聚焦 API 场景，支持 JSON 对象、JSON 数组、`sourceUrls` 聚合文档、单个 `remoteUrl`。
3. 这属于产品边界差异，不属于解析链路 bug。

## 4. URL / 请求 / AnalyzeUrl 兼容差异

这是当前 Reader 与 Legado 的最大差异面。

### 4.1 Reader 已覆盖的 AnalyzeUrl 子集

Reader 当前已经覆盖：

- 基础 URL 绝对化。
- 自定义 URL 属性中 `method` / `body` / `headers` / `type` / `charset` / `origin`。
- 宽松 URL option JSON 解析：单引号、未加引号 key、中文引号。
- Query / form body 编码。
- 默认 UA、gzip、响应大小限制、重定向限制、SSRF 防护。

### 4.2 Legado 已有但 Reader 未完整覆盖的 AnalyzeUrl 能力

Legado `AnalyzeUrl.kt` 当前还处理：

- `retry`
- `proxy`
- `useWebView`
- `webJs`
- `sourceRegex`
- `serverID`
- `webViewDelayTime`
- 登录头自动合并
- `enabledCookieJar` 条件启用

Reader 当前的真实状态：

1. Reader 构造普通请求时当前已消费 `method/body/headers/type/charset/origin/retry`。
   - 见 `internal/app/search/single_source.go`
   - 见 `internal/app/book/fetch_request.go`
   - 见 `internal/app/content/content.go`
2. `proxy/useWebView/serverID/webViewDelayTime` 在 Reader 主链路中没有对应执行实现，但 search/book/content/script 请求链现在都会对这类非零 option 返回显式 `unsupported url option`；`webJs` 已接入正文链，`sourceRegex` 目前按一期浏览器流非目标显式收口。
3. 这意味着依赖 WebView 抓取、代理或页面源码二次抓取的 Legado 书源，在 Reader 中会命中“受控拒绝 + 兼容子集”边界，而不是静默走偏。

### 4.3 `enabledCookieJar` 已按书源对齐

这是高优先级真实缺口。

Legado 侧：

- `AnalyzeUrl.kt` 用 `source?.enabledCookieJar == true` 决定是否接入 CookieStore。
- `BaseSource.getHeaderMap()`、`CookieStore`、`AnalyzeUrl.setCookie()` 共同形成“按书源显式开关”的 Cookie 持久化链路。

Reader 侧：

- `fetch.Request.UseCookieJar` 已由搜索、发现、详情、目录、正文和脚本请求链按书源透传。
- `HTTPFetcher` 当前只在请求显式要求时加载和保存 Cookie。

结论：

- 这条差异已经不再成立，当前 live 代码已实现按书源开关语义。

### 4.4 登录头 / 登录信息状态已打通到主链路

这也是高优先级真实缺口。

Legado 侧：

- `BaseSource.putLoginHeader()` 会把登录头写入缓存，并把 Cookie 同步到 `CookieStore`。
- `BaseSource.getHeaderMap(hasLoginHeader = true)` 会把登录头并入普通请求头。
- `AnalyzeUrl` 默认会走这条路径。

Reader 侧：

- `ScriptIntegration.HeaderMap()` 当前已把 compat `loginHeader` 合并回普通请求头。
- 搜索、详情、目录、正文链路都会真实消费 `source.putLoginHeader()` 的结果。

结论：

- 这条差异已经不再成立，当前 live 代码已完成主请求链闭环。

### 4.5 Cookie 域名归并算法已对齐 eTLD+1

Legado 侧：

- `NetworkUtils.getSubDomain()` 使用 public suffix 数据库求 effective TLD+1。
- 例如 `www.biquge.com.cn` 会归并到 `biquge.com.cn`。

Reader 侧：

- 当前 `cookieDomain()` 已改为使用 public suffix 的 eTLD+1 归并。

结论：

- 这条差异已经不再成立。

## 5. 规则引擎 / JS / CSS / 变量语义差异

### 5.1 CSS / XPath / JSONPath / Regex 能力

Reader 当前已经覆盖了：

- CSS 选择器链、属性提取、`text/ownText/html/all/textNodes/outerHtml`。
- XPath 字符串与节点提取。
- JSONPath 与裸路径补全。
- Regex 提取、替换、Java 风格前瞻兼容子集。

这一层已经能覆盖大量主流源。

### 5.2 Reader 的 JS 运行时是“受限兼容层”，不是 Rhino 等价实现

Reader 当前 goja runtime 已补了不少兼容绑定：

- `java.base64Encode/base64Decode/md5Encode/HMacHex`
- `java.aesBase64DecodeToString`
- `java.desEncodeToBase64String`
- `java.createSymmetricCrypto`
- `java.get/getString/getElements`
- `java.ajax/get/post/connect`
- `JavaImporter`
- `Packages.java/javax/android/cn.hutool` 的受限子集
- `cookie`、`cache`、`source`、`book`、`chapter`

但它仍然不是 Legado Rhino 行为等价替身：

- 只补了真实书源高频依赖的 Java/Hutool 子集。
- 没有开放任意 Java 类、进程、文件、系统访问。
- 没有完整 WebView JS 环境。

这属于设计性差异，不一定是 bug，但必须明确：

- Reader 当前是“兼容常见源规则的受限沙箱”。
- 不是“无条件兼容所有 Legado JS 规则”。

### 5.3 `AnalyzeRule` 变量域主链路已基本对齐

Legado 侧：

- `@put` 最终写入 chapter / book / ruleData / source 的变量域。
- `@get` / `java.get()` 会按 chapter -> book -> ruleData -> source 的顺序取值。
- 变量天然附着在当前实体或当前源上。

Reader 侧：

- 当前 `Analyzer` 已支持显式 carriers，`@get/@put` 可读写 chapter/book/source 变量域。
- 搜索、发现、目录循环都已增加 item 级变量基线恢复，避免条目间污染。
- 目录阶段现已像 Legado `BookChapterList.kt` 一样，在每个章节 item 解析前挂上 `chapter` carrier，因此 `@put` 不会再误写到 `book`。
- 虽然 Reader 内部仍保留 `Analyzer.vars` 作为实现层局部缓存，但当前 analyzer/service 级测试已覆盖 chapter -> book -> source 的读取顺序，以及“章节变量不污染书籍变量”的关键语义。

结论：

- 这一项主链路差异已收口，不再作为当前未修 gap。

### 5.4 共享 `script.Runtime` 的跨书源状态串扰风险已修复

这是本轮审计发现的最高优先级实现 bug 之一。

事实链：

1. `internal/bootstrap/bootstrap.go`（原 `cmd/server/main.go`）在启动时创建了一个全局共享 `scriptRuntime`。
2. `internal/infra/script/runtime.go` 在 `EvalWithBindings()` 中，如果 `policy.SourceKey` 为空，就把它写成当前 source key。
3. 这个字段写进了 `Runtime.policy` 本身，不是局部变量。
4. 同一个 `Runtime` 后续再处理其他书源时，因为 `policy.SourceKey` 已不为空，就不会再更新。
5. `cache.get/put` 与 `java.get/put` 都直接使用 `r.policy.SourceKey`。

结果：

- 当前 live 代码已改为每次执行按 bindings/source 计算局部 `sourceKey`，不再把首个书源写回共享 `Runtime.policy`。
- 这条差异已经不再成立。

## 6. 搜索链路差异

### 6.1 已对齐部分

- `searchUrl` 基础拼装。
- `@js:` / `<js>...</js>` URL 片段处理。
- `{{key}}` / `{key}` / `{{page}}` / `{page}` 基础替换。
- URL option 的 `method/body/headers`。
- 搜索结果的 `name/author/intro/kind/wordCount/lastChapter/bookUrl/coverUrl` 提取。

### 6.2 关键差异与问题

1. `bookUrlPattern` 命中详情页时，Reader 只返回一个最小 `SearchBook`，不继续按详情规则补全信息。
   - Legado `BookList.kt` 会走 `getInfoItem()`，直接按详情规则解析书名、作者、简介、封面等。
   - Reader 当前只返回 `BookURL/Origin/OriginName/OriginOrder/Type`。
   - 这会让“搜索直接跳详情页”的源在 Reader 中丢失核心元数据。

2. 当搜索列表为空时，Reader 没有复刻 Legado 的详情页 fallback。
   - Legado `BookList.kt`：如果列表为空且 `bookUrlPattern` 为空，也会尝试按详情页解析。
   - Reader 当前没有这条兜底。

3. `ruleSearch.bookList` 的 `+` / `-` 前缀语义没有完整复刻。
   - Legado 会移除前缀，并在最终结果阶段按 `reverse` 处理。
   - Reader 搜索链路没有实现这个 reverse 语义。

4. 搜索头部 JS 返回值解析比通用 header parser 更弱。
   - `parseSearchHeaders()` 对静态 header 支持宽松 JSON。
   - 但 JS 结果走 `parseSearchHeaderValue()` 时只接受严格 JSON，不走宽松对象兼容。

5. 搜索链路当前已统一复用 `ScriptIntegration.LoginCheck()`。

## 7. 发现链路差异

### 7.1 已对齐部分

- `exploreUrl` 支持静态列表、JSON 数组、`@js:` / `<js>`。
- 发现列表与搜索列表共享同一套解析骨架。

### 7.2 差异

1. Reader 没有复刻 Legado 的 `exploreKinds()` 缓存层。
   - Legado 会对 `@js:` 生成的分类做缓存。
   - Reader 每次都重新求值。

2. `exploreScreen` 当前只落库，不参与任何发现筛选逻辑。
   - 这在 Reader 中是“字段保留未消费”。
   - Legado 主在线书源运行链里也不是核心解析环节，但 UI/配置层会保留它。

3. 发现链同样没有实现 `bookList` 的 `+/-` 前缀语义。

4. 发现链没有复刻 Legado `BookList.kt` 的“列表为空则按详情页尝试解析”分支。

## 8. 详情链路差异

### 8.1 已对齐部分

- `init` 初始化规则。
- `name/author/kind/wordCount/lastChapter/intro/coverUrl/tocUrl` 提取。
- `tocUrl` 为空时回退 `bookUrl`。
- `CanReName` 的“非空即允许改名”语义与 Legado 当前实现一致。

### 8.2 差异

1. Reader 当前已补上 `downloadUrls`、`infoHtml`、`tocHtml` 运行态字段，并在详情/目录/正文链路中复用。
2. `coverDecodeJs` 当前已接入图片代理，但依赖显式图片代理上下文，而不是像 Android 端那样在本地阅读链路隐式生效。
3. 文件型书源对外 API 表达当前已补到 `Book.downloadUrls`，不再只停留在运行态字段。

## 9. 目录链路差异

### 9.1 已对齐部分

- `preUpdateJs`
- `chapterList/chapterName/chapterUrl/isVolume/isVip/isPay/updateTime`
- `nextTocUrl`
- `formatJs`
- 空目录报错

### 9.2 差异与问题

1. `chapterList` 的 `+/-` 前缀语义被 Reader 去掉了，但没有实现 reverse。
   - Legado 目录链会根据前缀决定是否反转列表。
   - Reader 只移除了前缀，没有执行最终顺序调整。

2. 目录抓取后当前已统一执行 `loginCheckJs`。

3. Reader 没有复刻 Legado 的章节词数补全、最新章节元数据刷新、`reverseToc` 用户配置等附加语义。
   - 这属于能力弱化，不是单点 bug。

4. 目录项变量也存在跨章节污染风险。
   - 原因同第 5 节的 `Analyzer.vars` 生命周期问题。

## 10. 正文链路差异

### 10.1 已对齐部分

- 章节正文缓存。
- `content/title/nextContentUrl/replaceRegex` 基础处理。
- 多页正文串接。
- 章节标题在正文阶段可被覆盖。

### 10.2 关键差异与问题

1. Reader 抓正文后当前已统一执行 `loginCheckJs`。
2. `ContentRule.webJs` 当前已在正文抓取后执行，`nextChapterUrl` 也已注入正文 JS 绑定环境。
3. `sourceRegex` 当前没有做 WebView 等价实现，而是按一期浏览器流非目标显式收口为不支持。
4. `imageDecode` 当前已接入图片代理；`imageStyle` / `payAction` 已明确为客户端阅读 / 付费 UI 字段，服务端只保留兼容字段，不执行。
5. Reader 的正文规范化当前已补上接近 Legado `HtmlFormatter.formatKeepImg()` 的关键语义：
   - HTML 块标签转行、评论剥离、实体解码。
   - `<img>` 标签保留并按正文页 `redirectUrl` 绝对化。
   - 服务端仍不会复制 Android 阅读 UI 的段首缩进排版，这部分继续留给正文后处理与客户端展示层。

## 11. 替换规则链路差异

### 11.1 已对齐部分

- 标题规则和正文规则分域查询。
- 去重标题。
- 规则命中记录。
- 正则与普通替换两种模式。

### 11.2 差异

1. Reader 当前已补上服务端可配置的 `reSegment()` 与简繁转换链。
2. `reSegment()` 的主启发式已经覆盖引号状态机、词典避免误断、对话段补切和尾引号叙述切段；与 Legado 的剩余差异主要是 Android 侧随机断段在服务端被改成确定性实现，以保证同一输入稳定输出。
3. Reader 当前在 regex 超限路径上已经会自动禁用规则并落库，但异常模型和 Legado 的 `RegexTimeoutException` 仍不完全等价。

## 12. 调试 / 校验链路差异

### 12.1 调试

- Reader 用显式 `mode` + SSE，放弃了 Legado 的 `++` / `--` / `::` 字符串协议。
- 这是服务端 API 设计差异，不是 bug。

### 12.2 校验

Reader 的校验 / 调试失败分类当前已经基本收口：

- check/debug 主链路已引入统一失败诊断器，并持久化/事件化 `failureClass`。
- 真实 E2E 报告也已经能输出细粒度 `failureClass` 聚类。
- 在最新真实 E2E 口径下，失败聚类已主要表现为上游 HTTP、空 JSON、DNS/TLS/timeout、授权等外部或源级问题，而不再大量折叠为笼统 `website_failed`。

## 13. 当前最重要的差异清单

按优先级排序，最值得优先修的不是“再补一个网站”，而是以下通用问题：

1. `proxy/useWebView/serverID/webViewDelayTime` 等浏览器流高级 option 仍未实现，只能按一期边界显式收口。
2. `sourceRegex` 仍未做 WebView 等价实现，当前只显式收口为不支持。
3. regex 超限/超时的异常模型仍不是对 Android `RegexTimeoutException` 的逐语义复刻。
4. 文件型书源主链路已补到运行态和对外 `Book.downloadUrls` 表达；剩余差异不再集中在该能力本身。

## 14. 结论

如果把目标定义为“Reader 已经有了可运行的 Legado 书源服务端骨架”，这个结论成立。

如果把目标定义为“Reader 与 Legado 在线书源链路已经在请求、状态、JS、Cookie、CSS、正文处理上全面等价”，当前结论不成立。

当前最真实的状态是：

- 规则解析主框架已经搭起来了。
- 兼容工作已经从“完全不可用”进入“主链路通用语义基本补齐，只剩浏览器流边界和少量异常模型差异”的阶段。
- 后续工作的主战场不该只是继续堆单源 patch，而应优先决定浏览器流能力是否进入下一阶段范围，以及是否继续逼近 Android 侧异常模型。
