# Reader 全项目深度审核报告（2026-10-21）

- 审核对象：`/root/Coding/General/Reader` @ `ee143fd`（master，工作树仅有用户原有未跟踪文件，审核未改动任何源码）
- 审核方式：主审拆分 18 个边界互斥的切片，由 18 个子代理（`cn/glm-5.3-flash`）并行只读审核；主审对 P0/P1 抽样复核、调级、去重，并追加两轮真实网络复现（搜索、校验）
- 级别定义：**P0** 崩溃/数据损坏/可利用漏洞/核心功能不可用；**P1** 高影响错误或明显性能瓶颈；**P2** 中等缺陷/边界错误；**P3** 低影响/代码质量
- 每条问题字段：级别、类别、直接相关文件、间接相关文件、问题描述、修改方案、验证方式（详见第 6 节分切片明细）

## 1. 结论摘要

| 级别 | 有效问题数 |
|---|---|
| P0 | 1 |
| P1 | 14 |
| P2 | 57 |
| P3 | 108 |
| 合计 | 180（含 2 万源实测新增 4 条、上调 2 条；另有 10 条重复项已合并到主条目，11 条记录/证据项不计入） |

核心判断：

1. **"数百书源搜索极慢"已复现，根因已定位**：578 源真实网络下单次 CLI 搜索 131–165 s。决定因素依次是：默认无每源 deadline（S01-01，CLI `--timeout` 默认 0、HTTP 搜索/发现入口从不设置 Timeout），慢源按 `http.Client.Timeout=30s` 粒度拖住批次；全局并发默认 8（`config.go:73`），578 源约 72 批；抓取层把所有错误（含 POST）都重试（S08-03），且 30 s 总超时被多跳重定向共享（S08-01/02）；单源内分配过重导致 GC 占 CPU 28.4%（S05-01、S01-03）。
2. **2 万源 OOM 已定位（见 2.3 节）**：直接触发点是 `single_source.go:133` 对每个结果条目整份复制响应体（S00-01，P0），9 分钟内仅这一行就分配 15.6 GB，单源最坏可达 10 MB × 条目数。常驻底座包括：校验任务持有整批完整书源（S00-02，约 230 MB/任务）、事件流全量保留（S03-10）、搜索一次性挂起 2 万个 goroutine（S01-02），并发任务越多叠加越多。用户的三个假设中，① CookieManager.cache 无界、② checkStream.events 全量保留都成立，但都不是主因；③ `goja.New` 每次 Eval 新建属于分配/GC 压力（S05-01），不是泄露，`programCache` 是容量 1024 的 LRU。578 源规模下，同进程连续 3 次搜索 GC 后堆为 34.7→38.1→39.3 MB，没有无界增长。另外，按源或路由的 map 只增不删（S01-07、S08-12）、goja VM 无内存上限（S04-03）、fetch 换源后残留章节缓存（S16-01）属于长期运行风险。
3. **校验链路本身没有卡死点**：578 源 16 并发真实网络 2 m 7 s 跑完；P50 1.2 s / P90 6.0 s / P99 30 s / max 64 s。真正的瓶颈是持久化：每源 3–4 次独立写事务，加上 `SetMaxOpenConns(1)`，全部串行（S03-02、S11-03）。另外，API 创建的任务绑定在请求 ctx 上，请求一结束任务就被中止（S03-01，P1 功能缺陷）。
4. **安全面整体扎实**：SSRF 在拨号阶段校验，抗 DNS rebinding；超时中断覆盖所有阻塞绑定；未发现沙箱逃逸。剩下的风险集中在：依赖中有可达的已知 CVE（S17-01，P1）、VM 内存炸弹（S04-03，P1）、SVG 代理 XSS（S13-01）、重定向不校验 scheme（S08-13）、脚本可注入任意请求头（S04-02）、错误原文回显（S10-03）、脱敏遗漏（S09-01）、文件沙箱不检查符号链接（S04-01）。
5. **功能完整性**：Requirement 一期 16 项必做全部有真实实现，OpenAPI 35 个端点与 handler 一一对应（S18）。偏差集中在规则语义（`@put` 截断与键序随机 S06-01/02，`||`/`%%` S06-06，`class.a.b` S07-02，jsonpath `=~` S07-03）、目录/正文语义（S12-01~04、S13-02）、替换规则被误永久禁用（S14-01）。

## 2. 已知问题复现数据（真实网络，578 源：docs/Reader纯协议可用书源_578个.json）

### 2.1 多源搜索（S01 追加复现）

| 场景 | 耗时 | 失败源 | 峰值 RSS / GC 后堆 | 结束后 goroutine |
|---|---|---|---|---|
| CLI `search 斗破苍穹` 默认参数 | 131.4 s | — | maxRSS 94 MB | — |
| 同进程第 1 次 | 164.7 s | 315 | 34.7 MB | 216 |
| 同进程第 2 次 | 133.8 s | 316 | 38.1 MB | 284 |
| 同进程第 3 次 | 148.8 s | 313 | 39.3 MB | 231 |
| 60 源子集，并发 8 / 16 / `--timeout 3s` | 35.5 s / 30.1 s / 8.1 s | — | 42–48 MB | 9（基线） |

- 搜索进行中 goroutine 约 600–690，峰值 872：578 个每源 goroutine 一次性全部 spawn（S01-02），再加上 netpoll。
- 单次搜索分配 317.6 MB（httptest 模拟）。主要分配点：`EvalWithBindings` 106.6 MB（33.6%），`installJavaImporterCompat` 64.5 MB，`SourceBindings` 44.1 MB，每条结果新建 `ScriptAdapter` 33.6 MB。

### 2.2 书源校验（S03 追加复现）

- 578 源、16 并发：总耗时 2 m 6.9 s，RSS 峰值 156 MB，goroutine 峰值 452，结束后收敛。
- 结束后的 goroutine：165 个停在 readLoop，10 个停在 writeLoop，都是空闲 keep-alive 连接，没有任何 goroutine 卡在 Reader 的 fetcher/script/rule/regex 层。
- 失败 523/578。逐类聚合后用 curl 对照核对，未发现 Reader 在请求构造、编码、header、cookie 方面的缺陷，也没有 SSRF 误拦。高失败率来自书源本身失效和反爬 JS 挑战：空结果 160、HTTP 4xx/5xx 141、其他（JS 挑战 / HTTP/2 PROTOCOL_ERROR / EOF）125、TLS 46、超时 30、DNS 5、`{key}@get:URL` 写法 3、SSRF 正确拦截 2。
- `[待核]` "其他"中的 HTTP/2 PROTOCOL_ERROR 和 3 个 `{key}@get:URL` 源没有逐个对照 Legado 和 OkHttp 的行为，建议在修复阶段补一次归因。

### 2.3 2 万源实测（用户样本 sources_importable_22353.json，导入 20646 源 / 13674 host）

| 场景 | 耗时 / 吞吐 | 峰值 heapInuse | sys | goroutine | 结论 |
|---|---|---|---|---|---|
| 导入 22353 条 | 14.2 s（新增 20646、更新 1150、跳过 462、失败 95） | 537 MB | 665 MB | 3 | 正常，GC 后归零 |
| ListAll 全量 | 20.2 s | 282 MB（常驻 228 MB） | 314 MB | 2 | OFFSET 分页 O(n²)（S00-03） |
| 全量校验单任务 9 min | 1113/20646，2.06 源/s，全量约 2.8 h | 591 MB | 984 MB | 117–286 | 常驻约 230 MB 为整批 BookSource（S00-02）；alloc 共 35 GB，其中 15.6 GB 来自 single_source.go:133（S00-01） |
| 全量搜索 6 min（全局并发 8） | 完成约 230 源，20415 源超时，结果 1234 条 | 511 MB | 644 MB | 20666 | 全量 spawn（S01-02）；全量约 4.7 h |

- 在单进程、单任务、10 s 采样的条件下，heapInuse 在 350–600 MB 之间波动，没有出现单调上涨；sys 出现过 590→984 MB 的跳变，对应 S00-01 的短时尖峰。
- OOM 的成因判定：S00-01 的"大页 × 多条目"整份复制是**尖峰触发器**（10 MB 上限 × N 条 × 16 并发）；S00-02（每任务约 230 MB）、S03-10（事件流）、S01-02（2 万 goroutine）是**常驻底座**，会随并发任务数、请求数线性叠加；Go 默认 GOGC=100 且未设 `GOMEMLIMIT`，RSS 可达到存活堆的 2 倍。`[推测]` 用户在 serve 下同时跑全量校验和全量搜索，或者重复提交全量任务，底座翻倍后再遇上尖峰，就会 OOM。

## 3. 主审复核与调级

| 条目 | 子代理级别 → 终审级别 | 理由 |
|---|---|---|
| S04-01 | P0 → P2 | 脚本 API 无任何创建符号链接的能力（files.go/unzip 不写 symlink，grep `Symlink|ModeSymlink` 零命中），利用前提是 DataDir/script-files 内已存在指向外部的链接（需本机写权限），属纵深防御缺口，由 P0 降为 P2 |
| S13-01 | P1 → P2 | 鉴权仅接受 `Authorization: Bearer` 头（middleware/auth.go），浏览器导航无法携带，仅在空 token + 回环监听模式下可被利用，由 P1 降为 P2 |
| S01-02 | P2 → P1 | 2 万源实测：搜索一开始就挂起 20666 个 goroutine，6 分钟只完成约 230 源，其余 20415 源全部超时；规模越大越不可用 |
| S03-10 | P3 → P2 | 2 万源下单任务事件约 4.1 万条，每条带完整 item；`[推测]` 按 1113 源时 `CheckSource` 常驻 8.6 MB 外推，全量约 160 MB；收敛后要等下一次 Open/Publish/Subscribe 才会被回收 |
| 用户假设 ③ runtime.go:175 每次 `goja.New` | 不是泄露，维持 S05-01（P1，分配/GC 压力） | Eval 结束后 VM 不再被引用，GC 可以回收；`programCache` 是 LRU，容量 1024（script/cache.go:22），并非没有上限。实测它造成的是分配抖动：goja `_put`/`setOwnStr`/`expand`/`init` 合计 3.6 GB，`SourceBindings`+`javaBindings` 2.3 GB（9 分钟内），推高 GC 频率，但不留常驻 |
| 用户假设 ① CookieManager.cache | 成立，新增 S00-04（P2） | 确实无界，但单条目很小，不是 OOM 主因 |
| 用户假设 ② checkStream.events | 成立，S03-10 上调为 P2 | 见上行 |

排除项（不计入统计）：

- S01-08：历史问题核对记录（已修复），非问题
- S03-16：实测证据，并入第 2 节结论
- S03-17：实测证据，并入第 2 节结论
- S03-18：失败归因证据，并入第 2 节结论；改进建议见 S03-18 原文
- S06-18：历史问题核对记录
- S08-16：阴性结论（vet/-race 通过）
- S17-02：审核期间子代理在仓库根临时放置 go 文件导致的瞬时现象，审核结束后工作树已干净，非项目问题
- S18-12：记录项，确认无误
- S18-13：记录项，确认无问题
- S18-14：记录项，与 Legado 一致
- S18-15：记录项，非缺陷

重复/同根合并（明细保留，统计只计主条目）：

- S10-09 → S01-01
- S11-01 → S01-04
- S09-02 → S01-07
- S08-10 → S01-07
- S10-02 → S15-01
- S09-04 → S07-06
- S02-03 → S01-03
- S12-06 → S01-03
- S14-07 → S01-03
- S08-06 → S04-08

## 4. 修复路线（按收益排序）

| 批次 | 目标 | 条目 |
|---|---|---|
| 0 立即（OOM） | 2 万源 OOM：每页响应体只转换一次 string 并共享；校验 Runner 只持有 sourceID、按槽懒加载；多源搜索改为固定 worker 池；ListAll 改 keyset/流式；同进程全量任务限 1；serve 默认设 `GOMEMLIMIT` | S00-01、S00-02、S01-02、S00-03、S03-10、S00-04 |
| 1 立即 | 搜索提速：默认每源 deadline（如 15 s，CLI/HTTP/发现统一注入）、抓取只对幂等方法和瞬时错误重试、Transport 增加 `ResponseHeaderTimeout`，Dialer 超时跟随配置 | S01-01、S08-01、S08-02、S08-03、S08-05 |
| 1 立即 | 安全：升级 x/net、x/text、antchfx/xpath；VM `SetMemoryLimit`；SVG 代理加 `nosniff`+CSP 或拒绝 SVG；重定向校验 scheme | S17-01、S04-03、S13-01、S08-13 |
| 1 立即 | 校验任务正确性：任务改用 `context.WithoutCancel`，由 Manager 统一管理生命周期；Cancel 与终态写入加状态机保护；持久化失败不得标 succeeded | S03-01、S03-04、S03-05、S03-08 |
| 2 近期 | 降低分配：每源复用 Analyzer 和 SourceBindings，VM 级 JavaImporter 对象改为惰性/原型共享，每条结果只 `SetContent`；`text.` 规则不再全量 ownText | S05-01、S01-03、S02-03、S12-06、S14-07、S06-07、S07-01 |
| 2 近期 | 持久化：校验结果批量写（每 N 源一个事务）；ListAll 只取搜索所需列；读写分离连接池（写 1、读 N）；补 `dur_chapter_time` 索引 | S03-02、S01-04、S11-03、S11-02、S03-06 |
| 2 近期 | 长期运行的资源：按源/路由的 map 加 TTL 淘汰或在删源时清理；任务结束 `CloseIdleConnections`；事件流只保留摘要；fetch 换源后清理残留缓存 | S01-07、S08-12、S03-15、S03-10、S16-01 |
| 3 语义 | 规则/目录/正文/替换的 Legado 对齐 | S06-01~06、S07-02~04、S12-01~04、S13-02~03、S14-01~06、S02-01 |
| 4 收口 | 错误回显、脱敏、导入递归上限、SSE 关闭、契约与文档漂移、CI 加 govulncheck/staticcheck | S10-01~04、S09-01、S15-01/02、S17-03~09、S18-* |

## 5. 问题总索引（按级别排序）

