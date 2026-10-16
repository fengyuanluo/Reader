# Legado 对齐审计报告

> 说明：本文是持续追加的增量审计日志，内部保留了多轮真实 E2E 快照和阶段性结论。文中较早位置出现的 `fullSuccessCount`、阶段统计和 blocker 描述仅代表当时快照，不自动等于“当前最新状态”。
>
> 读取方式：如果需要引用“当前最新通过率”或“当前剩余问题”，优先查看本文最后一轮更新、`test/reports/real_source_collection_e2e_report.json`、`test/reports/real_source_collection_e2e_report.md`，必要时重新运行真实 E2E。当前这两份报告作为 `0.0.1` 版本的审计快照保留在仓库中。

## 1. 目标与范围

本报告用于承接当前线程的任务 1 和任务 2：

- 任务 1：对照 `legado/` 原项目中的 JS 解析、CSS 解析、请求链、变量链和真实 E2E 表现，逐项生成完整报告。
- 任务 2：基于本报告持续逐项修复，严禁以“简化实现”替代 Legado 已有且被真实书源依赖的语义。

本报告只记录已经核实过的源码事实、Reader 当前实现状态、差距、修复状态和下一步优先级；不把猜测写成结论。

## 2. 事实来源

- Legado 规则链与请求链文档：`legado/docs/book-source-chain.md`
- Legado 规则执行：`legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeRule.kt`
- Legado URL 分析：`legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeUrl.kt`
- Legado CSS 解析：`legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeByJSoup.kt`
- Legado 搜索/详情/目录/正文链路：`legado/app/src/main/java/io/legado/app/model/webBook/*.kt`
- Legado 源对象 JS 能力：`legado/app/src/main/java/io/legado/app/data/entities/BaseSource.kt`
- Reader 当前真实门禁与审计样本：`test/e2e/real_source_collection_test.go`
- Reader 最近一次 100 候选报告：`test/reports/real_source_collection_e2e_report.json`、`test/reports/real_source_collection_e2e_report.md`

## 3. 当前总体状态

- 常规工程门禁已具备：OpenAPI、mock E2E、安全测试、CI、本地验证脚本均已落地。
- 真实远程集合 release gate 已具备，且仓库内最新 100 候选快照已达到 `releaseAllowed = true`。
- 仓库内最新真实报告（`test/reports/real_source_collection_e2e_report.*`，报告时间 `2026-10-14T10:05:57Z`）摘要：
  - `Parsed count = 459`
  - `Attempted candidates = 100`
  - `Full success count = 39`
  - `Release allowed = true`
- 结论：截至 `0.0.1` 版本，Reader 已经具备可发布的第一轮移植重构成果；本文后续历史快照仍保留 51/44/33/32/21/17 等阶段性数值，用于追溯演进过程，而不是作为当前状态判断。
- 最新真实样本口径下，Reader 已经从“基础规则链不通”推进到“主链路可发布、剩余主要是少量站点状态 / 反爬 / 源级请求差异”的阶段。后续工作应避免把外部 404/403/401 与 Reader 通用实现缺口混在一起。

## 4. 分项对照

### 4.1 JS 运行时与绑定

#### 已对齐

- `@js:`、`<js>...</js>` 基本执行能力已具备。
- `java.ajax/java.connect/java.get/java.post` 已接入真实 Fetcher。
- `java.base64Encode/base64Decode/md5Encode/HMacHex/aesBase64DecodeToString/desEncodeToBase64String/encodeURI/randomUUID/get/getString` 已具备。
- `cookie.*` 基本绑定已具备。
- `Packages`/`JavaImporter` 的受限兼容子集已具备，能够覆盖一部分 AES/GZIP 类正文脚本。
- `source.getKey/getLoginHeader/getVariable/putVariable/setVariable/loginUrl/header/jsLib` 已具备基础兼容实现。
- `java.getElements(rule)` 已具备，可在 JS 中基于当前 `result` 返回支持 `select/text/attr/html` 的对象列表。

#### 已确认曾经缺失且已修复

- 空规则返回输入而不是空值，导致详情 HTML 被误写入 `tocUrl`。
- `<js>...</js>` 嵌入式 JS 后续规则链未执行。
- JS 运行时未注入真实 Fetcher，导致 `java.ajax` 等实际不可用。
- `java.get` 不能读取 `book/chapter` 变量域。
- 宽松 URL option JSON、宽松 header JSON、Hutool 子集、`JavaImporter` 子集等真实书源依赖语义缺失。
- `source.loginUrl` / `source.setVariable` / `source.getVariable` / `source.putLoginHeader` 的源级状态语义缺失。
- `java.getElements(rule)` 缺失，导致一类 JS 拼装搜索列表规则直接失败。
- `Packages.java.util.UUID.randomUUID()`、`Packages.android.os.Build.*` 等常见 Rhino/Java 兼容入口缺失。
- JS `cookie.*` 绑定与 HTTP fetcher cookie 会话未共享，导致 `java.ajax()` 后的真实 cookie 无法被 `cookie.getCookie()` 读取。

