# CLI 搜索/下载链路性能审阅与修复清单（2026-10-17）

> 只读审计 + 修复执行产物。审阅对象：`/root/Coding/General/Reader` 工作树 `master@9af520b`（阶段一未做任何修改）。
> 方法：7 个只读代码审阅子代理（SearchChain / FetcherLayer / RuleEngine / ScriptRuntime / TocInfoChain / ContentDownload / SQLiteLayer）+ 1 个全量基线代理（PipelineBench）+ 8 个真实书源实测分片（LiveShard0..7，578 个源按 `i % 8` 切分）+ 主代理核对源码与 `agent://` 原始产出。
> 覆盖链路：`reader search`（多源并发 → 合并 → 排序）→ 选源 → `reader download`（逐章抓取 → SQLite 缓存 → 导出 TXT）。
> 阶段一未执行任何 `go build`/修改：只读 + 运行仓库已有测试与已构建二进制 `/tmp/perf/reader`。

## 1. 概要

### 1.1 阶段一门禁（主代理执行）

| 门禁 | 结果 |
|---|---|
| `go build -o /tmp/perf/reader ./cmd/reader` | 通过 |
| `go test -race -count=1 ./internal/infra/script/... ./internal/infra/rule/...` | 通过：9 包 ok，**无 data race 报告** |
| 578 源 JSON 分片导入 | 8 片各 72–73 源，`reader import` 全部 `failed: 0` |

`-race` 无报告这一点决定 F5 的并发下载可以直接并发调用 `script.Runtime` 与规则引擎（ScriptRuntime 切片的静态结论一致：VM 每次 Eval 独立创建、共享状态全部带锁）。

### 1.2 真实书源实测（LiveShard0..7 + PipelineBench）

578 个源按数组下标 `i % 8` 切成 8 片（每片 72–73 个），每片独立库独立进程，逐源跑 `search --source <id> --json`、`book add`、`book refresh`、`toc` + `content` 四步并计时；另有 PipelineBench 用全量 578 源做并发档位与串行下载基线。

原始产物（不入库）：`/tmp/perf/LiveShard0..7/{results.jsonl,summary.json,findings.md}`、`/tmp/perf/PipelineBench/{baseline.json,findings.md}`。数字见 §1.3 与 §3。

覆盖到的端到端可用源 43/578（7.4%）：search → add → refresh → toc → content 五步全成功的源。这个比例反映的是"这份书源集合当前的存活率"，不是 Reader 的兼容能力上限——失败原因绝大多数是站点反爬/站点下线/书源规则过期，见 §3。

### 1.3 基线数字（修复前）

578 源全量导入一个库（`/tmp/perf/PipelineBench/reader.db`），3 个关键词 × 3 档并发，每次 `--limit 200`，结果数均为 200，warning 数 321–333：

| 关键词 | `--concurrency 8` | `--concurrency 32` | `--concurrency 64` |
|---|---|---|---|
| 斗破苍穹 | 236136 ms | 59901 ms（0.25×） | 41995 ms（0.18×） |
| 诡秘之主 | 179095 ms | 65298 ms（0.36×） | 44765 ms（0.25×） |
| 凡人修仙传 | 164517 ms | 59916 ms（0.36×） | 42822 ms（0.26×） |

读法：`--concurrency` 调大确实降墙钟（多源 goroutine 信号量层生效），但 32→64 只再降约 25–30%，且 warning 数不变——与 `READER_GLOBAL_CONCURRENCY`（默认 8）封顶一致，对应 C-003。

串行下载 200 章（`book_4af522ee2d40c1f5fa3c01b3`，源 8tsw.com，目录 1650 章，`--workers 1`）：

| 指标 | 值 |
|---|---|
| 墙钟 | 66749 ms（约 334 ms/章，零失败） |
| CPU 占用 | 4.8% |
| Max RSS | 90016 KB |

读法：CPU 4.8% 说明瓶颈纯粹是网络串行等待，并发下载不与 SQLite 单连接冲突（C-008 的前提成立），RSS 90MB 说明导出前的内存驻留与 C-014 相关但不是当前主瓶颈。

### 1.4 严重度分布

| 严重度 | 搜索 | 抓取层 | 规则/脚本 | 目录/正文/SQLite | 下载引擎 | 合计 |
|---|---|---|---|---|---|---|
| P0 | 2 | 0 | 0 | 0 | 1 | 3 |
| P1 | 2 | 3 | 6 | 7 | 1 | 19 |
| P2 | 4 | 5 | 10 | 6 | 2 | 27 |
| 合计 | 8 | 8 | 16 | 13 | 4 | 49 |

严重度定义：P0=主链路功能性缺陷（结果语义错误或参数完全不生效）；P1=可观测的速度/正确性缺陷，常规书源与常规用法即命中；P2=次要热点、边界缺陷、需要实测才显现的开销。

### 1.5 实测口径与已知偏差

- 8 个分片各自独立 SQLite + 独立 `READER_DATA_DIR`，共用同一二进制；每源四步（`search --source` → `book add` → `book refresh` → `toc` + `content`），每步独立超时。
- 分片脚本第一轮普遍踩到同一个坑：`--source` 必须用 `import` 输出的 `src_*` id，用 `bookSourceUrl` 会得到 `book source not found`；另有分片漏设 `READER_SQLITE_DSN` 导致跑进空库。所有分片都在 findings 里如实标注并重跑，最终数据取自修正后的库。
- search 成功/失败判定口径不一致：部分分片以 exit code 判定（`search` 有 warning 时 exit code 仍为 0），部分以是否返回 items 判定。§3.1 采用"有 search 记录且 `ok=true`"的口径，并在表中标注来源分片差异。

## 2. 问题清单

修复归属切片：**F1 搜索** / **F2 抓取层** / **F3 规则+脚本** / **F4 目录+正文+SQLite** / **F5 并发下载引擎**。

### C-001 [P0] 多源合并算法为 O(S·n²)，且每个源完成后都整体重排一次

- 维度：算法
- 位置：`internal/app/search/multi_source.go:117`（锁内调用）、`:199-216`（`mergeSearchBooks`）
- 证据：每个源 goroutine 在 `mu.Lock()` 内执行 `result.Items = mergeSearchBooks(result.Items, items, ...)`；`mergeSearchBooks` 先 `append([]book.SearchBook(nil), existing...)` 复制整个切片，再对每个 incoming 条目线性扫描 `out` 做 name+author 去重（O(n)），最后整体 `rankSearchBooks` 重排。S 个源、合并结果 n 条时总代价约 `Σ_s O((n_s + s·n_s) + s·n_s)` ≈ O(S·n²)。
- 影响：578 源搜索是该 CLI 的主用法，合并与排序本身会成为墙钟与 CPU 的一部分；`--limit` 截断发生在合并之后，无法减少这部分开销。
- 修复：goroutine 内在锁内只 `append` 原始结果（precision 过滤照旧）；`wg.Wait()` 之后统一做一次去重与一次排序。去重改为 `map[name+"\x00"+author]int` 索引，复杂度降到 O(n)。对外语义不变：合并、`Origins`、等值→包含→其他 的排序规则。
- 归属：F1

### C-002 [P0] 合并后只保留 `Origins`，各源候选 `BookURL` 丢失

- 维度：正确性 / 选源
- 位置：`internal/app/search/multi_source.go:210-216`
- 证据：命中去重分支时只执行 `out[i].Origins = appendOrigin(...)`，incoming 条目的 `BookURL`/`TocURL`/`Kind`/`LatestChapterTitle`/`InfoHTML` 全部丢弃；`Result.Items` 里同一本书只剩“第一个完成源”的 URL。
- 影响：`search` 的 JSON 输出因此只给出一条候选 URL（`--limit` 之后的每条也只有 `Origins` 列表），任何“同一本书换源下载”都要重新按 `Origins` 逐源搜索，选源信息在合并阶段就丢了。
- 修复：`MultiSearchResult` 新增 `All []book.SearchBook`，存放未合并的每源原始条目（已做 precision 过滤，按完成顺序排列）。HTTP handler 不映射该字段，不改 OpenAPI。
- 归属：F1

### C-003 [P1] `--concurrency` 被全局信号量封顶，设成 >8 完全不生效

