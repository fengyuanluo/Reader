# Reader API 使用说明

Reader 对外 API 以 `api/openapi.yaml` 为唯一真相源，服务默认挂载在 `/api/v1`。

## 兼容边界

- 兼容 Legado 书源 JSON 数据格式和在线书源规则语义。
- 不兼容 Legado 旧 Web API 路由，例如 `/saveBookSource`、`/getBookSources`、`/searchBook`、`/getBookContent`。
- 除 `GET /health`、`GET /version`、`GET /openapi.yaml` 外，所有接口默认需要 Bearer Token。

## 端点清单

`api/openapi.yaml` 是唯一真相源，完整字段与响应 schema 以它为准；下面是当前已声明并实现的端点总表（本文件后续的 curl 示例只是其中一部分）：

| 方法 | 路径（前缀 `/api/v1`） | 说明 |
|---|---|---|
| GET | `/health` | 健康检查，无需 token |
| GET | `/version` | 版本，无需 token |
| GET | `/openapi.yaml` | 返回 OpenAPI YAML（`Content-Type: application/yaml`），无需 token |
| GET | `/book-sources` | 书源分页查询（keyword/group/enabled 过滤） |
| POST | `/book-sources` | 创建书源 |
| POST | `/book-sources/import` | 导入单个对象、数组、`sourceUrls` 文档或远程 URL |
| GET | `/book-sources/{sourceId}` | 书源详情 |
| PUT | `/book-sources/{sourceId}` | 全量更新书源（含 `bookSourceUrl`） |
| DELETE | `/book-sources/{sourceId}` | 删除书源及其关联数据 |
| PATCH | `/book-sources/{sourceId}/enabled` | 启用/停用 |
| PATCH | `/book-sources/{sourceId}/explore-enabled` | 启用/停用发现 |
| PATCH | `/book-sources/reorder` | 重排顺序 |
| GET | `/book-sources/{sourceId}/login` | 查询持久化登录状态 |
| POST | `/book-sources/{sourceId}/login` | 提交登录表单值并执行 `loginUrl` 脚本 |
| DELETE | `/book-sources/{sourceId}/login` | 清除 `loginInfo` / `loginHeader`（204） |
| POST | `/search` | 多源搜索（`keyword` 必填，可用 `sourceIds`/`group` 限定范围） |
| POST | `/discovery` | 发现分类与发现列表 |
| POST | `/books` | 加入书架 |
| GET | `/books` | 书架分页查询 |
| GET | `/books/{bookId}` | 书籍详情 |
| DELETE | `/books/{bookId}` | 删除书籍（连带章节与正文缓存） |
| POST | `/books/{bookId}/refresh-info` | 刷新详情 |
| POST | `/books/{bookId}/refresh-toc` | 刷新目录 |
| GET | `/books/{bookId}/chapters` | 章节列表 |
| GET | `/books/{bookId}/chapters/{index}/content` | 章节正文 |
| PUT | `/books/{bookId}/progress` | 保存阅读进度 |
| GET | `/images/proxy` | 图片安全代理 |
| GET | `/replace-rules` | 替换规则列表 |
| POST | `/replace-rules` | 创建替换规则 |
| PUT | `/replace-rules/{ruleId}` | 更新替换规则 |
| DELETE | `/replace-rules/{ruleId}` | 删除替换规则 |
| POST | `/replace-rules/test` | 测试替换规则 |
| POST | `/debug/book-source` | 创建调试会话（202） |
| GET | `/debug/sessions/{sessionId}` | 调试会话状态 |
| GET | `/debug/sessions/{sessionId}/events` | 调试事件流（SSE） |
| DELETE | `/debug/sessions/{sessionId}` | 取消调试会话（202） |
| POST | `/check/book-sources` | 创建批量校验任务（202） |
| GET | `/check/tasks/{taskId}` | 校验任务状态 |
| GET | `/check/tasks/{taskId}/events` | 校验事件流（SSE） |
| DELETE | `/check/tasks/{taskId}` | 取消校验任务（202） |

## 通用约定

- **异步语义**：`POST /check/book-sources`、`DELETE /check/tasks/{taskId}`、`POST /debug/book-source`、`DELETE /debug/sessions/{sessionId}` 成功返回 **202** 加任务/会话快照，终态由 `GET` 轮询或事件流确认。
- **请求体**：JSON 端点要求 `Content-Type: application/json`（否则 400）；普通 JSON 请求体上限 10 MiB，书源导入上限 32 MiB，超限返回 **413** `payload_too_large`。
- **分页**：`page >= 1`、`1 <= pageSize <= 200`，越界返回 400 `validation_failed`。
- **未装配能力**：可选服务缺失时返回 **501** `not_configured`，不再用 500 掩盖。
- **冲突**：`PUT /book-sources/{sourceId}` 修改 `bookSourceUrl` 时，若新地址已被其他书源占用，或 body 中的 `sourceId` 与路径不一致，返回 **409** `source_conflict`。
- **SSE**：`Content-Type: text/event-stream`；先回放历史事件（支持 `Last-Event-ID` 续传），随后实时推送并周期发送 `: heartbeat` 注释行；会话进入终态后关闭连接；订阅不存在的会话立即返回 404。