| 级别 | ID | 类别 | 标题 | 合并到 |
|---|---|---|---|---|
| P0 | S00-01 | 性能/资源 | 单源搜索对每个结果条目整份复制响应体，分配 O(条目数 × 响应体)，是 2 万源 OOM 的直接触发点 |  |
| P1 | S00-02 | 性能/资源 | 校验任务启动时逐个 FindByID 加载全部完整书源，并在整个任务期间持有（2 万源约 230 MB/任务） |  |
| P1 | S01-02 | 并发 | 每源搜索 goroutine 一次性全量启动：2 万源时同时挂起 20666 个 goroutine，仅靠信号量限流（2 万源实测上调） |  |
| P1 | S01-01 | 性能 | 默认（无 --timeout）多源搜索无每源超时上限，单源慢源拖住整次搜索 |  |
| P1 | S03-01 | 并发 | API 创建任务后 ctx 取消会中止整个后台任务（任务静默失败） |  |
| P1 | S03-02 | 性能 | 每源保存 3~4 次独立 DB 写（SaveItem + UpdateTaskProgress + diagnostics.Apply + 重启收敛时逐条 SaveItem），578 源时数百次写事务串行排队 |  |
| P1 | S04-03 | 性能 | goja VM 未设内存限制，脚本可分配多 GB 字符串（内存炸弹） |  |
| P1 | S05-01 | 性能 | 每 Eval 新建 goja.Runtime 并重建全套绑定：单次 ~280µs / 752 allocs，数百源并发搜索时成本放大 S×R 倍 |  |
| P1 | S06-01 | 功能 | @put 值正则 `@put:(\{[^}]+?\})` 无法匹配含 `}` 的值，嵌套对象/花括号值静默截断 |  |
| P1 | S06-02 | 功能/并发 | @put 多键 map 迭代顺序随机，键间依赖（b 依赖 a）结果不确定 |  |
| P1 | S07-01 | 性能 | `text.` 前缀规则对每个元素调 ownText，大文档 O(docsize×命中数) 慢且分配巨大 |  |
| P1 | S11-01 | 性能 | 书源/书籍列表查询 SELECT 全部大 JSON/HTML 列，多源搜索与发现全量拉取时放大内存与 IO | S01-04 |
| P1 | S14-01 | 功能|资源 | 大章节/取消请求会把合法正则替换规则永久禁用 |  |
| P1 | S14-02 | 性能 | reSegment 合段循环 O(n²)：shouldBreakMergedParagraphBuilder 每段整串拷贝 |  |
| P1 | S14-03 | 性能 | forceReSegmentSplit/deterministicReSegmentScore 每个句末符号对整个片段重新哈希，最坏 O(m²) |  |
| P1 | S17-01 | 安全 | 三个直接依赖存在已确认可达的已知 CVE（含规则引擎与脚本运行时路径） |  |
| P2 | S01-03 | 性能 | 单源搜索每次为每个条目重建 ScriptAdapter 并对每源重复执行 `installSafeGlobals`/`javaBindings` 的 VM 初始化，占单源分配 30%+ |  |
| P2 | S01-04 | 性能 | 全量源（含规则/原 JSON 大字段）一次性加载进内存 |  |
| P2 | S02-01 | 功能 | bookUrlPattern 直达详情页解析失败/无名字时返回空名 stub 条目，与 Legado 语义不符 |  |
| P2 | S02-02 | 性能 | 多源搜索同时保留 result.All 与 result.Items 双份、预览条目携带全量响应体，数百源时内存放大 |  |
| P2 | S02-03 | 性能 | 每条结果重复构造 itemAnalyzer、SourceBindings 绑定与 VariablesJSON 序列化，单源内逐条分配过多 | S01-03 |
| P2 | S03-03 | 并发 | diagnostics.Apply 与 check 流程对同一书源行的写覆盖无并发保护，多任务并行时互相覆盖诊断分组 |  |
| P2 | S03-04 | 并发 | 重复任务无防抖：同批源可并发重跑，Cancel/终态写库与新运行竞争（部分场景产生错误终态） |  |
| P2 | S03-05 | 并发 | Cancel 立即把任务标 cancelled，与 runner 的终态写入竞争，慢源被取消后 finished 计数可能丢失/回退 |  |
| P2 | S03-06 | 性能 | FindTask/ListItems 无分页：任务详情一次性返回全部条目（含 stages_json 反序列化），578 源任务每次轮询都全量构建 |  |
| P2 | S03-07 | 性能 | Publish 在锁内做扇出推送，事件风暴时发布与订阅竞争同一把锁；回放历史事件在锁外构造但有 O(events) 拷贝 |  |
| P2 | S03-08 | 错误处理 | runner.run 中 SaveItem/UpdateTaskProgress/diagnostics.Apply 失败只记 persistFailure，条目结果静默丢失，任务仍可能标 succeeded |  |
| P2 | S03-15 | 资源 | 真实网络 578 源全量校验后 165 条空闲 keep-alive 连接（165 readLoop + 10 writeLoop goroutine）在 IdleConnTimeout 到期前持续驻留，goroutine 峰值 452 |  |
| P2 | S04-01 | 安全 | 文件沙箱不校验符号链接，脚本可经 symlink 读/删/写 FileRoot 之外任意文件 |  |
| P2 | S04-02 | 安全 | java.get/post/connect 的第三/二参数 headers 未走 URL 选项校验与脱敏，脚本可注入任意请求头并强制携带任意 Cookie |  |
| P2 | S04-07 | 安全 | java.downloadFile 无文件大小上限，超大远程文件直接写入磁盘 |  |
| P2 | S04-08 | 安全 | 脚本 Cookie 域解析放宽到 eTLD+1，跨子域 Cookie 可被任意书源读取（设计权衡，中低） |  |
| P2 | S05-06 | 安全 | installSafeGlobals 中 JavaImporter 返回 vm.GlobalObject() 并挂 importPackage：全局对象直接暴露给脚本 |  |
| P2 | S06-03 | 功能/错误处理 | makeUpStep 中 {{}}/脚本模板缺 Script 时整条规则报错，Legado 中该类模板恒可用 |  |
| P2 | S06-04 | 功能 | GetURL 无结果返回空串，Legado 返回 baseUrl |  |
| P2 | S06-06 | 功能 | 顶层规则链 `&&`/`\|\|`/`%%` 语义与 Legado 不一致：无分隔符类型感知，`%%`/`\|\|` 被当成步进链 |  |
| P2 | S06-07 | 性能 | evalStep 每步都调用 putStepVars + makeUpStep，逐元素求值时重复展开 @get/{{}} 未缓存展开结果 |  |
| P2 | S06-08 | 性能 | asString 对 map/[]any 走 json.Marshal：大 Elements 列表逐元素序列化，正则/替换步骤开销大 |  |
| P2 | S06-09 | 安全/健壮性 | expandRawString 内 GetString 递归无深度限制，恶意/畸形规则可栈溢出 |  |
| P2 | S07-02 | 功能 | `class.a.b` / `tag.p.q` 多段语法与 Legado 语义不一致 |  |
| P2 | S07-03 | 功能 | jsonpath 不支持 jayway 的 `=~` 正则过滤运算符 |  |
| P2 | S07-04 | 功能 | parser.Split 对规则中的未闭合单引号硬失败，Legado 容错继续匹配 |  |
| P2 | S08-01 | 功能|性能 | Client.Timeout 覆盖整个请求周期，多跳重定向共享同一总预算，且 DNS 缓存命中路径仍消耗总超时 |  |
| P2 | S08-02 | 功能 | net.Dialer 的 30s 连接超时不受 WithTimeout 配置约束 |  |
| P2 | S08-03 | 功能 | Fetch 失败重试不区分幂等性，非幂等方法（POST）也自动重试 |  |
| P2 | S08-06 | 功能 | CookieManager.Save 未持久化的 cookie 丢失 Path/Domain 精确匹配，session cookie 域粒度过粗 | S04-08 |
| P2 | S08-13 | 安全 | fetchOnce 未校验重定向后的最终 URL scheme 仍为 http/https（checkRedirect 不检查 scheme） |  |
| P2 | S09-01 | 安全 | 日志脱敏存在多处遗漏：冒号分隔的 token、带引号的凭证值、自由文本中的 Bearer 密钥 |  |
| P2 | S10-01 | 资源 | 活跃 SSE 连接使优雅关闭必然超时，干净退出返回非 0 |  |
| P2 | S10-02 | 安全 | 远程导入 sourceUrls 递归无深度限制，可构造无限导入链（SSRF 放大/自 DoS） | S15-01 |
| P2 | S10-03 | 安全 | 多个错误映射分支把内部错误原文回显给客户端 |  |
| P2 | S10-04 | 功能 | 查询参数绑定错误返回纯文本，违反 Error schema 且无 traceId |  |
| P2 | S11-02 | 性能 | books.List 的 ORDER BY dur_chapter_time 无索引，全表扫描 + TEMP B-TREE |  |
| P2 | S11-03 | 并发 | SetMaxOpenConns(1) 使 WAL 读写并行能力完全失效，慢查询阻塞全部读写 |  |
| P2 | S11-04 | 功能 | SaveMany 双 ON CONFLICT upsert 与批内换址组合存在语义陷阱，可能把 URL 改写到他人地址/误删他人数据 |  |
| P2 | S11-05 | 性能（含历史功能问题仍存在） | replace_rules scope 子串 LIKE 全表扫描，且历史误匹配问题仍存在 |  |
| P2 | S12-01 | 功能 | 目录刷新后书籍元数据不更新、阅读进度不重映射 |  |
| P2 | S12-02 | 功能 | 章节去重的键与保留方与 Legado 不一致 |  |
| P2 | S12-03 | 资源（缓存一致性） | ReplaceByBook 全删全插不清正文缓存，索引漂移后正文与章节错位 |  |
| P2 | S12-04 | 功能 | RefreshInfo 无条件重置 book.Type，用户/流程设定的类型被覆盖 |  |
| P2 | S13-01 | 安全 | image/svg+xml 经图片代理原样回传且响应缺 nosniff/CSP，构成反射型 XSS 向量 |  |
| P2 | S13-02 | 功能 | title 规则在每个分页上重复执行，末页标题覆盖章节标题 |  |
| P2 | S13-03 | 功能/性能 | 并发分页分支没有页数上限 |  |
| P2 | S13-04 | 性能 | ExportTXT 每章触发 N+1 查询，长书导出极慢 |  |
| P2 | S13-05 | 并发/性能 | GetContent 无 singleflight，同章并发请求重复抓取并双写缓存 |  |
| P2 | S14-04 | 功能 | reSegment 私自增加 Legado 没有的两条切分变换，改变对话文本 |  |
| P2 | S14-05 | 并发|功能 | 规则快照共享指针被并发写：handleRuleFailure 数据竞争 + 禁用规则仍被逐章重放 |  |
| P2 | S14-06 | 功能 | 重加标题未做简繁转换；IncludeTitle=false 时仍执行标题域规则 |  |
| P2 | S14-07 | 性能 | parseBookList 每本书目新建 Analyzer，规则重复编译 O(条目×规则) | S01-03 |
| P2 | S15-01 | 资源|功能 | 远程 sourceUrls 导入无递归深度与条目数上限，自引用 URL 死循环 |  |
| P2 | S15-02 | 并发|资源 | 调试会话创建无并发上限，goroutine 与抓取可无限堆积 |  |
| P2 | S16-01 | 资源 | fetch 换源尝试与探测的章节缓存永久残留，SQLite 无限膨胀 |  |
| P2 | S16-02 | 资源 | exportTXT 非原子写，写中途失败截断损坏已有导出文件 |  |
| P2 | S17-03 | 质量 | CI 未运行 govulncheck / staticcheck / 覆盖率统计，lint 类门禁长期缺席 |  |
| P2 | S00-03 | 性能 | ListAll 按 LIMIT/OFFSET 分页，2 万源全量拉取 20 s（O(n²)） |  |
| P2 | S00-04 | 资源 | CookieManager.cache 按 domain 无界增长，未命中也缓存（2 万源样本 13674 host） |  |
| P2 | S03-10 | 资源 | Publisher 事件流在任务运行期及收敛后 5 分钟保留全部条目 item 对象；sweep 只靠后续活动触发（2 万源实测上调） |  |
| P3 | S01-05 | 并发 | 超时后的源仍会占用完整单请求预算直到 http.Client 超时（无 per-source deadline 的 warning 语义掩盖） |  |
| P3 | S01-06 | 并发 | `concurrency.Map` 任一失败即取消其余任务并丢弃全部结果，语义对多分页场景过于激进 |  |
| P3 | S01-07 | 资源 | ratelimit.Limiter 单源信号量 map 只增不减，长期运行进程随源数增长 |  |
| P3 | S02-04 | 资源 | 多源搜索 goroutine panic 时 WithTimeout 的 cancel 未调用，超时定时器泄漏 |  |
| P3 | S02-05 | 错误处理 | 字段规则求值错误被静默忽略，且不携带源标识；与 Legado 中止语义不一致 |  |
| P3 | S02-06 | 错误处理 | buildSearchRequest 将所有 Build 错误统一包装为 ErrUnsupportedSearchURL，错误分类失真 |  |
| P3 | S02-07 | 功能 | 多源搜索静默跳过空 searchUrl 源与 EnabledOnly 过滤源，无任何 Warning |  |
| P3 | S02-08 | 性能 | replaceInlineSearchExpressions 对每个内嵌表达式重建 searchURLBindings（含 SourceBindings 全量 map） |  |
| P3 | S02-09 | 功能 | 单源内去重按 bookUrl 丢弃，Legado 按整个 SearchBook 对象去重，语义不一致 |  |
| P3 | S02-10 | 错误处理 | search_timeout 分类在调用方主动取消时误标 |  |
| P3 | S02-11 | 功能 | renderSearchURL 中 `{{source.key}}` 与 `{{source.bookSourceUrl}}` 尾斜杠处理不一致 |  |
| P3 | S03-09 | 功能 | 事件流对"运行中但事件流缺失"的任务直接 500 而不是回退轮询；同一进程重启后 5 分钟保留期内流已丢失但状态机不知情 |  |
| P3 | S03-11 | 性能 | 取消后 `for _, src := range sources[started:]` 在 runner goroutine 内逐条 SaveItem（578 源最坏 578 次串行写） |  |
| P3 | S03-12 | 功能 | CLI 无 check 子命令（功能缺口，与 spec/文档对照） |  |
| P3 | S03-13 | 功能 | checkKeyword 对 `--`/`++` 的排除过宽，含这些子串的正常中文关键字被静默回退 |  |
| P3 | S03-14 | 错误处理 | 任务创建后立即 `UpdateTaskStatus(..., Running)` 用 context.Background() 且失败直接 return err（任务卡 pending 不可取消路径缺失） |  |
| P3 | S04-04 | 安全 | Function 构造器与 eval 可用，越过的全局仅在词法层屏蔽（低风险，且是 Legado 兼容层必需） |  |
| P3 | S04-05 | 质量 | installSafeGlobals 与 isForbiddenBinding 的禁止清单重复维护，存在漂移风险 |  |
| P3 | S04-06 | 性能 | scriptCallContext 派生 ctx 后 head/ajaxTestAll 等绑定未把"单请求超时"与 Eval 剩余预算取小，长超时参数可绕过 Policy.Timeout 总预算的语义边界（低） |  |
| P3 | S04-09 | 质量 | rewriteJavaCompatScript 的 rhinoLetConditionPattern 正则改写作用于含注释/字符串的任意脚本（既有历史问题的窄化版，剩余误改写风险低） |  |
| P3 | S05-02 | 性能 | rewriteJavaCompatScript 在每次 Eval 缓存命中路径上仍执行 1 个正则 + sha256 全文哈希 |  |
| P3 | S05-03 | 资源 | NamespacedCache 过期条目仅靠 Get 惰性清理，写满后被过期条目占位，且淘汰循环可能提前触发 |  |
| P3 | S05-04 | 性能 | queryTTF 每次调用对同一字体数据全量重解析，无解析缓存 |  |
| P3 | S05-05 | 性能 | scriptGetElementAt 对非 JSON 输入每次都做 json.Valid 全文扫描 |  |
| P3 | S05-07 | 质量 | EvalWithBindings 超时后 result 绑定导出对象可能逃逸共享引用 |  |
| P3 | S05-08 | 质量 | logBuffer.Append 每条消息都跑 RedactString + 超长截断用字节切片可能切断多字节 UTF-8 |  |
| P3 | S06-05 | 功能 | GetURLList 去重，Legado getStringList(isUrl=true) 不去重 |  |
| P3 | S06-10 | 功能/性能 | sourceRuleCache 只缓存分词，makeUpStep 后 expanded 串重新走 splitReplace 但未回写缓存；`##` 分割 strings.Split 无转义语义 |  |
| P3 | S06-11 | 功能 | parser.Split 的 `&&`/`%%` 匹配未考虑引号/平衡组外转义，validateBalanced 与 Split 对 `\` 处理不一致 |  |
| P3 | S06-12 | 性能 | lookupVariable 每次调用遍历 carriers 并 reflect.FieldByName，逐元素热点路径反射开销 |  |
| P3 | S06-13 | 功能 | GetElements 对多步链 `&&` 各步独立取元素后仅保留最后一步结果（selectElements out 覆盖），Legado 同样逐步覆盖但 Reader 丢失中间 step 的 replaceRegex |  |
| P3 | S06-14 | 性能 | containsScriptRule/splitEmbeddedJSRules 大小写折叠重复 ToLower，分词热点小分配 |  |
| P3 | S06-15 | 性能 | GetURL 每项调用 unescapeHTMLEntities + Absolute + TrimSpace，ParseCustom 重复解析属性 JSON |  |
| P3 | S06-16 | 并发/质量 | evalTemplateExpr 的 `$.` 前缀分支 `copy := *a` 共享 vars map，嵌套求值中 put 写变量会泄漏到外层 Analyzer |  |
| P3 | S06-17 | 功能 | evalPutRule 先 lookupInputField 再 GetString：put 值为纯字段名时语义与 Legado getString 不同 |  |
| P3 | S07-05 | 质量（并发正确性） | `optionValue` 按大小写不重复键取值时结果不确定 |  |
| P3 | S07-06 | 功能 | `{"type":""}` 空串触发 hex 响应编码，Legado 视为未设置 |  |
| P3 | S07-07 | 质量 | 疑似死代码：LooksLikeFormBody 与 ApplyRetryOption 无生产消费方 |  |
| P3 | S07-08 | 并发 | lru.GetOrCompute 锁外 compute，并发首扫同 key 重复编译（历史问题，评估为不修，记录） |  |
| P3 | S07-09 | 功能 | BodyJS 在 plain-HTTP 回退路径上执行，与 Legado webJs 仅 WebView 生效的语义有偏差 |  |
| P3 | S07-10 | 功能 | 正则替换串中裸 `$` 的展开语义与 Java 有边界差异（记录） |  |
| P3 | S08-04 | 质量 | 非 gzip 响应嗅探出 gzip magic 时，解压后的 Content-Encoding 判断与重试语义丢失原始 body 限长一致性 |  |
| P3 | S08-05 | 质量|性能 | gzip 解压误报：`unexpected EOF` 在截断（超限）时被当作网络错误导致重试 |  |
| P3 | S08-07 | 性能|并发 | CookieManager.Load 对持久化过期 cookie 逐条同步 Delete，每条一次 SQLite 往返且不加锁保护缓存一致性 |  |
| P3 | S08-08 | 性能|质量 | mapToCookie 超长截断用 O(n²) 的 strings.Join 且静默丢弃后续 cookie |  |
| P3 | S08-09 | 质量 | mergeQuery 修改调用方传入的 url.Values 并与 Legado 分页占位符行为有偏差（跨切片：占位符逻辑在 url 规则层） |  |
| P3 | S08-10 | 资源 | rateLimiter records 无淘汰，数百书源长期运行下 map 无界增长 | S01-07 |
| P3 | S08-11 | 性能 | dnsCache 并发解析同一 host 会重复发起 DNS 查询（无 singleflight） |  |
| P3 | S08-12 | 资源 | 客户端连接池缓存无淘汰，自定义代理路由（routeKey.proxy 含完整代理 URL）可无界增长 |  |
| P3 | S08-14 | 安全 | proxyFunc 在 allowPrivate=false 时对目标 host 的本地解析校验有 TOCTOU：代理侧远端解析结果与本地不同（已在注释声明，未缓解） |  |
| P3 | S08-15 | 质量 | decodeTextResponseBody 错误时返回原始 body 且 Fetch 吞掉解码错误（err == nil 判断使非文本解码失败静默降级） |  |
| P3 | S09-02 | 资源 | 每源 map 永不淘汰：ratelimit sources 与 fetcher rateLimiter records 无界增长，sourceChan 每请求竞争全局锁 | S01-07 |
| P3 | S09-03 | 功能 | config 缺少取值校验：READER_ENV 大小写敏感且不校验合法值，ContentChineseConverterType 无上限 |  |
| P3 | S09-04 | 功能 | requestoption "type" 选项空白字符串语义与 Legado 不一致（空白触发 hex 编码） | S07-06 |
| P3 | S09-05 | 功能 | 显式 Content-Type 时 body 不按 charset 编码，charset 选项对该 body 失效 |  |
| P3 | S09-06 | 资源 | chinese 词典加载失败被永久缓存，无重试路径 |  |
| P3 | S10-05 | 功能 | 导入接口 409 冲突状态码未在 OpenAPI 声明 |  |
| P3 | S10-06 | 功能 | check 事件流无 `id:` 字段、不支持 Last-Event-ID 断线续传，重连全量重放 |  |
| P3 | S10-07 | 安全 | 客户端提供的 X-Trace-Id 未做任何校验即回显 |  |
| P3 | S10-08 | 质量 | READER_DEBUG_BODY_LIMIT 配置项从未被消费 |  |
| P3 | S10-09 | 性能（与已知"超多书源搜索慢"直接相关；深挖归 S01/S02，此处仅 API 装配侧） | 搜索/发现请求未设置单源超时，Timeout 通道全程为空 | S01-01 |
| P3 | S10-10 | 性能 | ExportBookTxt 每请求把整本书写满 /tmp 再回读，无流式与磁盘水位保护 |  |
| P3 | S10-11 | 性能 | sourceIds 数组无上限，单请求可驱动海量逐条查询 |  |
| P3 | S10-12 | 质量 | check 任务 ID 生成与领域对象构造落在 handler 层 |  |
| P3 | S11-06 | 质量 | scanBook 吞没 download_urls JSON 反序列化错误（历史问题部分修复不彻底） |  |
| P3 | S11-07 | 性能 | DeleteExpired 全表扫描 cache_entries（含正文大 BLOB） |  |
| P3 | S11-08 | 性能 | keyword/group 过滤与 group 前缀匹配均为全表扫描，无可用索引 |  |
| P3 | S11-09 | 并发 | 双进程并发迁移无互斥，PRIMARY KEY 冲突导致后启动方启动失败 |  |
| P3 | S11-10 | 功能 | cookie_repository ListByDomain 不过滤过期行（历史问题仍存在，跨切片：fetcher 侧同样未过滤） |  |
| P3 | S11-11 | 功能 | BookProgress.Name/Author 携带但持久层完全忽略，与 Legado 进度归属校验语义不符（跨切片：app 层未传） |  |
| P3 | S12-05 | 功能 | preUpdateJS 修改的 book.variable 不持久化 |  |
| P3 | S12-06 | 性能 | 万章目录逐章新建 Analyzer 并复制变量 map，内存与 CPU 开销大 | S01-03 |
| P3 | S12-07 | 性能 | 并发分页分支对单页 nextURL 数量无上限 |  |
| P3 | S12-08 | 并发 | 并发分页共享同一 *book.Book 指针，URL 选项 JS 可并发读写 |  |
| P3 | S12-09 | 功能 | PreviewSearchBookFromInfo 传 canRename=true，与 Legado 搜索→详情路径的 false 不一致 |  |
| P3 | S13-06 | 功能 | replaceRegex 应用后缺少 Legado 的逐行 trim 与全角缩进 |  |
| P3 | S13-07 | 性能 | 正文处理热路径存在多余整本拷贝与 O(n·m) 替换 |  |
| P3 | S13-08 | 功能 | __READER_IMG_N__ 占位符与正文文本碰撞导致数据替换 |  |
| P3 | S13-09 | 资源/质量 | 图片缓存键含 BookID/DecodeMode 造成同图多份缓存 |  |
| P3 | S13-10 | 安全/性能 | 图片代理是已鉴权的任意公网 URL 抓取端点，无按调用方的速率约束 |  |
| P3 | S14-08 | 功能 | reduceLength 边界与 Legado 不一致：倒数第二段也会被强制切分 |  |
| P3 | S14-09 | 功能 | `<js>` exploreUrl 结束检测要求完整 `</js>`，且 JS 失败无 ERROR 类目回退 |  |
| P3 | S14-10 | 功能|性能 | discoverSource 以 categories[0].Title 充当 URL 兜底；Categories 无单飞（single-flight） |  |
| P3 | S14-11 | 质量 | handleRuleFailure 对同一次失败双重告警日志 |  |
| P3 | S14-12 | 性能 | rule.Validate 每次调用重新编译正则，绕过共享 LRU |  |
| P3 | S15-03 | 质量 | ScriptIntegration.LoginCheck 缺少 nil runtime 守卫，与 HeaderMap 不一致 |  |
| P3 | S15-04 | 功能 | loginUrl 为纯 URL 且 loginUi 非空时被整体当脚本文本执行 |  |
| P3 | S15-05 | 并发 | Publisher 并发发布时 SSE 事件顺序可能与 EventID 顺序不一致 |  |
| P3 | S15-06 | 功能 | 同 sourceId 不同 bookSourceUrl 的批量导入条目绕过 pending 去重，静默互覆 |  |
| P3 | S15-07 | 性能 | importSourceURLs 串行抓取且无整体 deadline，多 URL 时请求可长时间挂起 |  |
| P3 | S15-08 | 功能 | runFromBook 中 FindByID 失败的失败事件 stage 恒记为 "info" |  |
| P3 | S16-03 | 质量 | --probe/--workers/--max-sources 只校验下限，无上限 |  |
| P3 | S16-04 | 质量 | download --json 失败列表为 nil 时输出 null 而非 [] |  |
| P3 | S16-05 | 并发 | 下载重试 wave 中被取消的章节静默消失于 Failed 统计 |  |
| P3 | S16-06 | 质量 | root.go 对 ctx 的 AfterFunc(stop) 冗余注册 |  |
| P3 | S16-07 | 性能 | bootstrap 每次启动（含每条 CLI 子命令）跑 3 条全表扫描清理语句【历史问题 C-021 仍存在】 |  |
| P3 | S17-04 | 质量 | docker-publish.yml 发布前测试未开 -race，发布门禁弱于 CI |  |
| P3 | S17-05 | 质量 | internal/domain/rule 与 internal/infra/concurrency 零测试零覆盖 |  |
| P3 | S17-06 | 质量 | 两处测试用固定 sleep 断言时序/资源回收，存在 flaky 风险 |  |
| P3 | S17-07 | 质量 | internal/api/handlers 承载可测试的业务逻辑（导出文件名/Content-Disposition 构造、导出临时文件编排），handler 层边界渗漏 |  |
| P3 | S17-08 | 质量 | Makefile/scripts/verify.sh/ci.yml 三处门禁清单漂移 |  |
| P3 | S17-09 | 质量 | 两处测试文件不符合 gofmt |  |
| P3 | S18-01 | 功能 | `GET /books/{bookId}/export.txt` 端点未在 docs/API.md 覆盖（文档与代码不一致） |  |
| P3 | S18-02 | 功能 | 版本号硬编码三处不一致风险：`/version` 返回 0.1.0，发布说明为 0.0.1 |  |
| P3 | S18-03 | 功能 | `bookSourceType` 缺失时 OpenAPI 请求体校验与 Legado 语义冲突（导入路径一致、`POST /book-sources` 路径不一致） |  |
| P3 | S18-04 | 功能 | openapi.yaml `BookSource.required` 与导入默认值文档矛盾（lastUpdateTime 必填 vs ApplyLegadoDefaults 容忍缺失） |  |
| P3 | S18-05 | 功能 | `check.progress` 事件类型在 OpenAPI/任务枚举中声明但生产代码从不产生（契约冗余项） |  |
| P3 | S18-06 | 功能 | `docs/API.md` 未覆盖 `PATCH /book-sources/{sourceId}/explore-enabled` 之外仍缺 `GET /version`、`GET /openapi.yaml` 及 fetch 相关细节（历史问题 R-139 部分仍存在） |  |
| P3 | S18-07 | 功能 | `POST /book-sources/{sourceId}/login` 提交空 values 的 501/404 语义与 R-035 验收标准部分偏差（脚本失败 502 依赖 ErrLoginCheckFailed 映射） |  |
| P3 | S18-08 | 质量 | openapi.yaml `ScriptFailed` 等响应组件定义了但没有任何端点引用（契约死代码） |  |
| P3 | S18-09 | 功能 | GET /openapi.yaml 通过 goja（GetSwagger）+ yaml.Marshal 重新序列化，返回文档与 api/openapi.yaml 原文不等价（无 examples/description 字段级保真） |  |
| P3 | S18-10 | 功能 | R-042"空发现分类返回明确错误"：`exploreKinds` 为空时返回错误已实现，但 `DiscoveryRequest.discoveryUrl` 直连模式未在 docs/API.md 说明可用性差异（文档级） |  |
| P3 | S18-11 | 功能 | `hasMore` 语义为"本页有命中"而非分页信号，契约 description 未声明（历史问题 C-005 部分残留：代码注释已改，契约未改） |  |

## 6. 分切片问题明细

切片边界：S01 多源搜索调度与内存；S02 单源搜索链路；S03 书源校验；S04 脚本沙箱安全；S05 脚本运行时性能；S06 规则分析器核心；S07 子规则引擎(css/xpath/jsonpath/regex/url)；S08 抓取器 fetcher；S09 基础设施杂项(ratelimit/config/logging/requestoption/chinese)；S10 HTTP API 层；S11 SQLite 持久化；S12 详情与目录；S13 正文与图片代理；S14 替换规则与发现；S15 书源导入/调试/诊断；S16 CLI 与装配；S17 门禁/静态分析/测试质量；S18 功能完整性对照基线。

### S00 2 万源实测追加（主审，用户样本 sources_importable_22353.json）

审核范围：用户提供的 22353 源样本（导入后 20646 源、13674 个不同 host）在本机真实网络下的导入、ListAll、全量校验、全量搜索；heap/goroutine profile 由临时探针 `bootstrap.Open` 同装配采集，探针已删除。

#### S00-01 单源搜索对每个结果条目整份复制响应体（`string(resp.Body)`），分配量 O(条目数 × 响应体)

- 级别：P0
- 类别：性能|资源
- 直接相关文件：internal/app/search/single_source.go:133（逐条 `ScriptAdapter{Src: string(resp.Body)}`）、:112、:115、:85、:175（同一页再复制 3~4 次）
- 间接相关文件：internal/app/source/script_integration.go:114、internal/app/content/content.go:334-344（同一响应 3 次复制）、internal/app/book/toc.go:196/245、internal/app/discovery/discovery.go:241、internal/infra/script/runtime.go:2406
- 问题描述：Go 中 `string([]byte)` 每次都分配并复制。`Search` 在条目循环内为每条结果构造 `ScriptAdapter{Src: string(resp.Body)}`，一页 N 条就复制 N 份完整响应体。响应体上限 `READER_FETCH_MAX_BODY_BYTES` 默认 10 MB（config.go:70），一个 10 MB、500 条的列表页单源就会瞬时分配约 5 GB；校验 16 并发、搜索 8 并发叠加时，峰值可达数十 GB，这是 2 万源 OOM 的直接触发点。证据：全量校验 9 分钟（1113 源）alloc_space 共 34.98 GB，其中 `SingleSourceSearcher.Search` 自身 15.72 GB，`pprof -list` 定位到 single_source.go:133 一行 15.64 GB（每源平均 14 MB）；同期 sys 从 590 MB 跳到 984 MB，而 10 s 采样的 heapInuse 只有 350–590 MB，说明是短时尖峰。源越多，命中"大页 × 多条目"的源越多，OOM 概率随源数上升。
- 修改方案：每次响应只转换一次：`body := string(resp.Body)`，后续 `GetElements`、`ScriptAdapter.Src`、`PreviewSearchBookFromInfo` 全部复用同一个 string（Go string 不可变，可安全共享，不再复制）。`ScriptAdapter`/`ruleengine` 的上下文改为每页构造一次，每条结果只替换 `Book`/`SetContent`。content.go:334-344、script_integration.go:114 同样改为一次转换后共享。可再用 `unsafe.String(unsafe.SliceData(b), len(b))` 去掉最后一次复制，前提是 resp.Body 之后不再被修改。
- 验证方式：`BenchmarkSingleSourceSearch`（1 MB 响应 × 200 条）对比 allocs/op 与 B/op，修复后 B/op 应与条目数无关；再用 2 万源样本重跑校验 10 分钟，`SingleSourceSearcher.Search` 的 alloc_space 应下降两个数量级。

#### S00-02 校验任务启动时逐个 FindByID 加载全部完整书源，并在整个任务生命周期内持有

- 级别：P1
- 类别：性能|资源
- 直接相关文件：internal/app/check/manager.go:46-55（循环 `m.sources.FindByID`）、internal/worker/check/runner.go:41-69、:85-177（`sources []*source.BookSource` 被 run goroutine 持有直到结束）
- 间接相关文件：internal/api/handlers/check_handler.go（在 HTTP 请求内同步调用 Start）、internal/infra/sqlite/repositories/book_source_repository.go（scanBookSource 解析全部规则与原 JSON）
- 问题描述：20646 源时，Start 在 HTTP 请求里同步执行 20646 次单行查询，并把每个源完整解析（规则 + RawJSON）后放进切片。整个任务运行期间（实测吞吐 2.06 源/s，约 2.8 小时）都持有这批数据：heap profile 中 `scanBookSource` 常驻 226.74 MB（占 inuse 87%）。每多开一个全量任务就再叠加约 230 MB，事件流（S03-10）也随之线性增长。
- 修改方案：Runner 只保存 `[]string` sourceID，worker 拿到并发槽后再 `FindByID` 加载单个源，用完即释放；Start 只做 ID 存在性校验（一次 `SELECT source_id ... WHERE source_id IN (...)`，或分批），不解析规则；同一进程限制并发全量任务数（如 1），超出时排队或返回 409。
- 验证方式：2 万源任务运行中 heap profile，`scanBookSource` 常驻应 ≤ 并发数 × 单源大小（MB 级）；Start 接口耗时应为百毫秒级。

#### S00-03 ListAll 按 LIMIT/OFFSET 分页，全量拉取复杂度 O(n²)

- 级别：P2
- 类别：性能
- 直接相关文件：internal/domain/source/repository.go:65-80（`listAllPageSize=500` 循环翻页）、internal/infra/sqlite/repositories/book_source_repository.go:105（`ORDER BY custom_order, book_source_name LIMIT ? OFFSET ?`）
- 间接相关文件：internal/app/search/multi_source.go（resolveSources）、internal/app/discovery、internal/bootstrap（启动清理）
- 问题描述：每页都从头排序扫描后跳过 OFFSET 行，并且每页都重新执行一次 COUNT。实测 20646 源 ListAll 耗时 20.2 s，常驻 228 MB（全部规则 + RawJSON）。每次多源搜索、发现都会先执行一次 ListAll，所以光准备阶段就要 20 s。
- 修改方案：改用 keyset 分页（`WHERE (custom_order, book_source_name, source_id) > (?, ?, ?) ORDER BY ... LIMIT ?`），或单条查询流式 `rows.Next()` 回调；只在第一页取 Total。搜索只需要 searchUrl/ruleSearch 等列，按 S01-04 的方案做列裁剪。
- 验证方式：2 万源库上 ListAll 计时应 < 2 s；repository 测试覆盖同名同序书源的翻页不重不漏。

#### S00-04 CookieManager.cache 按 domain 无界增长，未命中也会缓存空切片

- 级别：P2
- 类别：资源
- 直接相关文件：internal/infra/fetcher/cookies.go:52-66（`cache map[string][]fetch.Cookie`）、:71-89（cachedCookies 未命中时写入 `m.cache[domain] = persisted`，空结果也写）
- 间接相关文件：internal/infra/fetcher/http_fetcher.go（每次请求都会 Load）、internal/bootstrap/bootstrap.go（进程级单例）
- 问题描述：每访问一个新 domain 就新增一个条目，进程存活期间从不淘汰；过期 cookie 只在命中 Load 时清理。2 万源样本有 13674 个不同 host，serve 长期运行时会把每个 host 都留在 map 里。单条目通常只有几十字节到数 KB，`[推测]` 全量规模约在几 MB 到数十 MB；9 分钟/1113 源的 heap profile 里没有进入 top 15，所以它不是 OOM 的主因，但属于确定的长期无界增长。
- 修改方案：改成有容量上限的 LRU（如 4096 个 domain，可复用 internal/infra/rule/internal/lru 或 script/cache.go 的 NamespacedCache），未命中的空结果用短 TTL 负缓存，或者干脆不缓存；条目记录 `minExpiresAt`，过期后整体失效。
- 验证方式：单测连续 Load 1 万个不同 domain，断言 `len(cache)` ≤ 上限；过期 cookie 在 TTL 后不再返回。

### S01 多源搜索调度与内存

审核范围：internal/app/search/multi_source.go、internal/app/search/single_source.go（搜索服务入口）、internal/infra/concurrency/concurrency.go、internal/infra/ratelimit/limiter.go、internal/cli/search.go、internal/cli/context.go、internal/bootstrap/bootstrap.go（搜索相关装配）、internal/domain/source/repository.go（ListAll）、internal/infra/sqlite/repositories/book_source_repository.go（行加载）

#### S01-01 默认（无 --timeout）多源搜索无每源超时上限，单源慢源拖住整次搜索

- 级别：P1
- 类别：性能
- 直接相关文件：internal/app/search/multi_source.go:82-89（`Timeout <=0 表示不启用`）、internal/cli/search.go:44（`--timeout` 默认 0）
- 间接相关文件：internal/cli/search.go:53-62、docs/CLI.md
- 问题描述：`MultiSearchInput.Timeout <= 0` 时不做任何 per-source 超时包装，只有 http.Client.Timeout（默认 30s，`internal/infra/fetcher/http_fetcher.go:249`）兜底单请求；规则解析、重定向链、bookUrlPattern 详情回退、LoginCheck 等不受该 client timeout 约束，可叠加超 30s。实测：无 `--timeout` 时全源搜索耗时 210.8s（578 源、离线 30s 拨号超时上限即每源 30s）；加 `--timeout 3s` 后 60 个显式源从 35.5s 降到 8.1s。默认不启用意味着 CLI 用户不加 flag 时无每源上限。
- 修改方案：`internal/cli/search.go` 将 `--timeout` 默认值改为非 0（如 30s），或 `runSearch` 在 `Timeout<=0` 时填入 `cfg.FetchTimeout`；HTTP API handler 同理给默认值。
- 验证方式：`/tmp` 临时 DB 导入 578 源，对比默认与 `--timeout 30s` 的耗时；`internal/app/search/multi_source_test.go` 补默认超时用例。

#### S01-02 每源搜索 goroutine 一次性全量启动：578 个 goroutine 同时建，仅靠信号量限流

- 级别：P1（原 P2，2 万源实测后上调：一次性挂起 20666 个 goroutine，6 分钟只完成约 230 源，见 2.3 节）
- 类别：并发
- 直接相关文件：internal/app/search/multi_source.go:86-91（`wg.Add(1); go func(){...}` 在循环里直接 spawn，`sem <- struct{}{}` 在 goroutine 内）
- 间接相关文件：internal/infra/concurrency/concurrency.go:33-47（仓库已有 `Map` 工具，本处未复用）
- 问题描述：与 `concurrency.Map` 不同（后者在 select 里先 `sem <-` 再干活的 worker 池语义其实也是每 item 一个 goroutine，两者等价），此处的问题是每个源 goroutine 在拿信号量之前就已分配栈并注册 `context.WithTimeout`（每源一个 timer，578 个 context deadline 同时挂在堆上，heap profile 见 512kB `context.WithDeadlineCause`）。真正的问题在于 goroutine 数与源数线性（S=5502 时 5502 个 goroutine），且 panic-recover + 锁 + sem 的样板每源重复。并发正确性本身无问题（`-race` 语义、锁、sem 释放均正确）。
- 修改方案：改为固定 `concurrency` 个 worker 从 `chan *source.BookSource` 消费（或直接复用 `concurrency.Map`），goroutine 数从 O(S) 降为 O(并发度)。
- 验证方式：`runtime.NumGoroutine()` 在 Search 中段采样（temporal profile），应恒 ≤ 并发度+少量。

#### S01-03 单源搜索每次为每个条目重建 ScriptAdapter 并对每源重复执行 `installSafeGlobals`/`javaBindings` 的 VM 初始化，占单源分配 30%+

- 级别：P2
- 类别：性能
- 直接相关文件：internal/app/search/single_source.go:130-137（每个 item new 一个 `ruleinfra.NewAnalyzer` + `ScriptAdapter{...}`）、internal/app/search/single_source.go:112（`Source: s.script.SourceBindings(ctx, src)` 每 item 重复调用）、internal/infra/script/runtime.go:169-196（每 Eval 新建 goja VM + `installSafeGlobals`）
- 间接相关文件：internal/infra/script/runtime.go:484-493、internal/domain/source/variable_repository.go
- 问题描述：alloc_space profile（578 模拟源、11560 条结果、总分配 317.6MB）：`SingleSourceSearcher.Search` cum 272.1MB（85.7%），其中 `buildSearchRequest`（搜索 URL 的 JS 模板 Eval）106.6MB、每 item 的 `ScriptAdapter`/`SourceBindings` 构造 33.6+36.6MB、`Runtime.SourceBindings` flat 44MB（每次调用新建完整 map[string]any + 11 个闭包）。`EvalWithBindings`→`installSafeGlobals` 87MB，其中 `installJavaImporterCompat` 64.5MB——每次 Eval 都重建整套 JavaImporter/okhttp/hutool 兼容对象树，而 precompiledPrograms 只缓存了 JS 源码编译结果，不缓存这些 host 对象树。跨源对象（fetcher、cache、cookie、jsLib programs）已共享，无每源重复创建 runtime 的问题；热点在每 Eval 重建 host 绑定。
- 修改方案：1) `single_source.go` 把 `s.script.SourceBindings(ctx, src)` 提到 item 循环外构造一次，每个 item 只构造 `Book: &searchBook` 差异部分；2) `script.Runtime` 为 `installJavaImporterCompat` 的对象树引入 per-VM prototype 模板或按需惰性安装（仅在脚本引用 `JavaImporter`/`okhttp`/`hutool` 时安装），可消掉 ~20% 单 Eval 分配。
- 验证方式：重跑同基准 `go test -run TestProfileMultiSearch`，对比 alloc_space 总量与 `installJavaImporterCompat` 行；CPU profile 中 GC 占比（当前 28.4% cum `gcBgMarkWorker`）应显著下降。

#### S01-04 全量源（含规则/原 JSON 大字段）一次性加载进内存

- 级别：P2
- 类别：性能
- 直接相关文件：internal/app/search/multi_source.go:186-196（`resolveSources` → `source.ListAll`）、internal/domain/source/repository.go:65-83（`listAllPageSize=500` 分页拉全量）、internal/infra/sqlite/repositories/book_source_repository.go:299-303（`selectBookSourceSQL` 含 `js_lib/raw_json/rule_*` 全部大列）
- 间接相关文件：internal/infra/config/config.go（无相关分页配置）
- 问题描述：未指定 `--source` 时全部书源连同 `RawJSON`（导入原文副本）、5 个规则 JSON、jsLib 全列加载。578 源 DB 4.4MB，常驻 RSS 实测 42-48MB（60 源子集）——全量时随源数线性；5502 源库时粗估 40-80MB 常驻 [推测：按 selectBookSourceSQL 全列与 raw_json 副本]。`--limit` 截断只影响输出，不影响这部分。历史问题 C-007 仍存在（该条标注"不修，记录"）。
- 修改方案：为搜索路径增加列裁剪查询（只取 searchUrl/ruleSearch/concurrentRate/header 等搜索必需列），或 `ListAll` 增加 `columns` 参数。
- 验证方式：5502 源库上对比 `search` 启动 RSS。

#### S01-05 超时后的源仍会占用完整单请求预算直到 http.Client 超时（无 per-source deadline 的 warning 语义掩盖）

- 级别：P3
- 类别：并发
- 直接相关文件：internal/app/search/multi_source.go:103-113（timeout 错误分类 `search_timeout`）、internal/app/search/single_source.go:67-69（`ratelimit.Do(ctx, ...)` 传入的 ctx 已带 deadline，正确传播）
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:249
- 问题描述：per-source `context.WithTimeout` 正确传播到 fetcher 与 script 中断（`runtime.go:170-181` `context.AfterFunc` → `vm.Interrupt`，且确认"goja 只在跳转处检查中断：脚本在网络请求被取消后可能直接跑完"的兜底已处理）。本条不是缺陷，仅确认 ctx 取消链完整；无 channel 阻塞泄露（sem 用 defer 释放、wg.Wait 收敛、goroutine 全部退出——goroutine 泄露检查通过：基准结束后 `goroutines=9` 恢复基线）。
- 修改方案：无需修改；记录确认结论。
- 验证方式：`go test -race ./internal/app/search/`（无报告）。

#### S01-06 `concurrency.Map` 任一失败即取消其余任务并丢弃全部结果，语义对多分页场景过于激进

- 级别：P3
- 类别：并发
- 直接相关文件：internal/infra/concurrency/concurrency.go:36-56（`cancel()` on first error，全部结果丢弃返回单个错误）
- 间接相关文件：internal/app/content、internal/app/book（正文/目录多分页调用方）
- 问题描述：`Map` 在第一个 error 即 `cancel()`，已成功的 `results[index]` 也被整体丢弃；对"多分页抓取，第 3 页失败但 1/2 页可用"的场景，调用方拿不到部分结果。多源搜索未用它（自写循环），故不影响 S01 切片主链，但属该工具的设计缺陷。跨切片：影响正文/目录多分页。
- 修改方案：增加 `KeepPartial` 选项或在 error 时仍返回已完成部分 + 错误。
- 验证方式：补单元测试：2 页成功 1 页失败时部分结果可见。

#### S01-07 ratelimit.Limiter 单源信号量 map 只增不减，长期运行进程随源数增长

- 级别：P3
- 类别：资源
- 直接相关文件：internal/infra/ratelimit/limiter.go:103-114（`sourceChan` 按 key 缓存 chan，无淘汰）
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:47-53（`clients map[routeKey]`、`dnsCache` 同样只增不减——跨切片）
- 问题描述：每个出现过的 bookSourceUrl 各留一个 chan（容量 SourceConcurrency，默认 2，每 chan ~100B），5502 源全量校验后约 0.5MB，量级小但只增不减。`Limiter.sources` 无 TTL/淘汰。同类：`HTTPFetcher.clients`（按 routeKey，默认只有一种 route → 1 个）与 `dnsCache`（有 TTL 清理 `f.dnsCache = map{}`，见 http_fetcher.go:67-68，实际会整体重建，可控）。跨切片：HTTPFetcher 属 S08。
- 修改方案：`Limiter.sources` 保持现状可接受（量级小），若要收紧可在 Delete 书源时移除对应 chan；或在 GC 时机对空闲 chan 做清理。低优先。
- 验证方式：构造 10 万 key 压测断言 map 大小有界。

#### S01-08 多源搜索结果合并与排序本身已修复为 O(n)，确认历史问题 C-001/C-002/C-003/C-004/C-005/C-006 均已修复（记录/证据项，不计入统计）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/search/multi_source.go:105-107（锁内只 append）、:110-120（wg.Wait 后统一去重+排序）、:124-141（map 索引 O(n) 去重）、:144-152（`MultiSearchResult.All` 保留各源原始条目）、:73-81（CLI globalConcurrency 覆盖生效，internal/cli/context.go:51-53）、:82-90（per-source Timeout）、:38-42（HasMore 注释明确"本页有命中的近似语义"）
- 间接相关文件：internal/cli/search.go:45-53、internal/cli/context.go:27-53、docs/CLI.md:119-122
- 问题描述：逐条核对 docs/CliPerfReview-2026-10-17.md 的 C-001~C-006（F1 归属项）：锁内重排（C-001）、All 字段（C-002）、--concurrency 封顶（C-003，`env.globalConcurrency` → `config.Load` 后覆盖，实测 60 源并发 8→16 耗时 35.5s→30.1s 有效）、per-source 超时（C-004，实测 `--timeout 3s` 35.5s→8.1s）、HasMore 注释（C-005）、过滤/分桶双写（C-006，现 precision 过滤在收集处仍写一遍与 rankSearchBooks 分桶并存，属轻微重复但语义一致）。均已修复或收口。
- 修改方案：无需修改。
- 验证方式：上表 file:line 与基准数据。

### S02 单源搜索链路

审核范围：internal/app/search/single_source.go、multi_source.go（同目录 hasmore_test.go/multi_source_test.go/single_source_test.go 为测试，一并核对）；对照 legado BookList.kt、WebBook.kt、AnalyzeUrl.kt；关联消费点 internal/app/book/info.go（PreviewSearchBookFromInfo/parseInfo）、internal/infra/requestoption/request.go、internal/infra/rule/analyzer.go、internal/infra/ratelimit/limiter.go。

#### S02-01 bookUrlPattern 直达详情页解析失败/无名字时返回空名 stub 条目，与 Legado 语义不符

- 级别：P2
- 类别：功能
- 直接相关文件：internal/app/search/single_source.go:77-100
- 间接相关文件：internal/app/book/info.go:189-224（PreviewSearchBookFromInfo）、legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt:68-83、internal/app/search/multi_source.go:140-143
- 问题描述：bookUrlPattern 匹配 resp.URL 时调用 PreviewSearchBookFromInfo；若解析出错（如书源未配 ruleBookInfo 返回 ErrBookInfoRuleMissing）或 preview.Name 为空，Reader 返回一个只有 BookURL/Origin 的 stub 条目。Legado BookList.kt:72-83 中 getInfoItem 返回 null（name 为空或 filter 不过）时 bookList 保持为空并 `return bookList`。后果：向用户展示一个无名字的假条目，点进去后详情/目录请求全部打到该 URL 上失败；且该条目在 precision 过滤（Contains("",kw) 为 false 时才滤掉）与排名中落入 other 桶，干扰结果。注意 Reader 在"空列表回退"路径（single_source.go:174-184）已正确要求 name 非空才返回，两条路径行为不一致。
- 修改方案：pattern 匹配分支中，`err != nil || strings.TrimSpace(preview.Name) == ""` 时返回空结果 `[]book.SearchBook{}, nil`（或返回错误），与空列表回退路径和 Legado 对齐；删除 stub 返回。
- 验证方式：新增测试：配置 bookUrlPattern 命中 + 无 ruleBookInfo（或解析出空 name）时 Search 返回空切片而非含空 Name 条目。

#### S02-02 多源搜索同时保留 result.All 与 result.Items 双份、预览条目携带全量响应体，数百源时内存放大

- 级别：P2
- 类别：性能
- 直接相关文件：internal/app/search/multi_source.go:141-149（All append）、:150-152（Items 由 All 派生）；internal/app/book/info.go:211-216、:222-223（InfoHTML/TocHTML = 整个 body）；internal/app/search/single_source.go:82-99、175-184
- 间接相关文件：internal/domain/book/book.go:51-53、:70-73（InfoHTML/TocHTML 字段）、internal/api/handlers（同时序列化 All 与 Items 的消费方）
- 问题描述：① MultiSearchResult 同时持有 All（原始条目）和 Items（All 去重排序的子集），两者并存导致结果集内存双份；数百源 × 每源数十条时显著。② bookUrlPattern 直达或空列表回退产生的条目，InfoHTML 与 TocHTML 各自保留整个响应体（info.go:211-216 InfoHTML=body 恒成立；TocURL==baseURL 时 TocHTML=body），单个条目最多 2×MaxBodyBytes（默认 10MB）常驻直到请求结束，500 源并发下瞬时内存可达数 GB 量级 [推测，按 10MB 上限推算]。③ resp.Body 在单次搜索中被多次 string 全量拷贝（single_source.go:109/131 传给 ScriptAdapter.Src、LoginCheck 的 body map、Preview 调用参数），每份拷贝在 analyzer 存活期间保留。
- 修改方案：① Items 生成后若 All 不再被调用方使用，将 All 置 nil 或仅在请求参数要求时填充（惰性）；或让 API 层二选一返回。② 预览条目只在确有消费方需要 InfoHTML/TocHTML 时填充（搜索结果本身不需要，Legado 只在 searchBook.bookUrl==baseUrl 时设 infoHtml，且搜索链路马上丢弃）；或对 SearchBook 预览路径不设置 InfoHTML/TocHTML。③ ScriptAdapter.Src 复用同一份 string（当前已经是 string(resp.Body) 一次转换，但 LoginCheck 与 Preview 各自再转一次），改为转换一次后传递。
- 验证方式：用 500 个 mock 源跑 multi-source 搜索，pprof heap 对比修改前后 alloc_space/inuse；断言返回后 SearchBook 不再持有 InfoHTML。

#### S02-03 每条结果重复构造 itemAnalyzer、SourceBindings 绑定与 VariablesJSON 序列化，单源内逐条分配过多（与 S01-03 同根，统计并入主条目）

- 级别：P2
- 类别：性能
- 直接相关文件：internal/app/search/single_source.go:128-167
- 间接相关文件：internal/infra/rule/analyzer.go:56-58、:600-630；internal/infra/script/variables.go:81-100；internal/infra/script/runtime.go:169-223（跨切片：每次 Eval 新建 goja VM）
- 问题描述：对每条结果：NewAnalyzer（含两个 map 分配）、SetVariables 拷贝 baseVars、`s.script.SourceBindings(ctx, src)` 重建约 20 项的 map 及闭包（variables.go:95-140，每次调用全新分配），最后 VariablesJSON 再 json.Marshal 一次。数百源 × 数十条结果时，仅绑定构造与序列化就是数十万次小分配；规则分词（sourceRuleCache）与脚本编译（runScriptCached）已缓存，剩余热点在此。每次 JS 字段求值还会新建 goja VM 并重跑 installSafeGlobals + jsLib（runtime.go:175、:379-392）——该 VM 新建属脚本切片（跨切片），但逐条逐字段的调用频次由本切片放大。
- 修改方案：Search 内将 SourceBindings 结果提升到循环外构造一次（src 与 ctx 在循环内不变）；baseVars 非空时 VariablesJSON 的 baseVars 序列化结果可缓存复用，仅当 itemAnalyzer.VariablesJSON() 与 baseVars 不同才重新编码；itemAnalyzer 可复用同一 Analyzer 实例并 ResetVariables（若 Analyzer 提供重置），避免每条 NewAnalyzer。
- 验证方式：go test -bench（mock 1000 条结果的源）对比 allocs/op；pprof 确认 SourceBindings/VariablesJSON 不再是热点。

#### S02-04 多源搜索 goroutine panic 时 WithTimeout 的 cancel 未调用，超时定时器泄漏

- 级别：P3
- 类别：资源
- 直接相关文件：internal/app/search/multi_source.go:112-118
- 间接相关文件：internal/app/search/multi_source.go:92-101（panic recover 在更外层 defer）
- 问题描述：`searchCtx, cancel = context.WithTimeout(ctx, input.Timeout)` 后若 `s.single.Search` panic，`cancel()` 不会执行（它不在 defer 中），外层 recover 把 panic 转成 Warning 后，WithTimeout 的定时器要等到 Timeout 到期才被 GC/触发。Timeout 配置较大时定时器常驻。`cancel := func() {}` 的空实现掩盖了这一点。
- 修改方案：改为在 goroutine 内 `if input.Timeout > 0 { searchCtx, cancel = context.WithTimeout(...); }` 后立即 `defer cancel()`（defer 与 recover 同层即可，cancel 幂等且 LIFO 顺序无碍）。
- 验证方式：go test -race：构造 single.Searcher panic 的 fake + Timeout=1h，断言无 timer 泄漏（可用 runtime.NumGoroutine 或 context 泄漏检测手段，或直接审查 defer 结构）。

#### S02-05 字段规则求值错误被静默忽略，且不携带源标识；与 Legado 中止语义不一致

- 级别：P3
- 类别：错误处理
- 直接相关文件：internal/app/search/single_source.go:139-166
- 间接相关文件：internal/infra/rule/analyzer.go:89-101、legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt:220-227、internal/app/search/multi_source.go:117-134
- 问题描述：name/author/bookURL/kind/intro/cover/wordCount/lastChapter 全部 `x, _ := itemAnalyzer.GetString(...)`，规则求值错误（如 JS 规则抛错、选择器非法）被吞掉，条目以残缺字段继续或被静默丢弃；单源直连调用（非 multi_source 包装）时这些错误完全不暴露。Legado 的 getSearchItem 中 getString 抛错会中止整个搜索（调用方 runCatching 捕获并上报书源错误）。Reader 的宽容策略本身可接受，但错误被完全丢弃、无日志、无 sourceId，排障困难；且 name 求值失败与"规则返回空"不可区分。
- 修改方案：至少对 name 与 bookURL 的求值错误记日志（带 src.BookSourceURL）或聚合后随错误返回；或提供 SearchOption 严格模式（Legado 语义，首个字段错误即中止）。
- 验证方式：新增测试：Name 规则为非法 JS 时，Search 返回的错误/日志包含源 URL 与规则错误。

#### S02-06 buildSearchRequest 将所有 Build 错误统一包装为 ErrUnsupportedSearchURL，错误分类失真

- 级别：P3
- 类别：错误处理
- 直接相关文件：internal/app/search/single_source.go:223-226
- 间接相关文件：internal/infra/requestoption/request.go:23-95（Validate/Build 的具体错误：ErrUnsupportedURLOption、代理/DNS 配置错误等）、internal/app/search/multi_source.go:120-134（按错误分类 Warning）
- 问题描述：`fmt.Errorf("%w: %v", ErrUnsupportedSearchURL, err)` 把 requestoption.Build 返回的一切错误（不支持的 URL 选项、非法 proxy/dnsIp 值等配置错误）都归类为"使用了不支持的 Legado URL 脚本"，调用方（multi_source 只区分 timeout/failed，HTTP API/CLI 展示该消息）无法区分"书源用了不支持的脚本"与"URL 选项配置错误"。
- 修改方案：仅当 errors.Is(err, requestoption.ErrUnsupportedURLOption) 或脚本类错误时包装 ErrUnsupportedSearchURL；其余错误原样返回（保留 %v 上下文但不再挂 ErrUnsupportedSearchURL）。
- 验证方式：测试：searchUrl 带 unsupported 选项（如 {"foo":1}）时报 ErrUnsupportedURLOption 语义而非 ErrUnsupportedSearchURL。

#### S02-07 多源搜索静默跳过空 searchUrl 源与 EnabledOnly 过滤源，无任何 Warning

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/search/multi_source.go:103-105（空 searchUrl continue）、:155-157（EnabledOnly 跳过）
- 间接相关文件：internal/app/search/multi_source.go:149-157（无效 ID 有 source_unavailable Warning，行为不一致）
- 问题描述：searchUrl 为空的源被静默跳过，EnabledOnly=true 时被禁用源也被静默跳过，但无效 ID 却有 Warning；用户无法知道哪些源根本没参与搜索，结果覆盖不全且无告警。Legado 对空 searchUrl 抛"搜索url不能为空"（WebBook.kt:57-59），至少是显式失败。
- 修改方案：为这两类跳过分别追加 Warning（code 如 source_skipped / source_disabled），与 source_unavailable 对齐。
- 验证方式：测试：含空 searchUrl 源与禁用源时 Warnings 非空且 code 正确。

#### S02-08 replaceInlineSearchExpressions 对每个内嵌表达式重建 searchURLBindings（含 SourceBindings 全量 map）

- 级别：P3
- 类别：性能
- 直接相关文件：internal/app/search/single_source.go:293-311
- 间接相关文件：internal/infra/script/variables.go:81-100
- 问题描述：循环内每个 `{{expr}}` 都调用 `searchURLBindings(ctx, runtime, src, keyword, page)`，其中每次都执行 `runtime.SourceBindings(ctx, src)` 构建全新 map + 十余个闭包；一个 searchUrl 有 N 个内嵌表达式时重复 N 次，而 bindings 中除 result 外全部不变。
- 修改方案：循环外构造一次 bindings，循环内仅更新 `bindings["result"]`（applySearchURLScripts 已是这个模式，single_source.go:270）。
- 验证方式：bench：含多个 {{}} 的 searchUrl 构建请求，对比 allocs/op。

#### S02-09 单源内去重按 bookUrl 丢弃，Legado 按整个 SearchBook 对象去重，语义不一致

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/search/single_source.go:147-151
- 间接相关文件：internal/app/search/multi_source.go:150（multi 层再按 name+author 去重）、legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt:131-134（LinkedHashSet(bookList)）
- 问题描述：Legado 用 LinkedHashSet 按 SearchBook data class 全字段 equals 去重（bookUrl 与其他字段均相同才去重）；Reader 在单源循环内按 bookUrl 去重，bookUrl 相同但 name/author 不同的第二条被直接丢弃。列表页规则把不同条目解析到同一 bookUrl（如选择器取错父节点）时，Reader 会比 Legado 少返回条目，且不报任何提示。
- 修改方案：改为与 Legado 一致的对象级去重（比较 BookURL+Name+Author），或至少在丢弃时记录去重次数。
- 验证方式：测试：两条 item 的 bookUrl 相同而 name 不同时，结果应含两条（对齐 Legado）或至少记录去重。

#### S02-10 search_timeout 分类在调用方主动取消时误标

- 级别：P3
- 类别：错误处理
- 直接相关文件：internal/app/search/multi_source.go:121-123
- 间接相关文件：internal/api/handlers（HTTP 客户端断开场景）
- 问题描述：`input.Timeout > 0 && errors.Is(err, context.Canceled)` 即标记 search_timeout；但 context.Canceled 同样产生于上层（如 HTTP 请求方断开连接取消 ctx）。此时该源被记录为"搜索超时"，与事实不符，污染校验/调试统计。
- 修改方案：区分：仅 `errors.Is(err, context.DeadlineExceeded)` 标 search_timeout；Canceled 时先检查 `ctx.Err() != nil`（上层取消）标 search_canceled，否则才归入超时。
- 验证方式：测试：传入已取消的根 ctx + Timeout>0，断言 Warning code 为 canceled 类而非 search_timeout。

#### S02-11 renderSearchURL 中 `{{source.key}}` 与 `{{source.bookSourceUrl}}` 尾斜杠处理不一致

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/search/single_source.go:242-244
- 间接相关文件：internal/infra/script/variables.go:88（source.key = stripSourceSuffix 的结果）
- 问题描述：`{{source.key}}` 替换值做了 `strings.TrimRight(..., "/")`，而 `{{source.bookSourceUrl}}` 用原始 src.BookSourceURL 不去尾斜杠；书源 URL 以 / 结尾时两种占位符产生不同基址，`{{source.bookSourceUrl}}/search` 会拼出双斜杠。Legado 中 source.key 与 bookSourceUrl 均不做此修剪（key 为去匹配后缀的 URL），此处 Trim 是 Reader 自加行为且只加在一处。
- 修改方案：统一：两者都不 Trim（交给 EncodeURLQuery/URL 规范化），或两者都 Trim，并在注释说明与 Legado 的差异。
- 验证方式：测试：BookSourceURL 以 / 结尾时两种占位符替换结果一致性。

### S03 书源校验

审核范围：internal/app/check/（manager.go、checker.go、publisher.go、diagnostics.go）、internal/worker/check/runner.go、internal/infra/sqlite/repositories/check_repository.go、internal/api/handlers/check_handler.go、check_mapper.go、sse.go（事件流消费端）、internal/bootstrap/bootstrap.go（装配）、internal/cli（check 相关命令——不存在）、internal/domain/task/check.go（状态机）。对照 legado/app/src/main/java/io/legado/app/service/CheckSourceService.kt。

#### S03-01 API 创建任务后 ctx 取消会中止整个后台任务（任务静默失败）

- 级别：P1
- 类别：并发
- 直接相关文件：internal/api/handlers/check_handler.go:44-50、internal/worker/check/runner.go:39-68、internal/app/check/manager.go:64-89
- 间接相关文件：internal/worker/check/runner.go:107-112（acquire 靠 ctx.Done 退出）
- 问题描述：`CreateBookSourceCheckTask` 用 `h.check.Start(r.Context(), …)` 把 **HTTP 请求的 ctx** 传进 Runner.Start。Runner 虽然用 `context.Background()` 派生运行 ctx，但 `repo.CreateTask(ctx, …)` 用的是请求 ctx；更关键的是 Manager.Start 逐个 `m.sources.FindByID(ctx, id)`（manager.go:69），数百个源时逐条查询期间客户端断开（或反向代理 30s 超时）即返回 500，任务根本没创建。即使创建成功，runner.go:55 `UpdateTaskStatus(context.Background(), …, Running)` 之后一切后台工作脱离请求 ctx，这部分是对的；但 Start 失败时已无回滚。对比 Legado：check 由 Service 生命周期持有，与请求无关。
- 修改方案：Handler 用 `context.WithoutCancel(r.Context())`（或 context.Background() + 只继承 traceID）调用 Start；FindByID 批量化（一次 `WHERE source_id IN (…)`）替代逐条循环查询。
- 验证方式：httptest 客户端创建任务后立即断开连接，任务仍应 Running→Succeeded；578 源创建耗时基准。

#### S03-02 每源保存 3~4 次独立 DB 写（SaveItem + UpdateTaskProgress + diagnostics.Apply + 重启收敛时逐条 SaveItem），578 源时数百次写事务串行排队

- 级别：P1
- 类别：性能
- 直接相关文件：internal/infra/sqlite/repositories/check_repository.go:88-121（SaveItem）、:74-86（UpdateTaskProgress）、internal/worker/check/runner.go:117-152
- 间接相关文件：internal/bootstrap/bootstrap.go:58（SetMaxOpenConns(1)）、internal/app/check/diagnostics.go:32-57（Apply 内 FindByID+Save 全量源行）
- 问题描述：每完成一个源：SaveItem（1 次 upsert，含 stages_json JSON 序列化）→ UpdateTaskProgress（1 次 UPDATE）→ diagnostics.Apply（FindByID + Save **整行书源**，含全部规则 JSON，1 读 1 写）。三个写互不合并，全部排在 SetMaxOpenConns(1) 的单连接后串行执行；真实网络下每个源完成时刻集中，写排队叠加。diagnostics.Apply 每次读改写整行 book_sources（规则 JSON 可达数十 KB），578 源就是 578 次全行重写。实测（快速源）无瓶颈，但真实慢源场景（源陆续完成）写队列会拉长整体墙钟时间。
- 修改方案：① UpdateTaskProgress 并入 SaveItem 同一事务（或改用 `finished = finished + 1` 原子自增，去掉读-改-写语义）；② diagnostics.Apply 改为窄列 UPDATE（`UPDATE book_sources SET respond_time=?, book_source_group=?, book_source_comment=?`），不整行读改写；③ 可选：批量攒 N 项后单事务批量 upsert。
- 验证方式：基准对比每源 3 写 vs 合并后 1 写的吞吐；EXPLAIN QUERY PLAN 确认窄列更新。

#### S03-03 diagnostics.Apply 与 check 流程对同一书源行的写覆盖无并发保护，多任务并行时互相覆盖诊断分组

- 级别：P2
- 类别：并发
- 直接相关文件：internal/app/check/diagnostics.go:32-57
- 间接相关文件：internal/infra/sqlite/repositories/check_repository.go（BookSourceRepository.Save 全列 upsert）
- 问题描述：Apply 是 `FindByID → 改内存字段 → Save 全列`。两个校验任务（或校验与用户手工编辑书源）并发对同一源执行时，后写的整行覆盖先写的（包括 RespondTime、分组、注释，甚至用户并发修改的其他列）。book_sources 无 version/updated_at 乐观锁。
- 修改方案：诊断写入改窄列 UPDATE（见 S03-02），分组/注释用 SQL 侧拼接（或在 WHERE 加 updated_at 条件失败则重读重试）。
- 验证方式：两个 goroutine 并发 Apply 同一源，断言两组都出现在最终 group 里。

#### S03-04 重复任务无防抖：同批源可并发重跑，Cancel/终态写库与新运行竞争（部分场景产生错误终态）

- 级别：P2
- 类别：并发
- 直接相关文件：internal/app/check/manager.go:64-89（Start 无 in-flight 检查）、internal/worker/check/runner.go:55、:186-194
- 间接相关文件：internal/domain/task/check.go:157-165（状态机允许 running→running 无意义迁移防护）、internal/api/handlers/check_handler.go:33-38（TaskID 用时间戳纳秒，无幂等键）
- 问题描述：Legado `CheckSourceService.check` 有 `checkJob?.isActive == true → toast "已有书源在校验"` 防重入（CheckSourceService.kt:91-94）。Reader 的 Manager.Start 无任何 in-flight 检查：同一任务 ID 重跑时 runner.go:186 的"只删自己的句柄"保护了 cancel map，但 UpdateTaskStatus 状态机允许 running→running（CheckTaskPredecessors(running) 含 running），两个运行对同一任务行的 finished/status 互相覆盖；新运行 CreateTask 若 task_id 相同会直接违反主键报错（返回 500 而非业务错误）。
- 修改方案：Manager.Start 先查存在 pending/running 的任务且源集相同（或全局）则拒绝（Legado 语义是全局单任务）；TaskID 生成失败改为 409/业务错误。
- 验证方式：连续两次 Start 同 ID，第二次应返回明确错误；并发 Cancel 与终态写库后状态机不变式成立。

#### S03-05 Cancel 立即把任务标 cancelled，与 runner 的终态写入竞争，慢源被取消后 finished 计数可能丢失/回退

- 级别：P2
- 类别：并发
- 直接相关文件：internal/app/check/manager.go:81-89（Cancel 里 UpdateTaskStatus→cancelled）、internal/worker/check/runner.go:175（run 末尾再 UpdateTaskStatus）
- 间接相关文件：internal/infra/sqlite/repositories/check_repository.go:41-71（状态机 WHERE 原子性）
- 问题描述：Cancel 先写 cancelled，然后 runner.run 结束时也写终态。时序：Cancel 写 cancelled（completed_at=T1）→ 已在途的条目 goroutine 仍会 SaveItem + UpdateTaskProgress（UpdateTaskProgress 的 WHERE `status='running'` 失效，进度静默丢弃，行为尚可）→ runner 结束时 `ctx.Err()!=nil → status=cancelled` 再次 UPDATE（CheckTaskPredecessors(cancelled) 含 running/pending/failed/succeeded？cancelled→cancelled 是否允许决定覆盖）。已保存条目的 finished 由 runner 内部计数 `finished++` 统计，但 Cancel 路径写的是 `current.Finished`（读库时刻的值），两条路径写的 finished 值可能不同，以最后写者为准——终态行数与 finished 字段可能不一致。
- 修改方案：Cancel 只置 cancel 函数 + 写一条"cancel requested"标记，终态统一由 runner 收尾路径写入（它已把排队条目记 cancelled 并统计 finished）；或状态机禁止 cancelled→cancelled 之外的二次写、finished 以 COUNT(check_task_items) 为准在终态时回填。
- 验证方式：取消 578 源任务后断言 `finished == COUNT(items WHERE status='cancelled')`。

#### S03-06 FindTask/ListItems 无分页：任务详情一次性返回全部条目（含 stages_json 反序列化），578 源任务每次轮询都全量构建

- 级别：P2
- 类别：性能
- 直接相关文件：internal/infra/sqlite/repositories/check_repository.go:25-37（FindTask 内联 ListItems）、:123-145（ListItems 全量）
- 间接相关文件：internal/api/handlers/check_handler.go:57-68（GetCheckTask）、internal/app/check/manager.go:99-102
- 问题描述：Get（API 轮询进度）每次都 `SELECT * FROM check_task_items WHERE task_id=?` 全量 578 行并把每行 stages_json Unmarshal 成结构体再 Marshal 回 JSON。客户端轮询时（典型 SSE 回退/前端进度条）每秒全量重建。实测单次全量读约毫秒级（578 项），但 CPU/GC 随轮询频率线性放大，且 API 无 items 分页参数。
- 修改方案：FindTask 拆成 FindTask（仅任务行）+ ListItems(page,pageSize, status 过滤)；GetCheckTask 默认不内联 items（或支持 `?include=items&page=`）；进度轮询走 check_tasks 单行。
- 验证方式：578 项任务 Get 1000 次的分配基准（pprof alloc_space 前后对比）。

#### S03-07 Publish 在锁内做扇出推送，事件风暴时发布与订阅竞争同一把锁；回放历史事件在锁外构造但有 O(events) 拷贝

- 级别：P2
- 类别：性能
- 直接相关文件：internal/app/check/publisher.go:49-77（Publish 锁内遍历 subs、sweepLocked 每次全 map 扫描）、:79-102（Subscribe 拷贝全部 history）
- 间接相关文件：internal/worker/check/runner.go:105、:113、:196-209（每源 3 个事件）
- 问题描述：578 源产生约 1734+ 事件（started + 每源 item_started/item 终态 + 终态），每条 Publish 都拿 p.mu 且每次调 sweepLocked 遍历全部 streams map；多任务并发时锁竞争点集中。Subscribe 每次重连把全部历史事件拷贝进 out channel（`make(chan, len(history)+16)` + 逐条预填），重连风暴时内存瞬时翻倍。另外 runner.go:105 `publish item_started` 在 goroutine 内、SaveItem 之前，事件顺序与 DB 写顺序无对齐，回放与实时混流时客户端可能见到 item_passed 早于 item_started 之外的乱序（item_started 先发，SaveItem 失败时不发终态事件——订阅者看到 started 却永远没有终态）。
- 修改方案：① sweepLocked 移到定时器/收敛钩子（终态 Publish 时顺带），不在每条 Publish 做；② Subscribe 回放改惰性（out channel 由 forward goroutine 先排空历史再切实时，避免锁外大缓冲预填）；③ SaveItem 失败时也发布 item_failed 终态事件，保证订阅者事件流收敛。
- 验证方式：1000 源并发 Publish 基准（-bench + mutex profile）；SSE 客户端断线重连内存曲线。

#### S03-08 runner.run 中 SaveItem/UpdateTaskProgress/diagnostics.Apply 失败只记 persistFailure，条目结果静默丢失，任务仍可能标 succeeded

- 级别：P2
- 类别：错误处理
- 直接相关文件：internal/worker/check/runner.go:119-153
- 间接相关文件：internal/worker/check/runner.go:160-183（终态判定）
- 问题描述：SaveItem 失败 → persistFailure=true 且该条目既无 DB 行也无 item 终态事件（事件在保存成功后才发）。任务终态因 persistFailure 会是 failed（正确），但：① finished 计数不含失败项，578 源任务可能 finished=570 而终态 failed，客户端无法知道哪 8 个丢了；② diagnostics.Apply 失败同样静默（诊断丢失但结果还在）；③ UpdateTaskProgress 失败标记 persistFailure 但进度已无法恢复。没有任何日志输出（Runner 无 slog 依赖），排障只能靠猜。
- 修改方案：① SaveItem 失败重试 1 次 + slog.Error 记录 taskID/sourceID/err；② 终态事件 Message 里带 lostCount；③ 注入 slog.Logger（bootstrap 已有）。
- 验证方式：注入失败 repo 的单测断言终态 failed + 日志含失败源 ID。

#### S03-09 事件流对"运行中但事件流缺失"的任务直接 500 而不是回退轮询；同一进程重启后 5 分钟保留期内流已丢失但状态机不知情

- 级别：P3
- 类别：功能
- 直接相关文件：internal/api/handlers/check_handler.go:86-99
- 间接相关文件：internal/app/check/publisher.go:104-152（sweepLocked 只清理收敛流；重启后 streams 全空）
- 问题描述：事件流纯内存。进程重启后任务若仍 running（被 FailInterruptedTasks 收敛为 failed，bootstrap.go:75，这点已修历史问题），但收敛前客户端若在重启间隙订阅会拿到 `internal_error` 而非终态合成事件——代码对 `CheckTaskTerminal` 分支已有回退（写终态事件），非终态才 500。窗口极小（FailInterruptedTasks 在启动时同步执行），实际影响低。
- 修改方案：非终态且流缺失时回退合成 progress 事件 + 定期查库，而不是 500。
- 验证方式：模拟 Publisher 为 nil 时订阅运行中任务返回 progress 事件。

#### S03-10 Publisher 事件流在任务收敛后保留 5 分钟且含全部条目 item 对象（含完整 item 结构），大任务下每个流占数 MB 内存

- 级别：P2（原 P3，2 万源实测后上调，见 2.3 节）
- 类别：资源
- 直接相关文件：internal/app/check/publisher.go:15-17（streamRetention=5min）、:58-60（events 无上限追加）
- 间接相关文件：internal/worker/check/runner.go:196-209（publishItem 携带完整 *CheckTaskItem，含 Stages 全量）
- 问题描述：事件只在内存保留，单个 578 源任务的流约 578 条 item 事件（每条含完整 item + stages 切片），数 MB；多个大任务并发时线性叠加。sweep 依赖"有新 Publish/Open 才触发"，若任务收敛后无人再 Publish，流会滞留到下一次任意任务活动——Retention 实际可能远超 5 分钟。
- 修改方案：① item 事件只带摘要（sourceID/status/elapsed），完整结果走 DB；② sweep 由后台 ticker 驱动（time.AfterFunc / Manager 定时器）。
- 验证方式：578 源任务完成后 heap profile 中 checkStream 占比；静置 6 分钟断言流被回收（需要 ticker）。

#### S03-11 取消后 `for _, src := range sources[started:]` 在 runner goroutine 内逐条 SaveItem（578 源最坏 578 次串行写）

- 级别：P3
- 类别：性能
- 直接相关文件：internal/worker/check/runner.go:156-166
- 间接相关文件：internal/bootstrap/bootstrap.go:58（单连接）
- 问题描述：取消时所有排队条目在单个 goroutine 里逐条 SaveItem；虽是正确语义（落 cancelled），但 500+ 源时串行写拖长 runner 收尾（每条 ~1ms 级，500 条约 0.5s+，真实负载更久），期间任务状态已是 cancelled（manager.Cancel 先写）而 items 还在陆续落库，客户端看到 cancelled 但 items 数持续增长。
- 修改方案：批量 INSERT（多 VALUES 单语句）或并发 SaveItem；终态响应前完成落库即可。
- 验证方式：500 源取消收尾耗时基准。

#### S03-12 CLI 无 check 子命令（功能缺口，与 spec/文档对照）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/cli/root.go:143-163（dispatch 无 check）、internal/cli/source.go:25-49
- 间接相关文件：docs/CLI.md（无 check 命令文档）、docs/Design.md
- 问题描述：CLI 只暴露 import/source/search/book/toc/content/download/fetch，书源校验仅能走 HTTP API。任务描述提到"internal/cli 中 check 相关命令"，实测不存在——若 spec 声明 CLI 覆盖校验则为缺口，若 spec 只声明 API 则不算。[推测] spec 端点已实现（API 侧完整），CLI 缺失更接近范围取舍而非缺陷，列出供裁定。
- 修改方案：如需对齐，新增 `reader check <source-ids|--all>` 子命令走 checkManager。
- 验证方式：`reader check --help`。

#### S03-13 checkKeyword 对 `--`/`++` 的排除过宽，含这些子串的正常中文关键字被静默回退

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/check/checker.go:278-292
- 间接相关文件：legado/.../data/entities/BookSource.kt:208-215（getCheckKeyword 原语义）
- 问题描述：为排除 execute 型规则（`http`/`::`/`++`/`--`），任何包含 `--`（如书名《XX--YY》）或 `++` 的关键字都回退默认"我的"。Legado 原语义只区分规则与字面值，不做子串猜测。历史问题 R-004（不读 checkKeyWord）已修复（现在优先读 RuleSearch.CheckKeyWord），此处是修复引入的过宽启发式。
- 修改方案：按 Legado 语义判断（含 `{{`、`@js:`、`<js>`、`http` 才视为规则），去掉 `++`/`--` 子串猜测。
- 验证方式：单测：checkKeyWord="斗破--特别版" 应原样使用。

#### S03-15 真实网络 578 源全量校验后 165 条空闲 keep-alive 连接（165 readLoop + 10 writeLoop goroutine）在 IdleConnTimeout 到期前持续驻留，goroutine 峰值 452

- 级别：P2
- 类别：资源
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:232-240（MaxIdleConns:100、MaxIdleConnsPerHost:8、IdleConnTimeout:90s、ForceAttemptHTTP2:true）
- 间接相关文件：internal/worker/check/runner.go（并发 16 但每源多次请求拆散到多 host）
- 问题描述：真实网络复现（2026-10-21，578 源全量校验，2m7s 完成）。结束后 30 秒 goroutine profile：177 个 goroutine 中 **165 个是 readLoop（IO wait）+ 10 个 writeLoop（select）**，全是空闲持久连接（每个 http2 clientConn 拆 readLoop+writeLoop 两个 goroutine，156 条 http2 连接），IdleConnTimeout 90s 内不释放。goroutine 运行期峰值 452（样本 t=105s）。不是泄露（90s 后会被 Transport 回收），但数百源跨数百 host 时连接数与 goroutine 数短暂膨胀到 450+，内存峰值 RSS 156MB。空闲连接数量受 MaxIdleConnsPerHost=8 × host 数控制，符合预期；若要降低驻留，可在校验任务结束后调用 Transport.CloseIdleConnections()。
- 修改方案：Runner.run 收尾时调用 `httpFetcher.CloseIdleConnections()`（HTTPFetcher 暴露该方法），校验后立即释放数百条空闲连接；或把 IdleConnTimeout 对校验场景降到 30s。
- 验证方式：校验结束后立即抓 goroutine profile，readLoop/writeLoop 数应接近 0。

#### S03-16 真实网络下 P50=1.2s / P90=6.0s / P99=30s / max=64s，慢源长尾由网络/超时构成，16 并发下 578 源 2m7s——"极慢"未复现，但 523/578 失败暴露大量源失效（记录/证据项，不计入统计）

- 级别：P2
- 类别：功能
- 直接相关文件：internal/app/check/checker.go（CheckSource 各阶段）、internal/infra/fetcher/http_fetcher.go（超时 30s）
- 间接相关文件：internal/app/check/diagnostics.go（失败归组）
- 问题描述：真实网络全量校验实测（2026-10-21，578 源、16 并发、DefaultTimeout 180s/源）：**总耗时 2m6.9s，578/578 完成**。耗时分布 P50=1235ms、P90=6045ms、P99=30002ms、max=63610ms；>60s 仅 1 个，>180s 为 0（per-source 180s 超时未成为瓶颈）。失败 523/578：search_failed 465、toc_failed 22、discovery_failed 14、content_failed 8、timeout 6、info_failed 4、js_failed 3、website_failed 1。最慢 6 个源全部是网络超时（Client.Timeout 30s，fetcher 级）。结论：校验流水线本身无异常慢点（httptest 快路径 5.6s、真实网络 2m7s，均与网络耗时线性相关）；"数百源极慢"更可能是**大量源确实失效（90% 失败率）+ 每源 30s fetch 超时在慢源上串行累积**，而非本切片代码缺陷。内存随源数增长部分：RSS 从 46MB 升到峰值 156MB，任务结束后 30s 回落到 156→156MB（HeapInuse 90→73MB，缓慢回落中，主因是空闲连接与 GC 节奏）。
- 修改方案：无缺陷修复项；建议诊断层面把 search_failed 465 的失败原因分布（404/超时/TLS/解析空）写入 diagnostics 便于归因。
- 验证方式：同环境重跑两次对比总耗时与失败分布稳定性。

#### S03-17 goroutine profile 证据：IO-wait goroutine 165 个全部停在 net/http 传输层（netpoll read），无一卡在 Reader 的 fetcher/script/rule/regex 层（记录/证据项，不计入统计）

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:232-240
- 间接相关文件：internal/infra/rule/、internal/infra/script/
- 问题描述：goroutine profile（/tmp/audit-S03/goroutine.txt，177 goroutine，diff 后已清理）：165 个 IO wait 全部是 `internal/poll.runtime_pollWait → net/http readLoop`（空闲持久连接）；10 个 select 是 writeLoop；1 个 chan receive 是测试主 goroutine；1 个 select 是 `database/sql.(*DB).connectionOpener`（正常）。**没有任何 goroutine 卡在 Reader 的 fetcher/script/rule/regex/check 层**——校验流水线无死锁/无阻塞点。heap profile top：最大分配点在 `goquery.Render → html.Render`（css.extractFromSelection 的 OuterHtml 渲染，css.go:778，单次 896B/16 次采样内为常态小分配）与 `json.Unmarshal（BookSource.ApplyLegadoDefaults，importer.go）`，均为正常路径，无异常大块驻留。
- 修改方案：无需修复；作为"卡住 goroutine 停在哪"的基线证据。
- 验证方式：同 profile 复查。

#### S03-18 真实运行失败原因分布（578 源，search-only 全量复跑）：HTTP 4xx/5xx 141、空结果 160、TLS 46、超时 30、DNS 5、SSRF 拦截 2、URL 形态不支持 3，其余 125 为 JS 挑战/协议错误等；手工核对未发现请求构造/编码缺陷（记录/证据项，不计入统计）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/check/checker.go、internal/infra/fetcher/http_fetcher.go
- 间接相关文件：internal/infra/requestoption/request.go:57-88（charset=GBK 表单编码已支持）
- 问题描述：对 578 源以 CheckSearch-only 复跑（1m24.9s，失败 476：search_failed 465 + timeout 8 + js_failed 3，通过 102），错误消息聚合分布：

  | 类别 | 数量 | 说明 |
  |---|---|---|
  | 搜索结果为空（empty search result） | 160 | 含站点反爬 JS 挑战（如 m.yawen.cc 返回 `ge_js_validator` cookie 挑战页，HTTP 200 但无书列表） |
  | HTTP 4xx/5xx（unexpected status） | 141 | 404×86、403×84、429×10、521×9、405×5、500×4、504×2 |
  | 其他（"other" bucket） | 125 | 含 JS 挑战页、HTTP/2 stream PROTOCOL_ERROR/INTERNAL_ERROR、EOF（站点断连）、反爬页 |
  | TLS 失败 | 46 | handshake failure/timeout、证书校验失败（x509） |
  | 超时 | 30 | context deadline exceeded |
  | DNS 失败 | 5 | no such host（含书源 URL 拼写错误：`wap.xyuzhaiwu9.commodules`，源数据少"/"） |
  | URL 形态不支持 | 3 | 如 `{{key}}@get:URL` 书源写法渲染后非法 URL |
  | SSRF 拦截 | 2 | `blxsw1.com` 解析到 127.0.0.2——**SSRF 防护正确拦截域名劫持/失效域** |

  手工核对（curl 对照）：
  - gxtongren.com：Reader 超时；curl GET /search/ 200 0.22s 但 **POST /search/（Reader 同构请求）30s 挂起**——站点 WAF 拦 POST，属站点不可达，非 Reader 缺陷。
  - m.51eshu.com：GET 200 1.98s；POST 同构请求多次重试 000/200 交替（10s 超时→6.2s 成功）——站点级不稳定，非 Reader 缺陷。
  - www.50zw.com / www.bixia.org / www.xxbiqudu.com / www.piaotian.net：curl 也超时/TLS 失败——站点不可达。
  - m.yawen.cc：curl POST 返回 `ge_js_validator` JS cookie 挑战页（200）——Reader 不执行 JS cookie 挑战 → 空结果，属 webView/JS 挑战功能边界（Legado 同样需要 WebView），非实现缺陷。
  - GBK 编码：168 个 gbk searchUrl 由 requestoption/request.go:88 `EncodeFormString(charsetName)` 处理，实现层面已支持。
- 修改方案：无 Reader 请求构造/编码/SSRF 误拦缺陷。可改进项：① JS 挑战页（`ge_js_validator`、`document.cookie`+reload 模式）识别为明确 failureClass（如 `source_js_challenge`），与"空结果"区分归组；② searchUrl 渲染失败（first path segment cannot contain colon）归为明确的书源写法错误分组。
- 验证方式：注入挑战页 HTML 的单测断言新 failureClass。

#### S03-14 任务创建后立即 `UpdateTaskStatus(..., Running)` 用 context.Background() 且失败直接 return err（任务卡 pending 不可取消路径缺失）

- 级别：P3
- 类别：错误处理
- 直接相关文件：internal/worker/check/runner.go:54-56
- 间接相关文件：internal/app/check/manager.go:81-89（Cancel 读到 pending 仍可取消，影响有限）
- 问题描述：CreateTask 成功后 UpdateTaskStatus(Running) 失败则 Start 返回 err，但任务行已存在且 pending，goroutine 未启动；客户端收到 500，任务永远 pending（重启时 FailInterruptedTasks 会收敛，缓解）。低概率（本地 SQLite）。
- 修改方案：失败时删除任务行（补偿）或忽略错误继续启动。
- 验证方式：注入失败 repo 单测断言无残留 pending 行。

### S04 脚本沙箱安全

审核范围：internal/infra/script/runtime.go、internal/infra/script/files.go、internal/infra/script/extensions.go（对照 legado/app/src/main/java/io/legado/app/help/JsExtensions.kt；连带核对 cache.go、variables.go、logging/redact.go、infra/fetcher/http_fetcher.go、bootstrap.go）

#### S04-01 文件沙箱不校验符号链接，脚本可经 symlink 读/删/写 FileRoot 之外任意文件

- 级别：P2（主审由 P0 调整：脚本 API 无任何创建符号链接的能力（files.go/unzip 不写 symlink，grep `Symlink|ModeSymlink` 零命中），利用前提是 DataDir/script-files 内已存在指向外部的链接（需本机写权限），属纵深防御缺口，由 P0 降为 P2）
- 类别：安全
- 直接相关文件：internal/infra/script/files.go:47-58（resolve）、files.go:72-84（readBytes）、files.go:116-124（delete，os.RemoveAll）、files.go:126-176（unzip）、files.go:304-323（newFileObject）
- 间接相关文件：internal/infra/script/runtime.go:88-89（Policy.FileRoot）、internal/bootstrap/bootstrap.go:114（FileRoot=DataDir/script-files）
- 问题描述：resolve 只做 `filepath.Clean(Join(root, rel))` 前缀校验，是纯词法检查，os.ReadFile/os.WriteFile/os.RemoveAll/zip 解压都会跟随符号链接。脚本执行 `java.readTxtFile("sub/link")`（link 为指向 root 外的符号链接）即可读到沙箱外文件；`java.deleteFile("sub/link")` 会 RemoveAll 链接目标目录（目录符号链接时删除外部整棵树）；downloadFile 写入同样跟随链接覆盖外部文件；newFileObject 的 exists/length/isFile 也 stat 目标。实测复现（临时测试，已删除）：FileRoot=/tmp/.../root，`java.readTxtFile("sub/link")` 返回 "SECRET-OUTSIDE"。unzip 对解压出的 symlink 条目（entry.FileInfo().IsDir() 为 false 时直接写 target，zip 内 symlink 名义上是普通文件）同样可被后续读取放大，但解压目标名有 resolve 前缀校验，主要逃逸面是沙箱内预先存在的/脚本先写出的 symlink。
- 修改方案：在 fileSandbox 的所有文件操作前做一致性校验：对每次 resolve 后的目标调用 `filepath.EvalSymlinks`（或用 `os.Lstat` 逐级校验路径组件），确认真实路径仍在 root 内；或统一改用 `os.OpenRoot`（Go 1.24+，InRoot API）执行读写/删除。delete 对目录符号链接改为 Lstat 判断后只 unlink 链接本身。unzip 解压前拒绝 zip 内 symlink 类型条目（Entry Mode 含 os.ModeSymlink）。
- 验证方式：新增测试：FileRoot 内创建指向外部的 symlink，断言 readTxtFile/readFile/deleteFile/downloadFile 均报非法路径或不影响外部文件；zip 内含 symlink 条目的解压测试；`go test ./internal/infra/script/ -run FileSandbox`。

#### S04-02 java.get/post/connect 的第三/二参数 headers 未走 URL 选项校验与脱敏，脚本可注入任意请求头并强制携带任意 Cookie

- 级别：P2
- 类别：安全
- 直接相关文件：internal/infra/script/runtime.go:2334-2375（fetchScriptCall）、runtime.go:2590-2612（mergeHeaders）
- 间接相关文件：internal/infra/fetcher/http_fetcher.go（请求头直发）、docs/ReviewIssues-2026-10-15.md（R-013 相关）
- 问题描述：fetchScriptCall 中 `mergeHeaders(req.Headers, normalizeScriptOptions(...))` 将脚本传入的 headers 键值原样并进请求头（实测 headers=map[Cookie:a=b] 出现在发出的请求中）。书源作者可用 `{"Host":..., "Cookie":..., "X-Forwarded-For":...}` 等任意头伪造请求；与 URL 选项路径不同，这里没有 validateRuntimeSupportedURLOptions 的键白名单，也没有对 Host/Connection 等危险头、敏感值日志脱敏检查。Legado 侧 JsExtensions 同样接受 headers，但服务端多用户场景下书源作者可与 app 层隔离；风险评级取决于威胁模型（单用户自托管，书源=用户安装内容）。另 mergeHeaders 对非 map/string 值静默丢弃（normalizeScriptOptions 返回空 map），headers 传数组/undefined 时无提示——低影响。
- 修改方案：fetchScriptCall 在 mergeHeaders 后复用 requestoption/app 层的请求头规范化：拒绝覆盖 Host、Connection、Content-Length；对 Cookie/Authorization 值经 logging.RedactString 后才进入错误消息与日志路径（当前 newScriptHTTPResponse 把 err.Error() 放进 body，间接泄露路径已被 redact 覆盖）。可选：给脚本 headers 增加 Host 等保留键拒绝列表。
- 验证方式：测试：java.connect 传 `{"Host":"evil.test"}` 断言报错或被丢弃；headers 含 Cookie 的请求日志不含明文。

#### S04-03 goja VM 未设内存限制，脚本可分配多 GB 字符串（内存炸弹）

- 级别：P1
- 类别：性能
- 直接相关文件：internal/infra/script/runtime.go:165-174（EvalWithBindings 新建 VM）、runtime.go:132-136（Policy/timeout 无内存项）
- 间接相关文件：internal/infra/script/cache.go（cache 上限只管缓存，不管 VM 内部值）、dop251/goja（Runtime.SetMemoryLimit 能力）
- 问题描述：实测 `var s="a".repeat(1e9)` 在单次 Eval 内分配约 1GB 字符串并在 5.4s 内跑完、无错误；超时中断（30s 默认）期间脚本可持续膨胀，数百个书源并发执行时叠加可触发 OOM。`"a".repeat(2e9)` 等更大值同样无上限。这与已知"多书源内存泄露"问题叠加放大。goja 提供 `Runtime.SetMemoryLimit(int64)`（默认 -1 无限制），当前未调用。
- 修改方案：NewRuntime/EvalWithBindings 中对每个 VM 调用 `vm.SetMemoryLimit(<预算，如 256MB>)`；超出后 goja 会以OutOfMemoryError 中断，wrapRunError 需新增分支归类为 script_failed（或独立错误类型）。同时在 Policy 增加可配项。
- 验证方式：测试 `s="a".repeat(1e9)` 在设限后返回 OutOfMemory 类错误且耗时 <1s；`go test -run Mem`。

#### S04-04 Function 构造器与 eval 可用，越过的全局仅在词法层屏蔽（低风险，且是 Legado 兼容层必需）

- 级别：P3
- 类别：安全
- 直接相关文件：internal/infra/script/runtime.go:467-472（blocked 全局设为 goja.Undefined()）
- 间接相关文件：internal/infra/script/runtime.go:940-952（JavaImporter 兼容）
- 问题描述：installSafeGlobals 将 require/process/globalThis/fetch 等设为 Undefined 而非删除，脚本经 `Function("return require")()` 只拿到 undefined（实测无逃逸）。但 eval/Function 未禁用——书源脚本确实需要（Legado 书源常用 eval），属设计取舍。全局设为 Undefined 意味着脚本可 `globalThis = 1` 或重新赋值 `fetch = ...` 屏蔽自身提示，不构成逃逸。仅在 jsLib 污染 String.prototype（stringProto 预编译补丁）与后续 eval 结果被脚本篡改时造成语义混乱，低影响。
- 修改方案：保持现状或用 `vm.RunString("Object.defineProperty(globalThis-like, ...)")` 前先 freeze 不可行（goja 全局非严格可写）。建议仅文档化：blocked 列表若将来加入 JavaImporter 之类需同步 isForbiddenBinding（runtime.go:3144-3154）与 installSafeGlobals 两处清单，目前两处已重复维护，应提取为共享变量避免漂移。
- 验证方式：代码审查；两处列表一致性加一个单测断言。

#### S04-05 installSafeGlobals 与 isForbiddenBinding 的禁止清单重复维护，存在漂移风险

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/script/runtime.go:467-472、runtime.go:3144-3154
- 间接相关文件：internal/infra/script/security_test.go
- 问题描述：同一份 8 项清单（require/process/global/globalThis/window/document/XMLHttpRequest/fetch）在 installSafeGlobals 的 blocked 切片与 isForbiddenBinding 的 switch 中各写一遍。将来新增一个被屏蔽的全局（如 XMLHttpRequest 子类、EventSource）只改一处时，bindings 路径与全局路径的防护面不一致——例如 allowLog/runtime 内部 Set("result") 等键不受 isForbiddenBinding 保护，若调用方传入 key="java" 的 bindings 会直接覆盖 java 绑定（EvalWithBindings 循环对任意 key vm.Set，无保留键检查：bindings 传 "java"/"cookie"/"Packages" 可整体替换沙箱绑定）。
- 问题描述补充（升级点）：EvalWithBindings 对 bindings 键只检查 isForbiddenBinding 的 8 项，不包含 "java"、"cache"、"cookie"、"log"、"Packages"、"Base64" 等运行时自身安装的绑定；调用方（app 层）目前传固定键，但任何将来传入 "java" 的路径都会静默替换沙箱。
- 修改方案：提取 `var reservedBindings = []string{...}` 单一来源，两处共用；EvalWithBindings 中把 java/cache/cookie/log/Packages/Base64/Jsoup/URLEncoder 等安装名并入保留清单，命中即拒绝（ErrUnsafeGlobal）。
- 验证方式：测试：EvalWithBindings 传 Bindings{"java": ...} 断言返回 ErrUnsafeGlobal；单测断言两处清单来自同一 slice。

#### S04-06 scriptCallContext 派生 ctx 后 head/ajaxTestAll 等绑定未把"单请求超时"与 Eval 剩余预算取小，长超时参数可绕过 Policy.Timeout 总预算的语义边界（低）

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/script/runtime.go:2002-2013（scriptCallContext）、runtime.go:797-804（head 绑定）
- 间接相关文件：internal/infra/script/runtime.go:920-946（ajaxTestAll）
- 问题描述：scriptCallContext 以 `context.WithTimeout(parent, ms)` 派生，parent 是 Eval ctx，所以实际仍受总超时约束（正确）。但 java.get/post/connect（fetchResponse，runtime.go:566-571）不接受 timeout 参数且使用 runCtx.ctx（正确）；问题在于 timeoutIndex 逻辑只在 head 与 ajaxTestAll 使用，Legado JsExtensions 的 post/get 也有 timeout 变体（JsExtensions.kt:420 等），功能层面缺 timeout 覆盖属跨切片功能项。并发正确性本身无问题：ajaxTestAll 串行请求（注释声称"并发批量请求"但实现为 for 循环串行，runtime.go:927-945），与 Legado 并发语义不符且百源校验场景更慢——与已知性能问题直接相关（跨切片 S01/S02 亦相关，此处为该函数自身证据）。
- 修改方案：ajaxTestAll 改为 errgroup 并发（并发度受 policy 限制）；或至少修正注释。get/post 补 timeout 参数支持。
- 验证方式：基准：ajaxTestAll 10 个慢 URL，修复后耗时≈单个而非求和；`go test -run Ajax`。

#### S04-07 java.downloadFile 无文件大小上限，超大远程文件直接写入磁盘

- 级别：P2
- 类别：安全
- 直接相关文件：internal/infra/script/files.go:364-391（downloadFile）、files.go:393-410（fetchBytes）
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:474-484（maxBodyBytes 10MB 截断——Fetcher 路径已有保护）
- 问题描述：downloadFile 走 r.fetchBytes → r.policy.Fetcher.Fetch，body 已被 HTTPFetcher 的 maxBodyBytes（默认 10MB）截断，磁盘风险实际可控；但 hex 分支 `hex.DecodeString(content)`（files.go:376-380）会为任意长脚本传入的十六进制串一次性解码分配 len/2 字节内存，无上限，可配合 VM 大字符串制造内存压力（与 S04-03 叠加）。zipEntryContent/unzip 有 64MB/4096 条目上限（files.go:24-29），单独防护良好。
- 修改方案：downloadFile hex 分支对 content 长度设上限（如 128MB 字符 → 64MB 字节），超限报 script_failed；或在 decodeSymmetricInput/archiveBytes 同类入口统一加长度检查。
- 验证方式：测试传 512MB 十六进制串断言快速失败。

#### S04-08 脚本 Cookie 域解析放宽到 eTLD+1，跨子域 Cookie 可被任意书源读取（设计权衡，中低）

- 级别：P2
- 类别：安全
- 直接相关文件：internal/infra/script/runtime.go:3125-3132（cookieDomain）、runtime.go:2941-2990（MemoryCookieStore.GetCookie）
- 间接相关文件：internal/infra/script/variables.go:128-136（putLoginHeader 同步 Cookie）、internal/infra/fetcher/http_fetcher.go（CookieManager 按 domain 发送）
- 问题描述：cookieDomain 将任意 URL 归一到 EffectiveTLDPlusOne（如 a.example.com 与 b.example.com 共享 example.com 命名空间），java.getCookie("https://b.example.com/") 会拿到脚本此前对 a.example.com setCookie 的全部值。单用户自托管下书源之间共享 Cookie 命名空间是 Legado 语义（App 端同样按 domain 共享），但服务端把不同书源视为不同信任域时，恶意书源可通过 setCookie 到公共域+getCookie 读取其他书源在同名公共域的登录态。getCookie/getKey 只按归一域键取，无来源书源隔离。
- 修改方案：MemoryCookieStore 增加书源级隔离（键为 sourceKey+domain），或在 GetCookie 校验请求域确属目标 host 的父域且来自同一书源上下文（runCtx 传入）。属威胁模型决策，建议至少在文档标注共享语义。
- 验证方式：测试：书源 A setCookie("https://a.example.com","k=v")，书源 B getCookie("https://b.example.com") 修复后取不到。

#### S04-09 rewriteJavaCompatScript 的 rhinoLetConditionPattern 正则改写作用于含注释/字符串的任意脚本（既有历史问题的窄化版，剩余误改写风险低）

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/script/runtime.go:1381-1385（rhinoLetConditionPattern ReplaceAllString）
- 间接相关文件：internal/infra/script/cache.go:214/237、docs/ReviewIssues-2026-10-15.md（R-068 已部分修复：with 块部分已 AST 化）
- 问题描述：R-068 的 with 块改写已改为 AST 精确偏移（runtime.go:1405-1460），历史问题基本修复。但 rhinoLetConditionPattern 仍是行级正则全量替换：`\blet\s*\(\s*id\s*=\s*...`  会命中字符串字面量中的 "let(x=y)" 形如文本，导致语义改变（例如 `s.replace("let(a=1)", ...)` 被改写）。发生条件苛刻、影响是解析结果偏差而非安全问题。
- 修改方案：将该改写同样搬进 AST 路径（解析成功时按 ast.LetStatement/表达式偏移改写，解析失败保持原文）。
- 验证方式：测试：含字符串 "let(a=1) f()" 的脚本执行后断言字符串未被改写；现有 TestRewriteJavaCompatScript 保持通过。

### S05 脚本运行时性能

审核范围：internal/infra/script/runtime.go（Eval/EvalWithBindings/installSafeGlobals/javaBindings/runScriptCached）、cache.go、ttf.go、extensions.go（重计算部分）、variables.go（SourceBindings）、internal/bootstrap/bootstrap.go（装配）

#### S05-01 每 Eval 新建 goja.Runtime 并重建全套绑定：单次 ~280µs / 752 allocs，数百源并发搜索时成本放大 S×R 倍

- 级别：P1
- 类别：性能
- 直接相关文件：internal/infra/script/runtime.go:175（vm := goja.New()）、:467-497（installSafeGlobals）、:564-990（javaBindings）、:487（jsoupBindings）
- 间接相关文件：internal/infra/rule/analyzer.go（每条 @js: 步触发一次完整 Eval）、internal/bootstrap/bootstrap.go:108、docs/CliPerfReview-2026-10-17.md C-040（历史问题仍存在，但已有意识决策）
- 问题描述：历史 C-040/C-041/C-042/C-043 的"无预编译"部分已修复（precompiledPrograms :409-463、programCache 容量已到 1024、callSites 编译缓存 :396-402、runtime_fix_test.go/runtime_pool_test.go 已覆盖），但"每 Eval 新建 goja.Runtime"是 runtime.go:174-175 注释里明确声明的**有意设计**（放弃池化以避免跨源原型污染，并有 TestRuntimePrototypeMutationDoesNotLeakAcrossSources 佐证）。基准测出：单次 Eval ~280µs、77.7KB、752 次分配；其中编译只占 ~25%，其余 ~200µs 是 VM 创建、installSafeGlobals（3 次 RunProgram + ~30 个 vm.Set + javaBindings ~80 个闭包 + jsoupBindings）与绑定导出。规则引擎每条 @js: 步都走完整 Eval，数百源搜索时 = 源数×规则数 次 VM 创建，正是"数百书源时搜索极慢"的脚本侧主因之一。
- 修改方案：保留"跨 Eval 不共享"语义的前提下做安全池化——用 sync.Pool 池化 goja.Runtime，放回前执行固化的一段 reset 程序：`for (var k of Object.getOwnPropertyNames(globalThis)) if (!whitelist.has(k)) delete globalThis[k]`，并对 String.prototype/Object.prototype 逐个 deleteProperty 白名单外属性（goja 中这些属性可配置，问题只在 goja 对非严格模式 var 声明创建的不可配置属性——池化时只需拒绝复用声明过全局 var 的 VM，或在 reset 程序里用 Reflect.deleteProperty 清理；不可配置残留的 VM 直接丢弃）。放回前检查 ctx 未超时（已有 ErrScriptTimeout 语义）。跨源污染由现有 runtime_pool_test.go 五个隔离测试回归验证。预计单次 Eval 降到 ~50µs 级。
- 验证方式：`go test -race ./internal/infra/script/...`（五个隔离测试 + 并发测试必须全过）；bench 对比 ns/op 与 allocs/op；`go test -bench . -benchmem`。

#### S05-02 rewriteJavaCompatScript 在每次 Eval 缓存命中路径上仍执行 1 个正则 + sha256 全文哈希

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/script/cache.go:236-247（compileScript 每次 rewriteJavaCompatScript(source) + sha256(site+"\x00"+rewritten)）、runtime.go:1377-1380（rhinoLetConditionPattern.ReplaceAllString 无条件执行）
- 间接相关文件：internal/infra/script/runtime.go:214,1868,2569,2577,2631（5 个 runScriptCached 调用点）
- 问题描述：compileScript 注释称"改写只执行一次，不会在缓存命中路径上重复跑正则"，但实现是先 rewrite 再哈希再查缓存——缓存命中路径上 `rhinoLetConditionPattern.ReplaceAllString`（对全文扫描）与 `strings.Contains(script, "with")`、sha256 都重复执行。含 "with" 的 jsLib 还会在 programs.compile 命中路径上重复跑完整 AST parse（cache.go:214 同样先 rewrite 后查缓存）。对长 jsLib（数 KB～数十 KB）这是每次 Eval 的固定成本。基准中缓存命中与未编译差 ~25% 即主要来自这里。
- 修改方案：compile/compileScript 先用原文（未经改写）的 sha256 查一次"原文→改写文本"的小型 LRU 映射，命中则复用改写结果与已编译 Program；或缓存键用原文哈希、命中后跳过 rewrite（rewritten 文本随 programEntry 一起缓存）。compileScript 命中路径可拆为：哈希→查 entries→命中即返回（与 compile 相同结构，但 key 生成移到 rewrite 之后仍不可避免——故需原文级一级缓存）。
- 验证方式：bench 缓存命中路径 ns/op 下降；TestRuntimeRepeatedEvalUsesCachedProgram 回归。

#### S05-03 NamespacedCache 过期条目仅靠 Get 惰性清理，写满后被过期条目占位，且淘汰循环可能提前触发

- 级别：P3
- 类别：资源
- 直接相关文件：internal/infra/script/cache.go:64-105（PutTTL 淘汰循环 :84-89、Get 惰性删除 :96-100）
- 间接相关文件：internal/bootstrap/bootstrap.go:113（共享 scriptCache）
- 问题描述：TTL 过期条目只有被同名 Get 才删除；超过 maxEntries/maxBytes 时按"最久未写入"淘汰，可能先淘汰仍新鲜的条目而留下已过期条目（order 按写入序而非过期序），降低有效容量。进程内 4096 条/16MB 上限下影响有限，但脚本 cache.put 带 saveTime 的源多、长期运行时表现差。另外 PutTTL :86-89 循环同时以条目数与字节数驱动淘汰：单次 Put 一个大值后连续淘汰多个旧条目，其中可能包含刚被 Get 访问过的热点条目（LRU 未实现访问续期——Get 不 MoveToBack，与注释"按最久未写入淘汰"一致，但 Get 命中不续期意味着纯读热点也会被淘汰）。
- 修改方案：Get 命中时 MoveToBack（真实 LRU 语义）；可选：过期条目在 PutTTL 淘汰循环里跳过（先清过期再按容量淘汰）。
- 验证方式：单测：put A/B（B 带 TTL 且短），sleep 过期，put C 触发淘汰，断言 A 未被淘汰、B 已清。

#### S05-04 queryTTF 每次调用对同一字体数据全量重解析，无解析缓存

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/script/ttf.go:64-140（parseTTF）、:293-300（queryTTFValue 每次 parseTTF）
- 间接相关文件：internal/infra/script/runtime.go:967-971（queryTTF/queryBase64TTF 绑定）
- 间接相关（跨切片）：docs/ReviewIssues-2026-10-15.md 无对应条目
- 问题描述：同一书源一章内容里 replaceFont 常被每个 JS 步骤重复调用 queryTTF（URL/base64 相同），每次都重新拉取（URL 时）+ 全量解析 cmap/loca/glyf 并为每个字形生成指纹字符串。小型 TTF 基准 5.9µs，但真实中文字体（数千字形、glyf 数百 KB）指纹字符串构建是 O(numGlyphs×points)，可达数十 ms 级 [推测，未实测大字体]。且 URL 输入时每次 queryTTF 都重新 fetchBytes，无 HTTP 结果缓存。
- 修改方案：在 Runtime（或 runCtx sourceKey 命名空间）加 `map[[sha256.Size]byte]*ttfFont` LRU（数据哈希→解析结果，容量 ~16）；URL 输入按 URL 哈希缓存。ttfFont 只读，可并发共享。
- 验证方式：bench：同一 data 循环 queryTTFValue，解析路径 ns/op 应降为哈希查找级；TestRuntimeQueryTTFAndReplaceFont 回归。

#### S05-05 scriptGetElementAt 对非 JSON 输入每次都做 json.Valid 全文扫描

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/script/runtime.go:1795-1806（scriptGetElementAt 每次 json.Valid([]byte(text))）
- 间接相关文件：internal/infra/script/runtime.go:873-884（getElements/getElement 每次调用）
- 问题描述：getElement 对整段 HTML 正文先 json.Valid 全文扫描后必然失败（HTML 内容），再走 scriptGetElementsAt。长正文（数百 KB）每次 getElement 都多一次全文 JSON 验证扫描。同类问题：scriptInputString（:1829-1852）对 map 输入 json.Marshal。均属每次调用重复计算。
- 修改方案：scriptGetElementAt 的 JSON 判断移入 scriptGetElementsAt 之前的调用方，或先做廉价的 text[0] ∈ {'{','['} 预检再 json.Valid；analyzerContent 与其"是否 JSON"结果可在 setContent 时计算一次。
- 验证方式：bench：大 HTML 字符串上循环 java.getElement，ns/op 下降。

#### S05-06 installSafeGlobals 中 JavaImporter 返回 vm.GlobalObject() 并挂 importPackage：全局对象直接暴露给脚本

- 级别：P2
- 类别：安全
- 直接相关文件：internal/infra/script/runtime.go:1000-1006（JavaImporter 返回 obj := vm.GlobalObject(); obj.Set("importPackage", ...)）
- 间接相关文件：internal/infra/script/runtime.go:475-481（blocked 列表把 globalThis/window 设为 undefined，但 GlobalObject 本体可经 JavaImporter 拿到）
- 间接相关（跨切片）：S04ScriptSecurity 的 security_test.go
- 问题描述：`new JavaImporter()` 返回的是 VM 全局对象本体（挂了 no-op importPackage）。脚本借此绕开 :475-481 对 "global"/"globalThis" 的 undefined 遮蔽——虽然 goja 里 GlobalObject() ≠ globalThis 绑定，脚本仍可对全局对象直接 defineProperty/delete（例如 delete 掉被遮蔽的 require/process 遮蔽值，若未来有真实宿主对象注入则直接暴露）。当前沙箱下被遮蔽的都是 undefined，实际可利用性低，但这是防御纵深缺口：任何后续以 vm.Set 挂到全局的宿主对象（log、cache、java）本就该可见，而 require 等遮蔽值可被脚本 delete 后…… goja 无宿主注入，故风险有限。
- 修改方案：JavaImporter 改为返回 vm.NewObject()，并在其上挂 importPackage/importClass no-op；或返回带 null 原型的隔离对象。同步补测试：`JavaImporter().getOwnPropertyNames === undefined` 之类断言（确认非全局对象）。
- 验证方式：单测断言 `Object.getPrototypeOf(new JavaImporter())` 不是全局对象、JavaImporter() 返回值上枚举不到 java/cache 等全局绑定。

#### S05-07 EvalWithBindings 超时后 result 绑定导出对象可能逃逸共享引用

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/script/runtime.go:220-223（return value.Export(), nil）
- 间接相关文件：internal/infra/script/runtime.go:2073-2124（newSelectionBinding 把 *goquery.Selection 指针放进绑定 map 导出）
- 问题描述：Eval 返回 value.Export()；脚本若 return 绑定里的复合对象（source 绑定 map、newElementCollection），Export 会导出含 Go 指针（*goquery.Selection、*ttfFont）的 map/slice 到调用方。调用方（app 层）拿到后跨 Eval 复用时，这些对象携带闭包引用的 runCtx；ttf.go 的 handle 用 FLAG_FALSE 不可写，但 Export 后不再受保护。当前调用方只读使用，无实际损害，属防御性质量问题。
- 修改方案：Export 前对返回值做一层净化（只允许标量/string/[]any/map[string]any 递归拷贝，遇到含非导出 Go 类型的对象替换为其 String()），或文档化"调用方不得跨 Eval 复用返回对象"。
- 验证方式：单测：脚本 return getElement(...) 结果，Eval 后修改返回的 map，断言不影响后续 Eval。

#### S05-08 logBuffer.Append 每条消息都跑 RedactString + 超长截断用字节切片可能切断多字节 UTF-8

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/script/cache.go:162-167（message[:b.maxBytes] 按字节截断）
- 间接相关文件：internal/infra/logging（RedactString）
- 问题描述：单条超过 maxBytes(256KB) 时 `message[:maxBytes]` 按字节截断，中文日志（3 字节/字）可能在 rune 中间切断产生无效 UTF-8；调用方（如 debug 事件渲染）若按字符串处理会出现乱码。影响仅日志展示。
- 修改方案：截断后用 `for !utf8.ValidString(message) { message = message[:len(message)-1] }` 回退最多 3 字节，或按 rune 截断。
- 验证方式：单测：Append 一条 300KB 中文消息，Snapshot 后断言 utf8.ValidString。

### S06 规则分析器核心

审核范围：internal/infra/rule/analyzer.go、internal/infra/rule/parser/parser.go（对照 legado AnalyzeRule.kt、RuleAnalyzer.kt、AnalyzeByJSonPath.kt；关联 internal/infra/rule/regex/、css/、jsonpath/、xpath/、internal/lru/）

#### S06-01 @put 值正则 `@put:(\{[^}]+?\})` 无法匹配含 `}` 的值，嵌套对象/花括号值静默截断

- 级别：P1
- 类别：功能
- 直接相关文件：internal/infra/rule/analyzer.go:673（putRulePattern）、analyzer.go:726（parsePutRules）
- 间接相关文件：internal/infra/rule/analyzer.go:864（putStepVars）
- 问题描述：`putRulePattern = @put:(\{[^}]+?\})` 在第一个 `}` 处截断。probe 验证：`@put:{a:{k:1}}{class.t@text}` 返回空串——`{k:1}}` 被截成 `{k:1`，parseRuleObject 解析失败丢键，且剩余文本被当主规则。Legado 用平衡嵌套（RuleAnalyzer.chompCodeBalanced/innerRule）处理 `{$.rule}`，`splitPutRule` 同样支持含 `}` 的值。
- 修改方案：仿照 Legado 的平衡组方法，把 putRulePattern 换成手写扫描：找到 `@put:` 后用 `parser.consumeBalanced` 风格的括号平衡提取完整 `{...}`（忽略引号内括号），putStepVars 前一次性提取所有 @put 段。
- 验证方式：新增测试 `GetString("@put:{a:{k:1}}{class.t@text}", html)` 应返回 `T` 且变量 `a` 为 `{k:1}`；含 `}` 的 @get 值回归。

#### S06-02 @put 多键 map 迭代顺序随机，键间依赖（b 依赖 a）结果不确定

- 级别：P1
- 类别：功能/并发
- 直接相关文件：internal/infra/rule/analyzer.go:864-871（putStepVars）、analyzer.go:724（parsePutRules 返回 map）
- 间接相关文件：internal/infra/rule/analyzer.go:869（evalPutRule→GetString→lookupVariable 读 a.vars）
- 问题描述：putMap 是 Go map，putStepVars 按 map 迭代顺序求值。规则 `@put:{a:{{1}}}{b:{{java.get('a')}}}` 中 `java.get('a')`/`@get:{a}` 读 a.vars；若 b 先于 a 求值则得到空。probe 验证：30 次执行 ok=0 bad=30（Script 为 nil 时 put 求值失败暴露问题；[推测] 配置脚本运行时后该缺陷在键依赖场景仍会随机出现）。Legado putMap 虽也是 HashMap，但 Legado 的 put 值求值不依赖同规则先前的 put 顺序语义（put(key, getString(value)) 且 get 读的是 RuleData 全局，书源通常按书写顺序依赖 [推测]）。确定性解析顺序是更安全语义。
- 修改方案：parsePutRules 改为返回有序 `[]struct{Key,Value string}`（或 map+顺序切片），putStepVars 按规则书写顺序求值。
- 验证方式：测试 `@put:{a:{{1}}}{b:{{java.get('a')}}}{@js:java.get('b')}` 在配置脚本运行时下 100 次执行结果恒为 `1`。

#### S06-03 makeUpStep 中 {{}}/脚本模板缺 Script 时整条规则报错，Legado 中该类模板恒可用

- 级别：P2
- 类别：功能/错误处理
- 直接相关文件：internal/infra/rule/analyzer.go:975-1000（makeUpStep，`err = ErrScriptUnavailable`）、analyzer.go:331（evalStep 返回该错误）
- 间接相关文件：internal/app/book/toc.go:312-321（title 等规则GetString 被吞为空）
- 问题描述：规则替换段含 `{{1+1}}`/`{{'x'}}` 时 makeUpStep 要求 `a.ctx.Script != nil`，否则整条规则返回 `ErrScriptUnavailable`，规则整体失败（probe：`class.t@text##(.*)##{{1+1}}` → err=script runtime is not configured）。而顶层纯文本模板 `a{{1+1}}b` 在无 Script 时优雅回退为原文 `a1+1b`（analyzer.go:486-488 返回 token）。Legado 中 evalJS 用内嵌 Rhino/JS 引擎恒可用，书源里 `{{js}}` 模板极常见，未配置脚本运行时的部署下大量书源规则会整体失败。行为不一致（顶层回退 vs makeUpStep 硬失败）本身也是缺陷。
- 修改方案：makeUpStep 内 Script == nil 时与 expandRawString 一致：保留原 token 返回（或把 `{{纯算术}}` 走内建求值），不置 ErrScriptUnavailable。
- 验证方式：probe 用例 `GetString("class.t@text##(.*)##{{1+1}}", html)` 无 Script 时应返回原文/合理结果而非错误。