#### 当前仍未完全对齐

- Legado `java.ajax` / `cookie` / `AnalyzeUrl` 的组合在真实站点上仍有更细的请求链差异：
  - 部分源在 Reader 中已能算出 URL / body / headers，但真实站点仍返回业务级空结果、401/403/404/405 或节流提示。
  - 这类问题多数不再是“绑定不存在”，而是更细的请求构造、签名上下文、cookie 使用时序、参数编码或站点反爬差异。
- `TestRealSourceSingleDebug` 当前使用独立 `debugRuntime` 做 raw `searchUrl` 观察，它不能直接证明正式 E2E 链路中的 cookie 联通状态；真实 cookie 行为应以正式 E2E 或共享 session store 的定向测试为准。

#### 当前结论

- JS 兼容层已从“基础可跑”进入“补 Legado 特定绑定语义”的阶段。
- 继续提升真实通过率的高优先级方向不是更换引擎，而是继续补 Legado 绑定和阶段上下文语义。

### 4.2 CSS/HTML 解析

#### 已对齐

- 默认 HTML 解析与 CSS selector 基本能力已具备。
- `@` 链、`text/ownText/html/all/attr`、`||`、`%%` 基本语义已具备。
- 搜索/目录/正文的基础 CSS 抽取链已经支撑 mock E2E 与部分真实源完整通过。

#### 当前仍未完全对齐

- CSS/HTML 主链本身目前不是第一优先级 blocker。
- 已确认并修复一条真实影响 100 候选结果的通用解析缺口：`internal/infra/rule/parser.Split()` 之前按字节遍历字符串，导致中文规则步骤会被拆成乱码，像 `text.查看更多章节@href` 这样的 UTF-8 CSS 规则在 Reader 中会静默失效。
- 修复后，真实源 `八零小说（优++）` 的 `ruleBookInfo.tocUrl = "text.查看更多章节@href"` 已能正确提取 `http://wap.80zw.la/426/page-1.html`，并把该源从 `toc_empty` 推进到完整成功。
- 已确认并修复另一条真实影响搜索到详情跳转的通用规则缺口：Legado 会先修剪规则前导的 `@`，因此 `@onclick@js:...` 这类“先取属性，再接 JS”写法是合法的；Reader 之前未兼容这层前导 `@` 规则链，导致属性提取失败后回退成搜索页 URL。
- 修复后，真实源 `阅友小说（优+）` 的 `ruleSearch.bookUrl = "@onclick@js:result.match(...)[1]"` 已能正确提取详情页 URL，并把该源从 `toc_empty` 推进到完整成功。
- 已确认并修复另一条真实影响 `custom URL` 传参的通用缺口：Reader 之前在解析 `url,{...}##...` 这类真实书源规则时，会把 JSON 属性对象后面的 `##...` 尾缀一起吞进属性解析，导致 `body/method` 丢失，只剩裸 URL。
- 修复后，真实源 `米读小说（优+）` 的搜索结果 `bookUrl` 已能稳定保留 `POST + body`，并把该源从详情 `405` 推进到完整成功。
- 已确认 `猫眼看书（优++）` 当前的 `toc_empty` 不是 Reader 缺少某个通用 header/UA/origin 语义：
  - 对同一详情 URL `http://api.lemiyigou.com/novel/zbqYrb?isSearch=1` 做三组实测：无 header、完整 app header 但无 Authorization、完整 app header + 书源内 Authorization。
  - 三组结果分别是 `code=4004`（缺 device/version/brand/source/client name）以及两次 `code=4005 认证失败`。
  - 这说明即便携带书源中提供的完整 header/token，该源当前也已落在源级授权失效或服务端状态问题，而不是 Reader 仍欠缺通用请求链能力。
- 已确认并修复两条会继续污染搜索阶段通过率的通用脚本缺口：
  - `script.Runtime` 里的 `java.ajax()` / `java.connect()` 现在会展开 custom URL 中的 `{{java.get("id")}}` 这类内嵌模板，不再把模板原样发到目标站点。
  - 搜索 URL 预处理现在不会再把 JS 模板字符串里的 `${key}` / `${baseUrl}` 误当成 Legado 的单花括号占位 `{key}` / `{page}` 去替换。
