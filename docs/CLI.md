# Reader CLI

`reader` 是单二进制 CLI：`serve` 子命令承接现有 HTTP 服务，其余子命令直连服务层 + SQLite，不需要 server 进程、不需要 token、不产生 HTTP 依赖。面向 Agent 自动化：全部子命令非交互，默认 tab 分隔行式输出，`--json` 输出机读 JSON。

## 运行方式

```bash
go run ./cmd/reader <command> [args]      # 开发
go build -o reader ./cmd/reader && ./reader <command> [args]
```

CLI 直连 `READER_SQLITE_DSN`（默认 `data/reader.db`）。`READER_TOKEN` 仅 `serve` 使用；CLI 子命令不读取 token，production 环境无 token 也不阻断。

## 帮助

- `reader --help` / `reader -h` / `reader help`：命令总览输出到 stdout，退出码 0。
- `reader <command> --help`（含 `serve`、`book add` 等）：列出该命令的 flag，统一双横线写法 `--name <type>`，退出码 0。
- `reader book --help`、`reader source --help`：列出可用子命令；未知子命令输出 error 加子命令列表，退出码 1。
- flag 解析失败（如未定义的 flag）：stderr 输出 `error: ...` 与该命令的 flag 列表，退出码 1。

## 退出码（所有子命令统一）

| 退出码 | 含义 |
|---|---|
| 0 | 成功（import 存在逐条 failed 项仍为 0，逐条状态已表达） |
| 1 | 用法/参数错误：未知命令、缺参数、flag 解析失败、取值越界 |
| 2 | 运行失败：config/db/网络/规则/下载有章节失败/目录为空/被 Ctrl-C 中断 |

错误输出 stderr `error: <msg>`；警告输出 `warn: <msg>`；数据输出 stdout。

用法错误在打开数据库之前判定：参数错误不会创建 `data/` 目录、数据库文件或执行迁移。

Ctrl-C / SIGTERM 会取消进行中的请求；第二次 Ctrl-C 直接终止进程。

## 环境变量

与 serve 共用同一套 `READER_*` 配置（见 `internal/infra/config/config.go`）。常用：

- `READER_SQLITE_DSN`：SQLite 路径（默认 `data/reader.db`）。
- `READER_ADDR`：serve 监听地址，默认 `127.0.0.1:8080`（`--addr` 可覆盖）。
- `READER_ALLOW_PRIVATE_NETWORK`：抓取内网地址默认禁止；本地 mock 调试需设 `true`。

## serve

```bash
reader serve [--addr <addr>]
```

启动 HTTP API 服务（路由 `/api/v1`，Bearer Token 鉴权，scheme 大小写不敏感）。

- 默认监听 `127.0.0.1:8080`，只接受本机连接。
- `READER_TOKEN` 为空时只允许监听回环地址（`127.0.0.0/8`、`::1`、`localhost`），此时不做鉴权；空 token 监听 `0.0.0.0`、`:8080`、局域网 IP 等非回环地址会拒绝启动（`error: READER_TOKEN is required ...`，退出码 2），所有环境都一样。
- production 环境无论监听哪里都必须设置 `READER_TOKEN`。
- 超时：读请求头 5s、读完整请求（含 body）60s、keep-alive 空闲 120s；不设全局写超时，保证 debug/check SSE 长连接不被截断。
- Ctrl-C / SIGTERM 优雅关闭（最长 5s）。
- 结构化 JSON 日志输出到 **stderr**（旧 `cmd/server` 输出到 stdout）。收集日志改用 `reader serve 2>app.log`；`1>app.log` 只会得到空文件。

## import

```bash
reader import <path|URL|-> [--keep-name] [--keep-group] [--keep-enabled]
              [--group <name>] [--group-mode append|replace] [--update <policy>] [--json]
```