#### S06-04 GetURL 无结果返回空串，Legado 返回 baseUrl

- 级别：P2
- 类别：功能
- 直接相关文件：internal/infra/rule/analyzer.go:88-102（GetURL）
- 间接相关文件：internal/app/book/info.go:145,172（调用方以 `value != ""` 判断是否覆盖）
- 问题描述：Legado getString(isUrl=true) 在结果为空时 `return baseUrl ?: ""`（AnalyzeRule.kt:309-313）。Reader GetURL 注释自称"无结果返回空串，由调用方决定回退值"，但全部 12 处调用点（info.go/discovery.go/search/single_source.go）都直接判空跳过，没有任何调用方回退 baseUrl。影响：规则未命中时 coverUrl/tocUrl/bookUrl 为空而不是回退到源地址（如某些书源 coverUrl 规则失败时期望回退源 logo 域名）。
- 修改方案：GetURL 增加 `fallbackBase bool` 或在 Context 中带 BaseURL，无结果时按 Legado 语义返回 `urlrule.Absolute(RedirectURL, BaseURL)`；或至少在注释与调用方中统一决策。跨切片：调用方需同步调整。
- 验证方式：probe `GetURL("class.none@href", html)` 对照 Legado 语义应返回绝对化 baseUrl。

#### S06-05 GetURLList 去重，Legado getStringList(isUrl=true) 不去重

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/rule/analyzer.go:104-122（GetURLList seen map 去重）
- 间接相关文件：internal/app/book/toc.go:284、internal/app/content/content.go:432（NextTocURL/NextContentURL 分页）
- 问题描述：Legado AnalyzeRule.kt:290-297 的 urlList 仅按顺序收集并绝对化，无去重；AnalyzeByJSonPath/JSoup 同样不去重。Reader 在 analyzer 层加了去重。对正文/目录"下一页"规则通常无害，但对返回同一 URL 两次的列表规则（如分页规则 `{...}` 对象展开重复）会改变语义；[推测] 实际影响低，因为上层 seenPages 也会去重。
- 修改方案：确认意图后移除 analyzer 层去重（保留上层去重），或注释声明为有意增强。
- 验证方式：GetURLList 对 `["u1","u1"]` 规则的输出与 Legado 对齐（两项）。