- 修复后，`轻说机翻（优+）` 的真实搜索失败已从 Reader 自身的 `parse \"map[resolve:...]\": first path segment in URL cannot contain colon` 和错误的 `$斗破苍穹` 查询参数，下沉为真实站点 `connection reset by peer`。这说明该源当前不再受 Reader 通用 search URL 预处理缺口阻塞。
- 继续补齐后，真实集合中这批高信号搜索失败又进一步下沉：
  - `script.Runtime` 现在支持 `source.bookSourceComment` / `source.variableComment` 字段、`java.timeFormatUTC(...)`、以及 `data:;base64,...,{\"type\":\"...\"}` 这类 custom URL 字节流通道。
  - 这些修复让 `download.yichnmedia.com` 那条 `猫眼看书（优++）` 从 `unsupported scheme -> illegal base64 -> run is not defined -> 缺少 timeFormatUTC` 连续下沉为最终的 `empty search result`。
  - `城堡小说（优+）` 也从 `java.get(...).headers().get(...)` 的 response API 不兼容，推进为 `empty search result`。
  - 默认脚本超时也已从 3 秒提升到 10 秒，并同步到真实 E2E 运行时；`城堡小说（优+）` 的单源调试因此从 `script_timeout` 下沉到真实 404 请求层。
  - 另一条高价值通用语义对齐是：`java.getString(rule)` 现在会在当前 `result` 查不到时回退到当前阶段根内容 `src`。这让 `阿巴小说（优+）` 的 `chapterList...<js>java.getString('$.data.freeContUrlPrefix')...` 这类规则终于能同时读到根 JSON 和当前章节项。
  - 直接收益：`阿巴小说（优+）` 从 `content_empty` 推进为完整成功；最新 100 候选真实门禁提升到 `fullSuccessCount=17`。
- 对 `番茄小说（优+++）` 做了脱离 Reader 的直连复核后确认：
  - 按当前规则生成的同一搜索 URL 与 `X-Khronos/X-Gorgon/User-Agent/Cookie` 组合，直接用外部 HTTP 客户端请求，服务端稳定返回 `HTTP 200`、`Content-Type: application/json`、`Content-Length: 0` 的空体。
  - 因此该源当前的 `Unexpected end of JSON input` 不能再直接归因于 Reader 的 gzip / body 读取逻辑，更像是规则侧签名链、接口漂移或服务端风控导致的空响应。
- 剩余 `toc_empty` / `content_empty` 更像是少量源级规则差异、返回结构变化或站点限制，而不是大面积 CSS selector 语义缺失。

#### 当前结论

- CSS 主链的大面积能力并非当前最大问题，但 UTF-8 规则切分曾经是一个真实会影响生产源通过率的通用缺口，现已补齐。
- 当前最大差距是“CSS 能力没有完整暴露给 JS 规则脚本复用”。

### 4.3 请求链与 URL option

#### 已对齐

- 相对 URL 绝对化。
- `#requestWithoutUA`。
- URL option 的 `method/body/headers` 基本链路。
- 宽松 URL option JSON。
- 详情/目录/正文阶段继承书源 Header。
- POST 字符串 body 的默认表单语义。
- 搜索阶段 JS 生成的 form body 现已按表单语义编码，而不是原样裸发。
- `script.Runtime` 与 `HTTPFetcher` 现已共享 cookie session store，避免 `java.ajax()` 与 `cookie.getCookie()` 分裂成两套状态。
- 目录阶段提取出的变量现在会写入 `chapter.Variable`；正文阶段在真正发请求前，会对 `chapterURL` 以及其 custom URL string body 再执行一次 raw token expand，避免 `@get:{...}` 原样发往目标站点。
- SSRF、timeout、max body、redirect 安全边界。
- gzip header 与 gzip magic 双重解压兜底。

#### 当前仍未完全对齐

- 真实站点失败中仍有大量 HTTP 400/401/403/404、TLS、DNS、connection reset、timeout。
- 少量仍像 Reader 可继续推进的请求链差异包括：
  - 详情阶段 `405`，当前已通过定点测试确认 Reader 的通用请求构造可以正确处理这类真实 `bookUrl` 的 `POST + body`；因此这类失败更适合按 source-specific 的详情参数来源问题 triage，而不是再当成通用框架缺口推进。
  - 正文阶段 `404`，更像章节 URL 生成已进入真实站点，但最终资源路径或参数仍与 Legado 差一层。
  - 正文阶段 `400/content_empty`，在部分 API 源上已经定位到“目录阶段生成的章节 URL 进入正文阶段后，其自定义 URL body 中仍残留 `@get:{...}` token”，这一层现已补上，`趣悦小说（优++）` 已被推进为完整成功。
- 这些失败必须继续区分：
  - Reader 通用实现缺口
  - 书源本身规则失效
  - 站点外部问题
  - 网络环境问题
- 当前剩余样本里，相当一部分已经更接近“源失效 / 外部问题”，不能再全部视为 Reader 框架问题。

### 4.4 变量链与源状态

#### 已对齐

- `@put:{...}` / `@get:{...}`。
- `{{java.get(...)}}` / `{{java.getString(...)}}`。
- 跨 `SearchBook -> Book -> Chapter` 的变量传递基础链。

#### 当前仍未完全对齐

