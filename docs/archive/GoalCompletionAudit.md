# 已归档：目标完成度审计

> 状态：已归档 / 已过期
>
> 归档日期：2026-05-05
>
> 过期原因：本文基于 2026-05-04 某一阶段的真实 E2E 快照撰写，文中的 `fullSuccessCount = 17` 等统计已被后续对齐修复和最新真实报告超越。
>
> 替代真相源：
> - `docs/LegadoAlignmentReport.md`
> - `test/reports/real_source_collection_e2e_report.json`
> - `test/reports/real_source_collection_e2e_report.md`
>
> 保留价值：保留当时对 objective 完成度的阶段性判断、证据口径和审计思路，便于回溯为什么某个时间点仍判定“任务 3 未完成”。
>
> 禁止用途：不得再将本文作为当前真实通过率、当前完成度或当前 release 判断的直接依据。

# 目标完成度审计

## 1. 审计对象

本审计直接对应当前线程目标：

1. 对比 `legado/` 原项目的 JS 解析、CSS 解析、请求链等和本项目已经实现的部分，逐个对照，生成完整报告。
2. 根据任务 1 的报告，全面逐个高质量修复对齐，严禁任何简化实现，需要以该文件作为任务级别记录。
3. 完整通过真实 E2E 测试，并使
   `https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361`
   中绝大多数书源都能正确通过全链路解析并达到生产可用级别。

## 2. 交付物检查表

### 2.1 任务 1：对照报告

要求：

- 必须存在面向 Legado vs Reader 的系统性对照报告。
- 报告必须覆盖 JS 解析、CSS/HTML 解析、请求链、变量链、真实 E2E 现状。
- 报告不能停留在早期快照，需要能反映当前已修复项和仍未完成项。

证据：

- 已存在 [LegadoAlignmentReport.md](/root/Coding/General/Reader/docs/LegadoAlignmentReport.md)。
- 报告已记录：
  - 事实来源文件
  - JS 运行时与绑定对齐情况
  - CSS/HTML 解析对齐情况
  - 请求链与 URL option 对齐情况
  - 变量链与源状态对齐情况
  - 当前高优先级缺口
  - 当前结论
- [progress.md](/root/Coding/General/Reader/docs/archive/progress-2026-05-03-to-2026-05-05.md) 与 [findings.md](/root/Coding/General/Reader/docs/archive/findings-2026-05-03-to-2026-05-05.md) 记录了后续每轮修复与真实 E2E 调查证据。

审计结论：

- 任务 1：**基本满足**。
- 保留说明：
  - 报告已具备任务级记录功能。
  - 后续仍可继续增补，但已经不是“缺报告”的问题。

### 2.2 任务 2：按报告持续高质量修复对齐

要求：

- 修复必须是 source-derived，而不是拍脑袋简化。
- 必须有测试或真实门禁证据支撑。
- 修复必须回写任务级记录。

证据：

- 本地测试门禁：
  - `go test ./...` 通过
- 真实门禁：
  - `READER_REAL_E2E_ENABLED=true READER_REAL_E2E_MAX_CANDIDATES=100 READER_REAL_E2E_CONTINUE_AFTER_SUCCESS=true READER_REAL_E2E_TIMEOUT_SECONDS=600 go test ./test/e2e -run TestRealSourceCollectionE2E -count=1 -v`
    通过
- 已有修复记录：
  - `java.getElements(rule)` 补齐
  - `source.loginUrl` / `setVariable` / `getVariable` / `putLoginHeader` 等源级状态补齐
  - 宽松 URL option / header JSON
  - `JavaImporter` / Hutool / UUID / Android Build 等兼容子集
  - shared cookie session
  - raw string token expand
  - chapter variable 传递到 content 前的请求展开
- 所有这些修复都已经记录在：
  - [LegadoAlignmentReport.md](/root/Coding/General/Reader/docs/LegadoAlignmentReport.md)
  - [progress.md](/root/Coding/General/Reader/docs/archive/progress-2026-05-03-to-2026-05-05.md)

审计结论：

- 任务 2：**持续推进中，且已有大量实质完成项**。
- 仍未完成的原因不是“没有按报告修”，而是剩余失败越来越集中在少量 source-specific 深层差异。

### 2.3 任务 3：绝大多数真实书源生产可用

要求：

- 必须完整通过真实 E2E。
- 必须让“绝大多数”书源可正确通过全链路。
- 不能只看门禁是否为绿，还要看成功覆盖比例。

证据：

- 真实 E2E 门禁通过。
- 最新真实报告：
  - [real_source_collection_e2e_report.md](/root/Coding/General/Reader/test/reports/real_source_collection_e2e_report.md)
  - [real_source_collection_e2e_report.json](/root/Coding/General/Reader/test/reports/real_source_collection_e2e_report.json)
- 报告关键指标：
  - `parsedCount = 459`
  - `attemptedCandidates = 100`
  - `fullSuccessCount = 17`
  - `search passed = 18 / failed = 82`
  - `info passed = 18 / failed = 0`
  - `toc passed = 17 / failed = 1`
  - `content passed = 17 / failed = 0`

审计结论：

- 任务 3：**未完成**。
- 理由：
  - 虽然真实门禁已通过，但这只证明“至少存在若干完整成功链路”，不能证明“绝大多数书源生产可用”。
  - 当前 `fullSuccessCount = 17 / attemptedCandidates = 100`，显然不满足“绝大多数”。

## 3. 当前剩余 blocker

当前剩余问题已不再主要是通用框架缺口，而是少量 source-specific 深层请求链差异与外部站点状态：