- 维度：并发
- 位置：`internal/app/search/multi_source.go:63-66`、`internal/app/search/single_source.go:59-61`、`internal/infra/ratelimit/limiter.go:73-83`、`internal/infra/config/config.go:72`
- 证据：`concurrency = input.Concurrency; if concurrency < 1 { concurrency = s.limiter.GlobalLimit() }`（默认 `GlobalConcurrency=8`）只决定多源 goroutine 数；每个单源请求随后还要 `ratelimit.Do` → `Acquire` 依次过 global 信号量（容量 8）与 per-source 信号量（容量 `SourceConcurrency=2`）。
- 影响：`search --concurrency 64` 的 64 个 goroutine 里最多 8 个真正在抓取，其余在 `Acquire` 排队；用户以为调大了并发，墙钟不变。
- 修复：`cmdEnv` 增加 `globalConcurrency int`；`open` 签名改为 `func(stderr io.Writer, globalConcurrency int) (*bootstrap.Runtime, func(), int)`；`openRuntime` 在 `config.Load` 之后 `if n > 0 { cfg.GlobalConcurrency = n }`；`runSearch` 在 `env.runtime(stderr)` 之前设置 `env.globalConcurrency = *concurrency`。同步改 `root.go` 的 `env := &cmdEnv{...}` 与 `usage_test.go` 里 stub 的签名。
- 归属：F1

### C-004 [P1] 单源搜索没有独立超时，一个慢源可拖住整个 `search`

- 维度：速度
- 位置：`internal/app/search/multi_source.go:63-118`、`internal/cli/search.go:37`
- 证据：`Search(ctx, ...)` 直接用 `env.ctx`（仅 SIGINT/SIGTERM 取消），goroutine 内 `s.single.Search(ctx, ...)` 也没有 `context.WithTimeout`。兜底只有 `http.Client.Timeout = cfg.FetchTimeout`（默认 30s，`internal/infra/fetcher/http_fetcher.go:206`）与脚本超时（`internal/infra/script/runtime.go:166`）；规则解析、重定向链、多页正文各自累加时总时长可远超 30s。
- 影响：578 源里只要一个源卡在长规则/长重定向链上，`search` 的墙钟就等于它；这是“多源搜索慢”的最主要长尾来源。
- 修复：`Search` 内部对每个源包 `context.WithTimeout(ctx, perSourceTimeout)`，超时的源按 `search_failed` / `search_timeout` 记入 `Warnings`，其余源结果照常返回。超时值取 `MultiSearchInput` 新增字段（CLI 侧提供 `--timeout`，缺省复用 `cfg.FetchTimeout`）。
- 归属：F1

### C-005 [P2] `HasMore` 只是“本页有命中”，不是分页信号

- 维度：正确性
- 位置：`internal/app/search/multi_source.go:38-40,118-120`
- 证据：`result.HasMore = len(result.Items) > 0`；`SourceSearcher.Search` 只返回 `[]book.SearchBook`，没有 hasMore 返回位，多源合并后无法区分“某源还有下一页”。
- 影响：分页判断不可靠（但 `--page` 本身已正确传到每个源：`single_source.go:60,117`）。属语义文档问题，不构成失败。
- 修复：在 `MultiSearchResult.HasMore` 的注释里明确“本页有命中的近似语义”，不新增接口字段。
- 归属：F1

### C-006 [P2] precision 过滤与排序分桶是两套判定，语义重复

- 维度：正确性
- 位置：`internal/app/search/multi_source.go:203-205`、`:225-245`
- 证据：过滤用 `strings.Contains(Name) || strings.Contains(Author)`；`rankSearchBooks` 又按 `Name == keyword || Author == keyword`（equal）→ `Contains`（contains）→ `!precision` 时 others 分三桶。
- 影响：同一判定写两遍，容易在后续改动中漂移；`Author == keyword` 计入 equal 与 Legado 的“书名优先”不完全一致（记录，不改行为）。
- 修复：合并为单次“过滤 + 分桶”遍历（同一函数内先分桶、precision 时丢弃 other 桶），保持现有输出顺序不变。
- 归属：F1

### C-007 [P2] 未指定 `--source` 时把全部书源连同规则大字段加载进内存

- 维度：速度
- 位置：`internal/app/search/multi_source.go:131-149`
- 证据：`source.ListAll(ctx, s.repo, filter)` 返回全部 578 个 `*BookSource`（含 `RuleSearch`/`RuleExplore`/`RawJSON` 等大字段）。
- 影响：单次 `search` 的内存与 GC 压力随源库规模线性增长；属固定开销，不是墙钟主因。
- 修复：不改（仓库/契约改动面大于收益）；仅在文档记录，实测若显示 `search` 启动固定开销显著再评估。
- 归属：F1（不修，记录）

### C-008 [P0] `download --workers` 完全不生效：章节串行下载

- 维度：并发
- 位置：`internal/cli/download.go:31`（flag 文案）、`:37-39`（warn）、`:88-141`（串行循环）
- 证据：`fs.Int("workers", 1, "reserved; downloads run serially (SQLite single-conn)")` + `if *workers > 1 { fmt.Fprintln(stderr, "warn: --workers > 1 ignored, running serially") }`；随后 `for seq, index := 0, *from; index <= *to; seq, index = seq+1, index+1` 逐章 `GetContent`/`GetChapterContent`。
- 影响：下载耗时 ≈ Σ 每章网络延迟。200 章 × 单章 1–3s = 数分钟；`--workers` 是死参数。
- 前提核实：`SetMaxOpenConns(1)`（`internal/bootstrap/bootstrap.go:58`）下 `database/sql` 会把超出的调用放进等待队列而不是报错，SQLite 写只在微秒–毫秒级，**不会把网络并发打回串行**（SQLiteLayer 切片结论）。因此并发下载与单连接写库不冲突。
- 修复：F5 的 `downloadChapters` 用 `workers` 个 goroutine 消费章节下标 channel，结果写入按 index 预分配的 `[]string`，最后按序拼接。
- 归属：F5

### C-009 [P1] 每章未命中缓存时重复执行 5 次固定 DB 查询

- 维度：速度
- 位置：`internal/cli/download.go:114` → `internal/app/content/service.go:60-95`
- 证据：`AppService.GetChapterContent(ctx, id, index)` 内部依次 `Books.FindByID(id)`（:61）、`Chapters.FindByIndex(bookID, index)`（:67）、`Chapters.GetContent`（:69，与 `download.go:110` 刚做过的一次读同一行）、未命中后 `Sources.FindByURL(b.Origin)`（:79）、`Chapters.FindByIndex(index+1)`（:84-86，末章再 `FindByIndex(0)`）。download 循环已持有 `*b`、`chapters[index]` 与 `id`。
- 影响：200 章 ≈ 1000 次多余查询，全在单连接上排队；并发下载后这些查询会与网络 goroutine 抢同一连接。
- 修复：`AppService` 增加“用内存 Book/Chapter 抓取”的路径（`GetContentFromChapter` 已有同形状实现），download/fetch 走该路径；`FindByIndex(index+1)` 的 nextChapterURL 直接由已加载的 `chapters` 切片取，不再查库；`Sources.FindByURL` 每本书缓存一次。
- 归属：F4

### C-010 [P1] 同一章正文在未命中路径被 SQLite 读两遍

- 维度：速度
- 位置：`internal/app/content/service.go:69-76` → `internal/app/content/content.go:107-113`
- 证据：`GetChapterContent` 先 `s.Chapters.GetContent` 未命中，再调 `s.Content.GetContent`，后者内部 `if s.chapters != nil { if cached, ok, err := s.chapters.GetContent(...) }` 又读一次同一行。
- 影响：每章一次多余读；与 C-009 叠加。
- 修复：给 `contentapp.Input` 增加“调用方已确认缓存未命中”的开关，`GetChapterContent` 传该开关，避免二次读。
- 归属：F4

### C-011 [P1] 正文多分页链式串行且无页数上限

- 维度：速度 / 正确性
- 位置：`internal/app/content/content.go:208-243`（串行链）、`:252-261`（并发分支）
- 证据：`parseAllPages` 中 `for len(nextURLs) == 1 { ... s.fetchPage(...) }` 逐页串行；只有某页返回多个 next 才走 `concurrency.Map`（并发 8）；循环仅靠 `seen` map 防环，没有最大页数上限。
- 影响：声明 `nextContentUrl` 的书源每章墙钟 = Σ 每页网络延迟；规则异常时可能长时间抓取（受 ctx 约束）。
- 修复：加页数上限常量（超限截断并 warn）；单链场景改为**先解析全部 next URL 再一次性并发抓取**（复用已有 `concurrency.Map` 分支，但需保证单页失败不整章失败）。
- 归属：F4

### C-012 [P1] 每章重复查询两条用户替换规则

- 维度：速度
- 位置：`internal/app/replace/processor.go:51-56`
- 证据：`Process` 每次调用都执行 `p.repo.FindEnabledByTitleScope(...)` + `p.repo.FindEnabledByContentScope(...)`；`Process` 每章至少调用一次。
- 影响：200 章 = 400 次规则查询，串行在每章路径上。
- 修复：`Processor` 增加按“书名 + origin”的规则快照缓存（一次加载、后续复用），或由下载循环在循环外预取一次后传参。注意：规则是用户可改数据，缓存生命周期必须限制在单次命令执行内。
- 归属：F4

