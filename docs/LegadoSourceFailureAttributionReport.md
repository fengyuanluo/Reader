# Reader 对 Legado 书源兼容性与 5,502 源实测报告

**审计时间：** 2026-08-18
**范围：** 只审查 Reader 对 Legado 书源的服务端兼容性；文件型书源允许返回 TXT 等文件；不实现 WebView/浏览器能力。
**代码基线：** Reader `a6def3734fe96410e3b094b23c5dbf8b33c185b8` 加当前工作树；legado-E `8b87c5aba4df91c39a3a0939a68a1180b9f2ee1c`。
**禁止项：** 不做域名特判，不放宽 TLS/SSRF，不替书源补缺失函数，不把网络失败伪装成兼容成功。

## 1. 结论

1. 从两个仍在自动更新的长期仓库取得 7,377 条原始记录，按 `trim + lowercase(bookSourceUrl)` 去重为 **5,502 个不同书源**；全新 SQLite 导入 `added=5502 / failed=0`。
2. 最终全量 search-only 校验为 `1,090 passed / 4,411 failed / 1 skipped`。失败主要是空结果、HTTP、DNS、TLS、超时和站点响应漂移，不能把 4,411 直接解释为 Reader 不兼容或永久坏源。
3. 本轮新增确认并修复一个 Reader 项目问题：CSS/JSoup 元素集合进入 `@js:` 后没有稳定保留 Legado 集合语义，且兼容方法会被 `for-in` 枚举。修复后 9 个真实复现源中 7 个通过，2 个转为正常空结果；全量结果中的 `.text/.attr/select` 缺失错误从 9 个降为 0。
4. 使用最终二进制对 4,861 个启用源真实搜索“斗罗大陆”：**1,062 个来源返回任意结果，833 个来源含关键词，465 个来源精确命中**；跨源合并后分别为 4,310、2,221、37 行。
5. 19 个文件型源的专项校验中，目录和正文阶段均未被当作网页正文源执行。3 个源搜索成功但本次没有提取到下载地址；TXT 或其他文件类型本身不构成失败。
6. 剩余 8 个明确的 `webView/startBrowserAwait` 调用属于既定边界。对其余失败逐项聚类后，没有发现新的、可由最小 fixture 稳定证明的 Reader 通用语义缺口。

## 2. 网络书源集合

### 2.1 来源与维护状态