- 参数：本地 JSON 文件路径、`http(s)://` 收集链接，或 `-`（stdin）。
- `--group-mode` 默认 `append`；非法值退出码 1。
- `--update` 是已存在同 URL 书源时的更新策略，非法值（区分大小写）退出码 1：
  - `lastUpdateTime`（默认）：导入项 `lastUpdateTime` 比库中新才覆盖，否则 skipped。
  - `always`：总是覆盖。
  - `skipExisting`：已存在即 skipped。
- 文件、stdin、远程内容开头带 UTF-8 BOM（Windows 导出常见）时自动去除。
- 远程 URL 拉取失败记入 `remoteErrors`（warn 行），不阻断其余条目。

文本输出（每条一行 + 末行汇总）：

```
added	src_xxx	笔趣阁
summary: added=1 updated=0 skipped=0 failed=0
```

JSON 输出：

```json
{
  "added": 1, "updated": 0, "skipped": 0, "failed": 0,
  "items": [
    {"sourceId": "src_xxx", "bookSourceUrl": "https://...", "bookSourceName": "笔趣阁", "status": "added", "error": ""}
  ],
  "remoteErrors": [{"url": "https://...", "error": "..."}]
}
```

## source

```bash
reader source list [--keyword <kw>] [--group <g>] [--enabled true|false]
                   [--explore-enabled true|false] [--page 1] [--page-size 50] [--json]
reader source show <sourceId> [--json]
reader source enable <sourceId> [--json]
reader source disable <sourceId> [--json]
```

`list` 文本行：`idx<TAB>sourceId<TAB>name<TAB>group<TAB>enabled<TAB>explore<TAB>hasSearch`（group 为空显示 `-`；hasSearch = 是否配置 searchUrl）。JSON：`{"total":N,"items":[{"index","sourceId","name","group","enabled","enabledExplore","hasSearchUrl"}]}`。

`show` 文本：`name/url/group/enabled/enabledExplore/searchUrl/lastUpdateTime` 七行；`--json` 输出完整 BookSource。

`enable|disable` 输出 `<sourceId>\tenabled=<bool>`。

## search

```bash
reader search <keyword> [--source id1,id2] [--group <g>] [--page 1] [--limit 20]
              [--precision] [--concurrency N] [--json]
```

- 不指定 `--source` 时只搜启用源；显式指定 `--source` 允许搜未启用源。
- `--limit` 只截断输出条数，不影响搜索本身；必须 >= 1，否则退出码 1。
- 文本模式下每条源失败/告警输出 stderr `warn: <sourceName>: <message>`。

文本行：`idx<TAB>name<TAB>author<TAB>sourceId<TAB>latestChapter`。

JSON 输出（`bookUrl` + `sourceId` 是 `book add` 的稳定输入句柄）：

```json
{
  "items": [
    {"index": 1, "name": "斗破苍穹", "author": "天蚕土豆", "bookUrl": "https://...", "sourceId": "src_xxx", "origin": "https://...", "originName": "笔趣阁", "latestChapter": "第一章"}
  ],
  "warnings": [{"sourceId": "", "bookSourceName": "...", "code": "...", "message": "..."}]
}
```

`sourceId` 通过 origin 反查书源库；查不到为空字符串。

## book

```bash
reader book add --source <sourceId> --url <bookUrl> [--name <n>] [--author <a>] [--no-refresh] [--json]
reader book list [--page 1] [--page-size 50] [--json]
reader book info <bookId> [--json]
reader book refresh <bookId> [--json]
reader book rm <bookId> [--json]
```

- `add` 默认在入库后执行 info refresh（`CanRename=true`，修正书名/简介/tocUrl）；refresh 失败仅 warn，不回退入库。`bookId = sha256(bookUrl)` 前缀 `book_` 的 24 位十六进制。
- `list` 文本行：`idx<TAB>bookId<TAB>name<TAB>author<TAB>originName<TAB>durChapterIndex<TAB>latestChapter`。
- `info` 文本：`name/author/originName/wordCount/tocUrl/coverUrl/intro`（intro 截 200 字符）；`--json` 输出完整 Book。
- `refresh` 依次执行 info + toc 刷新，输出 `refreshed<TAB><bookId>\tchapters=<N>`。