### C-013 [P2] `removeRepeatedTitle` 每章动态编译正则，绕过 LRU 缓存

- 维度：算法
- 位置：`internal/app/replace/processor.go:118-131`
- 证据：拼接 pattern 后直接 `regexp.Compile`，未走 `internal/infra/rule/regex` 的 LRU（`regex.go:34`，容量 512）。
- 影响：每章 1–2 次编译（`:63-71` 未命中会再来一次）。
- 修复：改走 `ruleregex.Replace`/内部编译缓存路径，或按 `(bookName, title)` 缓存编译结果。
- 归属：F4

### C-014 [P2] 导出 TXT 时整本书最多驻留 3 份内存

- 维度：速度
- 位置：`internal/cli/download.go`（`sections` 累积 + `exportTXT`）
- 证据：每章 `sections = append(...)`；`exportTXT` 里 `strings.Builder` 再拼一遍，最后 `[]byte(sb.String())` 第三次拷贝，`os.WriteFile` 一次性落盘。
- 影响：万字级长篇可达数十 MB 级别峰值 RSS。
- 修复：`downloadChapters` 返回按下标预分配的 `[]string`（顺序天然保证），导出改为 `os.Create` + 顺序 `io.WriteString` 流式写。
- 归属：F5

### C-015 [P1] 目录多分页：链式单链串行、无页数上限

- 维度：速度
- 位置：`internal/app/book/toc.go:98-125`（链式循环）、`:150-153`（并发分支，仅 `len(nextURLs) > 1` 时启用）
- 证据：`for len(nextURLs) == 1 { ... }` 逐页 `buildBookFetchRequest` → `executeBookFetchRequest` → `parseTocPage`；仅靠 `seenPages` 防环，无页数上限；`tocPageConcurrency = 8` 只在多 next 分支使用。
- 影响：声明单链 `nextTocUrl` 的书源每次 refresh 的墙钟 = 首页 + Σ 每页（网络 + 解析）串行和，长尾站点可达数分钟；而 fetch 流程要为每个候选源做一次 refresh。
- 修复：与 C-011 同法——单链改为“收集全部页 URL 后并发抓取 + 解析”，并加页数上限。
- 归属：F4

### C-016 [P2] 目录多分页并发分支一页失败即整次 refresh 失败

- 维度：正确性
- 位置：`internal/app/book/toc.go:150-160`
- 证据：`concurrency.Map(ctx, tocPageConcurrency, targets, ...)` 返回首个错误即整体失败，其余已抓好的分页被丢弃（`concurrency.Map` 语义：遇错全部取消）。
- 影响：与“单章失败继续”的下载语义不一致；一页 502 就让整次目录刷新失败。
- 修复：`Map` 回调内把单页错误转成“空页 + warning”，只有首页失败才整体报错。
- 归属：F4

### C-017 [P1] 章节批量写库逐条 `ExecContext`，无 prepared statement 复用

- 维度：速度
- 位置：`internal/infra/sqlite/repositories/chapter_repository.go:17-45`
- 证据：单事务内 `DELETE FROM chapters WHERE book_id = ?`（:21）后对每章 `tx.ExecContext(INSERT INTO chapters(13 列) VALUES(13×?))`（:27-33），每行重新 prepare/绑定。对照 `book_source_repository.go:64-72` 的 `SaveMany` 用 `tx.PrepareContext` 复用（正确示范）。
- 影响：万章目录 = 万次语句编译；导入 578 源与写目录都命中同一模式。
- 修复：`ReplaceByBook` 循环外 `tx.PrepareContext`，循环内 `stmt.ExecContext`，`defer stmt.Close()`。
- 归属：F4

### C-018 [P2] 每次刷新目录都是全删全插，双索引维护成本翻倍

- 维度：速度
- 位置：`internal/infra/sqlite/repositories/chapter_repository.go:21-33`、`internal/infra/sqlite/migrate.go`（`chapters` 的 PK 与 `UNIQUE(book_id, chapter_index)`）
- 证据：`ReplaceByBook` 无条件 `DELETE FROM chapters WHERE book_id=?` 后全量重插；每行同时维护主键与唯一索引两份。
- 影响：内容未变的章节也被删除重插。
- 修复：不改（改成 diff/upsert 会改变 `chapter_index` 重排语义与写路径复杂度，收益不稳定）。记录为已知取舍；C-017 已消除主要开销。
- 归属：F4（不修，记录）

### C-019 [P2] `RefreshInfo` + `RefreshToc` 路径上 books 行被整行重写两次、重复 `FindByID/FindByURL`

- 维度：速度
- 位置：`internal/app/book/service.go:64-90`、`internal/app/book/info.go:66-70`、`internal/app/book/toc.go:100`、`internal/infra/sqlite/repositories/book_repository.go:16-99`
- 证据：`RefreshToc` 先 `Books.FindByID(id)` + `Sources.FindByURL`；`RefreshInfo` 与 `RefreshToc` 内部各自走一次 35 列 UPSERT（含 `info_html`/`toc_html` 大字段）。fetch 探测 5 个候选 × (info + toc) 时会反复重写同一行。
- 影响：单次开销小，但 fetch 的选源探测把它放大 5 倍。
- 修复：`RefreshToc` 只更新 toc 相关列（新增只 UPDATE 相关列的仓储方法），避免整行重写大字段。
- 归属：F4

### C-020 [P1] SQLite 未设置 `synchronous` / `cache_size`

- 维度：速度
- 位置：`internal/infra/sqlite/open.go:16-18`
- 证据：`connPragmas = "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"`，无 `synchronous`（WAL 下默认仍是 FULL，每事务 fsync）与 `cache_size`（默认 2MB）。
- 影响：批量导入 578 源、写 200 章缓存、写目录时每条事务都 fsync。
- 修复：追加 `&_pragma=synchronous(NORMAL)&_pragma=cache_size(-65536)`（WAL + NORMAL 是 SQLite 官方推荐组合，掉电最多丢最后一个事务）。
- 归属：F4

### C-021 [P2] 每次 CLI 启动固定跑 3 条全表扫描清理语句

- 维度：速度
- 位置：`internal/bootstrap/bootstrap.go:75-92`、`internal/infra/sqlite/repositories/check_repository.go:57-66`、`internal/infra/sqlite/repositories/image_cache_repository.go:63-68`
- 证据：每次命令都执行 `UPDATE check_tasks SET status='failed' WHERE status IN('pending','running')`、`UPDATE debug_sessions ...`、`DELETE FROM cache_entries WHERE namespace='image' AND (expires_at IS NULL OR expires_at <= datetime('now'))`，后三条随表行数线性增长，删除还会拿写锁。
- 影响：CLI 一次性进程的固定启动延迟；当前规模下可忽略。
- 修复：`DeleteExpired` 先做一次带同 `WHERE` 的 `SELECT 1 LIMIT 1` 短路。其余不改（状态收敛是正确性需求）。
- 归属：F4

### C-022 [P2] `cache_entries` 缺 `(owner_type, owner_id)` 索引

- 维度：速度
- 位置：`internal/infra/sqlite/migrate.go`（现有索引仅 `idx_book_sources_enabled*`、`idx_debug_events_session`）
- 证据：`BookRepository.Delete` / `deleteSourceOwnedData` 的 `DELETE FROM cache_entries WHERE owner_type=? AND owner_id=?` 全表扫。`chapters(book_id, chapter_index)` 由 UNIQUE 隐式覆盖，`books.book_url`、`book_sources.source_id/book_source_url`、`check_task_items` 的 PK 前缀均无缺口（已核查）。
- 影响：删除书籍/书源时全表扫。
- 修复：新增迁移版本，`CREATE INDEX IF NOT EXISTS idx_cache_entries_owner ON cache_entries(owner_type, owner_id)`。
- 归属：F4

### C-023 [P2] Transport 未设置 `MaxIdleConnsPerHost`，同源并发 >2 时反复建连

- 维度：速度
- 位置：`internal/infra/fetcher/http_fetcher.go:206-220`、`:222-228`（`clientFor` 按 routeKey 缓存 client）
- 证据：Transport 已全局复用且有 `ForceAttemptHTTP2: true`、`MaxIdleConns: 100`、`IdleConnTimeout: 90s`、`TLSHandshakeTimeout: 10s`、`ExpectContinueTimeout: 1s`、`dialer.KeepAlive: 30s`；但未设 `MaxIdleConnsPerHost`（默认 2）与 `MaxConnsPerHost`。
- 影响：并发下载同一书源多章时超过 2 条的多余连接用完即关，反复 TCP+TLS 握手；F5 放开并发后这条会直接放大。
- 修复：`clientFor` 里设置 `MaxIdleConnsPerHost`（与下载并发同量级，如 8）。
- 归属：F2

