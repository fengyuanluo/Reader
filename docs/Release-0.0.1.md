# Reader 0.0.1 发布说明

## 版本定位

`0.0.1` 是 Reader 的第一轮移植重构交付版本。

该版本表示：

- Reader 已具备可运行的 Legado 在线书源服务端骨架。
- OpenAPI-first、SQLite、受限 goja、调试/校验、替换净化、书架/进度、图片代理、mock/real E2E 门禁已经落地。
- 当前版本仍不是“与 Legado 全面对齐的稳定生产版”，而是首轮可审计交付版本。

## 已实现能力

- 兼容 Legado 书源 JSON 核心字段和在线规则主链路语义。
- 提供书源导入、查询、管理、启用、发现启用和重排 API。
- 提供搜索、发现、详情、目录、正文、书架、进度、替换规则、调试和批量校验 API。
- 提供 Bearer Token 鉴权、SSRF 防护、JS 超时与白名单绑定、日志脱敏、SQLite 持久化。
- 提供 mock E2E、安全测试、真实远程集合 gate、CI 和本地 `verify` 门禁。

## 明确边界

- 不兼容 Legado 旧 Web API 路由。
- 不实现 Android 客户端 UI、Content Provider、RSS、本地 TXT/EPUB、TTS、云同步、多用户。
- 不实现完整浏览器 WebView 渲染抓取；依赖浏览器流或 `webView` option 的书源会显式返回不支持错误。

## 发布验收快照

当前仓库以以下门禁作为 `0.0.1` 验收依据：

- `go generate ./...`
- `go test ./... -count=1`
- `go vet ./...`
- `git diff --exit-code internal/api/generated`

真实远程集合 gate 最新快照：

- 报告文件：
  - `test/reports/real_source_collection_e2e_report.json`
  - `test/reports/real_source_collection_e2e_report.md`
- 集合 URL：
  - `https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361`
- 报告时间：
  - `2026-05-05T07:20:28Z`
- 关键结果：
  - `Parsed count = 459`
  - `Attempted candidates = 100`
  - `Full success count = 51`
  - `Release allowed = true`

## 文档分层

- 当前真相源：
  - `docs/Requirement.md`
  - `docs/Design.md`
  - `docs/Tasks.md`
  - `docs/LegadoAlignmentReport.md`
  - `test/reports/real_source_collection_e2e_report.*`

- 历史归档：
  - `docs/archive/`

首轮实现阶段的 `task_plan`、`findings`、`progress` 已归档，不再保留在仓库根目录。