## 认证

```bash
curl -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/book-sources
```

缺少 token 返回 `auth_required`，token 错误返回 `auth_invalid`。日志和调试事件不得输出 token、Authorization、Cookie 或 Set-Cookie 明文。

## 书源导入

导入单个书源：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/book-sources/import \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{
    "payload": {
      "bookSourceUrl": "https://example.test",
      "bookSourceName": "Example",
      "bookSourceType": 0,
      "enabled": true,
      "enabledExplore": false,
      "enabledCookieJar": true,
      "customOrder": 0,
      "lastUpdateTime": 0
    }
  }'
```

导入远程 URL：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/book-sources/import \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{"remoteUrl":"https://example.test/sources.json"}'
```

`remoteUrl` 支持 `#requestWithoutUA` 后缀，实际请求会移除该后缀并不发送默认 User-Agent。远程导入会经过 Fetcher 的 SSRF、超时、重定向和响应大小限制。

## 搜索到正文链路

搜索：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/search \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{"keyword":"斗破苍穹","page":1,"enabledOnly":true}'
```

保存搜索结果到书架：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/books \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{"bookUrl":"https://example.test/book/1","origin":"https://example.test","originName":"Example","name":"斗破苍穹","author":"天蚕土豆","type":0,"originOrder":0}'
```

刷新详情和目录：

```bash
curl -X POST -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/books/{bookId}/refresh-info

curl -X POST -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/books/{bookId}/refresh-toc
```

获取正文：

```bash
curl -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/books/{bookId}/chapters/0/content
```

保存阅读进度：

```bash
curl -X PUT http://127.0.0.1:8080/api/v1/books/{bookId}/progress \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{"durChapterIndex":0,"durChapterPos":120,"durChapterTitle":"第一章","durChapterTime":1710000000}'
```

## 替换规则

测试替换规则：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/replace-rules/test \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{"text":"广告正文","rule":{"id":0,"name":"去广告","pattern":"广告","replacement":"","scopeTitle":false,"scopeContent":true,"isEnabled":true,"isRegex":false,"timeoutMillisecond":3000,"order":0}}'
```

Regex 替换有输入大小和 context 限制，超限返回 `replace_failed`。

## 书源登录

查询持久化登录状态：

```bash
curl -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/book-sources/{sourceId}/login
```

提交登录表单值并执行 `loginUrl` 中的 login 脚本（`values` 省略/为空等价于只清除 `loginInfo`）：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/book-sources/{sourceId}/login \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{"values":{"username":"u","password":"p"}}'
```

响应为 `BookSourceLoginState{sourceId,hasLoginUrl,hasLoginUi,loggedIn,loginInfo,loginHeader}`。清除登录状态：

```bash
curl -X DELETE -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/book-sources/{sourceId}/login
```

错误：书源不存在 404；未声明 `loginUrl` 或表单值非法 400；`loginUi` 为空且 `loginUrl` 非脚本（Legado 走 WebView 登录）返回 501 `unsupported_login_flow`；脚本执行失败 502 `script_failed`。

## 调试与校验

创建关键词调试（成功返回 202 加会话快照）：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/debug/book-source \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{"sourceId":"src_xxx","mode":"keyword","keyword":"斗破苍穹"}'
```

`mode` 必须是 `keyword`、`info_url`、`toc_url`、`content_url`、`discovery_url` 之一，否则返回 400。

订阅调试事件（历史回放 + 实时推送，`Last-Event-ID` 续传）：

```bash
curl -N -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/debug/sessions/{sessionId}/events
```

创建批量校验任务（成功返回 202；指定不存在的 `sourceId` 返回 400）：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/check/book-sources \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{"sourceIds":["src_xxx"],"options":{"keyword":"斗破苍穹","checkSearch":true,"checkInfo":true,"checkToc":true,"checkContent":true}}'
```

## 图片代理

封面和正文图片可通过安全代理访问：

```bash
curl -L -H "Authorization: Bearer dev-token" \
  "http://127.0.0.1:8080/api/v1/images/proxy?url=https%3A%2F%2Fexample.test%2Fcover.jpg" \
  --output cover.jpg
```

图片代理复用 Fetcher 安全策略，默认禁止私网、回环、link-local 和 metadata 地址，且限制响应大小和重定向。