| 仓库 | 原始数 | 去重后保留 | 固定 commit | 仓库创建 | 本次联网确认的最近 push |
| --- | ---: | ---: | --- | --- | --- |
| [tickmao/Novel](https://github.com/tickmao/Novel) | 3,470 | 3,470 | `2cf1673d62f32fb81bc4faf468798b8f78cf9a97` | 2020-08-03 | 2026-08-17 |
| [aoaostar/legado](https://github.com/aoaostar/legado) | 3,907 | 2,032 | `ecc7a6fc041a6a7f41b278421c97ab53b3cf3faf` | 2022-11-28 | 2026-08-18 |

两个仓库均未归档，且固定提交均由自动维护任务产生。`tickmao/Novel` 的提交说明为“日常维护 - 替换 2 个失效书源”；`aoaostar/legado` 的提交为自动同步。选择固定 commit 是为了让本次数字可复现，不代表以后继续使用旧快照。

去重规则：

```text
key = lowercase(trim(bookSourceUrl))
priority = tickmao/Novel, then aoaostar/legado
```

原始集合重叠 1,875 条，最终唯一 key 为 5,502。`tickmao/Novel` 候选记录带有其维护任务的 `valid` 标记，但该标记只代表仓库自己的最近校验，不能替代本次 Reader 实时请求。

审计工件：

- `/tmp/reader-source-research/output/legado-sources-5502.json`
- `/tmp/reader-source-research/output/provenance.json`
- `/tmp/reader-source-research/select_sources.jq`

### 2.2 类型分布

| `bookSourceType` | 总数 | 启用 |
| --- | ---: | ---: |
| 文本 0 | 5,351 | 4,711 |
| 音频 1 | 54 | 54 |
| 图片 2 | 78 | 77 |
| 文件 3 | 19 | 19 |
| 合计 | 5,502 | 4,861 |

## 3. 真实执行基线

流程不是静态解析，而是：

```text
当前工作树构建
-> 全新 SQLite
-> 5,502 源分块导入
-> 5,502 源 search-only 校验
-> 暴露问题定向复验
-> 4,861 启用源真实 /api/v1/search
-> 19 个文件源专项校验
```

全量校验显式传入 5,502 个 source ID，因此也覆盖 641 个禁用源；关键词为“斗罗大陆”，单源超时为 30 秒。真实搜索遵守线上默认行为，只使用 4,861 个启用源。

## 4. 本轮新增项目修复

### 4.1 根因

Legado 规则允许先用 CSS 得到元素集合，再在下一段 `@js:` 中调用：

```javascript
list = result.toArray();
for (i in list) {
  if (list[i].text().indexOf("斗罗") > -1) list1.push(list[i]);
}
```

Reader 原先存在两个相互叠加的问题：

1. request-option evaluator 把 `[]*goquery.Selection` 直接写入 VM 的 `result`，没有经过统一 binding 转换。
2. `toArray/size/get/first/text/attr/html/select/selectFirst/not` 被作为可枚举属性挂在数组上，`for-in` 会把这些函数也当作元素，随后出现 `Object has no member 'text'`。

### 4.2 修复

- evaluator 设置 `result` 时统一调用 `prepareBindingValue`。
- Go selection slice 转换为带 Legado/JSoup 方法的元素集合。
- 集合兼容方法改为不可枚举属性，数组索引仍保持正常枚举。
- 保持 `text/attr/html/select/selectFirst/get/first/not/toArray/size` 的链式行为，不引入通用 Java 反射或浏览器运行时。

### 4.3 回归证据

新增覆盖：

- request-option evaluator 接收 selection slice；
- CSS -> `@js:` 完整规则链；
- 真实“随心看”式 `for-in + text/select/attr` 搜索规则。

定向任务 `check-20260818153648.239561306`：

| 结果 | 数量 |
| --- | ---: |
| 通过 | 7 |
| 正常空结果 | 2 |
| `.text/.attr/select` 运行时错误 | 0 |

7 个通过源包括 5 个“随心看”变体和 2 个“晋江文学城”变体；“和包网”“饭饭论坛”不再报运行时缺方法，而是根据当前响应返回 `empty search result`。

## 5. 全量校验与波动

| 运行 | Task ID | passed | failed | skipped |
| --- | --- | ---: | ---: | ---: |
| 修复前 | `check-20260818130716.973021522` | 1,100 | 4,401 | 1 |
| 中间构建 | `check-20260818142859.457373655` | 1,136 | 4,365 | 1 |
| 最终构建 | `check-20260818153948.910182522` | 1,090 | 4,411 | 1 |

相邻两次运行中有 88 个源 `failed -> passed`，同时有 134 个源 `passed -> failed`。这证明全量净通过数被实时网络和站点状态显著影响；单项修复应看定向复验和特征错误是否消失，不能用净增减判断。

最终失败归并：

| 类别 | 数量 | 归因 |
| --- | ---: | --- |
| 明确空搜索结果 | 1,855 | 当前关键词/站点/规则没有结果 |
| HTTP 状态或认证 | 1,037 | 上游状态、限流或授权 |
| DNS/TLS/超时/重置等网络问题 | 1,236 | 网络或上游 |
| 上游空 JSON/非 JSON | 29 | 响应与源预期不符 |
| 源脚本失败或语法错误 | 77 | 源脚本、响应漂移或慢站 |
| `implementation_or_rule_failed` 待定标签 | 147 | 见下方逐项复核 |
| 安全策略拦截 | 22 | 既定 Fetcher 边界 |
| 浏览器能力 | 8 | 明确排除 |
| 合计 | 4,411 |  |

147 个待定项已按原始错误逐条归并：

| 实际信号 | 数量 |
| --- | ---: |
| 非法/非预期 JSON 或源内 JSON | 73 |
| TLS、HTTP/HTTPS 错配、重定向等外部协议问题 | 52 |
| 源内非法 URL | 17 |
| 缺失 `ruleSearch.bookList` | 3 |
| 源规则自身语法错误 | 2 |

这里的分类器有意保守，标签名不等于已确认 Reader 缺陷。例如“爱奇文学”旧规则为 `class.c wrap@ul@tag.li`，但站点现在返回 `{"code":0,"data":"<html>..."}`；Legado 会按 JSON 内容选择 JSON 分析器，旧 CSS 规则同样失效。另一条失败 XPath 本身多出一个 `]`。

70 个源脚本失败中有 38 个在空匹配上访问索引/属性、16 个引用源自身未定义的函数或变量、16 个脚本超时。超时脚本均包含真实网络调用；一次慢站结果不足以证明 JS 语义不兼容，也不足以把全局脚本超时继续放大。

## 6. “斗罗大陆”真实搜索

为避免单请求默认 4 并发造成不必要等待，将 4,861 个启用 source ID 分成 16 个真实 API 请求；16 批均为 HTTP 200。每批内部仍走 Reader 的完整搜索路径，最终再按 Reader 的 `name + author` 规则跨批合并，并合并 `origin/origins`。

| 指标 | 数量 |
| --- | ---: |
| 收集书源 | 5,502 |
| 启用书源 | 4,861 |
| 启用且有搜索规则 | 4,296 |
| 批内结果行合计 | 12,871 |
| 跨源合并结果行 | 4,310 |
| 有任意结果的来源 | 1,062 |
| 含关键词结果行 | 2,221 |
| 含关键词来源 | 833 |
| 精确结果行 | 37 |
| 精确命中来源 | 465 |
| warning / warning 来源 | 2,181 / 2,181 |

口径：

- “有任意结果”统计所有结果的唯一 `origin/origins`。
- “含关键词”要求书名或作者包含“斗罗大陆”。
- “精确”要求书名或作者等于“斗罗大陆”。
- 4,296 个可搜索源守恒为 `1,062 有结果 + 2,181 warning + 1,053 空结果`；另有 565 个启用源没有搜索规则。

结果工件位于 `/tmp/reader-source-research/output/search-douluo-5502/`，其中 `search-summary.json`、`search-merged.json`、`search-warnings.json` 分别保存统计、合并结果和逐源 warning。

## 7. 文件型书源

专项任务 `check-20260818160339.668920342`：

| 结果 | 数量 |
| --- | ---: |
| 文件源总数 | 19 |
| 搜索成功 | 3 |
| 搜索失败 | 15 |
| 无搜索规则 | 1 |
| 详情未提取到 `downloadUrls` | 3 |
| 目录被执行或判失败 | 0 |
| 正文被执行或判失败 | 0 |

15 个搜索失败由 7 个超时、4 个无效 TLS、2 个 HTTP、1 个空结果和 1 个源脚本失败组成。3 个搜索成功源本次均未提取到下载地址，所以没有可下载文件可继续验证；这只说明当前响应和规则没有给出 URL。Reader 不要求文件源再提供目录或网页正文，下载地址指向 TXT 也是有效路径。

## 8. 非项目问题与边界

- 8 个浏览器调用：`startBrowserAwait` 2 个，`webView` 6 个；按范围不实现。
- `QDSign/GetUrl/getSign/getUrl/getHost/getArguments/base64/method/t` 等引用所在源没有相应 `jsLib`，也不是 legado-E 内建全局；不由 Reader 猜测实现。
- 17 个非法 URL 中有 12 个使用 `,< ... >` 选项。legado-E 的 `AnalyzeUrl.paramPattern` 只识别逗号后紧跟 `{` 的形式，这些源在当前 legado-E 中同样不会被解析为 URL 选项。
- regex 未匹配后访问 `[1]`、对 HTML/空响应执行 `JSON.parse`、缺失搜索列表规则，均是源脚本或响应漂移。
- HTTP 4xx/5xx、DNS、证书过期/未知 CA、TLS handshake、连接重置和慢站不通过放宽安全策略修复。

只有以下证据才升级为 Reader 项目问题：同一 fixture 稳定复现、legado-E 源码明确提供对应语义、且 Reader 与其控制流或数据契约存在确定差异。本轮新增 JSoup 集合问题满足该标准并已修复；其余样本不满足。

## 9. 验证记录

聚焦回归已通过：

```text
go test ./internal/infra/script ./internal/app/search \
  -run 'Test(RuntimeWrapsSelectionsAcrossRuleScriptStep|VMRequestEvaluatorWrapsGoquerySelectionSlices|SingleSourceSearchWrapsJSoupElementsInBookListScript)' \
  -count=1
```

最终发布门禁已通过：

```text
go generate ./...
go test ./...
go vet ./...
go test -race ./internal/worker/check/...
go build ./cmd/server
git diff --check
```

`go test ./...` 已覆盖 `test/openapi`、`test/e2e` 和 `test/security`，未重复执行相同测试。真实业务验证还完成了全新构建、导入、全量校验、定向复验、全局搜索和文件源专项链路，而不只是进程启动或 HTTP 200。