`add` JSON：`{"bookId","name","author","originName","latestChapterTitle"}`；`refresh` JSON：`{"bookId","name","latestChapterTitle","chapters":N}`。

## toc

```bash
reader toc <bookId> [--json]
```

文本行：`index<TAB>title<TAB>url`（卷章节 title 前加 `[卷] `）。JSON：

```json
{"bookId": "book_xxx", "name": "斗破苍穹", "count": 2, "chapters": [{"index": 0, "title": "第一章", "url": "https://...", "isVolume": false}]}
```

目录为空：stderr `error: toc is empty, run: reader book refresh <bookId>`，退出码 2。

## content

```bash
reader content <bookId> <index> [--json]
```

文本模式 stdout 仅输出正文原文（缓存命中直接返回，不发网络请求）。JSON：`{"bookId","index","title","content"}`。

## download

```bash
reader download <bookId> [--from 0] [--to -1] [--export <path>] [--no-export] [--workers 1] [--json]
```

- `--from` 必须 >= 0 且不超过最后一章索引，否则退出码 1；`--to -1` 表示最后一章，超过最后一章时夹取到最后一章；`--to` 小于 `--from` 退出码 1。
- 逐章写入 SQLite 缓存：缓存命中不再抓取，重跑即断点续跑；单章失败记入 `failed` 继续不中断。
- 卷章节（isVolume）计入 `skipped`，不入 TXT。
- 默认导出 TXT 到 `downloads/<bookId>.txt`（`--export` 可指定路径，覆盖写）；`--no-export` 跳过导出。区间内没有任何正文（全是卷章节或全部失败）时不导出，`warn: no chapter content, export skipped`，已有文件保持不变，`exported` 为空。
- Ctrl-C 中断：停止后续章节，不导出（避免残缺内容覆盖已有 TXT），stderr `error: interrupted, export skipped; rerun to resume from cache`，退出码 2；已抓章节在缓存中，重跑即续传。
- `--workers > 1` 当前按 1 执行并 warn（SQLite 单连接，并发写无收益）。
- 进度输出 stderr：`[序号/总数] index title`。

TXT 格式：文件头 `<name>\n<author>\n\n`，每章 `\n\n=== <index> <title> ===\n\n<content>`。

JSON：

```json
{"bookId": "book_xxx", "from": 0, "to": 2, "downloaded": 2, "cached": 1, "skipped": 0, "failed": [{"index": 3, "error": "..."}], "exported": "downloads/book_xxx.txt"}
```

`failed` 非空退出码 2，否则 0。文本等价 tab 行（`failed` 明细行 + `downloaded/cached/skipped/failed/exported` 汇总行）。

## Agent 管道配方

一次编排全链（import → 搜索 → 加书架 → 目录 → 下载）：

```bash
# 1. 导入书源
reader import https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361

# 2. 看有哪些源可用（找 hasSearch=yes 的）
reader source list --page-size 5

# 3. 搜索，取 JSON 里的 bookUrl + sourceId
reader search 斗破苍穹 --limit 5 --json | jq '.items[0]'

# 4. 加书架（默认自动刷新书名/简介）
reader book add --source "$SRC_ID" --url "$BOOK_URL"

# 5. 刷新目录并查看
reader book refresh "$BOOK_ID"
reader toc "$BOOK_ID" --json | jq '.count'

# 6. 下载前 3 章导出 TXT
reader download "$BOOK_ID" --from 0 --to 2 --export /tmp/book.txt

# 7. 单章正文直接读
reader content "$BOOK_ID" 0
```

Agent 编程接口建议：一律加 `--json` 并用 jq 解析；把 search 返回的 `bookUrl`/`sourceId` 透传给 `book add`；`bookId` 用 `book list --json` 或 `book add` 返回值，不要自行计算。