- Legado `BaseSource.setVariable()/getVariable()/putLoginHeader()/getLoginHeader()` 是跨脚本阶段可复用的源级状态。
- 当前基础源级状态链已打通到可用水平：`source.setVariable/getVariable`、`putLoginHeader/getLoginHeader`、runtime cache、book/chapter variable 均可协作。
- 剩余问题不再是“有没有变量链”，而是部分真实站点如何利用这些变量去生成最终受反爬约束的请求。

## 5. 当前高优先级缺口

按最新真实报告与源码事实综合排序，当前优先级如下：

1. 继续扩大真实报告中的“实现缺口 vs 站点问题”分类，避免把站点 404/403/401 当成框架 bug 反复修。
2. 聚焦少量仍像 source-specific 深层请求链差异的非搜索阶段失败：
   - 目录 `toc_empty`
   - 正文 `404` / `content_empty`
   - 搜索/详情阶段依赖额外签名、会话或服务端参数注入的源
3. 对真实仍在返回业务级空结果的 JS/API 源，继续对照 Legado `AnalyzeUrl` 排查更细的 POST/body/header/cookie/sign 时序差异，但要明确区分“通用缺口”和“源级参数缺失”。
4. 严格避免为了提升通过数而做域名硬编码或放宽安全边界。

## 6. 任务 2 执行规则

- 本报告是任务级记录，后续每次补兼容都必须回写本文件。
- 新修复必须满足：
  - 有明确 Legado 源码事实支撑。
  - 有单元测试或 E2E 证据。
  - 能说明它解决的是哪一类真实失败，而不是“感觉更像 Legado”。
- 严禁：
  - 为单一书源硬编码域名分支。
  - 以放宽安全边界替代规则兼容。
  - 以跳过阶段、伪造成功、关闭错误分类来制造“通过率”。

## 7. 当前结论

- 任务 1：未完全结束，但已经具备第一版正式审计基线；后续需持续增补。
- 任务 2：正在进行；`source` 状态语义、`java.getElements()`、cookie session 联通、form body 编码等通用缺口已补齐，当前应转向少量剩余请求链差异与站点级 triage。
- 任务 3：未完成；按仓库内最新 100 候选快照（`test/reports/real_source_collection_e2e_report.*`）已有 39 条完整成功，但仍不满足“绝大多数书源生产可用”。当前剩余非搜索失败已收敛为少量 `toc_empty`/`content_empty` 与上游/网络级失败。
- 2026-05-04 的 source-specific 深层 triage 进一步确认：当时剩余的 `toc_empty` 仍是 `api.lemiyigou.com` 详情/目录接口直接返回 `code=4005 认证失败`，而不是 Reader 目录解析主链缺少新语义；同时，搜索阶段里剩余最显眼的 `番茄小说（优++）` 与 `七猫小说（优++）` 也分别暴露出死域名和 host 字段污染。现阶段继续扩大通过数，更可能依赖源级修复或站点恢复，而不是再追加一轮高收益框架补丁。
- 为避免后续重复人工 triage，真实集合 E2E 报告现在已补结构化失败分类：`real_source_collection_e2e_report.json/.md` 会输出逐条 `failureClass` 和聚合 `failureSummary`，把失败拆成 `source_search_empty_result`、`source_upstream_http_*`、`source_rule_script_syntax_error`、`source_url_host_polluted`、`source_authorization_failed`、`network_dns_failure`、`network_tls_invalid`、`network_timeout`、`network_connection_reset` 等类别。最新真实 100 候选报告已验证该分类在当前样本上可用，后续 source-specific 修复应直接以这些类别为入口，而不是再次从原始 message 扫描开始。
- 2026-05-04 后续两轮 source-specific 修复已进一步把三类 Reader 自身兼容缺口从真实失败集合里清掉：
  - `bookSourceUrl` / `source.getKey()` 现在会清洗全角 `＃` 后缀，`七猫小说（优++）` 已从 `lookup api-bc.wtzw.com＃妍希` 推进到 `search/info/toc` 全通过，仅剩正文阶段上游代理返回非 JSON。
  - 搜索链现在支持 Legado 风格的“字面 URL 后接 `@js:` 链式处理”，并在搜索阶段执行 `@js:` header；`番茄小说（优++）` 已不再死于 Reader 侧 `source_rule_script_syntax_error`，而是下沉成真实 `fanqie.hnxianxin.cn` DNS 失败。
  - `script.Runtime` 新增了 Rhino `let (...)` 条件表达式预处理和 `java.getWebViewUA()` helper；`得奇小说（优+++）` 已不再因为 JS helper 缺失而失败，而是下沉成真实 TLS 证书错误。