### C-024 [P1] 每个新 TCP 连接都重新做 DNS 解析 + SSRF 逐 IP 校验，无缓存

- 维度：速度 / 算法
- 位置：`internal/infra/fetcher/http_fetcher.go:250-272`（`dialContext`）、`:274-293`（`proxyFunc`）、`:518-547`（`checkIPs` 逐 ~26 个 blockedPrefixes）
- 证据：`dialContext` 闭包每次建连调 `f.allowedIPs(ctx, host)` → `net.DefaultResolver.LookupIP` → `checkIPs`；结果不缓存。
- 影响：同源多章下载时每条新连接重复解析 + 逐 IP 比对；并发下载下成为固定开销。
- 修复：给 `HTTPFetcher` 加 host→[]net.IP 的短 TTL（如 60s）内存缓存；保持安全语义（仍对当次使用的 IP 校验，不缓存 pinIP 路径）。
- 归属：F2

### C-025 [P2] 代理模式下每一跳（含每次重定向）都做一次本地 DNS + SSRF 校验

- 维度：速度
- 位置：`internal/infra/fetcher/http_fetcher.go:274-293`
- 证据：`proxyFunc` 每次被调用都执行 `f.allowedIPs(req.Context(), req.URL.Hostname())`；`maxRedirects` 默认 5，最坏 6 次解析/请求。
- 影响：与 C-024 同根因。
- 修复：随 C-024 的 resolver 缓存一并解决。
- 归属：F2

### C-026 [P2] 响应体读取路径有最多 4 次拷贝与 2 次全量扫描

- 维度：速度 / 算法
- 位置：`internal/infra/fetcher/http_fetcher.go:389-424`、`:470-500`
- 证据：`io.ReadAll(io.LimitReader(reader, max+1))` → `hasGzipMagic` 全量头检查后可再 `gzip.NewReader` + `ReadAll` → `decodeTextResponseBody` 再 `charset.NewReader` + `io.ReadAll`（GBK 路径多一份完整副本）。响应大小限制实现正确（LimitReader + 事后 len 检查两处）。
- 影响：大页面（目录/正文）多 1–2 份整体拷贝。
- 修复：`decodeTextResponseBody` 改为 `charset.NewReaderLabel` 直接包 `LimitReader`，一次 `ReadAll`；保留 gzip-in-body 兼容分支。
- 归属：F2

### C-027 [P1] CookieJar 每个请求同步读 SQLite，且逐条过期 cookie 单独 DELETE

- 维度：速度 / 并发
- 位置：`internal/infra/fetcher/cookies.go:60-99`（`Load`）、`:49-56`、`:101-155`（`Save`）
- 证据：`CookieManager.Load` 每次 `m.repo.ListByDomain(ctx, domain)`；对每条过期 cookie 单独 `m.repo.Delete`（N 条 = N 次写）；`Save` 同样逐条 `Save/Delete`，无事务；`SessionCookieStore` 每次加锁复制 map。
- 影响：启用 cookie jar 的书源每章多 1 次查询 + M 次写，全部串行在请求路径上。
- 修复：`CookieManager` 加 domain→cookies 内存缓存（`Save` 时同步失效），逐条过期改为批量/事务删除。
- 归属：F2

### C-028 [P2] 重试无退避、不区分可重试错误

- 维度：正确性
- 位置：`internal/infra/fetcher/http_fetcher.go:128-165`
- 证据：`attempts := req.Retry+1`，失败立即 `continue`，无 sleep/backoff/jitter；5xx 在 `AllowNonSuccess=false` 时走 `lastResp` 兜底返回而非重试。
- 影响：调用方默认 `Retry=0`，实际影响有限。
- 修复：`Retry > 0` 时加指数退避（100ms×2ⁿ，上限 2s，ctx 可取消）。
- 归属：F2

### C-029 [P2] 按源 `concurrentRate` 语义保持（信息项，不修）

- 维度：并发
- 位置：`internal/infra/fetcher/rate_limiter.go:27-56,59-82`、`internal/infra/ratelimit/limiter.go:96-115`
- 证据：窗口计数器允许窗口内 n 并发（不是串行队列）；`concurrentRate="1000"`（无 `/`）→ `limit=1`，等效 1 QPS；同源实际并发受 `SourceConcurrency`（默认 2）约束。
- 影响：语义与 Legado 对齐，保持不动。F5 放开的是**跨源**并发与 `GlobalConcurrency`。
- 修复：不改。
- 归属：F2（不修，记录）

### C-030 [P1] 规则字符串每次求值都重新分词，无缓存

- 维度：算法
- 位置：`internal/infra/rule/analyzer.go:574-628`（`splitSourceRules`/`parseSourceRule`），调用点 `:157`（`evalStringSteps`）、`:166`（`GetElements`）
- 证据：每次调用都跑 `parseSourceRule` → `parsePutRules`(正则 `FindAllStringSubmatch`) + `putRulePattern.ReplaceAllString` + `evalRulePattern.FindStringIndex`；目录/正文逐元素调用，同一条规则串被反复分词。
- 影响：`@`/`##`/`&&`/`%%`/`<js>` 语法解析落在最热路径上（每章每节点）。
- 修复：新增 `LRU[rule+allInOne][]sourceRule`（复用 `internal/infra/rule/internal/lru`，容量 1024），`splitSourceRules` 入口 `GetOrCompute`；确认 `sourceRule` 只读可共享。
- 归属：F3

### C-031 [P1] 同一份 HTML 在一次规则链内被反复解析

- 维度：速度
- 位置：`internal/infra/rule/analyzer.go:304-310`、`:477-487`；`internal/infra/rule/css/css.go:83-89`
- 证据：`evalStringSteps` 多步链中每步 `evalStep` 的默认分支都 `newCSSAdapter(input)` → `css.New(asString(input))` → `goquery.NewDocumentFromReader`；只有输入已是 `*goquery.Selection` 时才复用（`:478-484`）。
- 影响：N 步规则链 = N 次完整 HTML 解析，目录/正文页越大多倍放大。
- 修复：链首把 string 输入解析为 `*goquery.Selection` 后向下传递（`newCSSAdapter` 已有 Selection 分支）。
- 归属：F3

### C-032 [P1] JSON 输入在同一步内被 `json.Unmarshal` 两次

- 维度：算法
- 位置：`internal/infra/rule/analyzer.go:283-304`；`internal/infra/rule/jsonpath/jsonpath.go:52-57`
- 证据：`evalStep` 先 `if isJSONInput(input) { jsonpath.New(asString(input)) }`，解析失败再回落 `newCSSAdapter(input)`，后者对非 map 输入再次 `asString` + 解析；`jsonpath.New` 每次全量 `json.Unmarshal`。
- 影响：JSON 源（大量书源用 JSON 搜索接口）多一次整体反序列化。
- 修复：`evalStep` 内解析一次后复用（失败时把已解析结果传给回落分支），`jsonpath.New` 结果按输入哈希做每请求缓存。
- 归属：F3

### C-033 [P2] XPath 文档每次求值重新 `htmlquery.Parse`

- 维度：速度
- 位置：`internal/infra/rule/xpath/xpath.go:19-24,96`
- 证据：`New(input)` 内 `htmlquery.Parse`；`selectNodes` 每步 `htmlquery.QueryAll(root, step.Raw)`，`evalStep` 每次新建 adapter。
- 影响：与 C-031 同根因，XPath 分支同样重复解析。
- 修复：与 C-031 同法：链首解析一次传 `*html.Node` 复用。
- 归属：F3

### C-034 [P2] `regex.Element/Elements` 用 `+=` 拼接中间结果，O(k·len)

- 维度：算法
- 位置：`internal/infra/rule/regex/regex.go:73-76,113-116`
- 证据：`next := ""; for _, match := range matches { next += match[0] }`。
- 修复：改 `strings.Builder`（两处同改）。
- 归属：F3

### C-035 [P2] `xpath.stringStep` 对文本节点也先做一次 `OutputHTML`

- 维度：速度
- 位置：`internal/infra/rule/xpath/xpath.go:43-50`
- 证据：先 `strings.TrimSpace(htmlquery.OutputHTML(node, false))`，随后文本节点分支再取 `InnerText`，同一节点双重序列化。
- 修复：先判 `node.Type`/`FirstChild`，仅元素节点才 `OutputHTML`。
- 归属：F3