#### S06-06 顶层规则链 `&&`/`||`/`%%` 语义与 Legado 不一致：无分隔符类型感知，`%%`/`||` 被当成步进链

- 级别：P2
- 类别：功能
- 直接相关文件：internal/infra/rule/analyzer.go:166-195（evalStringSteps 逐步链）、analyzer.go:197-221（GetElements）
- 间接相关文件：legado AnalyzeRule.kt:289-305（getString 逐步链确实逐步执行）、AnalyzeByJSoup.kt:88-118（子选择器层处理 || / %%）
- 问题描述：Legado 顶层 getString 也是逐步链（probe 对照一致），但 `||`/`%%` 的"或取首个非空 / 交错合并"语义在 Legado 是在子选择器层（AnalyzeByJSoup.splitRule("&&","||","%%")）实现的。Reader 把顶层 `class.a@text||class.b@text` 交给 css adapter 的 extractStrings 处理（probe：`||` 取首个非空正确、`%%` 交错正确），行为大体对齐。但 evalStringSteps 顶层链上 `&&` 步进时 collapseScalarResult 把每步结果 join "\n" 变字符串再进下一步（probe：`id.wrap@class.item@text&&tag.b@text` → `"Ib1\nb1"`，第二步在第一步的文本上重新跑 CSS，而 Legado 第二步跑在第一步 Elements 结果上时 isUrl=false 也走 getString(result) 相同逻辑——此项对齐 [核对无差异]）。真正差异：evalStep 中 `||` 语义只存在于 css/jsonpath/xpath adapter 内部，顶层链 `A||B` 中 A、B 是两个独立 step 顺序执行而非取或，`@js:...||@js:...`、`:regex1||:regex2` 这类非选择器规则下 `||` 被静默当 `&&`。probe D：`class.t@text||@js:result.toUpperCase()` 返回空串（css 步失败后 JS 步在空结果上执行）而 Legado 会在 JS 内或顶层取非空。
- 修改方案：evalStringSteps 识别链分隔符类型（复用 parser.Split 的 Separator），`||` 时遇首个非空结果短路、`%%` 时收集各组交错，`&&` 保持步进；或对含 `||`/`%%` 的顶层规则把整串交给各 mode adapter 处理。
- 验证方式：probe D 用例应返回 `AB`；新增 `A||B`（A 空 B 非空）与 `%%` 顶层测试。

#### S06-07 evalStep 每步都调用 putStepVars + makeUpStep，逐元素求值时重复展开 @get/{{}} 未缓存展开结果

- 级别：P2
- 类别：性能
- 直接相关文件：internal/infra/rule/analyzer.go:323-330（evalStep 入口 putStepVars+makeUpStep）、analyzer.go:958-1000（makeUpStep 对 step.raw 全量正则替换）
- 间接相关文件：internal/app/book/toc.go:299-321（逐章 NewAnalyzer + 逐规则 GetString）、internal/app/search/single_source.go:130-154
- 问题描述：sourceRule 分词已缓存（sourceRuleCache），但 makeUpStep 的 @get/{{}} 展开依赖 input，无法按规则缓存——这是结构性差异。逐元素（数百章目录）时每条规则每元素都做 2-4 次正则 ReplaceAllStringFunc（evalRulePattern、getRulePattern、jsRulePattern）+ isRuleExpression/GetString 递归。Legado makeUpRule 同样逐元素执行（无此层缓存），故非回归；但 Reader 额外在 evalStep 尾部对每个 string 结果再跑一次 expandRawString（makeUpString），与 makeUpStep 的展开重复：含 {{}}/@get 的规则全串被展开两次。
- 修改方案：makeUpStep 已展开时给 step 标记 expanded，evalStep 尾部 makeUpString 跳过重复 expandRawString（或仅对含 `@get:`/`{{` 的结果展开）；hasEval 为 false 时跳过 makeUpString。
- 验证方式：bench：对 1000 元素目录求值含 `##@get:{bookName}` 的标题规则，展开次数减半（可用计数 hook 验证）。

#### S06-08 asString 对 map/[]any 走 json.Marshal：大 Elements 列表逐元素序列化，正则/替换步骤开销大

- 级别：P2
- 类别：性能
- 直接相关文件：internal/infra/rule/analyzer.go:1044-1066（asString map[string]any,[]any 分支 json.Marshal）、analyzer.go:1004-1023（applyReplace default 分支 asString）
- 间接相关文件：internal/infra/rule/regex/regex.go（Element/Elements 输入）
- 问题描述：JSON/正则混排规则链中，`[]any`（jsonpath List 结果，可能上千元素）每经过一个正则 step 或 applyReplace 就整体 json.Marshal 一次（asString(input)），随后 regex 在序列化大字符串上跑。`groupsToAny` 保留 `[][]string`，再经 asString 也走 Marshal。数百书源搜索时该路径乘以书源数放大（与已知"多书源搜索慢"相关）。
- 修改方案：applyReplace 对 `[]string` 已逐项处理（好）；对 `[]any` 也应逐项 asString 后逐项替换，避免整体 Marshal；evalStep ModeRegex 分支 asString(input) 可对 `[]any` 先扁平化成 []string 复用。
- 验证方式：bench 对 5000 元素 jsonpath→regex 链，修改前后 ns/op 与 allocs 对比。

#### S06-09 expandRawString 内 GetString 递归无深度限制，恶意/畸形规则可栈溢出

- 级别：P2
- 类别：安全/健壮性
- 直接相关文件：internal/infra/rule/analyzer.go:362-400（expandRawString→a.GetString→evalStringSteps→evalStep→makeUpString→expandRawString 循环通路）
- 间接相关文件：internal/app/search/single_source.go（书源规则来自用户导入，不可信输入）
- 问题描述：规则串由书源导入，属不可信输入。`{{...}}` 内 isRuleExpression 命中 `@`/`$.` 时调用 a.GetString(expr)，expr 可再含 `{{...}}` 嵌套展开，每层深一轮完整解析+可能脚本执行，无递归深度上限。畸形规则如 `{{@get:{a}}{{@get:{b}...` 或自引用 @put 可造成指数/深递归。goja 脚本执行有超时，但纯规则递归无保护。[推测] 实际可构造出深递归 panic（栈溢出不可恢复），单用户自托管下风险有限但存在。
- 修改方案：Analyzer 增加求值深度计数器（evalDepth），expandRawString/GetString 递归超阈值（如 16）返回错误或原 token。
- 验证方式：构造 32 层嵌套 `{{@get:{x}}}` 规则，修复后返回错误而非 panic；go vet/test 全绿。

#### S06-10 sourceRuleCache 只缓存分词，makeUpStep 后 expanded 串重新走 splitReplace 但未回写缓存；`##` 分割 strings.Split 无转义语义

- 级别：P3
- 类别：功能/性能
- 直接相关文件：internal/infra/rule/analyzer.go:753-768（splitReplace strings.Split(rule,"##")）、analyzer.go:958-1000
- 间接相关文件：legado AnalyzeRule.kt:698-707（makeUpRule 尾部同样 split("##")）
- 问题描述：与 Legado 一致（`##` 不支持转义），但 Reader 在 makeUpStep 展开后重新 splitReplace，若展开值本身含 `##`（如 @get 取到的变量含 `##`），替换段会被错误重切；Legado makeUpRule 在展开后的串上 split("##") 行为相同——核对为一致 [核对无差异]，仅列出潜在坑：值含 `##` 时替换语义漂移，两边同病。
- 修改方案：可选增强：splitReplace 只在首个/固定数量的 `##` 处切（与 Java split 语义对齐已一致），文档化限制即可。
- 验证方式：对照 Legado 行为的黄金用例。

#### S06-11 parser.Split 的 `&&`/`%%` 匹配未考虑引号/平衡组外转义，validateBalanced 与 Split 对 `\` 处理不一致

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/rule/parser/parser.go:31-70（Split 主循环）、parser.go:72-89（matchSeparator）、parser.go:92-117（validateBalanced）
- 间接相关文件：internal/infra/rule/css/css.go:128,157,194,223,331、jsonpath/jsonpath.go:170、xpath/xpath.go:39,104
- 问题描述：validateBalanced 遇 `\` 跳过下一字节（parser.go:97-100），但 Split 主循环 matchSeparator 在引号内仍会匹配分隔符吗？——不会：引号由 consumeQuoted 消费（parser.go:57-66），平衡组由 consumeBalanced 消费。但 `(a&&b)` 内的 `&&` 被 consumeBalanced 保护，正确；问题在于转义字符：validateBalanced 的 `\` 跳过在引号外生效，而 Split 主循环的 `\` 不跳过（`r == '\\'` 无特殊处理，写 rune 后继续 matchSeparator），规则 `a\&&b` 中 `\&` 后的 `&&` 仍会被切分，两个函数行为不一致。另外 consumeQuoted 出错时（未闭合引号）Split 返回错误，但 Legado 规则里未闭合引号（如 `text.'`）常见于畸形书源，Legado RuleAnalyzer 容错继续，Reader 直接 ErrSyntax 整规则失败。
- 修改方案：Split 主循环对引号外 `\` 与 validateBalanced 对齐（跳过下一 rune）；未闭合引号可降级为按字面量继续（记录 warning）而非 ErrSyntax，对齐 Legado 容错。
- 验证方式：测试 `Split("a\\&&b")` 应不切分或与 validateBalanced 一致；畸形引号规则在 css.go:157 路径不再整链失败。

#### S06-12 lookupVariable 每次调用遍历 carriers 并 reflect.FieldByName，逐元素热点路径反射开销

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/rule/analyzer.go:1196-1220（lookupVariable）、analyzer.go:1223-1252（carrierStringField 反射）、analyzer.go:1310-1340（lookupVariableFromStructCarrier 反射）
- 间接相关文件：internal/infra/rule/analyzer.go:364-375（expandRawString 每个 @get token 调用一次）
- 问题描述：每个 `@get:{key}` token 展开都走 lookupVariable → lookupInputField → lookupVariableFromContext（input+ctx 两轮）→ variableCarriers 分配切片 → 反射 FieldByName。逐章/逐书求值时（含 @get 的替换规则）反射调用可达数千次/页。Legado get() 直接查 map（AnalyzeRule.kt:754-764）。
- 修改方案：对 Book/Chapter 载体用类型断言（`*book.Book` 等）替代反射 FieldByName；variableCarriers 返回固定数组避免切片分配；常见键（bookName/title/bookUrl 等）短路查 map。
- 验证方式：bench 目录求值含 `##@get:{bookName}` 规则 ns/op、allocs 前 -race 后对比。

#### S06-13 GetElements 对多步链 `&&` 各步独立取元素后仅保留最后一步结果（selectElements out 覆盖），Legado 同样逐步覆盖但 Reader 丢失中间 step 的 replaceRegex

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/rule/analyzer.go:197-221（GetElements 链尾 result 为最后一步）、css/css.go:194-215（selectElements 各步独立 select 后合并 out）
- 间接相关文件：legado AnalyzeRule.kt:336-360（getElement 逐步链）
- 问题描述：核对 GetElements 逐步链与 Legado getElement 一致（逐步把上一步结果作下一步输入）；css.selectElements 内部 `&&` 的多分支合并逻辑独立处理 [核对大体一致]。差异点：evalStep 中 ModeJS 步骤若返回 Go 结构体（脚本返回 JS 对象），后续 CSS 步 newCSSAdapter 走 `css.New(asString(input))`——JS 对象被 json.Marshal 成 JSON 文本再当 HTML 解析，必然解析失败整链报错。Legado 中 evalJS 返回的对象可直接继续被 JSonPath/CSS 分析（AnalyzeRule.kt:292-296 对 NativeObject 特判）。probe 佐证：JS 步后接 CSS 步的规则链产出异常。
- 修改方案：evalStep 在 JS 步结果为 map[string]any/[]any 时，后续 Default 步优先走 parsedJSONAdapter（类似 isJSONInput 分支），不要把结构化数据序列化成 HTML 解析。
- 验证方式：测试 `@js:JSON.parse('{"a":"x"}')` + `$.a` 链应返回 `x`；`@js:返回对象` + `class.t@text` 应明确报错而非 JSON-as-HTML。

#### S06-14 containsScriptRule/splitEmbeddedJSRules 大小写折叠重复 ToLower，分词热点小分配

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/rule/analyzer.go:569-574（containsScriptRule）、analyzer.go:782-835（splitEmbeddedJSRules 每轮循环 strings.Index lower 切片）、analyzer.go:712-750（parseSourceRule 多次 strings.ToLower(rule)）
- 间接相关文件：internal/infra/rule/analyzer.go:691-696（缓存入口）
- 问题描述：parseSourceRule 对每段规则调用 6+ 次 strings.ToLower(rule)（@js:/<js>/@xpath:/@css:/@json: 各一次），splitEmbeddedJSRules 整串 ToLower 一次。因有 sourceRuleCache 分词缓存，只在缓存未命中时执行——数百书源首次搜索时每条规则 5-10 次整串 ToLower 分配。低影响（缓存后仅一次），列出备查。
- 修改方案：parseSourceRule 入口 ToLower 一次传递；或用 strings.EqualFold/前缀字节比较（'@'/'j' 大小写判断）避免分配。
- 验证方式：bench 分词 allocs。

#### S06-15 GetURL 每项调用 unescapeHTMLEntities + Absolute + TrimSpace，ParseCustom 重复解析属性 JSON

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/rule/analyzer.go:90-99（GetURL）、internal/infra/rule/url/url.go:16-43（ParseCustom：JSON Unmarshal + 宽松正则修复，每次调用全新解析）
- 间接相关文件：internal/infra/rule/url/url.go:95-99（Absolute→ParseCustom）
- 问题描述：GetURLList/GetURL 对每个候选 URL 调 Absolute→ParseCustom，ParseCustom 内含 json.Unmarshal、singleQuotedStringsToDouble 逐 rune 扫描、looseObjectKeyPattern 正则替换，无缓存。目录/正文分页规则每页至少 1-2 次，开销可忽略；但搜索去重循环内逐项 Absolute（GetURLList）对长列表有放大。低影响。
- 修改方案：Absolute 对无 `,` 的普通 URL（绝大多数）走快速路径（先 strings.IndexByte(raw, ',')，无逗号直接 absolutePlain），ParseCustom 仅在有属性段时调用。
- 验证方式：bench GetURLList 1000 项。

#### S06-16 evalTemplateExpr 的 `$.` 前缀分支 `copy := *a` 共享 vars map，嵌套求值中 put 写变量会泄漏到外层 Analyzer

- 级别：P3
- 类别：并发/质量
- 直接相关文件：internal/infra/rule/analyzer.go:443-447（`copy := *a; return copy.GetString(...)`）
- 间接相关文件：internal/infra/rule/analyzer.go:869（putStepVars 写 a.vars）
- 问题描述：`copy := *a` 浅拷贝 Analyzer，vars map 与 ctx 共享。模板表达式 `{{$.rule}}` 求值期间若该规则含 @put，写入的是同一份 vars map（外层可见）。单链串行下无数据竞争（注释已声明前提），但变量作用域与 Legado 的 RuleData 作用域语义可能有差异 [推测]；跨 goroutine 时（如多个 Analyzer 共享同一 ctx.Book 载体的 Variable 字段写入，storeVariableOnStructCarrier 对共享 *book.Book 并发 SetString）存在竞争面——调用方 toc.go 并发分页已用 book 快照规避（toc.go:212 注释），已缓解。
- 修改方案：文档化 vars 共享语义；或在 copy 时对 vars 做写时复制（仅 putStepVars 检测到 copy 标记时新建 map）。优先级低。
- 验证方式：-race 下并发分页目录回归（已有覆盖）。

