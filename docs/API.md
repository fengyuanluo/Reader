# Reader API 使用说明

Reader 对外 API 以 `api/openapi.yaml` 为唯一真相源，服务默认挂载在 `/api/v1`。

## 兼容边界

- 兼容 Legado 书源 JSON 数据格式和在线书源规则语义。
- 不兼容 Legado 旧 Web API 路由，例如 `/saveBookSource`、`/getBookSources`、`/searchBook`、`/getBookContent`。
- 除 `GET /health`、`GET /version`、`GET /openapi.yaml` 外，所有接口默认需要 Bearer Token。

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

## 调试与校验

创建关键词调试：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/debug/book-source \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{"sourceId":"src_xxx","mode":"keyword","keyword":"斗破苍穹"}'
```

订阅调试事件：

```bash
curl -N -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/v1/debug/sessions/{sessionId}/events
```

创建批量校验任务：

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
