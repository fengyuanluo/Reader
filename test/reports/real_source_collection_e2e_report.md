# Real Source Collection E2E Report

- Test time: 2026-05-05T07:20:28Z
- Collection URL: https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361
- HTTP status: 200
- Response bytes: 4849198
- Parsed count: 459
- Max candidates: 100
- Attempted candidates: 100
- Full success count: 51
- Keyword: 斗破苍穹
- Release allowed: true
- Failure class: 

## Stage Summary

- search: passed=57, failed=43
- info: passed=54, failed=3
- toc: passed=53, failed=1
- content: passed=51, failed=2

## Failure Summary

- source_search_empty_result: 13
- network_tls_invalid: 6
- network_connection_reset: 5
- network_timeout: 5
- source_upstream_http_403: 5
- network_dns_failure: 4
- source_upstream_http_404: 3
- source_upstream_empty_json_response: 2
- security_policy_blocked: 1
- source_authorization_failed: 1
- source_content_empty: 1
- source_toc_empty: 1
- source_upstream_http_500: 1
- source_upstream_non_json_response: 1

## Failed Stages

- [search] 起点读书（优+++） (src_ba56c1b5c646467e91a9e28a) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404
- [info] 番茄小说（优+++） (src_e13960f70d9807554ed4ae9f) [source_upstream_empty_json_response]: script_failed: e2e: SyntaxError: Unexpected end of JSON input (EOF) at parse (native)
- [search] 天天看书（优+++） (src_2e69e222325a50ab5fcacc3a) [network_timeout]: empty search result; warnings: search_failed: Get "https://cn.ttkan.co/novel/search?q=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
- [search] 得奇小说（优+++） (src_e65b7afd81062e9b55ab40f1) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://www.deqibook.com/tag/?key=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": tls: failed to verify certificate: x509: certificate is valid for *.freeshorttv.com, freeshorttv.com, not www.deqibook.com
- [search] 篱笆文学（优+++） (src_f4cb64d3ffc3a27ef0d0d39c) [source_search_empty_result]: empty search result
- [search] 我的书城（优++） (src_525b28d5f4d56bd8e94e9911) [network_dns_failure]: empty search result; warnings: search_failed: Post "https://wodushu.com/search/": dial tcp: lookup wodushu.com on 172.20.65.146:53: server misbehaving
- [search] 天天小说（优++） (src_3c11912efc699c481e08bc12) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [toc] 猫眼看书（优++） (src_e86c92fc3572d5388ba96a75) [source_toc_empty]: toc_empty
- [search] 鲸云轻说（优++） (src_556c281e6af98e47021261b4) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://jyapi.jyacg.com/web/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1&type=1": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-05-05T15:21:40+08:00 is after 2026-02-10T15:32:03Z
- [search] 神凑轻说（优++） (src_acd7f6523e1a63a37769680b) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [search] 话本小说（优++） (src_e80901f6c7a641a9c6e88eae) [source_search_empty_result]: empty search result
- [search] 番茄小说（优++） (src_802a30ceb32c39a84c74e34b) [network_dns_failure]: empty search result; warnings: search_failed: Get "https://fanqie.hnxianxin.cn/search?query=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=0&tab_type=3": dial tcp: lookup fanqie.hnxianxin.cn on 172.20.65.146:53: no such host
- [search] 有度中文（优++） (src_d38a730ccbdfbba899b5221e) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [content] 七猫小说（优++） (src_226e9a79d6623f6145729ae8) [source_upstream_non_json_response]: script_failed: e2e: SyntaxError: invalid character 'G' looking for beginning of value at parse (native)
- [search] 番茄小说（优+） (src_ae48dc2981afcd5dbeea4255) [source_search_empty_result]: empty search result
- [info] 番茄小说（优+） (src_f8a3a2e7e8df8d9df945f590) [source_upstream_empty_json_response]: script_failed: e2e: SyntaxError: Unexpected end of JSON input (EOF) at parse (native)
- [search] 有度轻说（优+） (src_818a053544bc9270a08fd51f) [network_connection_reset]: empty search result; warnings: search_failed: Get "https://www.yodu.org/sa/all-%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9-1.html": read tcp 172.20.65.119:56108->64.185.228.38:443: read: connection reset by peer
- [search] 轻说机翻（优+） (src_835ebbfd39f44f6aa68a2a63) [network_connection_reset]: empty search result; warnings: search_failed: Get "https://n.novelia.cc/api/novel?page=0&pageSize=20&query=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&provider=kakuyomu%2Csyosetu%2Cnovelup%2Chameln%2Cpixiv%2Calphapolis&type=0&level=0&translate=0&sort=1": read tcp [2001:da8:9000:a362:68b7:4cff:fed9:e31]:39986->[2606:4700:3033::ac43:c558]:443: read: connection reset by peer
- [search] 天天书吧（优+） (src_dfc5e2c079ea84a464b13e25) [source_upstream_http_500]: empty search result; warnings: search_failed: unexpected status: 500
- [search] 天天书吧（优+） (src_1bb8d072276f4ad3708a5d1e) [security_policy_blocked]: empty search result; warnings: search_failed: fetch blocked: unsupported scheme
- [search] 中华典藏（优+） (src_35b7e7ed637987d3126f0936) [network_timeout]: empty search result; warnings: search_failed: Post "https://www.zhonghuadiancang.com/e/search/index.php": dial tcp [2a03:2880:f134:83:face:b00c:0:25de]:443: i/o timeout (Client.Timeout exceeded while awaiting headers)
- [search] 中华典藏（优+） (src_0b3ad25c91471566070ebddc) [network_timeout]: empty search result; warnings: search_failed: Post "https://www.zhonghuadiancang.com/e/search/index.php": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
- [search] 金庸小说（优+） (src_a868a503411048c51d887329) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://www.jinyongwang.net/search/%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9/": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-05-05T15:26:36+08:00 is after 2026-04-10T13:04:24Z
- [search] 金庸小说（优+） (src_a311aba6335e1e00f27d7d6a) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://www.jinyongwang.net/search/%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9/": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-05-05T15:26:36+08:00 is after 2026-04-10T13:04:24Z
- [search] 盗墓笔记（优+） (src_348a4f1ee3be6f78cfd14bd6) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404
- [search] 盐选文库（优+） (src_fe929c1e56fad544e2dee30d) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404
- [search] 古诗文网（优+） (src_a0345050588428ebb29f8b65) [source_search_empty_result]: empty search result
- [search] 古诗文网（优+） (src_5072a6eed2bf13219c7df90e) [source_search_empty_result]: empty search result
- [info] 天涯知识（优+） (src_844c516e0716b6d1b0ae021d) [network_timeout]: Get "http://m.sbkk8.com/gudai/zhongguogudaishenhuagushi/217763.html": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
- [search] 熊猫文学（优+） (src_85204b4c8ade867fdf3470e6) [network_connection_reset]: empty search result; warnings: search_failed: Get "https://www.dxmwx.org/list/%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9.html": read tcp [2001:da8:9000:a362:68b7:4cff:fed9:e31]:33712->[2606:4700:3033::ac43:b2ac]:443: read: connection reset by peer
- [search] 百合爱会（优+） (src_4c0af541a551d1445adec5e6) [source_search_empty_result]: empty search result
- [search] 全本小说（优+） (src_371d847bd9e984de7bdbd525) [source_search_empty_result]: empty search result; warnings: search_failed: Post "https://m.qbxsba.com/modules/article/search.php": http: server gave HTTP response to HTTPS client
- [search] 旭日小说（优+） (src_be6bc224a0d554d82c4ddb39) [source_authorization_failed]: empty search result; warnings: search_failed: unexpected status: 401
- [search] 狸猫故事（优+） (src_b28018fc62ce8d744ffcb9d8) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [search] 书香阁子（优+） (src_e35e1e2cd1f510c8bf994084) [network_timeout]: empty search result; warnings: search_failed: Get "https://www.sxgread.com/s/?sword=%B6%B7%C6%C6%B2%D4%F1%B7": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
- [search] 风云小说（优+） (src_efffd3d64d374730dc38dffb) [source_search_empty_result]: empty search result
- [search] 八三中文（优+） (src_94ec0165cde467ca225d0db6) [network_connection_reset]: empty search result; warnings: search_failed: Post "https://www.83zws.com/soso/": read tcp 172.20.65.119:49226->154.17.237.105:443: read: connection reset by peer
- [search] 安读书网（优+） (src_b80cb5f33f6635be64c13a52) [source_search_empty_result]: empty search result
- [search] 小米阅读（优+） (src_5a3717c98dc35415a2e67187) [network_dns_failure]: empty search result; warnings: search_failed: Get "https://reader.browser.miui.com/api/v2/search/word?query=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&size=100": dial tcp: lookup reader.browser.miui.com on 172.20.65.146:53: no such host
- [search] 小米书城（优+） (src_7956897ace18c66b4b8e5c47) [network_dns_failure]: empty search result; warnings: search_failed: Get "https://reader.browser.miui.com/api/v2/search/word?query=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&size=100": dial tcp: lookup reader.browser.miui.com on 172.20.65.146:53: no such host
- [search] 神凑轻说（优+） (src_ed5d11ff9ad445ffef371bc9) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [search] 鲸云轻说（优+） (src_a38faf7e5285445cef87a57c) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://jyapi.jyacg.com/web/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1&type=1": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-05-05T15:28:27+08:00 is after 2026-02-10T15:32:03Z
- [search] 鲸云轻说（优+） (src_ded92cad885a53cc49f5aacc) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://jyapi.jyacg.com/web/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1&type=1": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-05-05T15:28:27+08:00 is after 2026-02-10T15:32:03Z
- [search] 炫动小说（优+） (src_f6eb03cad223579556ead442) [source_search_empty_result]: empty search result
- [search] 书法小说（优+） (src_b737ce7afb3dddca373e755b) [source_search_empty_result]: empty search result
- [search] 爱久久网（优+） (src_cf66bfeee51cfb09c24b1c4c) [source_search_empty_result]: empty search result
- [content] 桔纸书屋（优+） (src_c91c18f448f64e89aa7cb82a) [source_content_empty]: content_empty
- [search] 有度中文（优+） (src_2ccb91753b7c52e0802265c3) [network_connection_reset]: empty search result; warnings: search_failed: Post "https://www.yodu.org/sa": read tcp 172.20.65.119:56200->64.185.228.38:443: read: connection reset by peer
- [search] 笔下文学（优+） (src_07f2191f96e9e77274a19da2) [source_search_empty_result]: empty search result