#### S06-17 evalPutRule 先 lookupInputField 再 GetString：put 值为纯字段名时语义与 Legado getString 不同

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/rule/analyzer.go:873-885（evalPutRule）
- 间接相关文件：legado AnalyzeRule.kt:396-401（putRule 直接 put(key, getString(value))）
- 问题描述：Legado put 值恒走 getString（选择器/模板求值）；Reader 先查 input map 字段，命中即返回原值，不再求值。当 input map 中恰好存在与选择器同名的键（如 `{"name":"class.a@text"}`）时，Reader 把字段值当字面量返回，Legado 会把它当规则求值。边界场景，低影响。
- 修改方案：仅在值为非规则形态（不含 @ . / {{ 等）时走字段快捷路径，或删除快捷路径恒走 GetString（GetString 对纯文本返回原文，行为兼容）。
- 验证方式：测试 put 值与 input 键同名场景。

#### S06-18 历史问题核对结果（记录/证据项，不计入统计）

- 级别：P3
- 类别：功能
- 直接相关文件：docs/ReviewIssues-2026-10-15.md:52、docs/CliPerfReview-2026-10-17.md:336-653
- 问题描述（核对当前代码）：
  - literal 误判（历史 P1）：已修复——analyzer.go:737-741 现按"首个 @get/{{}} 位于 ## 之前"判定（`loc[0]==0 || !strings.Contains(rule[:loc[0]],"##")`），makeUpStep 在完整 raw 上展开（对齐 Legado makeUpRule）。不列。
  - URL 绝对化缺 isUrl 门槛（历史 P1）：已修复——GetStringList 不再做 URL 归一化，仅 GetURL/GetURLList 走 Absolute（analyzer.go:88-122）。但见 S06-04 的回退语义差异。
  - splitSourceRules 无缓存（历史 F3/C-030）：已修复——sourceRuleCache LRU（analyzer.go:680-696）。
  - 正则重复编译（历史 issue）：已修复——patternCache（regex/regex.go:27,177-196）、matcherCache/parsedCache（css/css.go:41-44）。
  - JS 步收到 *parsedInput（C-051）：已修复——analyzer.go:254 unwrapParsedInput。
  - asString(*parsedInput) fmt.Sprint 失配（C-052）：已修复——analyzer.go:1049-1051 专用 case。
  - bookName/title 特判缺失（历史）：已修复——analyzer.go:1198-1205。
  - 实体反转义不一致（历史）：已修复——统一走 unescapeHTMLEntities（GetString 入口），htmlEntityPattern 仅匹配分号结尾实体。
  - 残留：jsonpath isEmptyTraversalError 类型名匹配（历史 float64 issue）已修复（jsonpath.go:186-196 现匹配 "unsupported value type ... for select"）。
  - 仍存在的相关历史问题见本报告 S06-07/S06-08（逐元素展开与 asString Marshal 开销，与"多书源慢"直接相关）。
- 修改方案：无需修改（记录性条目）。
- 验证方式：—。

### S07 子规则引擎(css/xpath/jsonpath/regex/url)

审核范围：internal/infra/rule/css/css.go、internal/infra/rule/xpath/xpath.go、internal/infra/rule/jsonpath/jsonpath.go、internal/infra/rule/regex/regex.go、internal/infra/rule/internal/lru/lru.go、internal/infra/rule/url/{url,option,page,encoding}.go、internal/infra/rule/parser/parser.go、internal/infra/rule/analyzer.go（子引擎消费点）、internal/infra/requestoption/request.go（AnalyzeUrl 选项求值）

#### S07-01 `text.` 前缀规则对每个元素调 ownText，大文档 O(docsize×命中数) 慢且分配巨大

- 级别：P1
- 类别：性能
- 直接相关文件：internal/infra/rule/css/css.go:243-278（filterDeepestTextMatch）
- 间接相关文件：internal/infra/rule/analyzer.go:319（evalStep 默认分支消费）、docs/CliPerfReview-2026-10-17.md C-036（历史：二次扫描已修，但每元素 ownText 仍是 O(n)）
- 问题描述：filterDeepestTextMatch 先 `root.FindMatcher(matchAnyElement)` 取全文档所有元素，再对每个元素调 `ownText`（css.go:767-782，遍历直接子文本节点并 Join），随后第二遍沿命中节点祖先链标记。`text.` 规则常用于正文规则（如 `text.第?章`），在数千节点章节上每次求值都全量扫描。基准证据：2000 元素×3 层文档 `text.` 命中 1 项要 129.5ms、47.5MB、30155 allocs/op；同文档逗号选择器仅 3.6ms/0.56MB。Legado 的 getElementsContainingOwnText（JsoupExtensions.kt，NodeTraversor 单遍）为 O(n)。
- 修改方案：把两遍扫描合并为一次自建 DFS：递归访问元素节点，先计算自身 ownText 并缓存，命中时沿祖先链（用 `node.Parent` 上溯）标记"有更深命中"后输出；避免对每个节点构造 goquery.Selection（改直接用 *html.Node 判断）。或预建"元素→ownText"单遍映射再筛选。
- 验证方式：`go test ./internal/infra/rule/css/ -bench 'Text' -benchmem`，目标 2000 元素命中场景从 129ms 降到 <10ms；保留 TestSelectStepTextRuleKeepsDeepestMatch 等既有测试全过。

#### S07-02 `class.a.b` / `tag.p.q` 多段语法与 Legado 语义不一致

- 级别：P2
- 类别：功能
- 直接相关文件：internal/infra/rule/css/css.go:386-410（selectBase class./tag. 分支）、css.go:155-170（normalizeSelectorToken）
- 间接相关文件：legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeByJSoup.kt:316-317；docs/LegadoAlignmentReport.md:325（称"支持多 class"，与 Legado 实际语义不符）
- 问题描述：Legado `class.a.b` 按 `beforeRule.split(".")` 取 `rules[1]`，即 getElementsByClass("a")——只按**第一段**匹配；`tag.p.q` 同理 getElementsByTag("p")。Reader 归一为 cascadia `.a.b` / `p.q`，要求**全部**段同时命中。探针：`<p class="q">X</p><p>Y</p>` 查 `tag.p.q@text` Reader 得 `[X]`，Legado 应得 `[X, Y]`；`<div class="item">` 查 `class.item.title@text` Reader 空、Legado 命中。写了 `class.item.title` 这类常见写法的书源在 Reader 下漏匹配。
- 修改方案：selectBase 的 class./tag. 分支与 normalizeSelectorToken 一样只取第一段（`strings.Split(rule[6:], ".")[0]`），或与 Legado 报告文档对齐其一——关键是消歧：要么改实现取 rules[1]，要么确认 Legado 语义后修文档。需注意 normalizeSelectorToken（extractStep 链中间步）与 selectBase 必须一致。
- 验证方式：探针用例 `<p class="q">X</p><p>Y</p>` + `tag.p.q@text` 期望与 jsoup 行为一致；补 css_test.go 用例。

#### S07-03 jsonpath 不支持 jayway 的 `=~` 正则过滤运算符

- 级别：P2
- 类别：功能
- 直接相关文件：internal/infra/rule/jsonpath/jsonpath.go:18-24（language 组合）
- 间接相关文件：internal/infra/rule/analyzer.go:261-271（JSON 步骤直接上抛 ErrRuleParse）
- 问题描述：language 组合了 pjsonpath.Language() + Arithmetic + Text + PropositionalLogic，但 gval 无 `=~` 运算符。探针：`{"l":[{"t":"abc"},{"t":"xyz"}]}` 查 `$.l[?(@.t=~/a.c/)].t` 报 `parsing error ... unknown operator =~/`，整条规则失败（analyzer.go:315 显式 JSONPath 错误直接上报不回落）。Legado 走 jayway JsonPath，`[?(@.t=~/a.c/)]` 是常见书源过滤写法。其余运算符（`> < >= <= && || !=`、存在过滤、`['k-1']` 带连字符键、单引号转义、`[::2]`、`[-1:]`）探针全部通过。
- 修改方案：在 gval.NewLanguage 里注册自定义 `=~` 二元运算（gval.PostfixOperator / InfixOperator），左侧字符串右侧 `/.../` 字面量，编译为 regex.MatchString；或预处理规则把 `=~//` 改写为自定义函数调用。
- 验证方式：jsonpath_test.go 补 `$.l[?(@.t=~/a.c/)].t` 用例。

#### S07-04 parser.Split 对规则中的未闭合单引号硬失败，Legado 容错继续匹配

- 级别：P2
- 类别：功能
- 直接相关文件：internal/infra/rule/parser/parser.go:25-27、44-49、135
- 间接相关文件：internal/infra/rule/css/css.go:72-73、internal/infra/rule/analyzer.go（所有走 parser.Split 的路径）
- 问题描述：validateBalanced/consumeQuoted 遇到未闭合引号返回 ErrSyntax，整条规则直接失败。探针：`div:contains(it's)@text` 在 `<div>it's ok</div>` 上返回 `rule syntax error: unclosed quote '''`，本应匹配 `[div it's ok]`。Legado RuleAnalyzer 不会因未闭合引号让整个选择器失败。书源规则含撇号文本（英文书源常见）时 Reader 全链失效。
- 修改方案：Split 中 consumeQuoted 失败时降级为把该引号当普通字符继续（返回已消费部分 + err=nil），仅在规则字符串场景（模板/JS）保留硬错误；或给 Split 加 lenient 选项供选择器路径使用。
- 验证方式：parser_test.go 补 `div:contains(it's)@text` 与 `a[href~=\[\]]@href`（现有行为保持）用例。

#### S07-05 `optionValue` 按大小写不重复键取值时结果不确定

- 级别：P3
- 类别：质量（并发正确性）
- 直接相关文件：internal/infra/requestoption/request.go:312-318（optionValue，遍历 map）
- 间接相关文件：internal/infra/rule/url/url.go:32-39（ParseAttributeObject 反序列化后键保持原大小写）
- 问题描述：URL 选项 JSON 里出现 `{"Headers":{...},"headers":{...}}` 这类仅大小写不同的重复键时，optionValue 靠 Go map 随机迭代顺序取第一个匹配，同一条 URL 两次构建结果可能不同。Legado 的 GSON 解析是"后键覆盖前键"，确定。单用户自托管下影响小，但属于可复现性缺陷。
- 修改方案：ParseAttributeObject 阶段就把键归一为小写并做后键覆盖（与 GSON 语义一致），optionValue 直接小写查表，删掉逐键 EqualFold 扫描。
- 验证方式：url_test.go 补重复大小写键用例，断言确定取"后键"。

#### S07-06 `{"type":""}` 空串触发 hex 响应编码，Legado 视为未设置

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/requestoption/request.go:45-47
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:408-409、492-498；legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeUrl.kt:746-759（setType/getType：isNullOrBlank → null）
- 问题描述：`if value, ok := optionValue(custom.Attribute, "type"); ok && value != nil { base.EncodeBodyAsHex = true }` 对 `{"type":""}` 也开 hex；Legado 中 type 空白串被 setType 归为 null，不触发 getStrResponseAwait 的 hex 分支（AnalyzeUrl.kt:405）。空串 type 的书源会拿到十六进制正文导致解析失败。
- 修改方案：改为 `optionString(custom.Attribute, "type") != ""` 判断（与 Legado isNullOrBlank 一致）。
- 验证方式：requestoption 测试补 `{"type":""}` 不设 EncodeBodyAsHex、`{"type":"1"}` 设定的用例。

#### S07-07 疑似死代码：LooksLikeFormBody 与 ApplyRetryOption 无生产消费方

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/rule/url/option.go:13-48
- 间接相关文件：internal/infra/rule/url/option_test.go；internal/infra/requestoption/request.go（用 isStructuredBody / positiveInt 替代）
- 问题描述：全仓（排除测试）检索 `LooksLikeFormBody`、`ApplyRetryOption` 无调用点；注释宣称"搜索、书籍信息、正文与脚本四条抓取链路共用这一份实现，避免行为漂移"，实际表单判断走 requestoption.isStructuredBody（json.Valid 或 `<` 前缀），retry 走 positiveInt。两者判定规则已漂移（LooksLikeFormBody 要求含 `=`/`&`，isStructuredBody 不看），注释误导维护者。
- 修改方案：删除 option.go 的两个函数及其测试，或让 requestoption 改为消费它们（二选一，倾向删除）。
- 验证方式：`grep -rn "LooksLikeFormBody\|ApplyRetryOption" --include=*.go | grep -v _test` 为空；`go build ./...`。

#### S07-08 lru.GetOrCompute 锁外 compute，并发首扫同 key 重复编译（历史问题，评估为不修，记录）

- 级别：P3
- 类别：并发
- 直接相关文件：internal/infra/rule/internal/lru/lru.go:45-52
- 间接相关文件：internal/infra/rule/css/css.go:76-86、internal/infra/rule/regex/regex.go:31-43、internal/infra/rule/jsonpath/jsonpath.go:37-45
- 问题描述：compute 在两次 Lock 之间执行，数百书源并发首扫时同一 pattern/selector 可能重复编译，一次性浪费，结果幂等无正确性影响。`-race` 下 lru、css、jsonpath、regex、xpath 全部并发探针与全量测试通过（`go test -race ./internal/infra/rule/...` ok），无数据竞争。docs/CliPerfReview-2026-10-17.md C-038 已记录为不修，维持该结论。
- 修改方案：如需消除，可加 singleflight；当前不建议（复杂度大于收益）。
- 验证方式：`go test -race ./internal/infra/rule/internal/lru/`。

#### S07-09 BodyJS 在 plain-HTTP 回退路径上执行，与 Legado webJs 仅 WebView 生效的语义有偏差

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/requestoption/request.go:102-119（Fetch 对 BodyJS 无条件求值）
- 间接相关文件：internal/app/content/content.go、internal/app/search/single_source.go（Fetch 消费方）；legado/app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeUrl.kt:400-470（webJs 只在 useWebView 分支的 getStrResponse 中执行）
- 问题描述：`{"webView":true,"webJs":"..."}` 时 Reader 设 WebViewFallback=true 且 BodyJS=webJs；Fetch 不论是否 WebView 回退都执行 BodyJS。项目明确不引入 webView、回退 plain HTTP，此时对静态 HTML 跑 webJs 的结果与 Legado 在 WebView 页面上跑不同（Legado 中 webJs 操作的是渲染后 DOM）。[推测] 实际书源多为取 innerText，行为近似，故定 P3；但 `{"webJs":"...","webView":false}` 时 Reader 也会执行 BodyJS，而 Legado 完全不执行（getStrResponse 非 webView 分支不跑 webJs），这是确定偏差。
- 修改方案：Fetch 中仅在 `req.WebViewFallback` 为 true 时执行 BodyJS；webJs 且未声明 webView 的组合在 Build 时丢弃 BodyJS（对齐 Legado）。
- 验证方式：content/content_test.go 补 `{"webJs":"document.body.innerText"}`（无 webView）断言 BodyJS 为空且响应原文返回。

#### S07-10 正则替换串中裸 `$` 的展开语义与 Java 有边界差异（记录）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/rule/regex/regex.go:200-208（replaceFirstStd 用 ExpandString）、regex.go:172-175（ReplaceAllString）
- 间接相关文件：internal/infra/rule/analyzer.go:339-348（applyReplace）
- 问题描述：Go `ExpandString`/`ReplaceAllString` 用 `$name`/`${name}` 展开，`$` 后跟非组名字符时行为与 Java `$` 保留有细微差异；探针 `Replace("a","a","$",false)` 得 `$`（无异常），常见场景（`$1` 分组引用）探针正确。未构造出实际失败用例，列为记录项：书源替换文本含 `$` 字面量（如金额符号）时建议显式 `$$` 转义或预处理。RE2/regexp2 回退链路（反引用 `\1`、灾难回溯、`\p{Han}`、非法 pattern 字面量降级）探针全部符合预期；regexp2 已设 1s MatchTimeout。
- 修改方案：替换文本进入 Expand 前把字面 `$`（不属于 `$\d`/`${`）预处理为 `$$`。
- 验证方式：regex_test.go 补替换文本含 `$100` 字面量的用例。

### S08 抓取器 fetcher

审核范围：internal/infra/fetcher/http_fetcher.go、cookies.go、rate_limiter.go 及同包测试；接线 internal/bootstrap/bootstrap.go:99-108、internal/infra/config/config.go:69-72；Legado 参照 ConcurrentRateLimiter.kt、AnalyzeUrl.kt、OkHttpUtils.kt、CookieManager.kt。

#### S08-01 Client.Timeout 覆盖整个请求周期，多跳重定向共享同一总预算，且 DNS 缓存命中路径仍消耗总超时

- 级别：P2
- 类别：功能|性能
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:271（`client := &http.Client{... Timeout: f.timeout ...}`）
- 间接相关文件：internal/infra/config/config.go:69（默认 30s）
- 问题描述：`http.Client.Timeout` 是从发起请求到读完整个 body 的总时限，会同时限制连接、TLS、所有重定向跳和响应体读取。Legado（OkHttp）分层为 connect/read/write/call 超时。对慢速大章站点（接近 maxBodyBytes 的 10MB 响应 + 5 跳重定向），单跳正常但总时长超过 30s 即失败，且没有独立的连接/响应头超时语义。`Transport` 上没有 `ResponseHeaderTimeout`，慢响应头站点会占满 Client.Timeout。缺少分跳粒度控制是层级问题而非错误，但"响应头慢 + 大 body"场景会误报超时。证据：http_fetcher.go:259-271 只设 Client.Timeout，Transport 无 ResponseHeaderTimeout。
- 修改方案：改为 Transport 层分层超时：`Dialer.Timeout`（现固定 30s，未随 WithTimeout 调整）、`TLSHandshakeTimeout`、新增 `ResponseHeaderTimeout: f.timeout`，Client.Timeout 保留为整体上限或去掉；dialer 的 30s 应取 `min(f.timeout, 30s)`。
- 验证方式：测试慢响应头（handler 先 sleep 再写头）与大 body 分别计时；断言 dialer 超时随 WithTimeout(100ms) 变为 ≤100ms。

#### S08-02 net.Dialer 的 30s 连接超时不受 WithTimeout 配置约束

- 级别：P2
- 类别：功能
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:85（`dialer := &net.Dialer{Timeout: 30 * time.Second, ...}`）
- 间接相关文件：internal/bootstrap/bootstrap.go:100
- 问题描述：`NewHTTPFetcher` 里 dialer 硬编码 30s，与 `f.timeout`（可配置，测试里用 5ms）完全脱钩。WithTimeout(5ms) 时 TCP 连接阶段仍可挂 30s，仅靠 Client.Timeout 兜底截断；若 Client.Timeout 被设很大而连接目标不可达（DROP 而非 REJECT），单次拨号挂满 30s×多 IP 尝试。证据：http_fetcher.go:85 硬编码，无任何 Option 影响 dialer。
- 修改方案：构造 dialer 时用 `f.timeout`（在 opts 应用后再建 dialer，或 dialer 在 dialContext 内取 `min(f.timeout, 30s)`）。
- 验证方式：`WithTimeout(100ms)` 下对 DROP 端口拨号，断言总耗时 <2s。

#### S08-03 Fetch 失败重试不区分幂等性，非幂等方法（POST）也自动重试

- 级别：P2
- 类别：功能
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:158-186（retry 循环对 `resp, err := f.fetchOnce(...)` 的任何 err 都 `continue`）
- 间接相关文件：legado/app/src/main/java/io/legado/app/help/http/OkHttpUtils.kt
- 问题描述：请求失败（含网络错误、body 超限、状态码非 2xx）时无条件重试 `Retry` 次。POST 请求（书源登录、submit 类）在连接已建立、服务器可能已处理的情况下重试会造成重复提交。fetchOnce 返回 "unexpected status" / "response body too large" 这类确定性错误也被重试，白白放大延迟（指数退避最高 2s×Retry）。证据：http_fetcher.go:175-184 无 method 判断、无错误分类。
- 修改方案：按 method 分流——GET/HEAD 重试网络类错误，POST 不重试或仅在连接未建立（`net.OpError` 且无 response）时重试；确定性错误（too large、ErrBlocked、unsupported scheme）直接返回不重试。
- 验证方式：单测 POST handler 计数，连接中断后断言 handler 至多收到一次请求；`too large` 错误断言单次尝试。

#### S08-04 非 gzip 响应嗅探出 gzip magic 时，解压后的 Content-Encoding 判断与重试语义丢失原始 body 限长一致性

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:486-494
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:474-489
- 问题描述：第二段 sniff 解压用 `io.ReadAll(io.LimitReader(gzipReader, f.maxBodyBytes+1))` 后，`len(respBody) > maxBodyBytes` 检查在此分支解压结果上仍执行（:489 在 sniff 之后），逻辑正确；但 sniff 分支若 `gzip.NewReader` 失败（非真 gzip、只是恰好 1f8b 开头的二进制），返回 `err` 使整次 fetch 失败——而 Legado OkHttp 对此直接按原文本处理。恰好以 0x1f 0x8b 开头的二进制（如某些图片/PDF）会被误判并报错。证据：http_fetcher.go:487-489 无 err 回退到原字节路径。
- 修改方案：sniff 分支 `gzip.NewReader` 失败时保留原 `respBody` 继续走文本解码，而非返回错误。
- 验证方式：构造 body 为 `[]byte{0x1f,0x8b,0,0}` 的响应，断言 Fetch 成功返回原字节。

#### S08-05 gzip 解压误报：`unexpected EOF` 在截断（超限）时被当作网络错误导致重试

- 级别：P3
- 类别：质量|性能
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:478-479
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:175-184（重试循环）
- 问题描述：gzip 流超限被 LimitReader 截断后未显式 Close/Drain，错误路径只在 ReadAll 本身报错时触发；但 Content-Encoding: gzip 的响应体在 maxBodyBytes 处截断时，下一次 Read 会返回 `unexpected EOF`，fetchOnce 返回该 err，Fetch 的重试循环把它当作瞬时网络错误重试整个请求，每次都白白下载 maxBodyBytes+1 后再截断——对超多源批量场景放大带宽与时延。证据：io.ReadAll(gzipReader) 在截断处 err=io.ErrUnexpectedEOF，无分类。
- 修改方案：先读原始 body（LimitReader 至 maxBodyBytes+1，判断超限），再做 gzip 解压（当前顺序对 Content-Encoding 路径相反）；或对 err 是 `io.ErrUnexpectedEOF` 且解压字节数已达上限时按超限错误处理，不重试。
- 验证方式：服务端返回 gzip 压缩的 20MB 数据，maxBodyBytes=1MB，断言错误为 "too large" 且仅一次尝试。

#### S08-06 CookieManager.Save 未持久化的 cookie 丢失 Path/Domain 精确匹配，session cookie 域粒度过粗（与 S04-08 同根，统计并入主条目）

- 级别：P2
- 类别：功能
- 直接相关文件：internal/infra/fetcher/cookies.go:196-206（sessionCookies 存到 `domain`，即 eTLD+1）
- 间接相关文件：internal/infra/fetcher/cookies.go:216-232（cookieDomain）
- 问题描述：Set-Cookie 带 `Domain=sub.example.com` 时持久化按 sub.example.com（:187-188 persistDomain），但 session cookie（无 Expires/Max-Age）一律存到 eTLD+1 粒度 `domain`，并在 Load 时按 eTLD+1 合并（:142）。访问 example.com 的请求会带上 example.com 子域种下的 session cookie，且不带 Path 区分，与 RFC 6265 的 domain-match（子域不回传父域 cookie）语义相反。多站点/多子域书源场景会互相污染会话。证据：cookies.go:199-203 `m.session.Save(domain, sessionCookies)`，domain 来自 cookieDomain(rawURL)（eTLD+1，cookies.go:216-232）。
- 修改方案：session cookie 按 `cookie.Domain`（缺省为当前 host）粒度存储；Load 时用 RFC 6265 domain-match：目标 host 必须等于 cookie 域或以其子域身份匹配（父域请求不匹配子域 cookie）。
- 验证方式：单测：a.example.com 种 session cookie，请求 example.com 断言不带；请求 b.a.example.com 断言按 domain-match 规则处理。

#### S08-07 CookieManager.Load 对持久化过期 cookie 逐条同步 Delete，每条一次 SQLite 往返且不加锁保护缓存一致性

- 级别：P3
- 类别：性能|并发
- 直接相关文件：internal/infra/fetcher/cookies.go:148-155
- 间接相关文件：internal/infra/fetcher/cookies.go:73-79（cachedCookies）
- 问题描述：Load 中遇到过期持久化 cookie 时，循环内逐条 `m.repo.Delete(ctx, ...)`，N 条过期 cookie 造成 N 次 SQLite 写（且在缓存读锁外），随后 `deleteCacheCookie` 每次重新加写锁。大量过期 cookie 的域首次访问会串行阻塞。另外并发 Load 同一域时：两个 goroutine 都从 repo 读到同一批过期 cookie，各自 Delete，第二次 Delete 返回 nil 无害，但重复执行；可接受但浪费。证据：cookies.go:148-155 循环内逐条 Delete。
- 修改方案：先收集过期项，循环外一次性批量删除（repo 增加批量 DeleteByDomainExpired 或一次事务），缓存一次失效。
- 验证方式：mock repo 记录 Delete 调用次数，5 条过期 cookie 断言 ≤1 次调用（或单事务）。

#### S08-08 mapToCookie 超长截断用 O(n²) 的 strings.Join 且静默丢弃后续 cookie

- 级别：P3
- 类别：性能|质量
- 直接相关文件：internal/infra/fetcher/cookies.go:241-254
- 间接相关文件：internal/infra/fetcher/cookies.go:164（调用点 maxLen 4096）
- 问题描述：`mapToCookie` 每次迭代 `strings.Join(append(parts, next), "; ")` 重新拼接全部累计内容判断长度——O(n²) 分配。cookie 数多（Legado 站点几十条常见）时每次 Load 都重复。另外超过 4096 后静默 `continue`，不保证确定性顺序（map 遍历随机），同一批 cookie 每次请求被丢弃的条目可能不同，造成偶发登录态丢失且难排查。证据：cookies.go:248-251 Join-on-each-iteration；无序 map 遍历。
- 修改方案：先按 key 排序，再线性累计长度（`len + 2 + len(next)`），超限时记 debug 日志；或按插入序稳定截断。
- 验证方式：单测 100 条 cookie，长度断言 ≤4096 且两次调用结果一致（确定性）。

#### S08-09 mergeQuery 修改调用方传入的 url.Values 并与 Legado 分页占位符行为有偏差（跨切片：占位符逻辑在 url 规则层）

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:517-521
- 间接相关文件：internal/infra/rule/url/（跨切片）
- 问题描述：`mergeQuery(parsed.Query(), extra)` 中 `base.Set(key, value)` 就地修改 `url.Parse` 产生的 Query——此处无害（parsed 是局部变量），但函数签名语义上会污染调用方传入的 base；且 `base.Encode()` 会对值做二次 URL 编码判断，`parsed.RawQuery = mergeQuery(...)` 后 `parsed.String()` 的 Opaque/fragment 处理依赖 url.Values.Encode 的排序，与 Legado AnalyzeUrl 的 query 合并顺序（保序）不同。功能影响低（query 顺序不影响大多数站点），但保序性丢失与 Legado 语义有偏差。
- 修改方案：mergeQuery 返回新 Values 或显式 clone base；如需保序改用手写拼接。
- 验证方式：单测断言 query 顺序与 Legado 一致或至少稳定。

#### S08-10 rateLimiter records 无淘汰，数百书源长期运行下 map 无界增长（与 S01-07 同根，统计并入主条目）

- 级别：P3
- 类别：资源
- 直接相关文件：internal/infra/fetcher/rate_limiter.go:11-14, 28-38
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:52（rateLimiter 挂在 HTTPFetcher 上，进程级单例）
- 问题描述：`sourceRateLimiter.records` 每个出现过的 (SourceKey) 永久保留一条 rateRecord（几十字节）。数百书源 × 单条记录量级小，实际增长有限（几 KB～几十 KB），非严重；但结合"超多书源"已知问题，删除书源后残留条目永不清理，且 interval 很大的记录（如 次数/3600000）会持续占位。无内存泄露级风险，列为低级别资源问题。证据：rate_limiter.go:31 只写不删，全文件无 delete。
- 修改方案：在窗口重置时若该 key 长期未使用（如 started 超过 max(interval, 10min)）删除条目；或定期清理。
- 验证方式：单测：写入 N 个 key，模拟时间推进后断言 records 数量收敛。

#### S08-11 dnsCache 并发解析同一 host 会重复发起 DNS 查询（无 singleflight）

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:329-352（cachedResolve）
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:274（dialContext 调用点）
- 问题描述：缓存 miss 时多个 goroutine 并发解析同一 host 各自调用 resolver，无 singleflight 合并。数百源并发搜索首批请求对同域（CDN 常见）会同时发起数十次 DNS 查询，放大上游 DNS 压力与首包延迟。数据本身无并发问题（map 读写均有锁，写入为整值替换）。证据：cachedResolve 在锁外调用 f.resolver，无 in-flight 合并。
- 修改方案：用 `golang.org/x/sync/singleflight`（或本地 in-flight map + chan）合并同 host 解析。
- 验证方式：单测并发 100 次 Fetch 同一未缓存 host，resolver 调用计数为 1。

#### S08-12 客户端连接池缓存无淘汰，自定义代理路由（routeKey.proxy 含完整代理 URL）可无界增长

- 级别：P3
- 类别：资源
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:59-60, 254-272（clients map）
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:63-67（routeKey）
- 问题描述：`f.clients` 按 routeKey（proxy string + pinHost + pinIP）缓存 http.Client，每个 client 持有一个 Transport 连接池。书源数多且各自配置代理时，每个 (proxy, pinHost) 组合常驻一个 Transport（含 idle 连接）。pinHost 是 Hostname 小写、来自书源 DNSIP 选项——不同书源对同一代理配不同 DNSIP 会各自建池；书源删除/修改后旧 client 永不回收。数量级：数百源 × 数代理，每个 Transport 空闲时内存占用小但 idle 连接（MaxIdleConnsPerHost=8）会保持 fd。长期运行 fd 缓慢增长。证据：clientFor 只增不减，无 LRU/淘汰。
- 修改方案：对 clients 加容量上限（LRU）或按 IdleConnTimeout 后惰性清理；或至少在 IdleConnTimeout 过后 CloseIdleConnections。
- 验证方式：单测构造大量不同 routeKey 后检查 `lsof`/fd 数量或注入 recorder 断言旧 Transport 被关闭。

#### S08-13 fetchOnce 未校验重定向后的最终 URL scheme 仍为 http/https（checkRedirect 不检查 scheme）

- 级别：P2
- 类别：安全
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:275-281（checkRedirect）
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:434-437（初始 scheme 门禁）
- 问题描述：初始 URL 检查了 scheme 必须 http/https（fetchOnce:436），但 `checkRedirect` 只限跳数，不检查重定向目标的 scheme。Go 标准库对 `http→file`/`http→ftp` 会返回错误（unsupported protocol scheme），但 `http→data:` 之类的内部跳转在 Go 1.x 中 Client 会尝试用相同 transport 处理并报错，不会真正执行；实际风险有限。更值得注意：`http→https→http` 回落会绕过用户显式的 https 选择，但 SSRF 由 dialContext 每跳校验兜底，不构成内网绕过。综上为低危，列出防漏。
- 修改方案：checkRedirect 中对 `req.URL.Scheme` 白名单 http/https，否则返回错误。
- 验证方式：单测：服务端 302 到 `data:text/plain,hi` 或 `ftp://`，断言 Fetch 报错而非静默跟随。

#### S08-14 proxyFunc 在 allowPrivate=false 时对目标 host 的本地解析校验有 TOCTOU：代理侧远端解析结果与本地不同（已在注释声明，未缓解）

- 级别：P3
- 类别：安全
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:288-299（proxyFunc）、注释自认"只能尽力而为"
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:274（非代理路径已有 dial 时校验）
- 问题描述：socks5h/远程解析代理下，本地校验的 IP 集合与代理实际连接的 IP 无绑定，DNS rebinding 在代理路径上仍可绕过（本地解析到公网、代理解析到内网）。代码注释已承认此局限。但 `socks5`（非 h）与 http/https CONNECT 由 transport 本地解析（socks5 默认本地解析，socks5h 远端），对本地解析路径其实可以像非代理路径一样在 DialContext 绑定已校验 IP——当前对 http/https/socks5 代理统一走 proxyFunc 发送前校验，未做 dial 绑定。降级为 P3（需要代理配合或攻击者控制 DNS + 代理远端解析才可利用）。
- 修改方案：对 http/https/socks5（本地解析）代理，在 transport.DialContext 中先本地解析目标 host 并校验后再连接（复用 allowedIPs），替换或叠加 proxyFunc 的前置校验；socks5h 保留现状并文档化限制。
- 验证方式：测试：http 代理 + rebinding resolver，断言连接被拒。

#### S08-15 decodeTextResponseBody 错误时返回原始 body 且 Fetch 吞掉解码错误（err == nil 判断使非文本解码失败静默降级）

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/fetcher/http_fetcher.go:492-494
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:569-570（解码失败 return body, err）
- 问题描述：`if decoded, err := decodeTextResponseBody(...); err == nil { respBody = decoded }` —— 解码失败（如 transform 编码器报错）时静默使用未解码原始字节，不告警。结合 :569 失败时 `return body, err` 的语义，超限错误（"response body too large"，:567）在解码路径上也会被这个 `err == nil` 吞掉——即 charset 解码后的超限错误不生效，原始字节（≤maxBodyBytes）直接透传，用户拿到乱码截断文本而非明确报错。证据：http_fetcher.go:492-494 与 567-570 组合行为。
- 修改方案：区分错误类型：超限错误向上传播；真编码错误才降级为原始字节并 slog.Warn。
- 验证方式：单测：GBK 编码响应超 maxBodyBytes 解码后长度，断言 Fetch 返回明确错误或告警日志。

#### S08-16 数据竞争与 goroutine 泄露：vet + -race 全部通过（阴性结论，供总报告合并）（记录/证据项，不计入统计）

- 级别：P3
- 类别：并发
- 直接相关文件：internal/infra/fetcher/（全包）
- 问题描述：`go vet ./internal/infra/fetcher/` 无告警；`go test -race -count=1 ./internal/infra/fetcher/` 通过（ok, 1.7s）。锁使用正确：HTTPFetcher.mu（clients）、dnsCacheMu、CookieManager.cacheMu、SessionCookieStore.mu 均无锁外共享读写；cachedCookies 返回副本避免底层数组竞争（历史 C-027 修复有效）；rate_limiter 的 records 读写均在 l.mu 内。历史 R-027（过期 cookie 仍发送）已修复：cookies.go:148-155 检查 ExpiresAt 并删除。历史 R-002（SSRF 未绑定连接 IP）已修复：dialContext 在拨号前 checkIPs（http_fetcher.go:262-274），并有 TestHTTPFetcherDialsValidatedIPDespiteDNSRebinding 覆盖。历史 R-063（无 Content-Type 时跳过编码检测）已修复（sniffCharset + charsetLabel 回退链）。历史 concurrentRate 零消费已修复（rateLimiter 接入 Fetch:151-155）。
- 修改方案：无需修改。
- 验证方式：以上命令输出为证。

### S09 基础设施杂项(ratelimit/config/logging/requestoption/chinese)

审核范围：internal/infra/ratelimit/limiter.go、internal/infra/fetcher/rate_limiter.go（concurrentRate 语义对照）、internal/infra/config/config.go、internal/infra/logging/redact.go、internal/infra/requestoption/request.go、internal/infra/chinese/chinese.go（对照 legado/app/src/main/java/io/legado/app/help/ConcurrentRateLimiter.kt、AnalyzeUrl.kt）

#### S09-01 日志脱敏存在多处遗漏：冒号分隔的 token、带引号的凭证值、自由文本中的 Bearer 密钥

- 级别：P2
- 类别：安全
- 直接相关文件：internal/infra/logging/redact.go:33-36（key=value 模式）、redact.go:29-30（头部行模式）
- 间接相关文件：internal/app/debug/publisher.go:34（脱敏入口）、internal/infra/script/cache.go:163、internal/infra/script/runtime.go:247
- 问题描述：R-080 修复后白名单与 query 参数已覆盖（x-api-key、proxy-authorization、access_token、apikey、password、userinfo 均验证通过），但仍有三类泄露，均用 /tmp 探针复现（临时目录已删除）：
  1. `token : abc` / `token: abc`（冒号分隔、token 不在 sensitiveHeaderNames 中且模式 2 只匹配 `=`）→ 原样保留。书源脚本日志与错误消息中 `var token: ...`、`token: xxx` 形态常见。
  2. 带引号的值：`?token="abc"`、`token='abc'` → 模式 2 的值字符类 `[^&\s#"']+` 排除引号导致整体不匹配，原样保留。
  3. 自由文本中的裸密钥（如错误消息里的 `Bearer sk-live-...`、`Bearer eyJhbGci...` JWT）无任何模式覆盖。
  这些内容经 publisher.go:34 落入 SQLite debug_events 并推送 SSE，造成凭证持久化泄露。
- 修改方案：① 模式 2 的值字符类允许引号包裹形态，或增加 `(?i)\b(token|key|password|...)\s*[:=]\s*["']?[^&\s#"']+(["']|$)` 备选；② 把 `token`、`password` 等通用词加入 sensitiveHeaderNames（`\b` 边界已保证 `x-session-token:` 这类头也能命中）；③ 增加 Bearer/Basic scheme 备选：`(?i)\b(bearer|basic)\s+[A-Za-z0-9\-._~+/]+=*`。
- 验证方式：redact_test.go 增加上述三类用例；go test ./internal/infra/logging/ -race。

#### S09-02 每源 map 永不淘汰：ratelimit sources 与 fetcher rateLimiter records 无界增长，sourceChan 每请求竞争全局锁（与 S01-07 同根，统计并入主条目）

- 级别：P3
- 类别：资源
- 直接相关文件：internal/infra/ratelimit/limiter.go:96-115（sourceChan）、internal/infra/fetcher/rate_limiter.go:14-17（records map）
- 间接相关文件：internal/infra/fetcher/http_fetcher.go:148-152
- 问题描述：`Limiter.sources` 与 `sourceRateLimiter.records` 以 SourceKey 为键，条目一旦写入永不删除；书源被删除/改名后旧键残留。单条目很小（chan struct{} 缓冲 2 + rateRecord ~40B），数百源仅数 KB，不是历史"内存泄露"的主因（主因在搜索/校验链路，跨切片，由 S01/S02 覆盖），但结构上无界。另外 sourceChan 每次请求都取 `l.mu` 全局锁（limiter.go:106-108），数百源高并发时所有请求在单锁上串行一小段；与 Legado 的 `synchronized(concurrentRecordMap)` 一致，实际影响有限。
- 修改方案：为两个 map 加容量上限 + 简单 LRU/过期淘汰（窗口记录可在窗口结束后惰性清理：wait 时顺带删除过期且无等待者的 record）；sourceChan 可改为 `sync.Map` 或分片锁。
- 验证方式：压测数千个假 SourceKey 后断言 map 长度有界；benchmark 对比锁改动前后。

#### S09-03 config 缺少取值校验：READER_ENV 大小写敏感且不校验合法值，ContentChineseConverterType 无上限

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/config/config.go:124-128（Env 读取）、config.go:129-133（ConverterType）
- 间接相关文件：internal/bootstrap/bootstrap.go:133-134、internal/app/replace/processor.go:242-250
- 问题描述：`cfg.Env` 接受任意字符串且大小写敏感：`READER_ENV=Production`（大写 P）不等于 `production`，既不触发 token 必填（config.go:135-137）也不使用测试默认 token，静默落入 development 分支。虽有 RequireTokenForAddr（serve.go:56）兜底非回环监听，但回环部署时鉴权被静默关闭且用户无感知。`READER_CONTENT_CHINESE_CONVERTER_TYPE` 只校验 min=0，值 ≥3（如误填 3）时 processor.go:243-250 静默返回原文，不报错不告警。
- 修改方案：Load 中校验 Env ∈ {development, production, test}（或用 strings.EqualFold 比较 production），非法值报错；ConverterType 增加 max=2 校验。
- 验证方式：config_test.go 增加表驱动用例（Production 大写、converter type=3 均应报错）。

#### S09-04 requestoption "type" 选项空白字符串语义与 Legado 不一致（空白触发 hex 编码）（与 S07-06 同根，统计并入主条目）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/requestoption/request.go:56-58
- 间接相关文件：legado/.../AnalyzeUrl.kt:98,754,405-406
- 问题描述：Reader 判定 `value != nil` 即置 `EncodeBodyAsHex = true`；Legado 解析时 `type = if (value.isNullOrBlank()) null else value`（AnalyzeUrl.kt:754），空白字符串视为未设置。书源写 `,{ "type":" "}`（工具生成的占位值）时，Reader 会对响应做 hex 编码，解析端拿到的是 hex 文本而非正文，字段采集失败。
- 修改方案：Build 中 type 分支改为 `value != nil && strings.TrimSpace(fmt.Sprint(value)) != ""`。
- 验证方式：request_test.go 增加用例：`{type:" "}` 不置 EncodeBodyAsHex，`{type:""}` 同样（nil/空串均已放行），`{type:"hex"}` 置位。

#### S09-05 显式 Content-Type 时 body 不按 charset 编码，charset 选项对该 body 失效

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/requestoption/request.go:90-92,231-244（applyBody string 分支）、95-98
- 间接相关文件：internal/infra/rule/url/encoding.go（EncodeFormString）
- 问题描述：applyBody 的 string 分支在已有 Content-Type 时返回 ""，Build 里 `rawFormBody == ""` 导致跳过 `EncodeFormString(rawFormBody, charsetName)`，body 以原始 UTF-8 字节发送。书源声明 `charset:GBK` 且自带 `Content-Type:application/x-www-form-urlencoded` 头时，非 ASCII 表单值不会被 GBK 编码，目标站点解析失败。无 Content-Type 的路径编码正常。
- 修改方案：applyBody string 分支返回 body 原文（无论是否已有 Content-Type），由 Build 统一按 charset 编码；或在 charsetName != "" 时强制走 EncodeFormString。
- 验证方式：request_test.go：charset=GBK + 预设 Content-Type 头 + 中文 body，断言 base.Body 为 GBK 字节。

#### S09-06 chinese 词典加载失败被永久缓存，无重试路径

- 级别：P3
- 类别：资源
- 直接相关文件：internal/infra/chinese/chinese.go:18-26
- 间接相关文件：internal/app/replace/processor.go:242-250、internal/infra/script/runtime.go:608-611
- 问题描述：`sync.Once` 同时缓存 cc 与 err；`opencc.New` 因瞬时原因（词典文件暂不可读、内存不足）失败后，err 永久缓存，此后所有 T2S/S2T 静默返回原文（convertOrOriginal 吞错），进程重启前繁简转换永久失效且无任何日志。
- 修改方案：失败时打 slog.Warn 一次；或 err 失败时重置 once（`c.once = sync.Once{}`，需小锁）允许下次重试。
- 验证方式：注入坏的 opencc 模式（如 t2s 词典缺失场景 mock）断言警告日志与重试行为。

### S10 HTTP API 层

审核范围：cmd/reader/{main,serve,serve_test}.go、internal/api/handlers/（source_handler.go、check_handler.go、debug_handler.go、login_handler.go、system_handler.go、sse.go、各 mapper）、internal/api/middleware/{auth,trace}.go、internal/api/generated/server.gen.go（只读消费）、api/openapi.yaml

#### S10-01 活跃 SSE 连接使优雅关闭必然超时，干净退出返回非 0

- 级别：P2
- 类别：资源
- 直接相关文件：cmd/reader/serve.go:68-80（Shutdown 5s）、internal/api/handlers/check_handler.go:100-125、internal/api/handlers/debug_handler.go:112-150、internal/api/handlers/sse.go:24-40
- 间接相关文件：cmd/reader/serve_test.go
- 问题描述：`runServe` 在 SIGTERM 后调用 `server.Shutdown(5s)`；但 SSE 流（/check/tasks/{id}/events、/debug/sessions/{id}/events）是活跃连接（无写超时、循环只在 `r.Context().Done()` 或终态事件时退出），`Shutdown` 会等待所有活跃连接结束，5 秒内 SSE 订阅者不断开则 `Shutdown` 返回超时错误 → 进程退出码 2、日志记 "reader server shutdown failed"。只要存在一个挂着的事件流订阅者，"优雅"关闭必然失败。serverShutdownTimeout=5s 与 SSE 心跳 15s 也不匹配。
- 修改方案：Shutdown 失败兜底 `server.Close()`（或注册 SSE 注册表在 Shutdown 前主动 cancel 各流 context）；或对 SSE 路由用 `http.NewServeMux`/中间件维护可取消 context 集合，收到 SIGTERM 时先取消全部事件流再 Shutdown。
- 验证方式：集成测试：起 server、订阅 check events SSE、向进程发 SIGTERM，断言退出码 0 且 Shutdown 无超时错误。

#### S10-02 远程导入 sourceUrls 递归无深度限制，可构造无限导入链（SSRF 放大/自 DoS）（与 S15-01 同根，统计并入主条目）

- 级别：P2
- 类别：安全
- 直接相关文件：internal/app/source/importer.go:111（RemoteURL→importSourceURLs）、:123-124（payload 为 `{` 且含 sourceUrls 时再次进入 importSourceURLs）、:134-159
- 间接相关文件：internal/api/handlers/source_handler.go:152-176（导入入口，32MiB 上限）
- 问题描述：`importSourceURLs` 对每个 URL `fetch` 后把响应体递归传入 `i.Import`；响应体若又是 `{"sourceUrls": [...]}` 文档会再次进入 `importSourceURLs`，且没有任何深度/跳数限制。恶意服务器返回指向自身的 sourceUrls 文档即可让单次导入请求变成无限串行抓取循环（每跳受 fetcher 30s 超时约束，但循环本身无界），长时间占用请求与出站带宽，也可当作经服务端发起的任意 URL 抓取跳板。`#requestWithoutUA` 后缀同理逐跳继承。
- 修改方案：在 `Importer` 增加 depth 参数（或 ImportInput.Depth），`importSourceURLs` 递归时 depth+1，超过上限（如 3，对齐 Legado 仅一层远程展开的语义）返回 RemoteError 并停止；同时限制单次导入的远程 URL 总数。
- 验证方式：测试：本地 httptest 服务器返回 `{"sourceUrls":["self"]}`，导入该 URL，断言在 depth 上限内收敛且不挂起。

#### S10-03 多个错误映射分支把内部错误原文回显给客户端

- 级别：P2
- 类别：安全
- 直接相关文件：internal/api/handlers/source_handler.go:164-169（writeImportError default `err.Error()`）、:905-910（writeImageProxyError default `err.Error()`）、internal/api/handlers/login_handler.go:73-75（502 `script_failed` 带 `err.Error()`）
- 间接相关文件：internal/api/handlers/source_handler.go:860-880（writeSourceError default 已隐藏为 "internal error"，行为不一致）
- 问题描述：与 `writeSourceError`/`writeDebugError` 的 default 分支（固定 "internal error"）不一致，导入、图片代理、登录三处 default 分支把底层错误原文放进 500/502 响应。`fetch` 层错误含内部解析出的 IP（如 `dial tcp 10.x.x.x:443`）、DB 错误含 SQL 细节、script 错误含书源脚本片段，都会泄露给持有 token 的任意客户端并进入其日志。`fetch.ErrBlocked` 分支回显被封禁地址属有意反馈，可保留。
- 修改方案：三个 default 分支改为固定 message（对齐 writeSourceError），真实错误只进服务端日志（handler 当前没有 logger，可在 WriteError 增加可选 slog 记录）。
- 验证方式：单元测试：图片代理注入返回 `errors.New("dial tcp 10.0.0.1:443: ...")` 的 fetcher，断言响应 message 不含 "dial tcp"。

#### S10-04 查询参数绑定错误返回纯文本，违反 Error schema 且无 traceId

- 级别：P2
- 类别：功能
- 直接相关文件：internal/api/generated/server.gen.go:3173-3178（默认 ErrorHandlerFunc 为 `http.Error(w, err.Error(), http.StatusBadRequest)`）、cmd/reader/serve.go:103-104（装配时未传 `ChiServerOptions.ErrorHandlerFunc`）
- 间接相关文件：api/openapi.yaml:1277-1289、:1368-1383（components.responses.BadRequest / Error schema）
- 问题描述：`?page=abc` 等参数解析失败走 ogen 生成的 wrapper 错误路径，返回 `text/plain` 的 400（如 `invalid parameter "page": ...`），而 spec 声明所有 400 响应体为 `application/json` 的 Error 对象（code/message/traceId 必填），且缺少 X-Trace-Id 响应头，客户端无法关联 trace。
- 修改方案：装配时通过 `HandlerWithOptions`/自定义 wrapper 注入 ErrorHandlerFunc，把 `*InvalidParamFormatError`/`*RequiredParamError` 转成 `middleware.WriteError(400, "validation_failed", ...)`。
- 验证方式：测试：请求 `GET /api/v1/books?page=abc`，断言 Content-Type 为 application/json、body 含 traceId。

#### S10-05 导入接口 409 冲突状态码未在 OpenAPI 声明

- 级别：P3
- 类别：功能
- 直接相关文件：internal/api/handlers/source_handler.go:166-167（ErrConflict→409）、api/openapi.yaml:130-168（import 操作 responses 仅 200/400/401/413/501/500）
- 间接相关文件：internal/app/source/importer.go
- 问题描述：`writeImportError` 会把 `source.ErrConflict` 映射为 409，但 `/book-sources/import` 未声明 409。若该路径实际不可达（SaveMany 的冲突是否上浮为 source.ErrConflict 未在本次切片核实，[推测] 上游可能上浮），则应删除死分支；可达则 spec 缺声明。
- 修改方案：核实 SaveMany 冲突错误链；要么 spec 增加 `"409"`，要么删除 writeImportError 的 ErrConflict 分支。
- 验证方式：针对 SaveMany 返回冲突错误的单元测试，观察响应码并与 spec 对照。

#### S10-06 check 事件流无 `id:` 字段、不支持 Last-Event-ID 断线续传，重连全量重放

- 级别：P3
- 类别：功能
- 直接相关文件：internal/api/handlers/check_handler.go:76-125（StreamCheckTaskEvents 未调用 lastEventID，writeCheckEvent 未写 `id:`）、internal/api/handlers/debug_handler.go:90-93（对照：debug 流有 id 且支持 Last-Event-ID）
- 间接相关文件：internal/app/check/publisher.go:83-108（Subscribe 无 afterEventID 参数）、internal/api/handlers/sse.go:39-48
- 问题描述：同一 sse.go 的 `lastEventID` 只被 debug 流使用；check 流重连时从历史头部全量重放（客户端收到重复事件），且事件无 id，客户端无法去重定位。终端任务且事件流已被 sweep（5 分钟后）时只收到一条合成终态事件，条目级历史只能轮询 REST 接口补齐——行为可接受但与 debug 流不对称，`lastEventID` 对 check 流是死代码路径的一半。
- 修改方案：publisher 增加事件序号（EventID），Publish 时分配；Subscribe 增加 afterEventID；writeCheckEvent 输出 `id: %d`。
- 验证方式：SSE 集成测试：中断重连带 Last-Event-ID，断言不重复。

#### S10-07 客户端提供的 X-Trace-Id 未做任何校验即回显

- 级别：P3
- 类别：安全
- 直接相关文件：internal/api/middleware/trace.go:19-30
- 间接相关文件：internal/api/middleware/trace.go:44-58（WriteError 同时写入响应头与 JSON body）
- 问题描述：请求携带任意 X-Trace-Id 会被原样写入响应头和错误 JSON 的 traceId 字段（SearchResponse 的 Warnings 里也逐条回显）。Go net/http 会拦截 CRLF，无响应拆分风险，但超长值、控制字符与伪造内容会原样进入客户端/网关日志并可能污染内部 trace 关联（服务端自己的日志未记录该 ID，两边 ID 可能不一致）。[推测] 若有日志采集管道，伪造 traceId 可造成日志混淆。
- 修改方案：对入站 traceId 校验格式（如 `^[0-9a-f]{16,64}$`，参考 newTraceID 的 32 hex），不合格则生成新 ID；同时把最终 traceId 记入服务端请求日志。
- 验证方式：测试：携带含空格/超长/非 hex 的 X-Trace-Id 请求，断言响应头为新生成的合法 ID。

#### S10-08 READER_DEBUG_BODY_LIMIT 配置项从未被消费

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/config/config.go:37,77,121,163（定义、默认 32KiB、环境变量加载、Redacted 输出）
- 间接相关文件：internal/api/handlers/debug_handler.go:23、internal/api/handlers/check_handler.go:22（实际用 h.bodyLimit=10MiB）
- 问题描述：全仓 grep 显示 DebugBodyLimit 除 config 自身外无任何消费点；debug 会话与 check 任务的请求体实际走 10MiB 的 defaultJSONBodyLimit。用户设置 READER_DEBUG_BODY_LIMIT=4096 无任何效果，配置文档（config Redacted 输出）与实际行为不符，属死配置。
- 修改方案：给 SourceHandler 增加 debugBodyLimit 字段并在 debug/check 两个 handler 使用，由 serve 装配注入 cfg.DebugBodyLimit；或删除该配置项。
- 验证方式：测试：设置 DebugBodyLimit=100，POST /debug/book-source 超 100 字节 body，断言 413。

#### S10-09 搜索/发现请求未设置单源超时，Timeout 通道全程为空（与 S01-01 同根，统计并入主条目）

- 级别：P3
- 类别：性能（与已知"超多书源搜索慢"直接相关；深挖归 S01/S02，此处仅 API 装配侧）
- 直接相关文件：internal/api/handlers/source_handler.go:543-551（MultiSearchInput 未设置 Timeout）
- 间接相关文件：internal/app/search/multi_source.go:32-33,110-114（Timeout<=0 表示不启用）、internal/bootstrap/bootstrap.go:121（未注入）、跨切片 S01/S02
- 问题描述：multi_source 支持 `MultiSearchInput.Timeout` 但没有任何调用点设置（bootstrap 不注入、handler 不传、API 请求体也无该字段），单源搜索超时逻辑永久不启用，`search_timeout` warning 不可达；多源搜索整体时长只受 r.Context()（客户端不断开就无截止）与每源 fetcher 30s 兜底约束。数百书源时单个搜索请求可占连接数分钟且持续累积内存（已知问题）。
- 修改方案：bootstrap 从 cfg 派生搜索超时（如 FetchTimeout）注入 MultiSourceSearcher 或由 handler 设置 input.Timeout；spec 可选增加 timeoutMs 字段。
- 验证方式：基准/集成：单源 fetcher 挂起超过 timeout，断言收到 search_timeout warning 而非等到 fetcher 超时。

#### S10-10 ExportBookTxt 每请求把整本书写满 /tmp 再回读，无流式与磁盘水位保护

- 级别：P3
- 类别：性能
- 直接相关文件：internal/api/handlers/source_handler.go:404-457
- 间接相关文件：internal/app/content（ExportTXT 实现，跨切片 S13）
- 问题描述：导出走 `os.CreateTemp("", ...)` 全量落盘 + `Sync()` + `Seek(0)` + `ServeContent`，断连的请求仍会完成整本书的磁盘写入；单个用户反复请求大书可耗尽 /tmp；`Content-Length` 先手动设置又被 ServeContent 覆盖属冗余。单用户自托管下影响有限。
- 修改方案：改为 `io.Pipe`/`http.ResponseWriter` 直接流式（ExportTXT 增加 writer 目标抽象），或至少去掉 Sync 并在 r.Context().Done 时中止。
- 验证方式：基准：导出 100MB 书目，观察 /tmp 峰值占用与断连后的行为。

#### S10-11 sourceIds 数组无上限，单请求可驱动海量逐条查询

- 级别：P3
- 类别：性能
- 直接相关文件：api/openapi.yaml:1810-1813、:1825-1828、:2270-2273（SearchRequest/DiscoveryRequest/CheckTaskCreateRequest 的 sourceIds 仅 minItems 无 maxItems）
- 间接相关文件：internal/api/handlers/check_handler.go:31-35、internal/app/check/manager.go:53-60（逐条 FindByID）
- 问题描述：10MiB 请求体可携带数十万条 sourceId；check.Start 逐条 `FindByID`（每次一次 DB 查询）并在任一不存在时 400 前已耗完全部查询；search/discovery 同理进入 resolveSources 逐条解析。受 body 上限约束非紧急，但缺一层 O(1) 防御。
- 修改方案：spec 加 maxItems（如 1000），handler 在 decode 后加 `len(body.SourceIds) > maxSourceIds` 校验返回 400；或改为一次 `FindByIDs` 批量查询（跨切片 S11）。
- 验证方式：测试：提交 10 万条 sourceIds，断言快速 400 而非长时间查询。

#### S10-12 check 任务 ID 生成与领域对象构造落在 handler 层

- 级别：P3
- 类别：质量
- 直接相关文件：internal/api/handlers/check_handler.go:33-38
- 间接相关文件：internal/app/check/manager.go:46-66（Start 已兜底 CreatedAt/Status，唯独 TaskID 由调用方给）
- 问题描述：`task.CheckTask` 的构造与 `"check-"+timestamp` ID 生成写在 HTTP handler；纳秒时间戳并发碰撞概率极低但存在，且 ID 格式作为不变量散落在 transport 层。manager.Start 已负责兜底其余字段，TaskID 生成也应下沉（对齐 debug 会话由 debugSessionFromCreateRequest 生成的现状——该函数同样在 handler 层，属同一模式）。
- 修改方案：checkapp.Manager.Start 与 debugapp 增加无 ID 入口，内部生成 TaskID/SessionID；handler 只传业务字段。
- 验证方式：现有 handler 测试改为断言返回的 taskId 格式，manager 测试断言 ID 唯一性。

### S11 SQLite 持久化

审核范围：internal/infra/sqlite/（open.go、migrate.go、migrate_test.go、open_test.go、repositories/ 全部 7 个仓储及测试）、internal/domain/{source,book,replace}/repository*.go、internal/bootstrap/bootstrap.go（连接配置调用点）

#### S11-01 书源/书籍列表查询 SELECT 全部大 JSON/HTML 列，多源搜索与发现全量拉取时放大内存与 IO（与 S01-04 同根，统计并入主条目）

- 级别：P1
- 类别：性能
- 直接相关文件：internal/infra/sqlite/repositories/book_source_repository.go:276-280（selectBookSourceSQL）、internal/infra/sqlite/repositories/book_repository.go:121-125（selectBookSQL）
- 间接相关文件：internal/domain/source/repository.go:64-79（ListAll）、internal/app/search/multi_source.go:184-193、internal/app/discovery/discovery.go:180-184、internal/api/handlers/source_handler.go:623、internal/cli/source.go:63
- 问题描述：selectBookSourceSQL 一次取 34 列，含 raw_json、rule_search/rule_toc/rule_content/rule_explore/rule_book_info、js_lib、login_ui、header 等大文本列；列表分页（`source list`、GET /sources）和多源搜索/发现的 ListAll（每页 500 行）全都加载这些列。数百个书源时单页即把全部规则 JSON + 原始 JSON 反序列化成 BookSource 结构常驻内存（每源数十 KB，578 源约数十 MB 起步，且 ListAll 把全部页累积在 `out` 中）。books 的 selectBookSQL 同样全列取 info_html/toc_html/download_urls。这与已知的"超多书源搜索内存大、速度慢"直接相关（存储侧根因之一，其余归搜索切片）。
- 修改方案：为列表/搜索路径增加投影查询（如 selectBookSourceListSQL 只取 source_id/name/group/url/custom_order/enabled 等元数据列），仅 FindByID/FindByURL 详情路径取规则与 raw_json 列；或给 BookSourceRepository 增加 `ListMeta` 接口方法，search/discovery 走 ListMeta。
- 验证方式：构造 500+ 书源的库，跑 `reader search --keyword x` 前后 RSS 对比；EXPLAIN QUERY PLAN 确认列表查询列数下降；go test ./internal/app/search/...。

#### S11-02 books.List 的 ORDER BY dur_chapter_time 无索引，全表扫描 + TEMP B-TREE

- 级别：P2
- 类别：性能
- 直接相关文件：internal/infra/sqlite/repositories/book_repository.go:69-71、internal/infra/sqlite/migrate.go（books 无 dur_chapter_time 索引）
- 间接相关文件：internal/api/handlers（书架列表端点）
- 问题描述：EXPLAIN QUERY PLAN（/tmp/audit-s11 实测）：`SELECT * FROM books ORDER BY dur_chapter_time DESC LIMIT 50` → `SCAN books` + `USE TEMP B-TREE FOR ORDER BY`。书架大时每页全表扫描并物化排序。另该查询同样取 info_html/toc_html 全列（见 S11-01）。
- 修改方案：追加迁移版本 `CREATE INDEX IF NOT EXISTS idx_books_dur_chapter_time ON books(dur_chapter_time DESC)`；列表路径改用只取元数据列的投影 SQL。
- 验证方式：插入 1 万行 books 后 EXPLAIN QUERY PLAN 应显示 `SEARCH books USING INDEX idx_books_dur_chapter_time`，无 TEMP B-TREE。

#### S11-03 SetMaxOpenConns(1) 使 WAL 读写并行能力完全失效，慢查询阻塞全部读写

- 级别：P2
- 类别：并发
- 直接相关文件：internal/bootstrap/bootstrap.go:58-59
- 间接相关文件：internal/cli/download.go:156、internal/worker/check/runner.go:119-155、internal/infra/sqlite/open.go:30（WAL 配置）
- 问题描述：生产装配固定 `SetMaxOpenConns(1)`/`SetMaxIdleConns(1)`。WAL 模式本允许多读 + 单写并行，单连接后所有 API 读、校验进度写（SaveItem/UpdateTaskProgress）、SSE 轮询读全部串行排队：一次 ListAll（S11-01 的全列扫描）或大库上的 LIKE 全表扫描期间，整个 HTTP 服务与校验进度写入都被阻塞。database/sql 虽不会报错（排队等待），但延迟被放大，且 busy_timeout(5000) 在单连接下形同虚设。注释（download.go:156）只论证了"写不打回串行"，未考虑读阻塞读/写。
- 修改方案：改为 `SetMaxOpenConns(n>1)`（如 4~8 读并发；SQLite 写并发由 WAL + busy_timeout 兜底），或至少把读路径与写路径分离。保留单写事务语义已由 BeginTx 保证。
- 验证方式：并发压测 GET /sources 与 POST /check，比较 p99；-race 全量测试确认无新竞争。

#### S11-04 SaveMany 双 ON CONFLICT upsert 与批内换址组合存在语义陷阱，可能把 URL 改写到他人地址/误删他人数据

- 级别：P2
- 类别：功能
- 直接相关文件：internal/infra/sqlite/repositories/book_source_repository.go:51-90（SaveMany）、:103-104（lookup）、:277-283（upsert 双冲突分支）
- 间接相关文件：internal/app/source/importer.go:193-219（importEntries）、internal/infra/sqlite/repositories/book_source_repository_test.go
- 问题描述：SaveMany 先按 source_id lookup 旧 URL，再 upsert。两个缺陷组合：① upsert 先命中 `ON CONFLICT(source_id)` 更新 book_source_url，若该 URL 恰好等于批内（或库内）另一书源的 URL，UNIQUE(book_source_url) 再触发 `ON CONFLICT(book_source_url)` 分支改写 source_id——同一条 INSERT 触发两次 upsert 后，另一个源的身份被覆盖；② 批内源 A 的旧地址与源 B 的新地址相同时，deleteSourceOwnedData(oldURL) 会把 B 刚写入的 source_variables/cache_entries 误删（:83-88 在 upsert 后无条件按 oldURL 删除）。批量导入含重复/交错的 sourceId+URL 组合时结果不可预期。
- 修改方案：在 SaveMany 事务内先做一次批内校验（source_id 与 book_source_url 的集合内去重/冲突检测，冲突即整批返回 ErrConflict）；换址删除改为仅当 oldURL 不在本批任何源的目标地址集合中才执行 deleteSourceOwnedData。
- 验证方式：表驱动测试：批内两源互换地址、批内两源目标 URL 相同、批内 A 旧地址 = B 新地址，断言结果与 source_variables 不被误删。

#### S11-05 replace_rules scope 子串 LIKE 全表扫描，且历史误匹配问题仍存在

- 级别：P2
- 类别：性能（含历史功能问题仍存在）
- 直接相关文件：internal/infra/sqlite/repositories/replace_rule_repository.go:108-113（findEnabledByScope）
- 间接相关文件：internal/app/replace/processor.go:76-93（带进程内缓存，缓解但未消除）、docs/ReviewIssues-2026-10-15.md（R-048，历史问题仍存在）
- 问题描述：SQL 仍为 `scope LIKE '%' || ? || '%'`（书名/源名子串匹配）。EXPLAIN QUERY PLAN 实测：`SCAN replace_rules` + `USE TEMP B-TREE FOR ORDER BY`。除全表扫描外，历史 R-048 指出的子串误匹配（书名 "斗" 误命中 scope "斗破" 等包含关系）未修：历史修复建议（应用层按逗号拆 scope/exclude_scope 后精确匹配）未落地，当前代码仍用 SQL LIKE 子串。
- 修改方案：查询出启用规则后（`is_enabled=1 AND scope_x=1`，可用部分索引），在应用层按 SplitGroups 同样的分隔符拆 scope/exclude_scope 并做精确项匹配；或维持 SQL 但为 `is_enabled, scope_content` 建部分索引缩小扫描面。
- 验证方式：EXPLAIN QUERY PLAN 不再 SCAN 全表或扫描面缩小；新增测试：scope="斗破" 时书名 "斗" 不命中。

#### S11-06 scanBook 吞没 download_urls JSON 反序列化错误（历史问题部分修复不彻底）

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/sqlite/repositories/book_repository.go:135-137（`_ = json.Unmarshal`）
- 间接相关文件：internal/infra/sqlite/repositories/book_source_repository.go:308-322（book_source 侧已修复，形成不一致）、docs/ReviewIssues-2026-10-15.md:40（历史问题仍存在——仅 book_source 侧修复）
- 问题描述：历史审核指出"JSON 反序列化错误吞没"；book_source_repository.scanBook 已改为报错（"书源 %s 的 %s 列 JSON 损坏"），但 book_repository.scanBook 对 rawDownloadURLs 仍 `_ = json.Unmarshal`，损坏时静默得到 nil DownloadURLs，下载导出功能静默缺数据且无法定位。
- 修改方案：与 book_source 一致：返回 `fmt.Errorf("书籍 %s 的 download_urls 列 JSON 损坏: %w", b.BookURL, err)`；若担心旧库脏数据阻断读路径，至少 slog.Warn 记录。
- 验证方式：手工写入非法 JSON 到 books.download_urls，FindByID 返回明确错误/日志。

#### S11-07 DeleteExpired 全表扫描 cache_entries（含正文大 BLOB）

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/sqlite/repositories/image_cache_repository.go:56-69
- 间接相关文件：internal/bootstrap/bootstrap.go:86-90（启动调用）、internal/infra/sqlite/migrate.go（仅 idx_cache_entries_owner）
- 问题描述：EXPLAIN QUERY PLAN 实测 `DELETE FROM cache_entries WHERE namespace='image' AND (expires_at IS NULL OR expires_at<=datetime('now'))` → `SCAN cache_entries`。cache_entries 混存正文缓存（大 BLOB，可达 MB/章）与图片缓存；启动清理在库大时需逐行扫过全部正文条目。已用 LIMIT 1 短路减少空跑开销，但有过期项时 DELETE 本身仍全表扫描。
- 修改方案：追加迁移 `CREATE INDEX IF NOT EXISTS idx_cache_entries_namespace_expires ON cache_entries(namespace, expires_at)`，两个语句都命中。
- 验证方式：EXPLAIN QUERY PLAN 显示 SEARCH ... USING idx_cache_entries_namespace_expires。

#### S11-08 keyword/group 过滤与 group 前缀匹配均为全表扫描，无可用索引

- 级别：P3
- 类别：性能
- 直接相关文件：internal/infra/sqlite/repositories/book_source_repository.go:236-260（buildBookSourceWhere）
- 间接相关文件：internal/api/handlers/source_handler.go:623、internal/cli/source.go:63
- 问题描述：EXPLAIN QUERY PLAN 实测：keyword 四列 LIKE '%kw%' → `SCAN book_sources`；group 的 `= OR LIKE 'g,%' OR LIKE '%,g' OR LIKE '%,g,%'` 四分支 OR → 同样 `SCAN book_sources`。数百源时可接受，数万源（Legado 用户常见规模）时列表/搜索入口变慢。LIKE 转义本身正确（escapeLike + ESCAPE '\'，无注入）。
- 修改方案：若需支撑大库：group 过滤改为规范化关联表（source_groups(source_id, group) 精确等值 + 索引），keyword 走 FTS5；小规模下保持现状可接受（标注为已知取舍亦可）。
- 验证方式：插入 5 万源后 EXPLAIN QUERY PLAN 与耗时对比。

#### S11-09 双进程并发迁移无互斥，PRIMARY KEY 冲突导致后启动方启动失败

- 级别：P3
- 类别：并发
- 直接相关文件：internal/infra/sqlite/migrate.go:56-66（applyMigrations 读 max(version)）、:69-88（applyMigration INSERT）
- 间接相关文件：internal/cli（CLI 与 server 同时启动会各自跑 Migrate）、internal/bootstrap/bootstrap.go:63
- 问题描述：Migrate 无进程级互斥。两个进程（如 server 启动中用户同时跑 `reader import`）同时对同一库执行未应用版本时，双方都读到相同 current，先提交者成功，后者的 `INSERT INTO schema_migrations(version)` 因 version PRIMARY KEY 冲突而整批报错 → 后启动进程 bootstrap.Open 失败退出（bootstrap.go:63-66 直接返回错误）。DML 迁移（版本 5 DELETE）若与先者交错虽被事务隔离，但版本记录冲突仍使一方失败。busy_timeout(5000) 只解决锁等待，不解决该逻辑冲突。
- 修改方案：INSERT 冲突时重读 schema_migrations 确认版本已存在且 apply 为幂等 DDL 后视为成功；或迁移入口用 `BEGIN IMMEDIATE` 全程持写锁。
- 验证方式：测试用两个 *sql.DB 指向同一文件并发 Migrate，双进程 -race 下均成功。

#### S11-10 cookie_repository ListByDomain 不过滤过期行（历史问题仍存在，跨切片：fetcher 侧同样未过滤）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/sqlite/repositories/cookie_repository.go:36-38（查询无 expires_at 条件）
- 间接相关文件：internal/infra/fetcher/cookies.go（历史文档指出 Load 合并结果不判断 ExpiresAt——跨切片，未逐一复核当前 fetcher 代码）、docs/ReviewIssues-2026-10-15.md:442（历史问题仍存在）
- 问题描述：历史 R-0xx 指出 ListByDomain 返回全部行且 fetcher Load 不判断 ExpiresAt，过期 cookie 被当作有效会话使用。当前仓储查询仍无 `expires_at IS NULL OR expires_at > ...` 条件（cookie_repository.go:36-38），仅扫描时解析 expires；该历史问题在仓储侧仍存在。
- 修改方案：查询加 `AND (expires_at IS NULL OR expires_at > ?)`（以 RFC3339Nano UTC 文本比较，与写入格式一致）。
- 验证方式：写入已过期与未过期 cookie 各一条，ListByDomain 只返回未过期条目。

#### S11-11 BookProgress.Name/Author 携带但持久层完全忽略，与 Legado 进度归属校验语义不符（跨切片：app 层未传）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/domain/book/repository.go:11-20（注释声明 name/author 是 Legado 校验字段）、internal/infra/sqlite/repositories/book_repository.go:108-110（SaveProgress 只写 dur_*）
- 间接相关文件：internal/app/book（调用方，跨切片）
- 问题描述：domain 注释明确"name/author 是 Legado 用来校验进度所属书籍身份的字段"，但 SaveProgress 的 SQL 与参数完全不用 Name/Author，进度按 book_id 无条件覆盖。若调用方未做归属校验，同名书换源/串号时进度被静默写到错误书上。属接口承诺与实现不一致。
- 修改方案：要么 SaveProgress 的 WHERE 增加 `AND name = ? AND author = ?`（不匹配时报错/忽略），要么从 BookProgress 中删除未用字段并修正注释，消除假接口。
- 验证方式：测试：Name/Author 不匹配的 SaveProgress 返回错误或不覆盖。

### S12 详情与目录

审核范围：internal/app/book/info.go、toc.go、format.go、fetch_request.go、service.go（对照 legado BookInfo.kt、BookChapterList.kt、BookContent.kt、WebBook.kt）

#### S12-01 目录刷新后书籍元数据不更新、阅读进度不重映射

- 级别：P2
- 类别：功能
- 直接相关文件：internal/app/book/toc.go:88-121（RefreshToc 完成后直接 return，无任何书籍元数据写回）
- 间接相关文件：internal/app/book/info.go:74-92（UpdateInfo 用的是 b.TotalChapterNum 旧值）、legado/app/src/main/java/io/legado/app/model/webBook/BookChapterList.kt:120-135、legado/app/src/main/java/io/legado/app/data/entities/Book.kt:341-348、legado/app/src/main/java/io/legado/app/help/book/BookHelp.kt:499-546
- 问题描述：Legado 目录解析完成后会更新 `book.totalChapterNum`、`book.lastCheckTime`、`lastCheckCount`、`latestChapterTime`（新增章节时）、`durChapterTitle`（BookChapterList.kt:120-135），并在换源/迁移时用 `BookHelp.getDurChapter` 做阅读进度重映射。Reader 的 `RefreshToc` 写完 chapters 表后不写任何 books 列：`total_chapter_num` 永远停留在旧值，`latest_chapter_title` 只靠详情页刷新；更关键的是没有进度重映射——章节重排（`-` 前缀目录）、收缩或插入新章后，`dur_chapter_index` 仍指向旧下标，可能越界或指向错误章节。证据：`ReplaceByBook`（toc.go:121-123）只写 chapters；`internal/infra/sqlite/repositories/chapter_repository.go:22-49` 无 books 更新；domain `book.Book` 的 `LastCheckTime/LastCheckCount/TotalChapterNum`（book.go:37-39）在 toc.go 全文无写入。历史问题仍存在（docs/ReviewIssues-2026-10-15.md:2247 §9.2-3 记录的"最新章节元数据刷新未迁移"即此）。
- 修改方案：在 `RefreshToc` 中 `ReplaceByBook` 成功后增加 books 元数据更新：`totalChapterNum=len(chapters)`、`lastCheckTime=now`、新增章数>0 时更新 `lastCheckCount/latestChapterTime`；并实现 `getDurChapter` 等价物（旧 `durChapterIndex/Title` + 新列表按标题 Jaccard 相似度重映射，可先做简化版：标题精确/去修饰匹配 + 越界钳制），与 `UpdateInfo` 一样只写相关列。
- 验证方式：单测——刷新前 dur_chapter_index=5，刷新后章节收缩到 3 章，断言进度被钳制/重映射且 total_chapter_num 更新；对照 Legado 语义用真实规则回归。

#### S12-02 章节去重的键与保留方与 Legado 不一致

- 级别：P2
- 类别：功能
- 直接相关文件：internal/app/book/toc.go:120-127（先 dedupe 后 reverse）、internal/app/book/toc.go:372-386（dedupeChapters 以 URL 单字段为键）
- 间接相关文件：legado/app/src/main/java/io/legado/app/model/webBook/BookChapterList.kt:113-119、legado/app/src/main/java/io/legado/app/data/entities/BookChapter.kt:38-57
- 问题描述：两处偏差。(1) 键：Legado 用 `LinkedHashSet(chapterList)`，BookChapter 是 data class，equals 比较全部字段（url+title+tag+isVip…），同 URL 但 tag/标题不同的两条都会保留；Reader `dedupeChapters` 只按 URL（URL 空时按 title）去重，会把"同链接不同附加信息"的章节误删。(2) 保留方：Legado 非倒序目录先 `chapterList.reverse()` 再去重再反回（BookChapterList.kt:113-118），默认配置下去重保留的是"最后一次出现"；Reader 在 reverse 之前按规则顺序去重，保留"第一次出现"。对分页目录中后面页面覆盖/更新了前面章节链接的站点，两者选出的是不同章节。
- 修改方案：`dedupeChapters` 改为对完整可比较键（url+title+tag+isVolume/isVip/isPay）去重；调整 `RefreshToc` 顺序为"非 reverse 时先整体 reverse → 去重（保留后出现）→ 按目标方向再 reverse"，或直接明确采用"保留最后出现"策略并补测试。
- 验证方式：单测构造两页目录，第二页重复首页 URL 但 tag 不同，断言与 Legado 一致地保留两条（或保留后出现的语义）；现有 `TestTocServiceFetchesNextTocPagesAndDedupes` 需同步更新。

#### S12-03 ReplaceByBook 全删全插不清正文缓存，索引漂移后正文与章节错位

- 级别：P2
- 类别：资源（缓存一致性）
- 直接相关文件：internal/app/book/toc.go:121-123、internal/infra/sqlite/repositories/chapter_repository.go:22-49（只删插 chapters，不删 cache_entries）、:76-84（`SaveContent` 以 `content:bookID:index` 为键）
- 间接相关文件：internal/app/content/content.go:119-122、:195-197；legado/app/src/main/java/io/legado/app/help/book/BookHelp.kt（Legado 正文缓存按章节 URL 文件名键控）
- 问题描述：正文缓存键是 `book_id + chapter_index`（chapter_repository.go:43-45），而 `ReplaceByBook` 每次刷新全删全插并可能重排 `chapter_index`（插入新章、`-` 倒序、去重删除都会移位）。刷新后按索引读缓存会拿到旧章节的正文，标题与内容错位，且无任何失效逻辑（只有删书和 migration version 5 清理）。Legado 按 URL 键缓存天然不受重排影响。跨切片：缓存读取在 content 切片，但失效责任随本次 `ReplaceByBook` 写路径。
- 修改方案：`ReplaceByBook` 同一事务内追加 `DELETE FROM cache_entries WHERE owner_type='book' AND owner_id=?`（与 `BookRepository.Delete` book_repository.go:144 同款）；或把缓存键改为 chapter URL。前者改动最小。
- 验证方式：集成测试——写入两章正文缓存 → 用"头部插入一章"的新目录 ReplaceByBook → `GetContent(bookID,0)` 应未命中而非返回旧正文。

#### S12-04 RefreshInfo 无条件重置 book.Type，用户/流程设定的类型被覆盖

- 级别：P2
- 类别：功能
- 直接相关文件：internal/app/book/info.go:59
- 间接相关文件：internal/app/book/service.go:76-87、legado/app/src/main/java/io/legado/app/model/webBook/BookInfo.kt:47-115、legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt:170-172
- 问题描述：`b.Type = src.BookSourceType` 每次详情刷新都执行并无条件写库（UpdateInfo 含 `type` 列）。Legado 的 `BookInfo.analyzeBookInfo` 从不修改 `book.type`——类型只在 BookList 添加书籍时设置一次，用户之后在本地改类型（如音频、webFile、本地书）不会被刷新冲掉。Reader 每次刷新/校验探测都会把类型打回书源声明值。
- 修改方案：`RefreshInfo` 仅在 `b.Type == 0`（未设置）时赋 `src.BookSourceType`，与同函数内 `OriginOrder == 0` 的守卫（info.go:60-62）保持同一模式；`parseInfo`/`PreviewSearchBookFromInfo` 同步。
- 验证方式：单测——book.Type=3（webFile）+ 书源 type=0，RefreshInfo 后断言仍为 3。

#### S12-05 preUpdateJS 修改的 book.variable 不持久化

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/book/toc.go:44-49
- 间接相关文件：legado/app/src/main/java/io/legado/app/model/webBook/WebBook.kt:211-221（runPreUpdateJs 后 upsertBook）
- 问题描述：`PreUpdateJS` 在本地副本 `b` 上执行，脚本对 `book.variable` 的修改只影响本次解析（章节变量展开等），`SkipSave=false` 时也只写 chapters 表，books 行的 `variable` 列不更新。Legado 执行 preUpdateJs 后会 upsert 书籍，脚本写入的变量跨刷新保留。
- 修改方案：`RefreshToc` 成功路径上若 `b.Variable` 与输入不同，随 books 更新写回（可与 S12-01 的元数据更新合并为一个只写 `variable`+元数据列的 UPDATE）。
- 验证方式：单测——PreUpdateJS 执行 `book.variable = ...`（经 source/book 绑定 put），刷新后读库断言 variable 变化。

#### S12-06 万章目录逐章新建 Analyzer 并复制变量 map，内存与 CPU 开销大（与 S01-03 同根，统计并入主条目）

- 级别：P3
- 类别：性能
- 直接相关文件：internal/app/book/toc.go:297-313（每章 `ruleinfra.NewAnalyzer` + `itemAnalyzer.SetVariables(baseVars)` 深拷贝 map）、:275、:309（每页/每章重复 `scriptRuntime.SourceBindings(ctx, src)` 构建十几项的 map）
- 间接相关文件：internal/infra/rule/analyzer.go:606-619（Variables/SetVariables 各一次完整 map 拷贝）、legado/app/src/main/java/io/legado/app/model/webBook/BookChapterList.kt:216-230（复用单一 analyzeRule，仅 setContent(item)）
- 问题描述：万章目录 = 万次 Analyzer 分配 + 万次 baseVars map 拷贝 + 2 万次 SourceBindings map 构建（每章 item analyzer 一份、每页顶层一份），产生大量短命对象，加剧 GC。变量隔离（历史 §5.7 修复）确有必要，但隔离只需要复制"可写变量"，不必每个章节完整重建 analyzer 与 source 绑定。
- 修改方案：`parseTocPage` 顶层构建一次 item 级 bindings/SourceBindings 模板，章节循环内仅替换 `Chapter` 载体；`SetVariables` 改为共享只读 baseVars + 写时复制（首次 @put 时再拷贝）。
- 验证方式：bench——`go test -bench` 构造 1 万章 JSON 目录，对比优化前后 allocs/op 与 ns/op；`go test -race` 回归变量隔离测试。

#### S12-07 并发分页分支对单页 nextURL 数量无上限

- 级别：P3
- 类别：性能
- 直接相关文件：internal/app/book/toc.go:208-219（targets 直接取整页 nextURLs，无 maxTocPages 检查）
- 间接相关文件：internal/app/book/toc.go:15-20（maxTocPages 仅用于串行链）
- 问题描述：`maxTocPages=100` 只约束串行链长度；若某个分页的 `nextTocUrl` 规则一次返回数百/上千个 URL（异常规则或列表页误配），会一次性把全部 URL 加入 targets，虽受 `tocPageConcurrency=8` 并发限制，但目标列表无界，长时间占用抓取与内存，且这些分页各自的 nextURL 又被丢弃（getNextURL=false），抓了也只取第一层。历史 C-015 修复只覆盖了串行链。
- 修改方案：并发分支同样以 `len(seenPages)+len(targets) <= maxTocPages` 截断并 warn（返回 ErrTocPaginationLimit 保持"不完整则不覆盖"语义）。
- 验证方式：单测——单页返回 500 个 nextURL，断言截断在 100 并返回错误。

#### S12-08 并发分页共享同一 *book.Book 指针，URL 选项 JS 可并发读写

- 级别：P3
- 类别：并发
- 直接相关文件：internal/app/book/toc.go:224-232（并发回调里 `buildBookFetchRequest`/`executeBookFetchRequest` 传共享 `b`，仅 parse 阶段用了 pageBook 拷贝）
- 间接相关文件：internal/app/source/request_evaluator.go:11-25（bindings 透传 `book`）、internal/app/source/script_adapter.go:20-27
- 问题描述：并发分页回调中请求构建/执行的 `bindings["book"] = b`（fetch_request.go:49-52）指向同一 `*book.Book`；若书源在 URL 选项 JS（`urlOptionJs`/`js` 参数）里通过 `book` 绑定写字段，8 个 goroutine 并发写同一 Go 结构体构成数据竞争。Legado 同样共享 `ruleData = book`（BookChapterList.kt:103-110），行为对齐，但 Go 侧 goja 绑定直接暴露可变指针，风险面更大。
- 修改方案：并发回调内一开始就 `pageBook := *b` 并将请求/执行/解析全程使用该快照（当前只解析用快照），或对 bindings 的 book 传值拷贝。
- 验证方式：单测——URL 选项 JS 写 `book.name`，`go test -race` 下并发分页不再报 race。

#### S12-09 PreviewSearchBookFromInfo 传 canRename=true，与 Legado 搜索→详情路径的 false 不一致

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/book/info.go:204（`parseInfo(..., true, ...)`）
- 间接相关文件：internal/app/search/single_source.go:85、:175、internal/app/discovery/discovery.go:395；legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt:175-183（`analyzeBookInfo(..., canReName = false)`）
- 问题描述：搜索/发现回退到详情页解析时，Legado 传 `canReName=false`——即书源声明了 `canReName` 也只用解析结果填补空缺，不覆盖搜索结果已有的 name/author；Reader 传 `true`，允许详情规则覆盖搜索阶段的 name/author，同名书在"搜索结果"与"加入书架后"名称可能不同。
- 修改方案：`PreviewSearchBookFromInfo` 的 canRename 改为 false（与 Legado BookList 对齐）；若有意保留改名能力需在文档记录差异。
- 验证方式：单测——搜索结果 name="A"，详情规则返回 name="B" 且书源声明 canReName，断言 preview 仍为 "A"。

### S13 正文与图片代理

审核范围：internal/app/content/content.go、service.go、image_proxy.go（含测试 content_test.go、service_test.go、image_proxy_test.go、export_test.go），对照 legado/app/src/main/java/io/legado/app/model/webBook/BookContent.kt、help/book/BookHelp.kt；关联消费点 internal/api/handlers/source_handler.go（ProxyImage/writeImageProxyError）、internal/infra/concurrency/concurrency.go、internal/infra/sqlite/repositories/image_cache_repository.go、chapter_repository.go。

#### S13-01 image/svg+xml 经图片代理原样回传且响应缺 nosniff/CSP，构成反射型 XSS 向量

- 级别：P2（主审由 P1 调整：鉴权仅接受 `Authorization: Bearer` 头（middleware/auth.go），浏览器导航无法携带，仅在空 token + 回环监听模式下可被利用，由 P1 降为 P2）
- 类别：安全
- 直接相关文件：internal/api/handlers/source_handler.go:511-514、internal/app/content/image_proxy.go:222
- 间接相关文件：api/openapi.yaml:798-850、internal/api/middleware/auth.go
- 问题描述：isAllowedImageContentType 放行 `image/svg+xml`（image_proxy.go:222），handler 将远端 SVG 字节以 `Content-Type: image/svg+xml` 内联写出（source_handler.go:511-514），且全应用未设置 `X-Content-Type-Options: nosniff` 或 CSP（grep 全仓无 nosniff）。书源图片 URL 可指向攻击者 SVG；读者"查看图片"直接导航到 /api/v1/images/proxy 时，SVG 内 script 在 Reader 源内执行，可读取页面中保存的 Bearer token 并外传 [推测：token 存储方式取决于前端]。Legado 把图片下载到本地文件，无此面。
- 修改方案：image_proxy.go 的允许列表移除 `image/svg+xml`，或对 SVG 响应强制 `Content-Security-Policy: script-src 'none'` + `X-Content-Type-Options: nosniff` + `Content-Disposition: inline; filename=...`（sanitize 更彻底：用 bluemonday 剥离 script/事件属性）。
- 验证方式：单测——SVG 响应头断言 nosniff/CSP，或 SVG 输入被拒绝；手工 curl 检查响应头。

#### S13-02 title 规则在每个分页上重复执行，末页标题覆盖章节标题

- 级别：P2
- 类别：功能
- 直接相关文件：internal/app/content/content.go:404-409（parsePage 内 title 块）、:250（串行循环每页调用 parsePage）
- 间接相关文件：legado/.../model/webBook/BookContent.kt:60-71（title 规则只在最外层 analyzeContent 执行一次）
- 问题描述：Legado 的 title 规则只在最外层 analyzeContent 对首页执行一次（BookContent.kt:60-71，私有 analyzeContent 不含 title 逻辑）；Reader 的 parsePage 对每个分页都执行 `contentRule.Title` 并直接覆盖 `chapter.Title`（content.go:404-409）。串行分页时末页解析出的标题（常为"第N页/上一页 下一页"类文本）覆盖首页正确标题，并经 content.go:191-193 SaveTitle 持久化，重启后仍是错标题。
- 修改方案：parsePage 增加 applyTitle 参数（或把 title 解析移出 parsePage），仅首页（parseAllPages 第一次调用）应用 title 规则；并发分支 getNext=false 本就不应改标题。
- 验证方式：单测——两页正文各含不同 title 规则命中，断言最终 chapter.Title 取自首页。

#### S13-03 并发分页分支没有页数上限

- 级别：P2
- 类别：功能/性能
- 直接相关文件：internal/app/content/content.go:257-288
- 间接相关文件：internal/infra/rule/analyzer.go:105-124（GetURLList）
- 问题描述：maxContentPages=100 只保护串行分支（content.go:228-231）。当首页 nextContentUrl 规则返回 >1 个 URL（如规则写成通配/列表型，AnalyzeUrl 列表规则可返回任意多行）时，并发分支直接对全部 targets 发起抓取（8 并发），无上限：一个返回 5000 个 URL 的规则会对同一站点发起 5000 请求，耗时与内存无界。
- 修改方案：targets 去重后加 `if len(targets) > maxContentPages { return "", ErrPaginationTruncated }`，与串行分支同一上限语义。
- 验证方式：单测——nextContentUrl 返回 101+ 个 URL，断言返回 ErrPaginationTruncated 且 fetcher 调用数为 0 或有界。

#### S13-04 ExportTXT 每章触发 N+1 查询，长书导出极慢

- 级别：P2
- 类别：性能
- 直接相关文件：internal/app/content/service.go:170（循环内 s.GetChapterContent(ctx, id, chapter.Index)）
- 间接相关文件：internal/app/content/service.go:68-97（GetChapterContent 内 Books.FindByID + Chapters.FindByIndex + GetContent）
- 问题描述：ExportTXT 已通过 ListByBook 拿到全部章节，但循环里每章调 GetChapterContent，内部再执行 Books.FindByID（1 次）+ Chapters.FindByIndex（1-2 次）+ cache_entries 查询（1 次），数千章的书 = 数千×4 次 SQLite 查询；且未命中缓存的章会现场联网抓取整章（导出被网络阻塞数小时）。
- 修改方案：循环内改用 `s.GetChapterContentWith(src, b, *chapter, next)`（复用已加载的 b/chapter，省 3 次查询），src 在循环前 FindByURL 一次；联网抓取行为可加参数控制（仅导出已缓存内容或显式 fetchMissing）。
- 验证方式：基准测试导出 1000 章缓存齐全的书，对比 SQL 查询计数（sqlmock 或 sqlite 计数器）与耗时。

#### S13-05 GetContent 无 singleflight，同章并发请求重复抓取并双写缓存

- 级别：P2
- 类别：并发/性能
- 直接相关文件：internal/app/content/content.go:118-127（缓存查）、:189-197（写）
- 间接相关文件：internal/infra/concurrency/concurrency.go
- 问题描述：serve 模式下两个并发请求（如预下载与阅读同时到达）对同一未缓存章节都会通过 content.go:119 的未命中检查，各自执行完整多页抓取（每页 LoginCheck/WebJS/正则），最后都执行 SaveContent 双写。浪费带宽、放大对书源站点的请求量，且两次结果可能不同（站点返回波动）导致后写覆盖。
- 修改方案：Service 增加 `inflight singleflight.Group`（golang.org/x/sync/singleflight），以 `bookID:index` 为 key 合并并发 GetContent 抓取路径，结果共享。
- 验证方式：单测——并发 10 个 GetContent 同章，断言 fetcher 请求数为单章页数而非 10 倍。

#### S13-06 replaceRegex 应用后缺少 Legado 的逐行 trim 与全角缩进

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/content/content.go:295-299
- 间接相关文件：legado/.../model/webBook/BookContent.kt:133-139
- 问题描述：Legado 在全文替换前逐行 trim（`split(LFRegex).joinToString { it.trim() }`）、替换后给每行加 `"　　"` 全角缩进（BookContent.kt:134-139）；Reader 只做整体 `strings.TrimSpace(replaced)`，丢失逐行缩进语义，依赖该缩进的阅读排版与导出 TXT 视觉效果与 Legado 不一致。
- 修改方案：parseAllPages 尾部对齐 Legado：替换前逐行 trim，替换后逐行加 `\u3000\u3000` 前缀。
- 验证方式：单测——replaceRegex 规则命中多行正文，断言每行前缀 \u3000\u3000。

#### S13-07 正文处理热路径存在多余整本拷贝与 O(n·m) 替换

- 级别：P3
- 类别：性能
- 直接相关文件：internal/app/content/content.go:333-346、:455-469、:471-483
- 间接相关文件：internal/app/content/content.go:219-293（每分页调用）
- 问题描述：(1) applyContentWebJS 中 `string(resp.Body)` 对同一 body 拷贝 3 次（result/src/response.body 绑定，content.go:334-345）；(2) normalizeContentHTML 先用 ReplaceAllStringFunc 提取图片、后再对每张图片执行一次全文 `strings.ReplaceAll`（content.go:469），n 张图 = n 次全文扫描；(3) normalizeContent 先 Split 后 Join 再逐行 TrimSpace（content.go:441-453），多轮中间串分配。每章多分页时叠加放大内存分配（与已知"校验/搜索内存泄露"同一热路径家族，跨切片）。
- 修改方案：三个绑定共享同一 string（计算一次）；图片占位符还原改为单次 `strings.NewReplacer(pairs...)` 或 builder 扫描；normalizeContent 用 bufio.Scanner + 单 builder。
- 验证方式：benchmark（b.ReportAllocs）对比大正文（1MB、50 图）单次 GetContent 的 allocs/op。

#### S13-08 __READER_IMG_N__ 占位符与正文文本碰撞导致数据替换

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/content/content.go:458-460、:469-471
- 间接相关文件：internal/app/content/content_test.go
- 问题描述：图片占位符 `__READER_IMG_%d__` 直接写入正文；若书源正文本身含该字面串（或分页拼接后恰好碰撞），strings.ReplaceAll 会把它替换成无关图片 HTML，正文被污染。概率低但属数据损坏路径。
- 修改方案：改用控制字符包裹的不可打印哨兵（如 `\x00READER_IMG_0\x00`）或在还原前校验图片数组长度与占位符编号。
- 验证方式：单测——正文含 `__READER_IMG_0__` 字面文本 + 1 张真图，断言字面文本不被替换。

#### S13-09 图片缓存键含 BookID/DecodeMode 造成同图多份缓存

- 级别：P3
- 类别：资源/质量
- 直接相关文件：internal/app/content/image_proxy.go:155-162
- 间接相关文件：internal/infra/sqlite/repositories/image_cache_repository.go:38-45、api/openapi.yaml:821-824
- 问题描述：imageCacheKey 把 SourceID、DecodeMode、BookID 全部拼入键（image_proxy.go:155-162），同一封面 URL 在不同 BookID 下各存一份完整字节（SQLite cache_entries，TTL 7 天）；openapi 注释称 BookID 为 "future book-scoped image caching" 的预留参数，当前实现却已参与键。decodeImage 每次请求回源 `sources.FindByID`，无短 TTL 缓存，图片密集型源重复查库。
- 修改方案：缓存键默认只用 URL（+DecodeMode，因解码产物不同）；BookID 仅传给 decode 上下文不参与键；decodeImage 的 source 查询加进程内 TTL 缓存或由调用方传入。
- 验证方式：单测——同 URL 不同 BookID 两次 Fetch，断言 cache_entries 只有一行；基准对比 DecodeMode=none 时不再查 sources 表。

#### S13-10 图片代理是已鉴权的任意公网 URL 抓取端点，无按调用方的速率约束

- 级别：P3
- 类别：安全/性能
- 直接相关文件：internal/app/content/image_proxy.go:78-90
- 间接相关文件：internal/api/handlers/source_handler.go:488-515、internal/infra/fetcher/http_fetcher.go:148-151
- 问题描述：/api/v1/images/proxy 接受任意绝对 URL，SSRF 由 fetcher 拨号期校验兜底（私网默认拒绝，含重定向，http_fetcher.go:30-33），这点做得对；但请求不带 SourceKey，rateLimiter 直接跳过（rate_limiter.go:29-31），持 token 的调用方可无限速地用 Reader 作公网内容中转/放大器，缓存表也可能被单调用方灌满。单用户自托管场景风险有限。
- 修改方案：为图片代理请求填入固定 SourceKey（如 "image-proxy"）并配默认并发上限；或对 proxy 端点加按 token 的令牌桶。
- 验证方式：单测/压测——同一 token 连续 N 次 Fetch 不同 URL，断言触发限速。

### S14 替换规则与发现

审核范围：internal/app/replace/processor.go、limited_regex.go、manager.go、resegment.go；internal/app/discovery/discovery.go、cache.go

#### S14-01 大章节/取消请求会把合法正则替换规则永久禁用

- 级别：P1
- 类别：功能|资源
- 直接相关文件：internal/app/replace/limited_regex.go:13,24-39；internal/app/replace/processor.go:214-225
- 间接相关文件：internal/app/replace/processor.go:154-170；legado/app/src/main/java/io/legado/app/help/book/ContentProcessor.kt:160-180
- 问题描述：`replaceText` 把三种不同错误统一折叠加进 `ErrRegexLimitExceeded`：(1) 输入超过 `maxRegexInputBytes=2MB`（limited_regex.go:24-26）、(2) ctx 已取消（:28-30）、(3) 执行后 ctx 超时（:40-42）。`handleRuleFailure`（processor.go:215-221）对任何 `ErrRegexLimitExceeded` 都执行 `rule.IsEnabled=false` 并 `repo.Save` 持久化。结果：抓取一个 >2MB 的大章节、或一次请求取消，都会把本来合法的替换规则**永久写入禁用**，用户无感知。Legado 只在 `RegexTimeoutException`（真正的正则超时）分支禁用（ContentProcessor.kt:170-173），输入大小限制与取消不属于该分支。
- 修改方案：拆分错误类型：`ErrRegexInputTooLarge`、`ErrRegexCancelled`、`ErrRegexTimeout` 三者独立；`handleRuleFailure` 仅对 `ErrRegexTimeout` 禁用并保存，其余只告警。超时判断应基于"执行前后 limitedCtx 过期且未先完成"，输入超大分支直接返回不触碰规则状态。
- 验证方式：单测：构造 2.5MB 输入 + regex 规则，断言规则 IsEnabled 仍为 true 且未 Save；ctx 取消场景同；仅 timeout 场景断言禁用。

#### S14-02 reSegment 合段循环 O(n²)：shouldBreakMergedParagraphBuilder 每段整串拷贝

- 级别：P1
- 类别：性能
- 直接相关文件：internal/app/replace/resegment.go:96-113（shouldBreakMergedParagraphBuilder 定义 :96-113、调用 :72-79）
- 间接相关文件：legado/app/src/main/java/io/legado/app/help/book/ContentHelp.kt:59-70
- 问题描述：`reSegment` 对每个段落调用 `shouldBreakMergedParagraphBuilder(&merged)`，其实现 `strings.TrimRight(builder.String(), "\r\n")` 会把 builder 当前全部内容拷贝成新字符串再取最后一个 rune。章节有 P 个段落、总长 n 时为 O(P·n)，长章节（数百段、数百 KB）退化为平方级，是正文处理慢的主要热点之一。Legado 直接访问 `buffer.last()`，O(1)。
- 修改方案：在合段循环内维护"上一个写入 rune"变量（或记录 builder.Len() 与最后 rune），把 shouldBreak 判断改为只看最后 1-2 个 rune，删除整串拷贝；函数签名改为接收 (last, last2 rune, length int)。
- 验证方式：`go test -bench` 写一个 500KB/2000 段基准，修复前后对比；或 pprof 确认 strings.Builder.String 占比消失。

#### S14-03 forceReSegmentSplit/deterministicReSegmentScore 每个句末符号对整个片段重新哈希，最坏 O(m²)

- 级别：P1
- 类别：性能
- 直接相关文件：internal/app/replace/resegment.go:476-502（forceReSegmentSplit）、:530-541（deterministicReSegmentScore）
- 间接相关文件：internal/app/replace/resegment.go:355-370、:376-398（三处调用点）
- 问题描述：`forceReSegmentSplit` 对片段内每个句末符号调用 `deterministicReSegmentScore(text, ...)`，后者每次 `fnv.Write([]byte(text))` 哈希**整个片段**。无引号的长段落（两引号之间可达数十 KB、句末符号上千个）为 O(k·m)≈O(m²)；且文本 + offset + idx + midCount 拼接后重复哈希未缓存。同一 (text,offset) 下 midCount 递增，但每次仍全量重写 text 字节。
- 修改方案：在 forceReSegmentSplit 内预计算一次 text 的 FNV 中间态（`h.Sum64()` 后续用 `fnv` 不支持 fork——改为对 text 只哈希一次得到 base uint64，再与 offset/idx/midCount 组合做最终混合，例如 base ^ uint64(offset)*prime…），保证同结果确定性；或对每个 (text,offset) 缓存 base 哈希。
- 验证方式：单段 200KB、无引号、句末符号密集的基准用例，修复前后耗时对比；结果值与修复前逐位一致（确定性不破坏）。

#### S14-04 reSegment 私自增加 Legado 没有的两条切分变换，改变对话文本

- 级别：P2
- 类别：功能
- 直接相关文件：internal/app/replace/resegment.go:35-36（reSegmentQuoteNarrationCut、reSegmentQuoteAfterNarrator）、:110-114（应用处）
- 间接相关文件：legado/app/src/main/java/io/legado/app/help/book/ContentHelp.kt:72-90
- 问题描述：Go 版在 reduceLength 之前额外应用 `QuoteNarrationCut`（`”。xxx…说…：“` 中在 24 字符窗口内的"说"前强制断行）和 `QuoteAfterNarrator`（`”。xxx` 一律断行）。Legado 的最终链（ContentHelp.kt:76-89）只有 5 条替换，不存在这两条。对含 `”。某某说道：“……”` 的正文，Go 版会在叙述语前后插段，与 Legado 输出不一致，且 `[^“"\n]{1,24}` 的窗口启发式对长人名/插入语会错断。
- 修改方案：若目标是"对齐 Legado 语义"，删除这两条 ReplaceAllString 调用与对应 regex 变量；若是有意增强，须加开关并默认关闭，文档注明差异。
- 验证方式：用含 `”。他缓缓说道：“……"` 的样例对比 Go 输出与 Legado 算法输出一致；processor_test 补用例。

#### S14-05 规则快照共享指针被并发写：handleRuleFailure 数据竞争 + 禁用规则仍被逐章重放

- 级别：P2
- 类别：并发|功能
- 直接相关文件：internal/app/replace/processor.go:214-225（写 IsEnabled）、:74-93（ruleSet 缓存共享指针）、:154-170（Process 循环不检查 IsEnabled）
- 间接相关文件：internal/app/content/content.go:212（多章节并发调用）
- 问题描述：ruleSet 缓存存 `[]*replace.ReplaceRule`，多 goroutine（如 HTTP API 并发取章、批量下载）共享同一批指针。某条规则超时后 `handleRuleFailure` 无锁写 `rule.IsEnabled=false` 并 Save —— 与其他 goroutine 的读取构成 data race（`go test -race` 可捕获）。且 `Process` 的规则循环只跳过 `rule==nil || Pattern==""`，不检查 `IsEnabled`：被禁用的规则在下一次 Process 仍会参与替换、再次超时、再次 Save，进程存活期内每章重复失败一次。
- 修改方案：快照存值拷贝或不可变列表，handleRuleFailure 通过 Manager 仓储更新后调 InvalidateRuleCache（而不是改共享指针）；Process 循环加 `if !rule.IsEnabled { continue }`。
- 验证方式：`go test -race -run Replace`；单测：禁用规则后再 Process 一次，断言 replaceText 未被调用。

#### S14-06 重加标题未做简繁转换；IncludeTitle=false 时仍执行标题域规则

- 级别：P2
- 类别：功能
- 直接相关文件：internal/app/replace/processor.go:118-124（displayTitle 生成与复用）、:245-260（applyTitleRules）
- 间接相关文件：internal/app/content/content.go:212-219（ProcessContent 未传 IncludeTitle，恒 false 语义）；legado ContentProcessor.kt:186-192、:106-109
- 问题描述：Legado 最终 `includeTitle` 重加标题时调用 `chapter.getDisplayTitle(titleReplaceRules)`，默认 `chineseConvert=true`——标题会随正文一起做简繁转换。Go 版 displayTitle 只做了 titleRules，未经 `convertChinese`，简繁设置开启时标题与正文（已转换）文字体系不一致。另外 Go 在 IncludeTitle=false 时也跑 applyTitleRules（Legado includeTitle=false 完全跳过标题处理），配合 content.go 恒传 false，等于标题规则只影响 sameTitle 去重判断。
- 修改方案：在 includeTitle 分支对 displayTitle 应用 `p.convertChinese(displayTitle)`（注意与正文同 mode）；Process 在 `!input.IncludeTitle` 时跳过 titleRules（或仅用于去重的最小路径），并让 content 层把真实 includeTitle 语义传下来。
- 验证方式：单测：ChineseConverterType=1 + 繁体标题 + IncludeTitle=true，断言标题被转换；IncludeTitle=false 断言 titleRules 不执行。

#### S14-07 parseBookList 每本书目新建 Analyzer，规则重复编译 O(条目×规则)（与 S01-03 同根，统计并入主条目）

- 级别：P2
- 类别：性能
- 直接相关文件：internal/app/discovery/discovery.go:388-401、:405-414
- 间接相关文件：internal/infra/rule/analyzer（规则编译）；legado/app/src/main/java/io/legado/app/model/webBook/BookList.kt:250-290（单一 AnalyzeRule + setBook 复用）
- 问题描述：`parseBookList` 对列表级建一个 Analyzer 后，**又对每个 element 新建一个 itemAnalyzer**（每个都重新构建 Context、ScriptAdapter，`SourceBindings` 每元素重算）。列表页常返回 20-50 条，规则里的每条子规则（正则/js/jsonpath）随之重复解析编译，发现页多源聚合时放大为 条目数×规则数 次解析。Legado 用同一 AnalyzeRule 并 `setBook(searchBook)` 复用。
- 修改方案：element 循环外建一个 itemAnalyzer，循环内仅 `SetBook(&searchBook)`/`SetVariables(baseVars)` 更新绑定（需确认 ruleinfra.Analyzer 支持重绑定；不支持则给 Analyzer 增加 SetBook 方法）。
- 验证方式：`go test -bench Discovery`（大规则源 × 50 条目）前后对比；pprof 中 analyzer 构建占比下降。

#### S14-08 reduceLength 边界与 Legado 不一致：倒数第二段也会被强制切分

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/replace/resegment.go:232-250
- 间接相关文件：legado ContentHelp.kt:107-110
- 问题描述：Legado 对话模式分支条件是 `dialogue > 0 && i < l - 2`（只处理到倒数第三段），Go 版写成 `i < len(paragraphs)-1`（允许处理倒数第二段），末段附近切分行为与 Legado 输出不同。
- 修改方案：改为 `i < len(paragraphs)-2` 对齐 Legado。
- 验证方式：构造末尾对话段落样例对比两实现输出。

#### S14-09 `<js>` exploreUrl 结束检测要求完整 `</js>`，且 JS 失败无 ERROR 类目回退

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/discovery/discovery.go:262-283
- 间接相关文件：legado/app/src/main/java/io/legado/app/help/source/BookSourceExtensions.kt:55-63
- 问题描述：(1) Go 用 `LastIndex("</js>")` 且要求 `end > 4`；Legado 用 `substring(4, lastIndexOf("<"))`——书源常见写法 `<js>...<js2>` 或注释残留时 Legado 能解析、Go 解析出空脚本或报错。(2) JS 执行失败时 Go 直接返回 error 使该源 discoverSource 失败；Legado 捕获后返回 `ExploreKind("ERROR:...", stackTrace)`，分类页仍可见错误信息。
- 修改方案：结束位置改为 `LastIndex(raw, "<")`（从 4 之后起找）；categoriesForSource JS 失败时返回 `[]Category{{Title:"ERROR:"+err}}` 或至少记录 Warning 而非让整个源失败。
- 验证方式：单测：`<js>...<//garbage` 形式的 exploreUrl 能取到脚本体；JS 抛错的源 Categories 返回非空。

#### S14-10 discoverSource 以 categories[0].Title 充当 URL 兜底；Categories 无单飞（single-flight）

- 级别：P3
- 类别：功能|性能
- 直接相关文件：internal/app/discovery/discovery.go:222-230（Title 兜底）、:236-283（categoriesForSource 无互斥）
- 间接相关文件：legado BookSourceExtensions.kt:45-49（per-source Mutex）
- 问题描述：(1) `discoveryURL = categories[0].URL` 为空时回退用 `Title` 当 URL 发起请求，会把中文类目名当 URL 抓取，必失败且浪费一次请求/限速额度；Legado 只用 URL。(2) JS 类目首次并发调用（Discover 多源 + Categories 同时）会重复执行同一 JS 脚本；Legado 用 per-source mutex 单飞。
- 修改方案：URL 为空直接返回 ErrEmptyExploreURL 或跳过该类目；categoriesForSource 对 (sourceURL, exploreURL) 加 singleflight（golang.org/x/sync/singleflight 或 map+mutex）。
- 验证方式：单测：URL 为空的类目不发起 fetch；并发 100 次 Categories 断言 JS EvalWithBindings 只执行 1 次。

#### S14-11 handleRuleFailure 对同一次失败双重告警日志

- 级别：P3
- 类别：质量
- 直接相关文件：internal/app/replace/processor.go:214-225
- 间接相关文件：无
- 问题描述：禁用分支内 `slog.Warn("disable replace rule failed", ...)` 之后，函数末尾又无条件 `slog.Warn("replace rule failed", ...)`——禁用路径每次输出两条相关日志，且后者把已处理的超时错误再次记录，噪音大。
- 修改方案：禁用分支保存成功后 `return`；末尾日志仅对非 ErrRegexTimeout 错误输出。
- 验证方式：单测捕获 slog 输出，断言 timeout 场景仅一条。

#### S14-12 rule.Validate 每次调用重新编译正则，绕过共享 LRU

- 级别：P3
- 类别：性能
- 直接相关文件：internal/domain/replace/replace_rule.go:22-25（跨切片：domain 层，因 replace 热路径直接受影响）
- 间接相关文件：internal/app/replace/limited_regex.go:17；internal/infra/rule/regex/regex.go:56-70
- 问题描述：`replaceText` 对每章每条 regex 规则先 `rule.Validate()` → `regexp.Compile(pattern)`，随后 `ruleregex.Replace` 又经 LRU 再取一次编译结果——同一 pattern 每章编译两遍，其一完全绕过 patternCache。规则多、章节多时是无谓的 CPU 开销。
- 修改方案：Validate 里的编译检查改用 `regex.CompileStd(rule.Pattern)`（命中 LRU），或 replaceText 对已过 repo 层校验的规则跳过二次 Validate。
- 验证方式：bench：100 规则 × 1000 章场景前后 CPU 对比；pprof 中 regexp.Compile 消失。

### S15 书源导入/调试/诊断

审核范围：internal/app/source/{importer.go, script_integration.go, script_adapter.go, login.go, manager.go, request_evaluator.go}、internal/app/debug/{flow.go, runner.go, publisher.go, service.go}、internal/app/diagnostic/failure.go、关联调用方（internal/api/handlers/source_handler.go、internal/cli/import.go、internal/infra/sqlite/repositories/{book_source_repository.go, debug_repository.go}、cmd/reader/serve.go、internal/bootstrap/bootstrap.go、internal/infra/script/{runtime.go, variables.go}、internal/infra/fetcher/http_fetcher.go）

#### S15-01 远程 sourceUrls 导入无递归深度与条目数上限，自引用 URL 死循环

- 级别：P2
- 类别：资源|功能
- 直接相关文件：internal/app/source/importer.go:107-141,161-171
- 间接相关文件：internal/api/handlers/source_handler.go:146-166、internal/cli/import.go:72-90
- 问题描述：`Import` 对 `{` 开头的 payload 调 `sourceURLs()` 判定后进入 `importSourceURLs`，后者逐个远程拉取并把响应体**递归**传回 `Import`（importer.go:130）。远程内容若本身是 `{"sourceUrls":["<同一 URL>", ...]}`（自引用或相互引用），将无限递归抓取——每次 fetch 有 30s 超时但循环本身没有终止条件，也没有对 `sourceUrls` 数组长度做任何上限校验。单个恶意/损坏的远程文档可让导入请求无限占用连接、网络与内存（RemoteErrors/Items 无界累积）。
- 修改方案：给 `Importer`（或 `importSourceURLs`）加递归深度参数（如最大 3 层，超出记 RemoteError 并 continue）；同时对单次 `sourceURLs()` 返回的 URL 数量设上限（如 1000），超限截断并在 result 中标注。
- 验证方式：新增单测：本地 httptest server 返回自引用 sourceUrls 文档，导入应在有限时间内返回且 RemoteErrors 含 depth-exceeded；旧测试全部通过。

#### S15-02 调试会话创建无并发上限，goroutine 与抓取可无限堆积

- 级别：P2
- 类别：并发|资源
- 直接相关文件：internal/app/debug/service.go:31-45、internal/app/debug/runner.go:31-52
- 间接相关文件：internal/api/handlers/debug_handler.go、internal/infra/sqlite/repositories/debug_repository.go:49-66
- 问题描述：`CreateSession` 落库后直接 `runner.Start(context.Background(), created)`，`Start` 无条件 `go func()` 执行整条搜索→详情→目录→正文抓取链。没有对运行中会话数量的任何上限（对比 check 链路有 `CheckConcurrency` 配置，bootstrap.go:126/102）。反复 POST 创建会话（或脚本刷接口）可堆积任意数量的后台 goroutine，每个还带着多阶段网络抓取与每 Eval 新建 goja VM 的开销，最终打满 CPU/内存/文件描述符。会话也没有身份/频率约束，`runs` map 无上限。
- 修改方案：`Runner` 增加带缓冲信号量（如 `chan struct{}`，容量来自配置，参照 `WithCheckConcurrency` 惯例）：`Start` 先非阻塞获取或排队，满时直接把会话置 failed（"too many running debug sessions"）；或复用全局并发配置 `GlobalConcurrency`。
- 验证方式：单测：并发 Start 超过上限的会话数，断言多余会话终态为 failed 且 goroutine 数回落；`go test -race ./internal/app/debug/...`。

#### S15-03 ScriptIntegration.LoginCheck 缺少 nil runtime 守卫，与 HeaderMap 不一致

- 级别：P3
- 类别：质量
- 直接相关文件：internal/app/source/script_integration.go:100-108
- 间接相关文件：internal/app/book/info.go:75-78、internal/app/book/toc.go:82-85,191,238、internal/app/content/content.go:170-173、internal/app/discovery/discovery.go:232
- 问题描述：`HeaderMapWithBindings` 对 `@js:`/`<js>` 分支显式判断 `s.runtime == nil`（script_integration.go:50,67），`LoginStateOf`/`Login` 也有 `s.runtime == nil` 守卫；但 `LoginCheck` 只要 `LoginCheckJS` 非空就直接 `s.runtime.EvalWithBindings(...)`——runtime 为 nil 时 `EvalWithBindings` 访问 `r.timeout` 触发 nil 指针 panic。当前所有生产调用方都包了 `if s.script != nil`，属潜在地雷而非现行崩溃，但同一文件内守卫约定不一致。
- 修改方案：`LoginCheck` 开头加 `if s == nil || s.runtime == nil { return resp, nil }`（与 HeaderMap 的"无运行时则跳过脚本"语义一致）。
- 验证方式：单测：`NewScriptIntegration(nil)` + 带 LoginCheckJS 的书源调用 LoginCheck，应原样返回 resp 而非 panic。

#### S15-04 loginUrl 为纯 URL 且 loginUi 非空时被整体当脚本文本执行

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/source/script_integration.go:233-243
- 间接相关文件：internal/app/source/login.go:38-50
- 问题描述：`ScriptIntegration.Login` 中 `scriptRuleText` 提取失败（非 @js:/<js> 前缀）后，把整个 `src.LoginURL` 当脚本文本去 eval（"Legado getLoginJs 对无前缀的 loginUrl 也按脚本处理"）。但 Legado 的 `BaseSource.getLoginJs` 对 `http(s)://` 开头的 loginUrl 是返回该 URL 由 WebView/UI 侧处理，并非把 URL 字符串当代码执行。后果：书源 `loginUrl` 是登录页 URL 且 `loginUi` 非空时，`LoginService.Login` 不会返回 `ErrUnsupportedLoginFlow`（它只在 loginUI 为空时判定），而是执行 `https://xxx` 这样的"脚本"，最终抛 `Function login not implements!!!`，错误语义误导（看起来像书源脚本缺 login 函数，实际是流程不支持）。
- 修改方案：`Login`（或 `LoginService.Login`）在 `!isScript` 时判断 `strings.HasPrefix(strings.TrimSpace(src.LoginURL), "http")`，是 URL 则返回 `ErrUnsupportedLoginFlow`；仅在确为裸 JS 文本时才按脚本 eval。
- 验证方式：单测：loginUrl="https://example.test/login"、loginUi 非空 → Login 返回 ErrUnsupportedLoginFlow；@js: 前缀用例保持通过。

#### S15-05 Publisher 并发发布时 SSE 事件顺序可能与 EventID 顺序不一致

- 级别：P3
- 类别：并发
- 直接相关文件：internal/app/debug/publisher.go:30-57
- 间接相关文件：internal/app/debug/flow.go:207-232、internal/infra/sqlite/repositories/debug_repository.go:69-91
- 问题描述：`Publish` 先 `repo.AppendEvent`（拿到自增 EventID）再持锁推送订阅者。两个 goroutine（如 flow 主流程与 CancelSession 的 publishCancelled）并发发布时，EventID 较大的事件可能先 `AppendEvent` 返回并先进入 channel，造成 SSE 流里事件顺序与 `Last-Event-ID` 增量语义（按 event_id 排序）不一致；断线重连后客户端按 event_id 重放会得到与实时流不同的顺序。低概率但真实存在（取消与自然结束竞态正是该设计里明确处理的场景）。
- 修改方案：让 EventID 分配与入 channel 原子化：先在锁内从仓库取 ID（或维护内存序号）再 AppendEvent；或 Publish 推送段改由单一 per-session 串行队列消费 `AppendEvent` 结果。
- 验证方式：`go test -race` + 专门单测：两个 goroutine 高频并发 Publish，订阅端断言收到的事件 event_id 单调递增。

#### S15-06 同 sourceId 不同 bookSourceUrl 的批量导入条目绕过 pending 去重，静默互覆

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/source/importer.go:207-248、internal/infra/sqlite/repositories/book_source_repository.go:39-83
- 间接相关文件：internal/app/source/importer_test.go
- 问题描述：`planImport` 的 pending 去重键是 `BookSourceURL`，而 `SaveMany` 的 upsert 第一键是 `source_id`（sourceId 冲突即覆盖旧地址并 `deleteSourceOwnedData` 清掉旧 URL 的书源变量/缓存）。同一批次里两条不同 URL 但相同 sourceId（伪造或截断碰撞）的条目都会被判为 added 并写入，后者在事务内静默覆盖前者，且前者的 result.Items 仍显示 added——与最终落库状态不符。正常书源 sourceId 由 URL 哈希派生不会碰撞，但导入外部文件时 sourceId 是显式字段（`json:"sourceId"`），外部数据完全可控。
- 修改方案：pending 额外维护 `map[string]bool`（sourceId 索引）；`planImport` 若发现本批已有相同 sourceId 的 pending 条目，将后者按"已存在"处理（复用 shouldSkip/applyImportOptions 分支），保证 result 与落库一致。
- 验证方式：单测：同一批次导入两条同 sourceId 不同 URL 的书源，断言 result 中第二条为 updated/skipped 且最终库里只有一条。

#### S15-07 importSourceURLs 串行抓取且无整体 deadline，多 URL 时请求可长时间挂起

- 级别：P3
- 类别：性能
- 直接相关文件：internal/app/source/importer.go:115-141
- 间接相关文件：internal/api/handlers/source_handler.go:146-166、cmd/reader/serve.go:26-31
- 问题描述：`importSourceURLs` 对 URL 列表逐个串行 fetch（每个最多 30s 超时 + 10MB 体）。远程 sourceUrls 文档含上百个 URL 时，单个导入请求可串行挂起几十分钟以上；server 无 WriteTimeout，请求 ctx 只有客户端主动断开才取消。Legado 侧同样是串行 forEach，但作为交互式 App 有 UI 超时兜底；自托管 API 面对的是无人值守请求。
- 修改方案：至少加整体预算：在 `importSourceURLs` 入口用 `context.WithTimeout(ctx, importTotalTimeout)`（可配置，默认如 2 分钟）派生子 ctx 供每个 fetch 与递归使用，超时后剩余 URL 记 RemoteError("import deadline exceeded")。进一步可做有界并发（errgroup + semaphore），但需保持 result 汇总顺序。
- 验证方式：单测：httptest server 每个 URL 延迟 200ms、提供 50 个 URL，断言总耗时受预算约束且结果完整。

#### S15-08 runFromBook 中 FindByID 失败的失败事件 stage 恒记为 "info"

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/debug/flow.go:178-186
- 间接相关文件：internal/app/diagnostic/failure.go:114-135
- 问题描述：`runFromBook` 里 `f.sources.FindByID` 出错时 `publishFailure(ctx, session, "info", ...)`，即使本次是 `DebugModeTocURL`/`DebugModeContentURL` 模式（doInfo=false，尚未进入 info 阶段）。客户端按 stage 展示时会把"书源不存在"挂在 info 阶段，与 toc/content 模式语义不符；同时 `fallbackFailureCodeForStage("info")` 恒返回 FailureWebsiteFailed，丢失更精确的阶段归属。
- 修改方案：`runFromBook` 增加首阶段参数（如 `firstStage string`，取 doInfo/doToc/doContent 决定），FindByID 失败时用它发布；或单独以 "source" 阶段发布。
- 验证方式：单测：DebugModeContentURL + 不存在的 sourceID，断言失败事件 stage == "content"（或 "source"）而非 "info"。

### S16 CLI 与装配

审核范围：internal/cli/（root.go、context.go、download.go、fetch.go、book.go、import.go、source.go）、internal/bootstrap/bootstrap.go（search/check 并发调度归 S01/S03，未审）

#### S16-01 fetch 换源尝试与探测的章节缓存永久残留，SQLite 无限膨胀

- 级别：P2
- 类别：资源
- 直接相关文件：internal/cli/fetch.go:225-250（换源循环）、internal/cli/download.go:152-160（缓存写入路径）
- 间接相关文件：internal/app/content/service.go:104-121（GetChapterContentWith 写缓存）、internal/cli/book.go（book rm 只删书架）
- 问题描述：`reader fetch` 按排名最多尝试 maxSources 个源，每个未达标源的整个区间正文都经 `downloadChapters → GetChapterContentWith` 写入 chapter 正文缓存；被换掉的源、以及即使最终采纳的书（fetch 全程不写书架，`probeOne` 用 SkipSave、下载也不 Save 书架）的缓存行都以 `book.IDFromURL(bookURL)` 为键永久留在 SQLite，book rm 也清不掉（书根本不在 books 表）。多次 fetch 同名书会累积数百 MB 孤儿缓存。
- 修改方案：①fetch 下载阶段改用 SkipCache 路径（GetTransientContent 语义）或探测+下载共用临时 bookID，导出后删除该 bookID 的缓存行；②或提供 `reader cache prune`/在 bootstrap 启动时清理"无对应 books 行的正文缓存"。
- 验证方式：`sqlite3 data/reader.db "select count(*) from chapter_contents where book_id not in (select ...)"` 前后对比；两次 fetch 同名书观察 DB 体积。

#### S16-02 exportTXT 非原子写，写中途失败截断损坏已有导出文件

- 级别：P2
- 类别：资源
- 直接相关文件：internal/cli/download.go:334-345（exportTXT）
- 间接相关文件：internal/cli/fetch.go:265-273（调用点）
- 问题描述：`exportTXT` 直接 `os.Create(path)` 截断目标文件后顺序写出。写中途任何失败（磁盘满、SIGKILL、io 错误）都会留下被截断的残缺 TXT，且已覆盖用户原文件。download 对"信号中断"明确跳过导出以免残缺覆盖（download.go:108-111），但导出过程中的失败没有同样保护，自相矛盾。
- 修改方案：写临时文件（同目录 `path+".tmp"`）+ 成功后 `os.Rename` 原子替换；rename 前同步 `f.Sync()`。
- 验证方式：注入部分写失败（小 tmpfs 填满）执行 download --export，断言旧文件内容不变。

#### S16-03 --probe/--workers/--max-sources 只校验下限，无上限

- 级别：P3
- 类别：质量
- 直接相关文件：internal/cli/fetch.go:64-83
- 间接相关文件：internal/cli/fetch.go:310-341（probeCandidates）
- 问题描述：`--probe 10000` 会为每个候选各起一个 goroutine 同时执行 FetchInfo→FetchToc→正文三级请求（probeCandidates 并发数等于候选数），`--workers 100000` 同理；仅靠 fetcher 的 per-source concurrentRate 限速兜底，对慢源站点是自我 DOS，也放大内存。其他数值参数（--search-workers 等）同样只有下限校验。
- 修改方案：给各并发参数加合理上限（如 probe≤32、workers≤64），越界报用法错误退出码 1。
- 验证方式：`reader fetch x --probe 10000` 应报错退出 1。

#### S16-04 download --json 失败列表为 nil 时输出 null 而非 []

- 级别：P3
- 类别：质量
- 直接相关文件：internal/cli/download.go:285-292（MarshalIndent）
- 间接相关文件：internal/cli/fetch.go:410（同一 summary 结构）
- 问题描述：`summary.Failed []downloadFailureJSON` 无失败时为 nil slice，JSON 输出 `"failed": null` 而非 `"failed": []`，机器消费方需特判。
- 修改方案：marshal 前若 len==0 置为 `[]downloadFailureJSON{}`，或在 summary 构造时初始化空切片。
- 验证方式：全量成功的 download --json 输出 failed 为 []。

#### S16-05 下载重试 wave 中被取消的章节静默消失于 Failed 统计

- 级别：P3
- 类别：并发
- 直接相关文件：internal/cli/download.go:216-235（runWave workers drain 分支）、:239-243（retry 后汇总）
- 间接相关文件：internal/cli/fetch.go:255-260
- 问题描述：ctx 取消后 worker 对 queue 中剩余下标 `continue` 直接丢弃，不写入 failed map，也不写 result；retry wave 期间取消时这些章节既不在 Downloaded 也不在 Failed，summary 数字自相矛盾（Downloaded+Failed < 非卷章节数）。当前因 runDownload/fetch 在 ctx.Err()!=nil 时提前返回 2 不导出，故无错误退出码，但 --json 场景若未来在取消后仍输出统计即失真。
- 修改方案：取消 drain 分支改为 `failed[index] = "canceled"`（或单独 skipped-cancelled 计数），保证区间守恒。
- 验证方式：下载中途 SIGINT，断言 summary 各计数之和等于非卷章节数。

#### S16-06 root.go 对 ctx 的 AfterFunc(stop) 冗余注册

- 级别：P3
- 类别：质量
- 直接相关文件：internal/cli/root.go:157
- 间接相关文件：internal/cli/root.go:158（defer stop）
- 问题描述：`context.AfterFunc(ctx, stop)` 与 `defer stop()` 重复；ctx 取消时调用自身 stop 无任何效果，纯死代码。
- 修改方案：删除 root.go:157。
- 验证方式：go build 通过；信号行为不变。

#### S16-07 bootstrap 每次启动（含每条 CLI 子命令）跑 3 条全表扫描清理语句【历史问题 C-021 仍存在】

- 级别：P3
- 类别：性能
- 直接相关文件：internal/bootstrap/bootstrap.go:76-99（FailInterruptedTasks、FailInterruptedSessions、DeleteExpired）
- 间接相关文件：internal/infra/sqlite/repositories/check_repository.go、debug_repository.go、image_cache_repository.go
- 问题描述：docs/CliPerfReview-2026-10-17.md C-021 指出每条 CLI 命令启动都执行 3 条全表扫描；当前代码（:76-99）三处调用原样保留。任务规模大时（数十万已结束 check task/debug session/image cache 行）每条 `reader toc`/`reader source list` 都付出全表扫描延迟。
- 修改方案：清理语句仅在 serve 启动或定时执行；CLI 短命令按需惰性触发（如仅 download/fetch/check 相关子命令，或加时间间隔节流）。
- 验证方式：对大表 DB 计时 `reader source list` 启动延迟前后对比。

### S17 门禁/静态分析/测试质量

审核范围：全仓库（排除 legado/）、internal/domain/ 分层约束、scripts/verify.sh、Makefile、.github/workflows/ci.yml 与 docker-publish.yml、prerelease.yml、go.mod/go.sum、tools.go、generate.go、测试质量抽查（cmd/reader、internal/cli、internal/app、internal/infra、test/）

#### S17-01 三个直接依赖存在已确认可达的已知 CVE（含规则引擎与脚本运行时路径）

- 级别：P1
- 类别：安全
- 直接相关文件：go.mod（golang.org/x/net v0.50.0、golang.org/x/text v0.34.0、github.com/antchfx/xpath v1.3.3）
- 间接相关文件：internal/infra/rule/xpath/xpath.go:21,110、internal/infra/script/runtime.go:222、go.sum
- 问题描述：govulncheck（`./cmd/... ./internal/...`）报告 7 个可达漏洞：
  - GO-2026-5025/5027/5028/5029/5030（golang.org/x/net v0.50.0，修复版 v0.55.0）：HTML 解析对外来内容命名空间元素处理不当、等 5 项；可达路径 `internal/infra/rule/xpath/xpath.go:21:29: xpath.New calls htmlquery.Parse, which calls html.Parse`。书源抓取的任意远端 HTML 都会进入该解析器。
  - GO-2026-4526（antchfx/xpath v1.3.3，修复版 v1.3.6）：`Infinite loop`，路径 `xpath.go:110:35: xpath.selectNodes calls htmlquery.QueryAll → NodeIterator.MoveNext`。书源规则/页面内容可触发死循环，单用户服务被独占 CPU。
  - GO-2026-5970（x/text v0.34.0，修复版 v0.39.0）：`Infinite loop on invalid input`，路径 `runtime.go:222:21: script.Runtime.EvalWithBindings calls goja.Object.Export, which eventually calls norm.Form.Append`。脚本结果规范化可触发死循环。
  三者均为"处理不可信输入即触发"的死循环/解析缺陷，对一个抓取任意书源的自托管服务是实际可利用的 DoS。CI 无 govulncheck 门禁，这些漏洞不会被主动发现。
- 修改方案：`go get golang.org/x/net@v0.55.0 golang.org/x/text@v0.39.0 github.com/antchfx/xpath@v1.3.6 && go mod tidy`；在 ci.yml 增加 govulncheck 步骤（`govulncheck ./...`，需先修复 S17-02 的根 package 冲突才能用 `./...`）。
- 验证方式：修复后 `govulncheck ./...` 输出 "No vulnerabilities found"；`go test -race ./internal/infra/rule/... ./internal/infra/script/...` 通过。

#### S17-02 仓库根 package 可被单一 go 文件破坏，go vet ./... 与 govulncheck ./... 无法整仓运行（记录/证据项，不计入统计）

- 级别：P2
- 类别：质量
- 直接相关文件：generate.go:1（package reader）、tools.go（package reader）
- 间接相关文件：.github/workflows/ci.yml（Generate/Check generated API 步骤隐式依赖 go generate 成功）、scripts/verify.sh
- 问题描述：根目录仅有 generate.go/tools.go（package reader），没有任何包级约束防止他人在根放置其他 package 的 go 文件。当前审计期间根目录被未跟踪的 `bench_audit_test.go`（package script）污染，导致 `go vet ./...` 与 `govulncheck ./...` 直接失败（见命令摘要）。`generate.go` 是 `//go:generate` 载体，更稳妥的做法是移入有真实包的目录或加 `_ = x` 保活引用并约定根不放代码。审核后残留的未跟踪探测文件还有 `internal/infra/rule/zz_probe_s06_test.go`、`internal/infra/script/{bench,compile,leak}_audit_test.go`、`zz_s04_audit_test.go`（跨切片：其他审核切片产物，`compile_audit_test.go` 编译不过，会破坏任何人跑 `go test ./internal/infra/script`）。
- 修改方案：清理根目录非 package reader 的 go 文件；将 `//go:generate` 指令保留在根但确保根无其他 go 文件（或在 ci.yml 加 `test -z "$(ls *.go | grep -v -E 'generate.go|tools.go')"` 门禁）；删除各切片遗留的 zz_*/audit_test 探测文件。
- 验证方式：`go vet ./...` 与 `govulncheck ./...` 在仓库根直接通过；`git status --short` 无 zz_*/audit_test 文件。

#### S17-03 CI 未运行 govulncheck / staticcheck / 覆盖率统计，lint 类门禁长期缺席

- 级别：P2
- 类别：质量
- 直接相关文件：.github/workflows/ci.yml（全部步骤）、Makefile:1-16、scripts/verify.sh
- 间接相关文件：tools.go
- 问题描述：ci.yml 仅 Checkout→Setup Go→mod download→Generate→generated diff→`go test -race -count=1`→`go vet`。无 staticcheck/golangci-lint、无 govulncheck、无覆盖率门槛；历史审核（2026-10-15 基线门禁表"staticcheck / golangci-lint / deadcode：未安装，未运行"）指出该缺口后至今未补。已知漏洞（S17-01）因此只能靠人肉发现。另外 `main.go:main` 覆盖率 0%（`cmd/reader/main.go:9` 只是转发 internal/cli，可接受但无覆盖说明），internal/domain/rule 与 internal/infra/concurrency 两包 0% 覆盖且无测试（见 S17-05）。
- 修改方案：ci.yml 增加三个步骤：`go install honnef.co/go/tools/cmd/staticcheck@latest && staticcheck ./...`、`govulncheck ./...`、（可选）`go test -coverprofile` + 最低覆盖率断言（用 `go tool cover -func` + grep 阈值）；Makefile 与 verify.sh 同步加 target 保持三处一致。
- 验证方式：CI 在含已知漏洞/低覆盖率分支上变红；本地 `make verify` 含新 target。

#### S17-04 docker-publish.yml 发布前测试未开 -race，发布门禁弱于 CI

- 级别：P3
- 类别：质量
- 直接相关文件：.github/workflows/docker-publish.yml（test job：`run: go test ./...`）
- 间接相关文件：.github/workflows/ci.yml:33
- 问题描述：发布镜像前的 test job 用 `go test ./...`（无 `-race -count=1`），弱于 ci.yml 的门禁；历史上存在过 race（2026-10-15 记录 script Runtime、replace Processor 两处，现已修复，当前 -race 全量通过），发布链路退回弱门禁意味着 race 回归可能随镜像发布。
- 修改方案：docker-publish.yml test 步骤改为 `go test -race -count=1 ./...`。
- 验证方式：grep workflow 文件确认命令行含 `-race -count=1`。

#### S17-05 internal/domain/rule 与 internal/infra/concurrency 零测试零覆盖

- 级别：P3
- 类别：质量
- 直接相关文件：internal/domain/rule/rules.go:83-113（Flexible[T] UnmarshalJSON/MarshalJSON）、internal/domain/rule/analyzer.go、internal/infra/concurrency/（全部文件）
- 间接相关文件：internal/infra/rule/analyzer.go（rule 的实现方，间接覆盖 Flexible 行为）
- 问题描述：`go test -cover` 显示两包 0.0%。`Flexible[T].UnmarshalJSON`（rules.go:83）含字符串包裹规则（`data[0]=='"'` 时先解字符串再当 JSON 解）与 Raw/Value 双模态序列化逻辑，是书源导入正确性的关键路径，仅靠 infra 层间接测试；并发基础包完全无测试。属关键包覆盖率低（<60% 视为低，0% 为最低档）。
- 修改方案：为 `Flexible[T]` 补表驱动测试：null、字符串包裹 JSON、字符串包裹非法 JSON、裸 JSON 对象、Raw 回写（MarshalJSON 优先 Raw）、Value-only 序列化；concurrency 包按其 API（WorkerPool/Limiter 等）补并发正确性 + `-race` 测试。
- 验证方式：`go test -cover ./internal/domain/rule ./internal/infra/concurrency` 覆盖率 >80%。

#### S17-06 两处测试用固定 sleep 断言时序/资源回收，存在 flaky 风险

- 级别：P3
- 类别：质量
- 直接相关文件：internal/app/debug/runner_test.go:62（`time.Sleep(20ms)` 后断言"被取消的运行未把会话改成 failed"）、internal/app/search/single_source_test.go:982 与 internal/app/discovery/discovery_test.go:537（blockingFetcher 以 10ms sleep 探测最大并发，高负载下 -race 调度变慢可能低报 max）
- 间接相关文件：internal/infra/script/leak_audit_test.go:20-35（未跟踪跨切片文件，goroutine 计数 + sleep 启发式，天然 flaky；跨切片）
- 问题描述：runner_test.go:61-62 的 sleep 后单次读 repo 断言"终态未被改"——若取消回调尚未完成，可能读到中间状态导致偶发失败或偶发漏检（此处是漏检方向：run 尚未写 failed 就退出了 sleep 窗口，断言静默通过，属弱断言而非误报）。search/discovery 的 blockingFetcher 依赖 10ms sleep 让并发爬升到上限，CI 高负载 + `-race` 下 `maxConcurrent()` 可能读不到预期峰值（当前全量通过，但属时间敏感）。仓库主流做法已是 deadline 轮询（waitForSessionStatus、waitForCheckTaskStatus，runner_test.go:113-119 注释明确"避免固定 sleep 预算 flaky"），这两处是残留。
- 修改方案：runner_test.go 改为 deadline 轮询确认 runner 状态机已稳定（例如轮询直到 `runner.Cancel` 相关句柄清理或固定 deadline 内多次确认终态未变）；并发探测测试提高 sleep 到 50ms 或改为显式屏障（goroutine 先 barrier.Wait 再 Fetch 返回），使 max 与调度无关。
- 验证方式：`go test -race -count=10 ./internal/app/debug ./internal/app/search ./internal/app/discovery` 稳定通过。

#### S17-07 internal/api/handlers 承载可测试的业务逻辑（导出文件名/Content-Disposition 构造、导出临时文件编排），handler 层边界渗漏

- 级别：P3
- 类别：质量
- 直接相关文件：internal/api/handlers/source_handler.go:419-487（ExportBookTxt 编排 os.CreateTemp/Seek/Sync/ServeContent、exportFilename、exportContentDisposition）、:488-516（ProxyImage 直接 `w.Write(result.Body)`）
- 间接相关文件：internal/app/content（ExportTXT 所在层）、docs/ReviewIssues-2026-10-15.md:39（历史 ApiContractAudit 已列"handler 层含业务逻辑"，仍存在）
- 问题描述：历史问题仍存在。文件名清洗（exportFilename/exportContentDisposition，含 RFC 5987 filename* 编码）与导出临时文件生命周期编排属于应用层逻辑，落在 handler 包（927 行单文件），只能通过 HTTP 层测试覆盖，复用性差。属分层约束问题而非缺陷（编码行为正确）。
- 修改方案：将 exportFilename/exportContentDisposition 及导出临时文件编排（创建→ExportTXT→Sync→Seek→返回 reader+size）下沉到 internal/app/content（如 `contentapp.ExportToTemp`），handler 只做 HTTP 头与 ServeContent。
- 验证方式：新逻辑在 internal/app/content 有单测；`go test -cover ./internal/api/handlers` 中相关分支改为应用层覆盖。

#### S17-08 Makefile/scripts/verify.sh/ci.yml 三处门禁清单漂移

- 级别：P3
- 类别：质量
- 直接相关文件：Makefile:1-16（verify = generate test vet generated-check openapi-check security e2e）、scripts/verify.sh（仅 generate/test/vet/generated-check 四步）、.github/workflows/ci.yml（Generate/diff/test/vet，缺 openapi/security/e2e 显式 target，靠 `go test ./...` 隐式覆盖）
- 间接相关文件：docs/ReviewIssues-2026-10-15.md:37（历史记录"Makefile/scripts/verify.sh/ci.yml 无 cmd/server 残留"——残留已清，但清单不一致仍在）
- 问题描述：同一"验证门禁"有三份手工维护的清单且互不一致：verify.sh 没有 openapi-check/security/e2e 专项（其测试仍会被 `go test ./...` 覆盖，但单独失败时输出不聚焦）；ci.yml 不复用 Makefile target，逐步内联。三处任何一处加门禁（如 S17-03 的 staticcheck）都要同步三份文件，易漂移。
- 修改方案：ci.yml 统一改为 `make verify`（或 `run: make openapi-check security e2e` 补齐），verify.sh 逐行调用 Makefile target（`make -s generate test ...`）消除重复定义。
- 验证方式：grep 确认三处门禁命令同源；故意改坏 openapi 契约时三条路径均失败。

#### S17-09 两处测试文件不符合 gofmt

- 级别：P3
- 类别：质量
- 直接相关文件：internal/app/content/image_proxy_test.go:195、internal/app/content/service_test.go
- 间接相关文件：无 gofmt CI 步骤（ci.yml 无 fmt 检查，与 S17-03 同根因）
- 问题描述：`gofmt -l` 列出两个测试文件（方法签名对齐空格），无 CI 门禁拦截。
- 修改方案：`gofmt -w internal/app/content/*_test.go`；若做 S17-03，可在 CI 加 `test -z "$(gofmt -l cmd internal api test)"`。
- 验证方式：`gofmt -l cmd internal api test` 输出为空。

### S18 功能完整性对照基线

审核范围：docs/Requirement.md、docs/Design.md、docs/Tasks.md、api/openapi.yaml、api/examples/、internal/api/（generated/handlers/middleware）、internal/app/、internal/domain/、internal/infra/（config/sqlite/fetcher/script/rule/requestoption/logging/ratelimit）、internal/cli/、cmd/reader/、test/（e2e/openapi/security/fixtures）、Makefile、scripts/verify.sh、README.md、docs/API.md

#### S18-01 `GET /books/{bookId}/export.txt` 端点未在 docs/API.md 覆盖（文档与代码不一致）

- 级别：P3
- 类别：功能
- 直接相关文件：docs/API.md:1-60（端点总表无该行）
- 间接相关文件：api/openapi.yaml:725（`/books/{bookId}/export.txt` 已定义）、internal/api/generated/server.gen.go:1471（ExportBookTxt 已实现）、internal/app/content/service.go
- 问题描述：T-A17 声称"已新增 GET /books/{bookId}/export.txt"且 OpenAPI 与 handler 均已存在，但 docs/API.md 的端点总表（第 20-48 行）没有 export.txt 条目，文档滞后于契约。Requirement R-045A 要求该能力，代码已实现，仅文档缺行。
- 修改方案：在 docs/API.md 端点表补一行 `| GET | /books/{bookId}/export.txt | 全书 UTF-8 TXT 导出 |`。
- 验证方式：grep docs/API.md export.txt 命中。

#### S18-02 版本号硬编码三处不一致风险：`/version` 返回 0.1.0，发布说明为 0.0.1

- 级别：P3
- 类别：功能
- 直接相关文件：internal/api/handlers/system_handler.go:16（`VersionResponse{Version: "0.1.0"}`）
- 间接相关文件：docs/Release-0.0.1.md:1、README.md:24、api/openapi.yaml:12（`version: 0.1.0`）
- 问题描述：API 版本与 OpenAPI 版本一致（0.1.0），但发布说明/README 称首个发布为 0.0.1。版本经 ldvars/单一来源管理的设计（Design.md 未定义版本注入）缺失：`/version` 为编译期常量，与 git tag/发布流程无关联，发版时三处需要手工同步。
- 修改方案：用 `go build -ldflags "-X reader/internal/api/handlers.version=..."` 或 `runtime/debug.ReadBuildInfo` 注入版本，Makefile 与 docker-publish workflow 统一传 tag。
- 验证方式：构建后 curl /version 与 git tag 一致。

#### S18-03 `bookSourceType` 缺失时 OpenAPI 请求体校验与 Legado 语义冲突（导入路径一致、`POST /book-sources` 路径不一致）

- 级别：P3
- 类别：功能
- 直接相关文件：api/openapi.yaml:1524-1560（BookSourceSummary `bookSourceType` 必填、`BookSource` required 只列 enabledCookieJar/lastUpdateTime）
- 间接相关文件：internal/api/handlers/source_mapper.go:23（直接取 dto.BookSourceType）、internal/domain/source/book_source.go:27（int 零值即 0）
- 问题描述：`BookSourceSummary` 把 `bookSourceType` 声明为 required，但顶层 `BookSource` 的 required 只含 enabledCookieJar/lastUpdateTime；kin-openapi 实际校验 allOf 分支时 `bookSourceType` 为必填。Legado 导入允许省略 `bookSourceType`（GSON 默认 0）。经 `POST /book-sources`（走 generated.BookSource DTO）缺该字段时，`bookSourceFromDTO` 取 dto 零值仍能工作，但契约层面缺省值语义未声明（无 `default: 0`），客户端无法区分"省略=0"。Requirement R-001 验收标准未要求 bookSourceType 缺省行为，属契约完备性偏差，非功能错误。
- 修改方案：给 `bookSourceType` 加 `default: 0` 并从 BookSourceSummary.required 移到可选（或保留 required 但在 description 注明缺省按 0）。
- 验证方式：test/openapi 用省略 bookSourceType 的样例 VisitJSON 通过。

#### S18-04 openapi.yaml `BookSource.required` 与导入默认值文档矛盾（lastUpdateTime 必填 vs ApplyLegadoDefaults 容忍缺失）

- 级别：P3
- 类别：功能
- 直接相关文件：api/openapi.yaml:1536-1539（`required: [enabledCookieJar, lastUpdateTime]`）
- 间接相关文件：internal/domain/source/book_source.go:101-133（ApplyLegadoDefaults 对 enabled/enabledExplore/enabledCookieJar/respondTime 容忍缺失并套默认值）、internal/api/handlers/source_mapper.go:60-63
- 问题描述：Requirement R-001 验收标准规定"缺失 enabled/enabledExplore/enabledCookieJar/respondTime 时分别使用 true/true/true/180000"；实现确实做到了（ApplyLegadoDefaults 按字段存在性回填，含显式 0/false 保留）。但 `POST /book-sources` 契约把 `enabledCookieJar`、`lastUpdateTime` 声明为必填，而 OpenAPI 层面对 `enabled`/`respondTime` 却未要求——同一个对象两类字段必填性不对称：enabledCookieJar 在 Legado 里默认 true（可省略），契约却必填；lastUpdateTime 在 Legado 导入路径可为缺失。导致经 `/book-sources` 单建的书源必须显式携带 Legado 语义上可省略的字段，与"兼容 Legado JSON"表述有摩擦（导入端点 `/book-sources/import` 走 RawJSON 无此限制）。
- 修改方案：BookSource.required 只保留 `enabledCookieJar` 之一或都放开为可选，在 description 注明缺省值（true/服务器当前时间或 0）。
- 验证方式：test/openapi 增加最小 BookSource 样例（仅 bookSourceUrl/bookSourceName）通过 schema 校验。

#### S18-05 `check.progress` 事件类型在 OpenAPI/任务枚举中声明但生产代码从不产生（契约冗余项）

- 级别：P3
- 类别：功能
- 直接相关文件：api/openapi.yaml:2417-2427（CheckEvent.type 枚举含 `check.progress`）、internal/domain/task/check.go:46-51（枚举同）
- 间接相关文件：internal/worker/check/runner.go:105-150（只发 check.started/item_started/item_passed/item_failed/completed/cancelled）、internal/api/handlers/check_handler.go:150-157（`default: return generated.CheckProgress` 仅在未知 status 时合成）
- 问题描述：Requirement R-070 验收标准写明"运行中 SSE 使用 `check.progress`"。实现里 `check.progress` 只作为 `writeCheckEvent` 对未知任务状态的兜底合成事件，真正的运行中逐项进度全部走 `check.item_started/item_passed/item_failed`。契约枚举与实际事件流一致地可工作（客户端订阅 item_* 也能拿到进度），但 R-070 字面要求"运行中 SSE 使用 check.progress"未满足——运行中没有任何一条 type=check.progress 的事件。
- 修改方案：二选一：runner 在每个 item 完成后额外发一条 `check.progress`（携带单调 progress 字段），或从 openapi.yaml 枚举与 Requirement 验收标准中删除该类型并把 item_* 记为进度事件。
- 验证方式：运行 check SSE，抓到 type=check.progress 事件（或确认枚举已清理）。

#### S18-06 `docs/API.md` 未覆盖 `PATCH /book-sources/{sourceId}/explore-enabled` 之外仍缺 `GET /version`、`GET /openapi.yaml` 及 fetch 相关细节（历史问题 R-139 部分仍存在）

- 级别：P3
- 类别：功能
- 直接相关文件：docs/API.md:20-48（端点表）
- 间接相关文件：api/openapi.yaml:24-52（/version、/openapi.yaml）、docs/ReviewIssues-2026-10-15.md:2000（R-139）
- 问题描述：docs/API.md 端点表覆盖了 health、search、discovery、books、images/proxy、replace-rules、debug、check 等主要端点，但 `/version` 与 `/openapi.yaml` 两个已实现端点未列出。属文档漂移的残留（大部分 R-139 列举的端点如 login、reorder、refresh-info、export.txt 之外的 images/proxy 已补，export.txt 见 S18-01）。
- 修改方案：端点表补两行。
- 验证方式：对照 openapi.yaml paths 与 docs/API.md 表逐行 diff 为空。

#### S18-07 `POST /book-sources/{sourceId}/login` 提交空 values 的 501/404 语义与 R-035 验收标准部分偏差（脚本失败 502 依赖 ErrLoginCheckFailed 映射）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/api/handlers/login_handler.go:85-95（ErrUnsupportedLoginFlow→501 unsupported_login_flow、ErrLoginURLMissing→501 not_configured）
- 间接相关文件：internal/app/source/login.go:47-56、internal/app/source/script_integration.go:270-283、docs/Requirement.md R-035
- 问题描述：R-035 验收标准要求"表单值非法 400；脚本失败 502"。login_handler 对 `ErrUnsupportedLoginFlow`/`ErrLoginURLMissing` 映射 501 正确；脚本失败经 `ErrLoginCheckFailed`（script_integration.go:119 errors.Join(ErrLoginCheckFailed, …)）→ 需确认 handler 是否映射 502：login_handler.go 中对 ErrLoginCheckFailed 的映射存在（该分支在 85-95 行附近的 switch 里，编译与测试通过），但"表单值非法 400"无对应路径——LoginRequest.values 是 `map[string]string`，任何 JSON 对象都被接受，没有"非法"输入能在 400 层面出现（非对象会在 decodeJSON 拒绝，确实 400）。语义闭环成立，仅 R-035 描述的"501 unsupported_login_flow"错误码在 openapi.yaml 的 NotImplemented 示例写的是 "webView login is not supported"，与实际返回的 code `unsupported_login_flow` 示例不一致。
- 修改方案：openapi.yaml NotImplemented 示例 code 改为 `unsupported_login_flow`。
- 验证方式：test/openapi 校验响应示例与 handler 实际 code。

#### S18-08 openapi.yaml `ScriptFailed` 等响应组件定义了但没有任何端点引用（契约死代码）

- 级别：P3
- 类别：质量
- 直接相关文件：api/openapi.yaml:1360 之后（responses.ScriptFailed 定义）
- 间接相关文件：internal/api/handlers/login_handler.go
- 问题描述：`components/responses/ScriptFailed` 已定义（"The book source script failed while serving the request"），但 grep 全部 paths 无任何 `$ref: "#/components/responses/ScriptFailed"` 引用——含登录与内容端点。这与 Requirement R-022"JS 错误都有示例和测试"的契约表达脱节：实际 502 场景由 handler 的 code=script_failed 走 InternalError/BadRequest 结构返回，未在契约中声明。
- 修改方案：为可能触发脚本失败的端点（/book-sources/{id}/login、content 相关）追加 `502: $ref ScriptFailed`，或删除该未引用组件。
- 验证方式：`grep -c 'ScriptFailed' api/openapi.yaml` 引用数 ≥1 或定义被移除。

#### S18-09 GET /openapi.yaml 通过 goja（GetSwagger）+ yaml.Marshal 重新序列化，返回文档与 api/openapi.yaml 原文不等价（无 examples/description 字段级保真）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/api/handlers/system_handler.go:19-32
- 间接相关文件：internal/api/generated/server.gen.go（GetSwagger 嵌入的内联 spec）、api/openapi.yaml:39-52（契约声明下载 OpenAPI 文档）
- 问题描述：`GetOpenAPI` 用 oapi-codegen 嵌入的 `GetSwagger()` 再 `yaml.Marshal`。kin-openapi 模型往返会丢弃 OpenAPI 的 examples 顺序、部分 `description` 排版与未建模扩展字段（x-），返回的 YAML 与仓库中 api/openapi.yaml 不是同一文档。Requirement R-020 说"OpenAPI 是唯一真相源"，R-023 说明 openapi.yaml 端点"Download the OpenAPI document"——实现返回的是再模型化版本，`go generate` 的 diff 检查与 `GET /openapi.yaml` 的内容之间存在漂移空间。功能上仍可用（可被 SDK 解析），属保真度问题。验证：system_handler_test 通过，但无"返回体 == api/openapi.yaml"断言。
- 修改方案：改为 `//go:embed ../../api/openapi.yaml` 原文返回（Content-Type: application/yaml），并保留 schema 校验测试。
- 验证方式：curl /api/v1/openapi.yaml 与 api/openapi.yaml `diff` 为空。

#### S18-10 R-042"空发现分类返回明确错误"：`exploreKinds` 为空时返回错误已实现，但 `DiscoveryRequest.discoveryUrl` 直连模式未在 docs/API.md 说明可用性差异（文档级）

- 级别：P3
- 类别：功能
- 直接相关文件：docs/API.md:35（discovery 一行）
- 间接相关文件：api/openapi.yaml:2380-2395（DiscoveryRequest.discoveryUrl description）、internal/app/discovery/discovery.go:206
- 问题描述：discoveryUrl 直连模式已实现且契约有 description，docs/API.md 的 discovery 行未说明可选 `discoveryUrl`/`page` 参数。属文档颗粒度不足，功能无缺失。
- 修改方案：docs/API.md discovery 行注明 body 可带 discoveryUrl/page/sourceIds/group。
- 验证方式：文档 diff。

#### S18-11 `hasMore` 语义为"本页有命中"而非分页信号，契约 description 未声明（历史问题 C-005 部分残留：代码注释已改，契约未改）

- 级别：P3
- 类别：功能
- 直接相关文件：api/openapi.yaml:2396-2410（SearchResponse.hasMore 无 description）
- 间接相关文件：internal/app/search/multi_source.go:39-43（注释已声明近似语义）、docs/CliPerfReview-2026-10-17.md:591（C-005 标注"行为不变"）
- 问题描述：C-005 复核结论是把 HasMore 语义注释写清、行为不变。代码侧已注明"近似语义，可据此继续请求下一页"，但 openapi.yaml 的 `hasMore` 属性没有任何 description，客户端按标准分页语义消费会误判（多源搜索每页总数未知，空页才是终止信号）。
- 修改方案：openapi.yaml SearchResponse.hasMore 加 `description: 近似分页信号：本页有命中即 true；以空页作为终止条件`。
- 验证方式：test/openapi 校验通过。

#### S18-12 `POST /book-sources/{sourceId}/login` 空 values 的"只清除 loginInfo"行为与 Legado `loginUi` 有值书源上仍要求脚本（逻辑一致但 R-035 边界表述差异，确认无误，列为记录项）（记录/证据项，不计入统计）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/app/source/login.go:47-56、internal/app/source/script_integration.go:228-231
- 间接相关文件：docs/Requirement.md R-035
- 问题描述：R-035 边界说"提交空 values 等价于只清除 loginInfo"。实现：`len(values)==0` 时仅 removeLoginInfo，不执行脚本——与需求一致。但 `loginUi` 非空且 `loginUrl` 非脚本的书源（Legado 会打开 WebView 的场景）在有 values 时会走到"把非脚本 loginUrl 当脚本执行"（script_integration.go:236-240 `scriptText = strings.TrimSpace(src.LoginURL)`），这类书源的 loginUrl 通常是 URL 而非 JS，执行会得到 script_failed 而非 501 unsupported_login_flow（501 只在 loginUi 为空时返回，login.go:51-54）。边界未覆盖：loginUi 非空 + loginUrl 为纯 URL 的书源返回的是 502 script_failed 而非明确的 unsupported_login_flow。
- 修改方案：login.go 判定 `isScript` 时无论 loginUi 是否为空都检查：非脚本且非空 loginUrl → 若 loginUi 非空返回 501 unsupported_login_flow（WebView 流），脚本才继续。
- 验证方式：单测：loginUi 非空 + loginUrl=http URL 的书源 POST login → 501。

#### S18-13 openapi.yaml `ImportOptions.groupMode` 枚举与实现合法值集合需确认（未见枚举差异，列为无问题确认）（记录/证据项，不计入统计）

- 级别：P3
- 类别：功能
- 直接相关文件：api/openapi.yaml:1671-1690（ImportOptions）、internal/app/source/importer.go:315（`options.GroupMode == "append"`）
- 问题描述：抽查 importer 对 GroupMode 只区分 append/其他；若 openapi 枚举含 "new"/"replace" 等其他值，实现会静默按覆盖处理。openapi.yaml:1671 处枚举实际只声明了合法值（含 append），与实现匹配。此项经核对无差异，列出以证明已核对（不需要修改）。
- 修改方案：无需修改。
- 验证方式：grep 确认。

#### S18-14 `GET /book-sources` 关键词过滤 `bookSourceComment` 为 NULL 行时 LIKE 不命中（空 comment 书源无法按空关键词意图匹配，行为与 Legado 一致，记录项）（记录/证据项，不计入统计）

- 级别：P3
- 类别：功能
- 直接相关文件：internal/infra/sqlite/repositories/book_source_repository.go:357-360
- 问题描述：`book_source_comment LIKE ?` 对 NULL 行为 SQL 语义为 NULL（不命中），Keyword 非空时合理；Keyword 为空时整个子句不出现。与 R-016 验收一致。已核对无缺陷。
- 修改方案：无需修改。
- 验证方式：repository 测试覆盖。

#### S18-15 `internal/infra/script/runtime.go` 中 `SourceKey` policy 字段为共享 Runtime 留下的显式覆盖口（T-A01 修复残留的合法机制，非缺陷，记录项）（记录/证据项，不计入统计）

- 级别：P3
- 类别：质量
- 直接相关文件：internal/infra/script/runtime.go:80,259,2537
- 问题描述：T-A01 声称修复共享 Runtime 的 SourceKey 串扰，当前实现按 bindings/source 计算局部 sourceKey（runtime.go:2537 每个 evalContext 单独设置 req.SourceKey），bootstrap 未设置 policy.SourceKey（bootstrap.go:108-115），机制正确。列出以证明 T-A01 修复已核实。
- 修改方案：无需修改。
- 验证方式：-race 测试通过（internal/app/search、internal/infra/script 测试均 ok）。

## 附录 A：修复状态（2026-10-21）

修复已落地到工作树，尚未提交。门禁实测结果：`go build ./...`、`go vet ./...`、`gofmt -l` 均干净；`go test -race -count=1 ./...` 全部通过。未跑 2 万源真实网络压测。

| 状态 | 条目 |
|---|---|
| 不修复-误报 | S01-05、S05-01（实测：goja.New 2.4µs，池化重置 125-140µs，净负收益）、S06-05、S06-10、S06-16、S10-09 |
| 不修复-需决策 | S04-08（Cookie eTLD+1 共享为 Legado 语义）、S06-13、S07-08、S08-14（代理路径 DNS TOCTOU 无法在本地绑定） |
| 部分修复 | S04-09（AST 优先，Rhino 专属语法回落行级正则）、S06-06（顶层 `\|\|`/`%%` 仅对非选择器规则拆分） |
| 已修复 | 其余全部条目 |

二次复核完成范围：Search、Source/Debug、Replace/Discovery、Engines、Cookie、Fetcher、Book、Analyzer、Sqlite。
未完成二次复核（仅第一轮修复，靠全量测试兜底）：Check、Script、Infra、API、Content、CLI 六个切片。

补充（实测发现并已修复）：`reader check` 终态摘要因 FindTask 不再携带条目（S03-06 分页）统计恒为 0 且失败时 exit 0；现于摘要前经 `Manager.ListItems` 分页取回全部条目（internal/cli/check.go）。实测 5 源校验输出 `failed 5`。