- 继续对齐后，又补上了两块 Legado 的 HTML/CSS/文本语义：
  - `java.t2s()` / `java.s2t()` 已接入 OpenCC 词典，不再缺失。
  - CSS 解析现在支持小写 `@css:`、`class.xxx` / `tag.xxx` 语法糖、逗号 selector list 逐分支求并集，以及单个末尾数字/负数索引选择（如 `a.0`、`tag.a.-1`、`div.-1`）。
- 真实收益：
  - `异轻纵然（优++）` 已从 `search items=0` 推进为单源完整成功，说明这轮对齐命中了真实 HTML 源而非只在 mock 测试中自洽。
  - `轻之文库（优++）` 的 `items=0` 经真实 HTML spot check 进一步确认是页面本身返回 `empty-content`，不是解析器继续漏掉了结果。
- 这说明 Reader 当前剩余 gap 已更集中在：
  - 源级 DNS/TLS/授权/代理异常
  - 站点真实无结果或停搜
  - 少量尚未覆盖的高阶 JSoup 索引/过滤语义
  而不是 `@css` / `class` / `tag` / 简繁转换这种基础兼容层仍大面积缺失。
- 这三类修复完成后，最新真实 100 候选 gate 维持 `fullSuccessCount=17`，但阶段通过数提升为 `search=19`、`info=19`、`toc=18`、`content=17`，且 `failureSummary` 进一步净化为以 `source_search_empty_result / source_upstream_http_* / DNS / TLS / timeout / connection_reset` 为主，说明目前剩余差距已更明确地集中在源级失效、上游证书/网络和代理服务状态，而不是 Reader 主链上仍缺大量通用语义。
- 2026-05-04 新一轮 source-specific 深层 triage 又补掉了三条真实通用兼容缺口，并全部用真实单源或真实 100 候选 gate 重新验证：
  - `script.Runtime` 现已提供 Legado `java.createSymmetricCrypto(...)` 兼容子集，至少覆盖 `decrypt` / `decryptStr` / `encrypt` / `encryptBase64` / `encryptHex`，支持 `AES`、`DES`、`DESede` 的 `ECB/CBC + PKCS5/PKCS7/NoPadding` 组合。
  - 真实收益：`src_b91689c2a3f54f74ac1b03f4` / `猫眼看书（优++）` 从 `toc` 阶段 `TypeError: Object has no member 'createSymmetricCrypto'` 推进为单源完整成功，`toc chapters=1455`，`content len=6875`。
  - `internal/infra/rule/css` 去掉了不符合 Legado `AnalyzeByJSoup.getResultLast()` 的“属性缺失时向任意后代节点兜底”行为，只保留 document/body 单根片段的最小兼容兜底。
  - Legado `BookChapterList.kt` 在 `chapterUrl` 为空时会明确回退 `baseUrl`；Reader 现在也重新回到这条语义，不再把 `tag.html` fallback 页面里的第一个 `link[href]` 误当成章节地址。
  - 真实收益：`src_8c754868647d40e20e00fcd6` / `古诗词网（优+）` 已从早前的 `invalid control character` / `content_empty` 系列错误推进为单源完整成功，首章 URL 回到正文页自身，`content len=43026`。
  - `internal/infra/rule/analyzer` 现已把“带多行 JSON URL option 的单个 custom URL”视为一个整体字符串，而不是按换行切成列表；同时 `GetStringList()` 的多阶段规则链在中间结果只有一个值时会像 `GetString()` 一样降成标量，再交给后续 `@js:` 步骤。
  - 真实收益：
    - `TestSingleSourceSearchSupportsLeadingAtAttributeThenJSStep` 和两条 custom URL multiline 回归测试已覆盖这层行为。
    - `src_7e57f5f88ef2b700654a3a49` / `趣悦小说（优++）` 不再把搜索得到的 `book/detail.do` POST body 丢掉，已从 `info` 阶段 `rule_parse_failed: unknown key data` 推进为单源完整链路通过。
  - 对应新增/更新的回归测试包括：
    - `TestRuntimeProvidesCreateSymmetricCryptoCompat`
    - `TestAdapterDoesNotDescendAttributeLookupFromMatchedHTMLFallback`
    - `TestTocServiceFallsBackToBaseURLWhenHTMLFallbackNodeHasNoHref`
    - `TestSingleSourceSearchPreservesMultilineCustomBookURLAttributesWithoutTrailingSuffix`
  - 本轮完整门禁已复跑通过：
    - `go generate ./...`
    - `go test ./...`
    - `go vet ./...`
  - 最新真实 100 候选 gate 已刷新为：
    - `fullSuccessCount=21`
    - `search passed=34 failed=66`
    - `info passed=31 failed=3`
    - `toc passed=27 failed=4`
    - `content passed=21 failed=6`
  - 更关键的是，`failureSummary` 已不再包含 `source_rule_script_failed` 或 `implementation_or_rule_failed`，说明当前剩余失败已进一步净化为源级授权、站点空结果、上游 HTTP、DNS/TLS/timeout 以及少量 `toc_empty/content_empty`。
  - 最新残余高信号 source-specific 结论：
    - `src_e86c92fc3572d5388ba96a75` / `猫眼看书（优++）` 的 `toc_empty` 仍是 `api.lemiyigou.com` 真实返回 `code=4005 认证失败`，不是 Reader 目录主链缺少新语义。
    - `src_226e9a79d6623f6145729ae8` / `七猫小说（优++）` 当前只剩正文阶段上游代理返回非 JSON。
    - `src_be6bc224a0d554d82c4ddb39` / `旭日小说（优+）` 已明确是搜索 `401` 授权失败。
    - `阅读库子`、`狸猫故事`、`快眼看书`、`中华诗词` 这几条当前更适合继续按 source-specific `toc_empty/content_empty` 做定点复核，而不是再回头泛修 JS/CSS/基础请求链。