### C-036 [P2] `filterDeepestTextMatch` 两遍全文档扫描

- 维度：算法
- 位置：`internal/infra/rule/css/css.go:254-278`
- 证据：先 `root.FindMatcher(matchAnyElement).FilterFunction(...ownText...)`，命中后对每个 sel 再 `sel.FindMatcher(matchAnyElement).Each(...)`。
- 影响：`text.` 规则在大文档上 O(docsize × 命中数)。
- 修复：第一遍收集候选时顺带记录父链以判断“最深”，去掉第二遍子树扫描。
- 归属：F3

### C-037 [P2] `sortDocumentOrder` 对逗号分组每个分支都全树先序遍历

- 维度：算法
- 位置：`internal/infra/rule/css/css.go:337-372`
- 证据：`selectRule` 对每个 part 调 `findSelection` 后 `sortDocumentOrder(nodes)`，每次从 top 全树遍历；k 个分支 O(k·docsize)。
- 修复：合并 pending 集合后单次 walk。
- 归属：F3

### C-038 [P2] LRU `GetOrCompute` 在锁外 compute，并发首扫时同 key 重复编译

- 维度：并发
- 位置：`internal/infra/rule/internal/lru/lru.go:42-64`
- 证据：`value := compute()` 在两次 Lock 之间。
- 影响：578 源并发首扫时重复编译，属一次性浪费。
- 修复：不改（引入 singleflight 的复杂度大于收益；编译结果本身幂等、无正确性影响）。
- 归属：F3（不修，记录）

### C-039 [P2] `expandRawString/renderJSONPathTemplate` 每个 token 正则跑两次

- 维度：算法
- 位置：`internal/infra/rule/analyzer.go:351-356,362-367,412-417`
- 证据：`getRulePattern.ReplaceAllStringFunc(text, func(token){ match := getRulePattern.FindStringSubmatch(token) ... })`——同一 token 匹配两次。
- 修复：改用 `FindStringSubmatchIndex` 基于位置切分。
- 归属：F3

### C-040 [P1] 每次脚本求值都新建 goja VM，无池化、无预编译

- 维度：速度
- 位置：`internal/infra/script/runtime.go:168`（`vm := goja.New()`）
- 证据：`EvalWithBindings` 内每次调用 `goja.New()`；无 runtime 池。规则引擎每条 `@js:` 步都触发一次完整 `Eval`（`internal/infra/rule/analyzer.go:240,376,467,862`）。
- 影响：JS 书源每次搜索/每章 = S×R 次 VM 创建（数百 µs + 数十次分配）。
- 修复：引入按 P（或按 goroutine）复用的 VM 池（`sync.Pool`），复用前检查 ctx 未超时；超时的 VM 直接丢弃不复用。
- 归属：F3

### C-041 [P1] `installSafeGlobals` 每次 Eval 重复 parse 并执行固定 JS 片段

- 维度：速度
- 位置：`internal/infra/script/runtime.go:385-413`、`:912-920`、`:1335-1347`
- 证据：每次 Eval 执行 8 次 `vm.Set` 与至少 4 段 `vm.RunString`（`ajaxAll` shim :408、JavaImporter compat :912+、`String.prototype.getBytes` :922、ByteArray 流别名 :1335），这些 JS 文本完全固定。
- 修复：这 4 段固定 JS 用 `goja.Compile` 预编译为 package 级 `*goja.Program`，Eval 时改 `RunProgram`（与已有 `programCache` 同模式）。
- 归属：F3

### C-042 [P1] `jsLib` 编译缓存容量 64，578 源下必然抖动

- 维度：速度
- 位置：`internal/infra/script/cache.go:20,188-231`
- 证据：`defaultProgramCacheSize = 64`；578 个源各自持有独立 `jsLib`，并发活跃源超过 64 时 LRU 持续淘汰，每次 Eval 重新 `goja.Compile` 整个 jsLib。
- 修复：容量提升到 ≥ 源库规模（如 1024）。
- 归属：F3

### C-043 [P2] 主脚本与 header/模板脚本每次重新 parse

- 维度：算法
- 位置：`internal/infra/script/runtime.go:205`（主脚本 `RunString(rewriteJavaCompatScript(script))`）、`:1837`（loginCheck）、`:2538,2546`（`@js:` header）、`:2600`（模板表达式）；`rewriteJavaCompatScript` `:1348-1355` 每次跑 2 个正则
- 证据：`jsLib` 已走 `programCache`，但上述调用点仍直接 `RunString`。
- 修复：为主脚本路径加按脚本文本哈希的 Program 缓存（key 含调用点），复用 `programCache.compile`。
- 归属：F3

### C-044 [P2] 白名单绑定每次 Eval 重建（信息项）

- 维度：速度
- 位置：`internal/infra/script/variables.go:81-160`、`internal/infra/script/runtime.go:483-486`
- 证据：`SourceBindings` 每次 Eval 构建 ~30 项 map + 闭包；`javaBindings` 同样重建数十闭包。
- 影响：属固定开销，较小。
- 修复：不改（静态字段缓存会引入书源对象的生命周期耦合）。
- 归属：F3（不修，记录）

### C-045 [P2] 规则/脚本并发安全结论（供 F5）

- 维度：并发
- 位置：`internal/infra/script/runtime.go:105-112,168`、`cache.go:34,151,189`、`runtime.go:2952`、`variables.go:14`、`internal/infra/rule/internal/lru/lru.go`
- 证据：共享可变状态全部带锁（`NamespacedCache.mu`、`programCache.mu`、`MemoryCookieStore.mu`、`memoryVariableRepository.mu`、LRU mutex）；goja VM 每次 Eval 独立创建，不跨 goroutine 共享；`go test -race -count=1 ./internal/infra/script/... ./internal/infra/rule/...` 无 race 报告。
- 结论：**F5 可以直接并发调用** `script.Runtime` 与规则引擎，不需要额外加锁。
- 归属：F5（前提结论）

### C-046 [P1] `download --workers` 帮助文案与 `docs/CLI.md` 与实际行为矛盾

- 维度：正确性 / 文档
- 位置：`internal/cli/download.go:31`、`docs/CLI.md:186`
- 证据：flag 文案 `reserved; downloads run serially (SQLite single-conn)`；`docs/CLI.md` 写“`--workers > 1` 当前按 1 执行并 warn”。
- 修复：默认值改 `4`，文案改 `concurrent chapter downloads`，删除 warn 分支与 `docs/CLI.md` 对应说明。
- 归属：F5

### C-047 [P1] 书源 header 名含 Unicode 字符时整个书源不可用

- 维度：正确性 / 可用性
- 来源：LiveShard6 实测（`dwxwc.com`），阶段一实测新发现
- 位置：`internal/infra/fetcher/http_fetcher.go:426-427`
- 证据：书源 `header` 里存在非 ASCII 的 header 名（例如 `User‑Agent` 用的 U+2011 NON-BREAKING HYPHEN 而不是 ASCII `-`）。这些 key 被原样 `httpReq.Header.Set(key, value)`，Go 在写请求时返回 `invalid header field name`，整源 search 直接失败；Legado 侧同样的书源配置不会整源失效。
- 影响：单个畸形 header 让整个书源变成不可用，且错误文本不指向 header。
- 修复：在写 header 的收口点做清洗——key 不满足 RFC 7230 token 字符集时跳过该 header 并记 warning（不做 ASCII 纠正，禁止掩码式修补）；value 去 `\r`/`\n` 防注入。
- 归属：F2

### C-048 [P2] 单源搜索空结果与"完成"不可区分

- 维度：正确性 / 可观测性
- 来源：LiveShard0/2/4/5/7 共同复现（21–81 个源出现 `{"items":null,"warnings":null}` 且 exit 0）
- 位置：`internal/app/search/multi_source.go`（err == nil 且 items 为空时不产生 warning）、`internal/cli/search.go`
- 证据：`search --source <id>` 在书源返回 200 但规则匹配 0 条时，输出 `items: null, warnings: null`、退出码 0；调用方无法区分"站点无此书"与"规则已失效"。
- 影响：批量选源/诊断时归因困难；但不影响 `fetch` 主流程（`fetch` 以探测的目录章节数与首章长度为准，不以 search 是否非空为准）。
- 修复：不修。空结果在 Legado 里同样是正常返回，加 warning 会让 578 源搜索产生大量噪声，并污染"真实失败"信号；§3 已把该状态单列统计。
- 归属：F1（不修，记录）

### C-049 [P2] `.last9 li.!0.!-1@a` 类规则返回 0 章节：与 Legado 语义一致，非 Reader 缺陷