- `八零小说（优++）`
  - 该源此前的 `toc_empty` 已确认不是站点失效，而是 Reader 在 UTF-8 中文规则切分上的通用缺口。
  - 修复 `parser.Split()` 的 UTF-8 遍历后，该源现已完成 `search -> info -> toc -> content` 全链路并进入完整成功统计。

- `阅友小说（优+）`
  - 该源此前的 `toc_empty` 已确认不是站点失效，而是 Reader 未兼容 Legado 风格的前导 `@` 规则链，导致 `@onclick@js:...` 无法从搜索结果中正确提取详情页 URL。
  - 修复前导 `@` 规则修剪后，该源现已完成 `search -> info -> toc -> content` 全链路并进入完整成功统计。

- `米读小说（优+）`
  - 该源此前的详情 `405` 已确认不只是站点波动，还包含 Reader 在 `custom URL` 尾随 `##...` 时丢失 `method/body` 属性对象的通用缺口。
  - 修复 `url,{...}##...` 的属性对象截断后，该源现已完成 `search -> info -> toc -> content` 全链路并进入完整成功统计。

- `猫眼看书（优++）`
  - 该源当前剩余的 `toc_empty` 已经通过直连实测收敛为源级授权失效，而不是 Reader 仍缺少通用 header 或 UA 语义。
  - 同一详情 URL 在“无 header”“完整 app header 但无 Authorization”“完整 app header + 源头 Authorization”三组条件下，返回体分别为 `code=4004` 和两次 `code=4005 认证失败`。
  - 因此它当前不再适合作为通用框架对齐的优先目标。

- `轻说机翻（优+）`
  - 该源原先的搜索失败并非站点自身问题，而是两条 Reader 通用缺口叠加：
    - 全局注入的 `url` helper 会和真实书源脚本里的 `var url` 变量名冲突，导致脚本返回 helper 对象并报 `map[resolve:...]`
    - 搜索 URL 预处理会误把 JS 模板字符串里的 `${key}` 破坏成 `$斗破苍穹`
  - 修复后，该源真实失败已下沉为 `connection reset by peer`，说明 Reader 自身的通用 search URL 预处理问题已被清掉。

- `番茄小说（优+++）` / `番茄小说（优+）`
  - 这两条当前的 `Unexpected end of JSON input` 已通过对同一搜索 URL 的外部直连复核收敛：在当前 `X-Khronos/X-Gorgon/User-Agent/Cookie` 组合下，服务端直接返回 `HTTP 200 + Content-Length: 0` 空体。
  - 因此它们当前不再像 Reader 的 fetch/decompress/body 读取缺口，而更像规则侧签名链漂移、接口风控或外部服务返回策略变化。

- `download.yichnmedia.com` 那条 `猫眼看书（优++）`
  - 它原先在搜索阶段命中的多层 Reader 通用兼容问题，已经被逐步清掉：`data: type`、`bookSourceComment`、`java.timeFormatUTC` 等都已补齐。
  - 最新单源调试已从 `unsupported scheme` 等框架错误下沉为 `empty search result`，说明这条源当前不再是高优先级的 Reader 通用 blocker。

- `城堡小说（优+）`
  - 该源原先在搜索阶段命中的 `java.get(...).headers().get(...)` response API 兼容问题已被修复。
  - 在把默认脚本超时从 3 秒提升到 10 秒后，单源调试也已从 `script_timeout` 下沉到真实请求层的 404 / 空结果。
  - 因此它当前更像站点侧行为和规则链组合问题，而不是 Reader 仍缺少通用 runtime 能力。

- `阿巴小说（优+）`
  - 该源此前的 `content_empty` 已通过 `java.getString(rule)` 的 `result -> src` fallback 语义修复打通。
  - 核心收益是 `chapterList` 规则内的 JS 可以从根 TOC JSON 读取 `freeContUrlPrefix`，同时 `chapterUrl` 规则仍能从当前章节项读取 `contUrlSuffix`，不再退化成错误的 `ocean...chapterlist?...missing param` 接口。

- `米读小说（优+）`
  - 详情阶段 `405`
  - 单源调试表明：搜索结果映射出的 `bookUrl` 只是详情 endpoint，本身缺少后续必需的 POST/body 参数。
- `阅友小说（优++）`
  - 正文阶段 `404`
  - 调试表明：已经进入较深的签名/下载链路，问题更偏 source-specific。
- `阿巴小说（优+）`
  - 正文阶段 `content_empty`
  - 调试表明：命中了源级内容接口/参数问题，不像通用 content parser 缺口。
- 若干搜索阶段 `404 / 403 / 401 / timeout / TLS / DNS`
  - 更像目标站点状态、证书问题、源失效或反爬，而不是 Reader 框架本身。

## 4. 最终审计结论

### 已完成

- 任务 1：有完整、持续更新的对照报告。
- 任务 2：已完成大量高价值通用对齐，并有测试与真实 E2E 证据支撑。

### 未完成

- 任务 3：未达到“绝大多数书源生产可用”。

### 为什么不能标记目标完成

- objective 的第三条要求是结果性指标，不是“已有真实门禁”就能替代的。
- 当前证据只支持“真实集合中已有 17 条完整成功链路”，不支持“绝大多数书源可用”；同时剩余非搜索失败已经收敛到 1 条源级问题。
- 因此当前线程目标整体仍应视为 **未完成**。

## 5. 后续建议

如果继续推进，最合理的策略不是再做大面积泛化修复，而是：

1. 只挑剩余少数非搜索阶段失败源继续 source-derived triage。
2. 将搜索阶段明显外部站点类失败与框架缺口明确拆开统计。
3. 只有当 `fullSuccessCount` 和阶段通过率出现显著上升后，才重新评估任务 3 是否接近完成。