- 2026-05-04 后续 source-specific 深层 triage 继续命中了 4 组仍在真实 E2E 里反复出现的通用 gap：
  - `internal/infra/rule/css` 现已补齐 Legado `AnalyzeByJSoup.ElementsSingle` 的旧式索引/排除/区间语义，以及 `textNodes` extractor。
  - 真实收益：
    - `src_6ee726b69dd2f237f65a17e9` / `中华诗词（优+）` 从 `content_empty` 推进为完整成功。
    - `src_a8a6f18201431abb598827b5` / `我是盐神（优+）` 从 `content_empty` 推进为完整成功。
    - `src_c8bbeb93a87ca3e5cbac1c14` / `快眼看书（优+）` 从 `content_empty` 推进为完整成功。
  - `internal/infra/rule/regex` 现在在 Go `regexp` 无法编译时自动回落到仓库中已存在的 `regexp2`，补上 Java 风格前瞻/回溯兼容。
  - 真实收益：`src_8312a4da2e972018e6c1be17` / `阅读库子（优+）` 不再因为 `chapterName = text##^0+\\s*(\\d+)(?!\\s*章)##$1章` 把标题整批清空，已推进为完整成功。
  - `script.Runtime` 的 `java.getString(...)` / `java.getElements(...)` 现在复用统一 rule analyzer，而不是只支持 JSONPath 或裸 goquery；同时 JS 返回的 element wrapper 会携带可被后续 Analyzer 继续消费的原始 selection 上下文。
  - `Analyzer` 还补上了 `<js>...</js>##regex` 这类“JS 先产出字符串，再只做 replaceRegex”的空 selector 语义。
  - `CSS @html` 已按 Legado 对齐为 `outerHtml()`，而不是 inner HTML。
  - 真实收益：
    - `src_38ffc1a82cb3c556e7ad6ca6` / `狸猫故事（优+）` 绘本源已完整成功。
    - `src_60d5f69b4a3b6613c7a22f12` / `狸猫故事（优+）` 听书源也已完整成功。
  - `internal/infra/fetcher` 现已按 `Content-Type` charset 自动把文本响应解码到 UTF-8，至少覆盖 `gb2312/gbk` 这类老站返回。
  - 这轮对 `src_4c0843cc0b0f267bdd9802e6` / `民间故事（优+）` 的单源调试确认：
    - 之前 body 是 GBK 乱码。
    - 现在 body 已正确解码为可读中文，但对应关键词返回的页面本身就是“未查询到相关记录”，说明这条已从编码缺口下沉为关键词/站点结果问题。
  - 对应新增/更新的回归测试包括：
    - `TestAdapterSupportsTextNodesExtractor`
    - `TestAdapterSupportsLegacyExcludeIndexSyntax`
    - `TestAdapterSupportsBracketRangeAndLegacyMultiIndexSyntax`
    - `TestReplaceSupportsJavaStyleLookaheadFallback`
    - `TestAnalyzerSupportsEmbeddedJSWithReplaceOnlySuffix`
    - `TestAnalyzerPreservesJavaGetElementsOutputContext`
    - `TestHTTPFetcherDecodesGB2312HTMLToUTF8`
  - 最新真实 100 候选 gate 已刷新为：
    - `fullSuccessCount=32`
    - `search passed=37 failed=63`
    - `info passed=34 failed=3`
    - `toc passed=33 failed=1`
    - `content passed=32 failed=1`
  - 目前后段真实失败已几乎完全收敛为外部问题：
    - `src_e86c92fc3572d5388ba96a75` / `猫眼看书（优++）`：唯一剩余 `source_toc_empty`，上游 `4005` 认证失败。
    - `src_226e9a79d6623f6145729ae8` / `七猫小说（优++）`：唯一剩余 `source_upstream_non_json_response`。
    - `src_be6bc224a0d554d82c4ddb39` / `旭日小说（优+）`：搜索 `401` 授权失败。
  - 这说明 Reader 当前最大的未完成面已经从 “书源后段链路不通” 明确转移到“搜索阶段大量外部 4xx/空结果/网络问题，以及少量关键词不适配的真实样本”。