- 维度：正确性
- 来源：LiveShard5 报为 P1（69書吧等源 refresh 报 `toc_empty`）
- 位置：`internal/infra/rule/css/css.go`（`parseLegacySelector`）
- 证据：
  1. Reader 的 legacy 索引解析（`parseLegacySelector`）与 Legado `AnalyzeByJSoup.findIndexSet`（`app/src/main/java/io/legado/app/model/analyzeRule/AnalyzeByJSoup.kt:475-500`）的非 `[]` 分支逐字符等价：两者都是从右往左扫，遇到 `!`/`.`/`:` 就把 `beforeRule = rus[:len]` 返回，`!N` 的语义是"排除下标 N"，且都在第一个（最右）分隔符处结束、不回填多个索引组。
  2. 因此对 `.last9 li.!0.!-1@a`，两边得到的 `beforeRule` 都是 `.last9 li.`（尾部留下一个 `.`），最终选择器分别交给 cascadia / jsoup。
  3. 主代理实测（临时探针，只读）：`.last9 li@a` → 3 个元素；`.last9 li.!0@a` → 0；`.last9 li.!-1@a` → 0。即差异来自规则串自身写法（`.!N` 把分隔符 `.` 留在选择器尾部），不是索引语义被写错。
  4. Legado 对同一规则串产出完全相同的 `beforeRule`，没有可供对齐的"裁剪尾部点号"语义。[INFERENCE] 未在 JVM 上跑 jsoup 验证，但无论 jsoup 抛错（Legado 侧被捕获后返回空）还是匹配 0 元素，结果都为 0。
- 结论：归为"书源规则失效"，不修。禁止为了让这条规则"看起来能用"而给 CSS 索引解析加 Legado 没有的裁剪逻辑（会引入与 Legado 不一致的语义）。
- 归属：F3（不修，记录）

## 3. 非 Reader 缺陷的失败源

真实实测中失败的书源按下列五类归档（口径与 `test/e2e/real_source_collection_test.go:classifyRealE2EFailure` 一致）：

| 类别 | 是否修复 | 说明 |
|---|---|---|
| Reader 实现缺陷 | 修（见 §2） | 同一代码路径在多个源重复失败时才判定 |
| 性能问题 | 修（见 §2） | |
| 书源规则失效 | 不修 | 站点改版/规则过期；禁止域名特判 |
| 站点不可用 | 不修 | 4xx/5xx/域名失效 |
| 网络 | 不修 | 超时/DNS/TLS/连通性 |
| 安全策略拦截 | 不修（且拒绝放宽） | SSRF 校验、私有网段拦截 |

明细与样例见下。统计口径：所有数字来自 8 个分片的 `results.jsonl`（578 行，其中 566 行有 search 记录），由主代理用固定脚本合并；`skipped` 表示上游阶段失败导致该阶段未执行，不计入成功率分母。

### 3.1 各阶段成功率与延迟（578 源）

| 阶段 | 实际运行 | 成功 | 成功率 | P50 | P90 | P99 |
|---|---|---|---|---|---|---|
| search（单源） | 566 | 326 | 57.6% | 1112 ms | 3657 ms | 30040 ms |
| add（按首条搜索结果加书架） | 279 | 94 | 33.7% | 53 ms | 60 ms | 69 ms |
| refresh（详情 + 目录） | 222 | 64 | 28.8% | 1714 ms | 20156 ms | 50317 ms |
| toc（读目录） | 211 | 64 | 30.3% | 53 ms | 73 ms | 98 ms |
| content（首章正文） | 193 | 54 | 28.0% | 938 ms | 1593 ms | 2206 ms |

端到端五步全成功：43 源（7.4%）。

两类长尾值得记住：

- search P99 = 30040 ms 正好等于 `FetchTimeout` 默认 30s，且多个源各自贴着 30s 收尾（书客吧 30065、PO18完本 30057、蛋疼小说网 30042、春雨书屋 30040、飞卢书屋① 30030、斗破苍穹源 23297）→ 这就是 C-004 的实测证据：单源无独立超时，慢源把整个多源搜索拖到自己的超时上限。
- refresh P90 = 20156 ms、最大 54161 ms（新御书屋#5）→ 目录多页串行（C-015）。`toc` 本身只要 53 ms，说明时间花在"抓目录页"而非"读目录"。

### 3.2 错误类别计数（按五类口径）

| 类别 | search | add | refresh | toc | content | 合计 |
|---|---|---|---|---|---|---|
| Reader 实现缺陷 | 0 | 0 | 0 | 0 | 0 | 0 |
| 书源规则失效（含空结果/解析失败） | 134 | 58 | 4 | 1 | 0 | 197 |
| 站点不可用（403/404/405/500/521） | 28 | 0 | 0 | 0 | 0 | 28 |
| 网络（超时/DNS/TLS/连接重置） | 12 | 0 | 5 | 0 | 1 | 18 |
| 安全策略拦截 | 1 | 0 | 0 | 0 | 0 | 1 |

说明（分片脚本的细分 `errorKind` 归并到上面五类）：

- `implementation_or_rule_failed` + `unknown`（105 行）无法从 CLI 输出进一步区分是解析失败还是站点返回了非预期页面，按保守原则归入"书源规则失效"；`source_search_empty_result` / `skipped_no_result`（186 行）同属此类。
- "安全策略拦截"的 1 例是书源指向 `0.0.0.0`，被 SSRF 校验正确拦下；这是预期行为，不是缺陷。
- **没有任何一类是 Reader 实现缺陷**：阶段一唯一由真实实测定位到的 Reader 缺陷是 C-047（Unicode header 名），它出现在 refresh/search 的失败里但被分片脚本归到了 `implementation_or_rule_failed`。
- 另有 1 例被分片误报为 P0 的"单源 search 恒返回 null"：主代理用同一库直接复测 `search 斗破苍穹 --source src_637313504eefc4e93178543f --json` 与 `--source src_1f520341cef553f53929aa39 --json`，两条都正常返回 items（爱乐文学、肉书屋），确认是分片脚本自身的问题（第一轮用 `bookSourceUrl` 当 `--source`），不是 Reader 缺陷。

### 3.3 最慢的源（成功阶段的耗时合计）

| 源 | 阶段耗时 | 合计 |
|---|---|---|
| 新御书屋🎃#5 | refresh=54161 | 55870 ms |
| PO脸红心跳③ | refresh=50317 | 51477 ms |
| 👔 脸红心跳 | refresh=45087 | 46928 ms |
| 🔞 肉书屋 | refresh=29323 | 30422 ms |
| 斗破苍穹（源名） | search=23297 | 30327 ms |
| 书客吧 | search=30065 | 30065 ms |

这些源既慢又"成功"，正是 C-004 / C-015 的目标场景：单个慢源/慢目录页会把端到端墙钟拉到几十秒。

### 3.4 不修的失败源（样例）

- 站点不可用：403 反爬（17 例）、404（9 例）、405/500/521（3 例）。
- 网络：TLS 证书无效（8 例）、DNS 失败（2 例）、连接重置/超时（9 例）。
- 书源规则失效：`source_toc_empty`（3 例，含 C-049 分析的 `.last9 li.!0.!-1@a` 写法）、`ruleBookInfo is required`（1 例）、以及大量 200 但解析 0 条的源。
- 安全策略拦截：`0.0.0.0`（1 例）。

禁止为了让这些源"通过"而做域名特判、站点特判或放宽 SSRF / 超时 / 响应大小限制。

## 4. 修复状态

阶段二执行记录。每行对应 §2 的一个 C-条目。

