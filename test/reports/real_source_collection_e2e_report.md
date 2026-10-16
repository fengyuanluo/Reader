# Real Source Collection E2E Report

- Test time: 2026-10-14T10:05:57Z
- Collection URL: https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361
- HTTP status: 200
- Response bytes: 4701554
- Parsed count: 459
- Max candidates: 100
- Attempted candidates: 100
- Full success count: 39
- Keyword: 斗破苍穹
- Release allowed: true
- Failure class: 

## Stage Summary

- search: passed=46, failed=54
- info: passed=43, failed=3
- toc: passed=41, failed=2
- content: passed=39, failed=2

## Failure Summary

- source_search_empty_result: 17
- network_tls_invalid: 8
- source_upstream_http_403: 6
- network_connection_reset: 5
- source_upstream_http_404: 5
- network_dns_failure: 4
- network_timeout: 3
- source_upstream_empty_json_response: 3
- source_authorization_failed: 2
- source_toc_empty: 2
- source_upstream_http_400: 2
- security_policy_blocked: 1
- source_rule_script_failed: 1
- source_upstream_http_500: 1
- source_upstream_non_json_response: 1

## Failed Stages

- [search] 起点读书（优+++） (src_ba56c1b5c646467e91a9e28a) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://api-x.shrtxs.cn/qd/search.php?keyword=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1": net/http: TLS handshake timeout
- [info] 番茄小说（优+++） (src_e13960f70d9807554ed4ae9f) [source_upstream_empty_json_response]: script_failed: e2e: SyntaxError: Unexpected end of JSON input (EOF) at parse (native)
- [search] 天天看书（优+++） (src_2e69e222325a50ab5fcacc3a) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [search] 得奇小说（优+++） (src_e65b7afd81062e9b55ab40f1) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://www.deqibook.com/tag/?key=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": tls: failed to verify certificate: x509: certificate signed by unknown authority
- [search] 篱笆文学（优+++） (src_f4cb64d3ffc3a27ef0d0d39c) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [search] 我的书城（优++） (src_525b28d5f4d56bd8e94e9911) [network_dns_failure]: empty search result; warnings: search_failed: Post "https://wodushu.com/search/": dial tcp: lookup wodushu.com on 223.5.5.5:53: server misbehaving
- [toc] 八零小说（优++） (src_9e9340fd762b536ea8e2bf42) [source_toc_empty]: toc_empty
- [toc] 猫眼看书（优++） (src_e86c92fc3572d5388ba96a75) [source_toc_empty]: toc_empty
- [search] 鲸云轻说（优++） (src_556c281e6af98e47021261b4) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://jyapi.jyacg.com/web/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1&type=1": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-10-14T18:06:39+08:00 is after 2026-02-10T15:32:03Z
- [search] 神凑轻说（优++） (src_acd7f6523e1a63a37769680b) [network_connection_reset]: empty search result; warnings: search_failed: Get "http://www.shencou.com/modules/article/search.php?searchtype=articlename+selected&searchkey=%B6%B7%C6%C6%B2%D4%F1%B7&page=1": dial tcp 0.0.0.0:80: connect: connection refused
- [info] 轻之文库（优++） (src_a554dc47ce3ac7aad51243ca) [network_timeout]: Get "https://www.linovel.net/book/103303.html": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
- [search] 轻之文库（优++） (src_6a60f43b6a01c5cd426ee7dc) [network_timeout]: empty search result; warnings: search_failed: Get "https://www.linovel.net/search/?kw=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
- [search] 话本小说（优++） (src_e80901f6c7a641a9c6e88eae) [source_search_empty_result]: empty search result
- [content] 全本小说（优++） (src_1d2910b6b9aa99e5188a9965) [network_connection_reset]: Post "http://119.45.176.116:5006/chapterContent": EOF
- [search] 番薯小说（优++） (src_8c4cda972439a6c3e8125fbd) [network_dns_failure]: empty search result; warnings: search_failed: Post "https://ggs.manmeng168.com/v1/search/do_search": dial tcp: lookup ggs.manmeng168.com on 202.118.1.29:53: no such host
- [search] 番茄小说（优++） (src_802a30ceb32c39a84c74e34b) [network_dns_failure]: empty search result; warnings: search_failed: Get "https://fanqie.hnxianxin.cn/search?query=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=0&tab_type=3": dial tcp: lookup fanqie.hnxianxin.cn on 202.118.1.29:53: no such host
- [search] 有度中文（优++） (src_d38a730ccbdfbba899b5221e) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [content] 七猫小说（优++） (src_226e9a79d6623f6145729ae8) [source_upstream_non_json_response]: script_failed: e2e: SyntaxError: invalid character 'G' looking for beginning of value at parse (native)
- [search] 风扇枕说（日+） (src_f58fc1c12a7ca4f750da5232) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [search] 番茄小说（优+） (src_548b68eea2029ceef58f7a05) [source_search_empty_result]: empty search result
- [search] 番茄小说（优+） (src_ae48dc2981afcd5dbeea4255) [source_rule_script_failed]: empty search result; warnings: search_failed: script_failed: e2e: TypeError: Cannot read property 'join' of undefined or null at <eval>:4:12(22)
- [info] 番茄小说（优+） (src_f8a3a2e7e8df8d9df945f590) [source_upstream_empty_json_response]: script_failed: e2e: SyntaxError: Unexpected end of JSON input (EOF) at parse (native)
- [search] 有度轻说（优+） (src_818a053544bc9270a08fd51f) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://www.yodu.org/sa/all-%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9-1.html": net/http: TLS handshake timeout
- [search] 轻说机翻（优+） (src_835ebbfd39f44f6aa68a2a63) [source_authorization_failed]: empty search result; warnings: search_failed: unexpected status: 401
- [search] 天天书吧（优+） (src_dfc5e2c079ea84a464b13e25) [source_upstream_http_500]: empty search result; warnings: search_failed: unexpected status: 500
- [search] 天天书吧（优+） (src_1bb8d072276f4ad3708a5d1e) [security_policy_blocked]: empty search result; warnings: search_failed: fetch blocked: unsupported scheme
- [search] 酷匠网吧（优+） (src_f3d1750edb38632a08acb5ff) [source_upstream_empty_json_response]: empty search result; warnings: search_failed: invalid json: unexpected end of JSON input
- [search] 国学書库（优+） (src_fd0d561a90307d803dc40dca) [network_connection_reset]: empty search result; warnings: search_failed: Get "https://book.mywebos.cn/bk/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": EOF
- [search] 国学经典（优+） (src_f1ed7753d419f7b1c84c4aae) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [search] 中华典藏（优+） (src_35b7e7ed637987d3126f0936) [source_search_empty_result]: empty search result
- [search] 中华典藏（优+） (src_0b3ad25c91471566070ebddc) [source_search_empty_result]: empty search result
- [search] 金庸小说（优+） (src_a868a503411048c51d887329) [source_upstream_http_400]: empty search result; warnings: search_failed: unexpected status: 400
- [search] 金庸小说（优+） (src_a311aba6335e1e00f27d7d6a) [source_upstream_http_400]: empty search result; warnings: search_failed: unexpected status: 400
- [search] 盗墓笔记（优+） (src_348a4f1ee3be6f78cfd14bd6) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404
- [search] 盐选文库（优+） (src_324fefedd44d5637bb733eff) [source_search_empty_result]: empty search result
- [search] 盐选文库（优+） (src_fe929c1e56fad544e2dee30d) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404
- [search] 古诗文网（优+） (src_a0345050588428ebb29f8b65) [source_search_empty_result]: empty search result
- [search] 古诗词网（优+） (src_8c754868647d40e20e00fcd6) [source_search_empty_result]: empty search result
- [search] 古诗文网（优+） (src_5072a6eed2bf13219c7df90e) [source_search_empty_result]: empty search result
- [search] 天涯知识（优+） (src_844c516e0716b6d1b0ae021d) [network_connection_reset]: empty search result; warnings: search_failed: Get "http://zhannei.baidu.com/cse/search?s=5140549751047532462&nsid=&q=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&searchtype=title&isNeedCheckDomain=1&jump=1": EOF
- [search] 熊猫文学（优+） (src_85204b4c8ade867fdf3470e6) [source_search_empty_result]: empty search result; warnings: search_failed: Get "https://www.dxmwx.org/list/%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9.html": too many redirects
- [search] 百合爱会（优+） (src_4c0af541a551d1445adec5e6) [source_search_empty_result]: empty search result
- [search] 全本小说（优+） (src_371d847bd9e984de7bdbd525) [source_search_empty_result]: empty search result; warnings: search_failed: Post "https://m.qbxsba.com/modules/article/search.php": http: server gave HTTP response to HTTPS client
- [search] 旭日小说（优+） (src_be6bc224a0d554d82c4ddb39) [source_authorization_failed]: empty search result; warnings: search_failed: unexpected status: 401
- [search] 狸猫故事（优+） (src_b28018fc62ce8d744ffcb9d8) [source_search_empty_result]: empty search result; warnings: search_failed: unexpected status: 503
- [search] 书香阁子（优+） (src_e35e1e2cd1f510c8bf994084) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://www.sxgread.com/s/?sword=%B6%B7%C6%C6%B2%D4%F1%B7": net/http: TLS handshake timeout
- [search] 风云小说（优+） (src_efffd3d64d374730dc38dffb) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403
- [search] 八三中文（优+） (src_94ec0165cde467ca225d0db6) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404
- [search] 阅读库子（优+） (src_8312a4da2e972018e6c1be17) [network_timeout]: empty search result; warnings: search_failed: Get "http://www.yuedsk.com/modules/article/search.php?q=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
- [search] 安读书网（优+） (src_b80cb5f33f6635be64c13a52) [network_dns_failure]: empty search result; warnings: search_failed: Get "https://www.88haoshu.com/sss/?searchkey=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": dial tcp: lookup www.88haoshu.com on 223.5.5.5:53: server misbehaving
- [search] 小米阅读（优+） (src_5a3717c98dc35415a2e67187) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404
- [search] 小米书城（优+） (src_7956897ace18c66b4b8e5c47) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404
- [search] 神凑轻说（优+） (src_ed5d11ff9ad445ffef371bc9) [network_connection_reset]: empty search result; warnings: search_failed: Post "https://m.shencou.com/pserchs.php": dial tcp 0.0.0.0:443: connect: connection refused
- [search] 鲸云轻说（优+） (src_a38faf7e5285445cef87a57c) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://jyapi.jyacg.com/web/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1&type=1": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-10-14T18:15:39+08:00 is after 2026-02-10T15:32:03Z
- [search] 鲸云轻说（优+） (src_ded92cad885a53cc49f5aacc) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://jyapi.jyacg.com/web/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1&type=1": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-10-14T18:15:40+08:00 is after 2026-02-10T15:32:03Z
- [search] 炫动小说（优+） (src_f6eb03cad223579556ead442) [source_search_empty_result]: empty search result; warnings: search_failed: script_timeout: e2e: script_timeout at <eval>:4:12(22)
- [search] 书法小说（优+） (src_b737ce7afb3dddca373e755b) [source_search_empty_result]: empty search result
- [search] 爱久久网（优+） (src_cf66bfeee51cfb09c24b1c4c) [source_search_empty_result]: empty search result
- [search] 桔纸书屋（优+） (src_c91c18f448f64e89aa7cb82a) [source_search_empty_result]: empty search result
- [search] 有度中文（优+） (src_2ccb91753b7c52e0802265c3) [network_tls_invalid]: empty search result; warnings: search_failed: Post "https://www.yodu.org/sa": net/http: TLS handshake timeout
- [search] 笔下文学（优+） (src_07f2191f96e9e77274a19da2) [source_search_empty_result]: empty search result