- 2026-05-04 再往后的搜索阶段 triage 又验证了两条新的事实，并据此补了两类通用对齐：
  - `AnalyzeUrl` 里的 `charset` 语义此前 Reader 仍未完整实现：
    - URL option `charset` 会对 query/body 做指定编码。
    - `java.encodeURI(str, enc)` 支持第二个 charset 参数。
  - Reader 现已在 `search/book/content/script` 四条请求构造链上统一支持 `charset` 编码，并在 fetcher 里按响应头 charset 自动把文本解码为 UTF-8。
  - 真实收益：
    - `src_4c0843cc0b0f267bdd9802e6` / `民间故事（优+）` 的搜索已从乱码空结果推进为 `search items=10`，证明编码链对齐命中了真实老站。
  - 为了减少 heterogeneous source collection 的评测偏差，真实 gate 现在会对“故事/诗词/国学/盐选/轻小说/武侠”等 source name 模式尝试 source-derived keyword fallback，并继续把实际使用的 `keywordUsed` 写入报告。
  - 这轮 fallback 不再是纯猜测：至少 `民间故事 -> 民间故事`、`国学经典 -> 论语`、`狸猫故事 -> 怪男孩@绘本 / 小熊嘟嘟上幼儿园@听书` 的单源 debug 都已证明原先失败并非 parser 主链不通，而是默认关键词不适配。
  - fallback 继续暴露出两条新的 Reader-side 缺口，现已补齐：
    - `book.getVariable()/putVariable()` 与 `chapter.getVariable()/putVariable()` JS 绑定。
    - `ruleBookInfo.init = @put:{...}` 这类 “只有 put、没有 selector” 的 step 应保留当前输入上下文，而不是继续送进 CSS parser。
  - 最新真实 100 候选 gate 口径：
    - `fullSuccessCount=33`
    - `search passed=38`
    - `info passed=35`
    - `toc passed=34`
    - `content passed=33`
  - 关键结论：
    - 当前报告里已不再出现 `implementation_or_rule_failed` 或 `source_rule_script_failed`。
    - 目前剩余失败几乎完全收敛为：
      - `source_toc_empty=1`：`猫眼看书（优++）`，上游 `4005` 认证失败
      - `source_upstream_non_json_response=1`：`七猫小说（优++）`
      - `source_authorization_failed=1`：`旭日小说（优+）`
      - 大量 `source_search_empty_result / source_upstream_http_400 / 网络类失败`
- 这意味着 Reader 当前离“绝大多数书源生产可用”仍有明显距离，但阻塞已经不再主要是 JS/CSS/请求链主实现，而是远程站点状态、授权、网络、以及 heterogeneous source collection 在搜索关键词上的天然分布差异。
- 2026-05-04 最新 source-specific 深层 triage 又命中了两条此前仍混在搜索失败里的通用请求链缺口：
  - Legado `AnalyzeUrl` 默认会对 URL query 做 UTF-8 编码；Reader 之前只在显式 `charset` 场景才编码。
  - Legado `BaseSource.getHeaderMap()` 会在每次请求前解析 `source.Header` 的 `@js:` / `<js>` / 宽松 JSON；Reader 之前只在搜索阶段做了这层 header JS，info/toc/content 与 `java.ajax` 仍只吃静态 header。
- 真实收益已经明确而且量级很大：
  - `src_fd0d561a90307d803dc40dca` / `国学書库（优+）` 已从 `search_failed: unexpected status: 400` 推进为单源完整成功。
  - 最新真实 100 候选 gate 已提升到：
    - `fullSuccessCount=44`
    - `search passed=53 failed=47`
    - `info passed=49 failed=4`
    - `toc passed=46 failed=3`
    - `content passed=44 failed=2`
  - `failureSummary` 中此前成片出现的 `source_upstream_http_400` 已彻底消失，这说明这轮修的是通用请求构造，而不是单源特判。
- 当前剩余失败面再次收窄：
  - 主体已是 `source_search_empty_result=18`、`source_upstream_http_403=5`、DNS/TLS/timeout/connection_reset、以及 3 条 `source_toc_empty`。
  - 仍有 1 条 `implementation_or_rule_failed`：`src_548b68eea2029ceef58f7a05` / `番茄小说（优+）`，现已下沉到 info 阶段 `invalid json`。
- 当前结论需要同步更新：
  - Reader 距离“绝大多数书源生产可用”仍有距离，所以任务 3 仍未完成。
  - 但高频 Reader-side blocker 已从“搜索 query 未编码、header JS 只在搜索阶段生效”继续后移，后续更适合围绕少数残余实现类样本做深挖，而不是再广撒网泛修基础链路。