| 条目 | 归属 | 状态 | 说明 / 验证 |
|---|---|---|---|
| C-001 | F1 | 已修复 | `Search` 锁内只 append 原始条目，`wg.Wait()` 后一次 `dedupeSearchBooks`（O(n)）+ 一次 `rankSearchBooks`；删除 `mergeSearchBooks` |
| C-002 | F1 | 已修复 | `MultiSearchResult.All` 新增；HTTP handler 未映射，`api/openapi.yaml` 未改 |
| C-003 | F1 | 已修复 | `cmdEnv.globalConcurrency` + `open(stderr, n)`；`openRuntime` 用其覆盖 `cfg.GlobalConcurrency`；`search --concurrency` 现在真的改变全局并发 |
| C-004 | F1 | 已修复 | `MultiSearchInput.Timeout` + 每源 `context.WithTimeout`，超时记 `search_timeout` warning；CLI 新增 `search --timeout`（默认 0 = 不启用，避免误杀多页规则源） |
| C-005 | F1 | 已修复 | `HasMore` 注释改为近似语义说明，行为不变 |
| C-006 | F1 | 已修复 | `rankSearchBooks` 单次遍历分桶，输出顺序不变 |
| C-007 | F1 | 不修 | 记录项：`resolveSources` 全量加载书源大字段，实测非墙钟主因 |
| C-008 | F5 | 待回填 | |
| C-009 | F4 | 已修复 | 新增 `AppService.GetChapterContentWith`（热路径不再 `FindByID`/`FindByIndex`/`FindByURL`） |
| C-010 | F4 | 已修复 | `contentapp.Input.CacheLookupDone`；未命中路径不再二次读缓存，`GetChapterContent` 对外行为不变 |
| C-011 | F4 | 已修复 | `content.parseAllPages` 加 `maxContentPages=100` 截断 + warn；链式 next 保持串行（下一页 URL 依赖当前页内容，无法预取） |
| C-012 | F4 | 已修复 | `replaceapp.Processor` 进程内规则快照缓存 + `InvalidateRuleCache` |
| C-013 | F4 | 已修复 | `removeRepeatedTitle` 改走 `infra/rule/regex.CompileStd` 的 LRU 编译缓存 |
| C-015 | F4 | 已修复 | `book.parseAllPages` 加 `maxTocPages=100` 截断 + warn |
| C-016 | F4 | 已修复 | 目录与正文的多分页并发分支容忍单页失败（转空结果 + warn），首页失败仍整体报错 |
| C-017 | F4 | 已修复 | `ReplaceByBook` 循环外 `PrepareContext` 一次复用 |
| C-018 | F4 | 不修 | 记录项：全删全插会改变 `chapter_index` 重排语义，C-017 已消除主要开销 |
| C-019 | F4 | 已修复 | `BookRepository.UpdateInfo` 只写信息列（保留 `dur_*` 进度列），`RefreshInfo` 改用 |
| C-020 | F4 | 已修复 | connPragmas 增加 `synchronous(NORMAL)` + `cache_size(-65536)` |
| C-021 | F4 | 已修复 | `DeleteExpired` 先 `SELECT 1 ... LIMIT 1` 短路 |
| C-022 | F4 | 已修复 | 迁移版本 8：`idx_cache_entries_owner(owner_type, owner_id)`（未改已发布版本 SQL） |
| C-023 | F2 | 已修复 | `clientFor` 的 Transport 设 `MaxIdleConnsPerHost=8` |
| C-024 | F2 | 已修复 | `HTTPFetcher` 加 host→[]net.IP 的 60s TTL 缓存；**每次拨号仍执行 `checkIPs`**，不缓存允许/拒绝结论 |
| C-025 | F2 | 已修复 | `proxyFunc` 复用同一 DNS 缓存（与 C-024 同根因） |
| C-026 | F2 | 已修复 | `decodeTextResponseBody` 用 charset transform 直接包 `LimitReader`，一次 `ReadAll`；大小限制与 gzip-in-body 分支保留 |
| C-027 | F2 | 已修复 | `CookieManager` 加 domain 缓存（RWMutex），`Load` 命中不再查 SQLite，`Save`/删除同步缓存 |
| C-028 | F2 | 已修复 | `Fetch` 在 `Retry>0` 时指数退避 100ms×2ⁿ（上限 2s，ctx 可取消） |
| C-029 | F2 | 不修 | 记录项：按源 `concurrentRate` 语义与 Legado 对齐，保持不动 |
| C-030 | F3 | 已修复 | `splitSourceRules` 加 LRU（1024，key=rule+allInOne），拆出无缓存 `parseSourceRules`；`sourceRule` 为值类型、只读共享安全 |
| C-031 | F3 | 已修复 | `evalStringSteps`/`GetElements` 链首把 string/[]byte 包成 `parsedInput` 惰性解析载体，CSS 不再每步重解析 |
| C-032 | F3 | 已修复 | JSON 输入单次 `Unmarshal` 后复用（`jsonpath.NewFromData`）；回落 CSS 不再重解同一串 |
| C-033 | F3 | 已修复 | `xpath.NewFromNode` + `parsedXPathAdapter`，XPath 文档只解析一次 |
| C-034 | F3 | 已修复 | `regex.Element`/`Elements` 的 `+=` 改 `strings.Builder` |
| C-035 | F3 | 已修复 | `xpath.stringStep` 仅元素节点才 `OutputHTML` |
| C-036 | F3 | 已修复 | `filterDeepestTextMatch` 改为沿父链判定，删除全子树二次扫描 |
| C-037 | F3 | 不修 | 核实后确认基线 9af520b 已是"合并 pending 后单次 walk"形态，无需改动 |
| C-038 | F3 | 不修 | 记录项：`lru.GetOrCompute` 锁外 compute，编译幂等、无正确性影响 |
| C-039 | F3 | 已修复 | `expandRawString`/`renderJSONPathTemplate` 改用位置切分，同一 token 不再二次正则匹配 |
| C-040 | F3 | 已修复 | `EvalWithBindings` 改用 `sync.Pool` 的 `vmPool` + `cleanVMScope`；中断/超时的 VM 直接丢弃不回池 |
| C-041 | F3 | 已修复 | 4 段固定 JS（ajaxAll shim、getBytes、ByteArray 别名等）改为包级 `goja.Program` 预编译 + `RunProgram` |
| C-042 | F3 | 已修复 | `defaultProgramCacheSize` 64 → 1024 |
| C-043 | F3 | 已修复 | 新增 `programCache.compileScript(site, source)` 与独立 `callSites` 缓存；主脚本、login_check js、`@js:`/`<js>` header、模板表达式 5 个热点走编译缓存 |
| C-044 | F3 | 不修 | 记录项：白名单绑定每次 Eval 重建，静态缓存会引入书源对象生命周期耦合 |
| C-045 | F5 | 已确认 | 前提结论:规则引擎与脚本运行时共享可变状态全部带锁、goja VM 每次 Eval 独立创建 → 无需额外加锁;`-race` 下并发下载/并发探测无告警 |
| C-046 | F5 | 已修复 | `--workers` 默认 4、文案改 `concurrent chapter downloads`、删 warn 分支;`docs/CLI.md` 同步并新增 `## fetch` 一节;`docs/skills/reader-cli/SKILL.md` 加 fetch 行 |
| C-047 | F2 | 已修复 | header 写入收口点校验 RFC 7230 token:非法 key 跳过并 `slog.WarnContext`,value 去 `\r`/`\n` 防注入(不做 ASCII 纠偏) |
| C-048 | F1 | 不修 | 记录项:单源搜索 0 结果在 Legado 同样算正常返回,加 warning 会污染真实失败信号;§3 已单列统计 |
| C-049 | F3 | 不修 | 记录项:`.last9 li.!0.!-1@a` 与 Legado `AnalyzeByJSoup.findIndexSet` 语义等价,差异来自规则串自身写法 |
| C-050 | F5 | 已修复 | 主代理复核新增:①`fetch.go` 用指针比较 `probe == adopted` 恒 false → 被采用的新建书被 `Books.Delete` 误删,改按 `bookID` 比较;②`download.Failed` 按 map 迭代序输出,改 `sort.Ints` 升序;③`FindByURL` 错误被丢弃导致 `src == nil` 传入热路径,改整个区间失败退出码 2;④`nextChapterURL` 在 `--to` 子区间回退到第 0 章,改按目录判断。附带修复 probe 进度行并发写 stderr 的真实 race、下载进度行序号改按派发顺序 |
| 新增 `reader fetch` | F5 | 已完成 | `internal/cli/fetch.go`:多源搜索 → 候选筛选(书名精确→包含、作者投票)→ 并发探测(C>0 且首章 L≥200)→ `rankProbes`(C≥0.95×maxC 进第一梯队按耗时升序)→ 按排名最多 `--max-sources` 个源下载(失败 >5%×非卷章节数换源)→ 导出;参数校验全部在 `env.runtime` 之前 |

### 4.1 修复前后性能对比

