---
name: reader-cli
description: Reader CLI：书源导入/搜索/书架/目录/正文/下载的非交互命令行，面向 Agent 自动化，单二进制 `reader`，`--json` 机读输出
---

# Reader CLI 技能卡

Reader 提供单二进制 CLI（`go run ./cmd/reader` 或构建后的 `reader`），全部子命令非交互、默认 tab 分隔输出、`--json` 机读输出。完整参考：仓库 `docs/CLI.md`。

## 何时用哪条命令

| 意图 | 命令 |
|---|---|
| 把书源集合 JSON（文件/URL/stdin）导入库 | `reader import <path\|URL\|->` |
| 查看已导入源、找可用搜索源 | `reader source list`（看 `hasSearchUrl`） |
| 精确看某源规则/状态 | `reader source show <sourceId>` |
| 按关键词跨源搜书 | `reader search <keyword> --json` |
| 把搜索结果加入书架 | `reader book add --source <sourceId> --url <bookUrl>` |
| 查看书架 | `reader book list --json` |
| 看书的详情 | `reader book info <bookId>` |
| 刷新书名/简介/目录 | `reader book refresh <bookId>` |
| 列章节目录 | `reader toc <bookId> --json` |
| 读单章正文 | `reader content <bookId> <index>` |
| 整本/区间下载并导出 TXT | `reader download <bookId> [--from N --to M --export path]` |
| 启停书源 | `reader source enable|disable <sourceId>` |

## 调用约定

- 解析输出一律加 `--json` + jq；不要解析 tab 文本（仅人类可读）。
- 不确定参数时先查 `reader <command> --help`（flag 统一双横线写法，exit 0）；`reader book --help` / `reader source --help` 列出子命令。
- 退出码：0 成功；1 用法/参数错误（在打开数据库前判定，不会建库）；2 运行失败。`download` 有章节失败时 exit 2，`failed` 数组含失败章节索引与原因。
- `import --update` 只接受 `lastUpdateTime`（默认）、`always`、`skipExisting`；带 UTF-8 BOM 的 JSON 可直接导入。
- `search --limit` 必须 >= 1；`download --from` 必须在 0 到最后一章索引之间。
- Ctrl-C / SIGTERM 取消进行中的命令；`download` 被中断时不导出 TXT，exit 2，重跑从缓存续传。
- 链式句柄：`search --json` 返回 `items[].bookUrl` 与 `items[].sourceId` → 原样传给 `book add`；`bookId` 从 `book add` 返回值取，不要自行计算。
- CLI 直连 SQLite（`READER_SQLITE_DSN`，默认 `data/reader.db`），不需要启动 server、不需要 token。`reader serve` 默认监听 `127.0.0.1:8080`，此时可以不设 token；监听非回环地址必须设置 `READER_TOKEN`（production 始终必须）。serve 的 JSON 日志在 stderr。
- `book add` 默认自动刷新书名与简介（可 `--no-refresh` 跳过）；`toc` 为空先执行 `reader book refresh <bookId>`。
- `download` 幂等：缓存命中的章节不再抓取，重跑即断点续跑；卷章节自动 skip，不写入 TXT。
- 默认禁止抓内网地址；对本地 httptest mock 源调试时需 `READER_ALLOW_PRIVATE_NETWORK=true`。

## 最小可复现链

```bash
reader import <collection-url>
reader search 斗破苍穹 --limit 5 --json | jq '.items[0]'
reader book add --source "$SRC_ID" --url "$BOOK_URL"
reader book refresh "$BOOK_ID"
reader download "$BOOK_ID" --from 0 --to 2 --export /tmp/book.txt
```