- 2026-05-04 再往后的 source-specific triage 又补掉了四类更深一层的通用 gap：
  - analyzer `@js:` 规则里的 `result` 现在会在 `*goquery.Selection` 场景下自动转成 runtime selection binding，而不是裸 Go 值。
  - selection binding 补了 `parentNode()`，直接命中 `轻之文库（优++）` 目录标题拼装链。
  - CSS `class.` 语法糖支持多 class，`class.entry-content clearfix` 会归一为 `.entry-content.clearfix`。
  - `book` 级请求链在 info/toc 阶段也会像 content 一样，先展开 `@get:{...}` / `{{...}}` 后再真正发请求。
  - `splitEmbeddedJSRules()` 也不再丢掉 `<js>##... </js>` 这类 replace-only block 的 `##regex` 语义。
  - CSS 在 descendants 为空时会做一次“当前节点自匹配”兜底，命中 `tag.a@href` 这类规则。
- 真实收益已经用单源和最新报告双重验证：
  - `盐选文库（优+）`、两条 `轻之文库（优++）`、`番茄小说（优+）`、`风扇枕说（日+)` 均已被推进为完整成功。
  - 这说明当前 remaining gap 已进一步从“真实后段规则链仍不通”收缩到“少数更边缘的源级脚本 / 固定预算下的 late timeout / 外部站点状态”。
- 但同时也要更新对 batch gate 的认识：
  - 在当前网络条件下，`READER_REAL_E2E_TIMEOUT_SECONDS=600` 已明显开始主导失败分布。
  - 最新带更高 `go test` 外层超时的真实报告中，`network_timeout=29`，而且这些条目里大量 message 是统一的 `context deadline exceeded`。
  - 因此当前 100 候选 `fullSuccessCount` 会受实时网络和固定 600 秒总预算显著影响，不能再把单次总数简单当成纯实现正确性的代理。
- 2026-10-14 新一轮真实 100 候选 gate（预算 1800s、`CONTINUE_AFTER_SUCCESS=true`、`go test -timeout=2000s`，本轮 `network_timeout` 仅 2，预算不再挤压结果）触发了三路 source-specific triage，结论与修复如下：
  - 通用实现缺口（已修）：`小米阅读（优+）/小米书城（优+）` content 阶段 `invalid json: invalid character '<'` 的根因不是站点改回 HTML，也不是 JSON 误判，而是 Reader 缺少 Legado `WebBook.getContentAwait` 对一级目录（卷名）章节的短路语义——卷章节 URL 是 toc 阶段合成的 `title+index` 占位串，抓取必然得到 404/HTML，再喂给 `$..` JSONPath 规则报 `ErrInvalidJSON`。已在 `internal/app/content/content.go` 补上 `isVolume && url 前缀 title` 短路（返回 `chapter.Tag`，不抓取、不缓存、不跑 processor），并补 3 条确定性回归测试。
  - 站点/源侧（不改代码，证据确凿）：报告里的 `TypeError: Cannot read property 'join'...` 实际属于同名源 `m.zym888.com`（`src_ae48dc2981afcd5dbeea4255`），其 `<js>` 第 4 行 `.match(...)` 因 `java.ajax` 拿到 openresty 403 WAF 页返回 null 而失败；`炫动小说` 的 `script_timeout` 是上游 TCP 可连但 HTTP 永不响应，`java.ajax` 阻塞至 10s 沙箱预算；`fq-book.netsite.cc` 共享 Xposed 云手机 API 后端自返 `InvocationTargetError` 后宕机。三者均无 Reader 通用修复点（且禁止站点特判）。git 事实：`a6def37..HEAD` 零提交，源 JSON `lastUpdateTime` 早于 05-05 快照，排除 Reader 回归。
  - 失败分类器（已修）：`net/http: TLS handshake timeout`、裸 `EOF`/`unexpected EOF` 此前漏配网络类，导致起点读书被误归 `source_search_empty_result`、阿巴小说/全本小说被误归 `implementation_or_rule_failed`，污染"剩余实现缺口"口径。已在 `internal/app/diagnostic/failure.go` 及 e2e 镜像分类器同步补齐并加表驱动回归测试；对旧报告 sweep 复核共 10 条翻转、0 条误翻。
  - 本轮 100 候选 gate 与仓库内快照（`test/reports/real_source_collection_e2e_report.*`，报告时间 `2026-10-14T10:05:57Z`）一致：`fullSuccessCount=39`，search 46/54、info 43/3、toc 41/2、content 39/2；修复后 `invalid json` 与 `implementation_or_rule_failed` 已清零，剩下的 content 两条失败为上游/源侧问题（全本小说 POST EOF、七猫 JS 语法错）。
  - 单源复核：`小米阅读（优+）` 真实 debug 全链路 search(17)→info→toc(442)→content 短路通过（卷章节返回 `tag ?: ''` 即空正文，与 Legado 一致）。