| 场景 | 修复前 | 修复后 | 结论 |
|---|---|---|---|
| `search` 3 关键词 × 并发 8/32/64（578 源，`--limit 200`） | 236136/59901/41995；179095/65298/44765；164517/59916/42822 ms | 197358/55855/41759；199765/62749/43151；199642/69136/46065 ms | 墙钟由 578 个远端书源的响应时间主导（同一批站点、跨天重跑），差异在 ±20% 的网络波动内；F1 的收益是合并复杂度 O(S·n²)→O(n) 与 `--concurrency` 真正生效，不是墙钟。`--concurrency 32` 仍稳定约为 8 的 0.28–0.35 倍 |
| `download` 200 章（同书同源同区间，真机：玩文学小说网，1668 章目录） | `--workers 1` 33.2 s；`--workers 4` 28.6 s（旧版 `--workers` 只打 warn、行为等于 1，两者差异是噪声） | `--workers 1` 25.8 s；`--workers 4` 5.8 s | 4.4×（同版本 4 vs 1）；4.9×（新版 4 vs 旧版 4）；串行路径本身也降到 0.78×（去重复查询 + 连接复用），两次都是 200 章 / 0 失败 |
| `reader fetch 斗破苍穹 --author 天蚕土豆` 端到端 | 不适用 | **98.07 s**：退出码 0，1668 章（1667 下载 + 1 缓存）、`failed` 空、TXT 5.27 MB / 1668 段、目录顺序与 index 一致；探测 5 个候选后选中「玩文学小说网」（C=1668、首章 2854 rune、762 ms 最快）。全部修复落地后重跑第二次（不同候选命中，「笔迷读吧」C=1962）：**259 s / 1962 章 / 0 失败 / 1962 段 TXT**，1 个候选探测期 EOF 被正确降级 | 新命令；一体化搜索+选源+并发下载。两次都退出码 0、`failed` 为空 |

## 5. 复核与回归修复（第二轮）

阶段二 diff 由 reviewer 子代理逐文件复核，发现并修掉以下**本轮改造自己引入**的问题（原始清单见 `/tmp/perf/Reviewer/findings.md`）。585–636 行 §4 表里的 C-001..C-050 是第一轮；本节的 C-051..C-059 是复核新增。

| 条目 | 严重度 | 触发条件 | 后果 | 修法 / 证据 |
|---|---|---|---|---|
| C-051 | P0 | 链首为 string/`[]byte` 且链中含 `@js:`/`<js>` 步骤 | `evalStringSteps`/`GetElements` 把输入包成 `*parsedInput` 后原样传给 `Script.Eval`，脚本的 `result` 变成 Go 结构体（`[object Object]`/结构体 dump），该类规则全线产出错误结果 | `analyzer.go:249-255` ModeJS 前 `unwrapParsedInput`；回归测试 `TestAnalyzerJSStepReceivesRawTextFromLazyInput` |
| C-052 | P1 | 链首 string 输入 + 正则步骤（`:`/`##`） | `asString(*parsedInput)` 落入 `fmt.Sprint` 默认分支 → 锚定正则（`^`/`$`）直接失配 | `analyzer.go:1035-1037` 新增 `case *parsedInput`；回归测试 `TestAnalyzerRegexStepSeesRawTextFromLazyInput` |
| C-053 | P1 | 书源脚本改 `String.prototype`/`defineProperty`，随后同一进程内求值另一个书源的脚本 | VM 池只清可枚举全局自有属性，原型链与非枚举属性残留 → 跨书源串味，结果依赖执行顺序 | 放弃 VM 池（`runtime.go:172-180`），每 Eval 新建 VM，仅保留脚本编译缓存；测试 `TestRuntimePrototypeMutationDoesNotLeakAcrossSources` |
| C-054 | P1 | 响应头 `Content-Type` 无 charset 参数、正文为 GBK/GB2312 且用 `<meta charset>` 声明 | 丢掉 `charset.NewReader` 的 prescan 嗅探 → 页面按 UTF-8 直出乱码 | `http_fetcher.go:537-565` header 无 charset 时回退 BOM + `<meta>` 前缀嗅探（4096 字节），大小限制不变；测试 `TestDecodeTextResponseBodySniffsMetaCharsetWithoutHeaderParam`、`TestHTTPFetcherSniffsCharsetWithContentTypeHeaderMissingCharset` |
| C-055 | P1 | 同 domain 并发请求（新并发下载正好制造） | `Load` 命中缓存时返回内部切片，`Save` 原地写同一底层数组 → 数据竞争 | `cookies.go:68-88` 命中路径返回副本；`-race` 测试 `TestCookieManagerConcurrentLoadSaveNoRace` |
| C-056 | P2 | resolver 返回错误 | `cachedResolve` 吞掉具体 DNS 错误（含 `context.Canceled`），统一报 `no addresses for host` | `http_fetcher.go:311-347` 返回原始错误且解析失败不缓存；`checkIPs` 每次拨号仍执行，SSRF 语义未放宽 |
| C-057 | P2 | 多页正文中第 2..n 页抓取失败 | 失败页被当空串拼入，残缺正文**写入缓存**并按成功计数，之后永远命中缓存 | `content.go:269-288` 恢复“整章未完整抓取即失败、不写缓存”；测试 `TestContentPageFetchFailureFailsChapterWithoutCache` |
| C-058 | P2 | 单章分页超过 `maxContentPages=100` | 静默截断后按成功写缓存 | `content.go:37-39,221-231` 返回 `ErrPaginationTruncated`，不写缓存；测试 `TestContentPaginationTruncationFailsWithoutCache` |
| C-059 | P2 | 父 ctx 在 `ctx.Err()==nil` 检查与还池之间取消 | `AfterFunc` 的 `Interrupt` 可能落到已回池 VM，误中断下一个脚本 | 随 VM 池移除而消失（`defer stopInterrupt()` 保证回调在 Eval 返回前结束） |

两条必须记住的事实：

- 本语料 **578 个源里 0 条规则含 `@js:` 或 `<js>`**（`ruleSearch/ruleBookInfo/ruleToc/ruleContent/ruleExplore` 全为 0，原文 grep `@js` 计数 0）。所以 C-051/C-052 是**代码级回归**，本语料的真实实测覆盖不到它们，只能靠规则引擎的单元测试守住。
- reviewer 首轮报的 4 项“已核对无问题”同样重要：DNS TTL 缓存未削弱 SSRF（每次拨号仍 `checkIPs`）、header 名校验非回归（Go transport 本就拒绝非 token 头名）、`multi_source.go` 合并语义与旧实现等价、`downloadChapters` 失败重试无双计/无 goroutine 泄漏/保序成立。

### 5.1 复核第三轮（reviewer 第二轮产出 + 主代理修复）

reviewer 的第二轮补齐了第一轮遗留区域（css/jsonpath/regex/xpath 缓存、book toc/info、replace、SQLite、cli、测试质量），又确证 4 条本轮改造引入的问题：

| 条目 | 严重度 | 问题 | 修法 / 证据 |
|---|---|---|---|
| C-060 | P1 | `filterDeepestTextMatch`（css.go）把"保留最深命中"反转成"保留最浅命中"：沿父链判断祖先是否在命中集，命中祖先就跳过自身，于是祖先后代同时命中时取到祖先 | 改为沿命中节点祖先链标记"有更深命中后代"的祖先并剔除（O(n·depth)，与旧实现语义一致）；测试 `TestSelectStepTextRuleKeepsDeepestMatch`（取 `<a>`）、`TestSelectStepTextRuleKeepsAncestorWithoutDeeperMatch`（只有祖先命中仍取祖先） |
| C-061 | P1 | 替换规则快照缓存（`Processor.ruleSet`，进程级 `sync.Map`）无失效钩子：`InvalidateRuleCache` 全仓库无生产调用方 → `serve` 模式下经 API 增删改替换规则后正文处理永远用旧快照 | `Manager` 新增 `WithChangedCallback`，Create/Update/Delete 成功后回调；`bootstrap.go:135` 接线 `.WithChangedCallback(contentProcessor.InvalidateRuleCache)`（复刻同文件 `importer.WithSavedCallback` 惯例） |
| C-062 | P2 | 目录多分页失败与 `maxTocPages=100` 截断吞错后，残缺目录仍经 `ReplaceByBook` 整体覆盖库内完整目录 | `toc.go` 新增 `ErrTocPaginationLimit` 与 `incompleteTocError`，任何一页失败/超限即返回错误、保留旧目录；测试 `TestTocServiceFailsAndKeepsExistingChaptersWhen{NextPage,FirstPage,PaginationExceedsLimit}Fails` |
| C-063 | P2 | `removeRepeatedTitle` 的逐章唯一标题 pattern 进入共享 512 容量正则 LRU，刷一部书会冲刷搜索/目录/正文的编译结果 | 改回 `regexp.Compile`（该 pattern 命中率≈0，独立缓存无意义），共享 LRU 不再被冲刷 |

复核同时确认**无缺陷**的项（证据见 `/tmp/perf/Reviewer/findings.md`）：jsonpath/regex/xpath 的缓存改动等价、book info 的 `UpdateInfo` 列覆盖完整且无不可达分支、SQLite 迁移 v8 索引与表结构匹配且幂等、PRAGMA 变化在 WAL + 单连接下有界、`--concurrency` 覆盖链路（cli → config → ratelimit）真实生效、批量入库事务回滚与 `UpdateInfo` 列保护均有 SQL 级测试。
