# 全书 TXT 导出真实链路验收

**执行时间：** 2026-08-18
**服务：** 最终构建 `go build ./cmd/server`，全新 SQLite，`127.0.0.1:18083`
**关键词：** `斗破苍穹`
**书源：** `src_51c98043d70b25c4cb428178` / `🌸七猫` / `https://api-bc.wtzw.com`

## 链路结果

| 阶段 | 结果 |
| --- | --- |
| 导入 | 5,502 个书源全部导入成功 |
| 搜索 | HTTP 200；该源返回 10 条结果，无 warning |
| 选书 | 《斗破苍穹之焚天武魂》/ 胡子流氓 |
| 详情 | HTTP 200；书名、作者、目录 URL 均解析成功 |
| 目录 | HTTP 200；438 个章节，首章“第一章 武魂觉醒”，末章“第四百三十八章 出关，归隐” |
| 首章接口 | HTTP 200；正文长度 2,262 字符 |
| 全书导出 | `GET /api/v1/books/{bookId}/export.txt` 返回 HTTP 200 |

## TXT 验证

- `Content-Type`: `text/plain; charset=utf-8`
- `Content-Length`: `3,428,675`
- 文件名同时提供 ASCII fallback 和 RFC 5987 UTF-8 `filename*`
- 首章标题和末章标题均出现在文件中
- UTF-8 round-trip 校验通过
- 书名、作者、简介、章节标题和正文均存在
- 未发现错误标记或残缺章节
- SHA-256：`5a20ce3decc944a27f45e8a1fb83ccc670fd51cdcc11f695580be53f4c37bbc2`

导出过程按目录顺序串行获取正文；成功章节写入现有正文缓存。临时导出文件只在响应期间存在，章节失败时不会发送部分文件。
