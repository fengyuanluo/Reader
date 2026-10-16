# Real Source Collection E2E Report

- Test time: 2026-10-16T07:57:53Z
- Collection URL: https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361
- HTTP status: 200
- Response bytes: 4701554
- Parsed count: 459
- Max candidates: 100
- Attempted candidates: 100
- Full success count: 41
- Keyword: 斗破苍穹
- Release allowed: true
- Failure class: 

## Stage Summary

- search: passed=47, failed=53
- info: passed=45, failed=2
- toc: passed=43, failed=2
- content: passed=41, failed=2

## Failure Summary

- source_search_empty_result: 16 (traceId: 3c99d9d51f16d937c6b5f6af83e9248b, 14dee7de76befee7f99fd81b5085f41e, 58f7c42324f82a2bddb3615524199138, 2eac99ea4e167f2f5774ac3b8acc1d3b, b3170ecf40bf0415a5716814efea5b99, a32b02f5535fa54174372da6b57f78aa, a3f8714b697dd2dce3f35758d8c67d9e, a8845e5a2b522ee6c13882e95ba78b0a, c704ddb1858863d295407536fdf9ab31, a527d1fbb4660fe19084a4963c99758e, 2e122d3b351476670132e21550d38067, 101382536dda233c2f0b373b215c38b4, e99882f22cc6ad834fd74691ff2e8844, 89e147decd1ff2938fb2c91c09e847b2, 9e76a5643bf168904791b9484014eb63, d68f716c4c4ba9ad32e15761c18a8af5)
- network_tls_invalid: 11 (traceId: e79de6f31380eba75395b6f76843b89a, 3992139201fe16ca8950ded49541befd, 6f8ffc50f602d60ae6f57167a8d9aac9, bfb86394272d15ecc905981c35e565a6, 6a2bfb075a5d8a4ee43917b76dff3e0f, 6e5fc61508f5fdda180c771d44d8fa05, d4a1479cddcd240d93563f9e4fa540b0, 4705109437c614cdca7339ac4ab86ec7, cdc942c88d7498bb48c713f854e0e88c, 23ac6a604c6df094c703601c6dedbe29, 29419053b88c4adc37903b9b5c9bff3f)
- source_upstream_http_403: 7 (traceId: 68251a1f1d5973057a58fb8353c39fa1, 43588ab55637af34c0dbf7ea32979706, f6b4fd7203e78fe1d2a5ab2b422cfd39, cad0a22ae7518c129a7917523f724c7f, eb7239dfbc39a591aabb0f70f6cfb60f, 14e87d7cfa39619ffb6c620f1b5c7058, 8c0da7ed3182ddf5825c8cbb94a4ef70)
- network_dns_failure: 4 (traceId: a52b39a1c1e58a7ac82d03ff0f0fbc01, e7f45e65ceccba90c7f0043230350369, 4d0ce10b4fa90b1d9744e3cd82701080, 3211af85f61d56be389a8e87ef3b27c6)
- source_upstream_http_404: 4 (traceId: 28b2ac72f8bc02a12aed348e903c81cf, 508791c674a9d3af8b1846032f4f4f68, e765537bf8c136d6644d3cb24a658284, 53baf8d59ee614d231f5315a91f4e7a8)
- network_connection_reset: 3 (traceId: 85f40b7da36c37ae48ebc76535ddfe3b, c9ab3b318c9f8bfbf48c7f7f12b184f3, 3770233705d29eaf3882442e0bb7732f)
- source_upstream_empty_json_response: 3 (traceId: d5027b4d96a2cb2e4db393d0764579a4, 219ce1b5a676fa90c5ef5743100beb6f, f4e0e9735a5fa8159d33609b01a9de46)
- source_authorization_failed: 2 (traceId: ffcbeb3d0496733aa13fd0902b745dfe, eb2ba6f9163005d1fb32733b93e425fd)
- source_toc_empty: 2 (traceId: 9656c005190a445777908d574ac68fa5, ffe22815653931ee58bd65c2aa7c1732)
- source_upstream_http_400: 2 (traceId: 7f8b4772d85a3dcebf75c286f2f74802, 20feb951edf4b0652b9f0609e6b788d9)
- network_timeout: 1 (traceId: 463a0aacd4720dd3dd10a2d4cc4d5cf6)
- security_policy_blocked: 1 (traceId: 6d7d2317ccb4c3d04a473919010957bf)
- source_rule_script_failed: 1 (traceId: 25ba11de112c0c07c6aa91aab4f9b680)
- source_upstream_http_500: 1 (traceId: 6bc9602a9e7b671dfb9410042447b980)
- source_upstream_non_json_response: 1 (traceId: c4823ade6b76afaab7b456f8efa7d869)

## Failed Stages

- [search] 起点读书（优+++） (src_ba56c1b5c646467e91a9e28a) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://api-x.shrtxs.cn/qd/search.php?keyword=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1": net/http: TLS handshake timeout (traceId: e79de6f31380eba75395b6f76843b89a)
- [info] 番茄小说（优+++） (src_e13960f70d9807554ed4ae9f) [source_upstream_empty_json_response]: script_failed: stage=info source=src_e13960f70d9807554ed4ae9f: SyntaxError: Unexpected end of JSON input (EOF) at parse (native) (traceId: d5027b4d96a2cb2e4db393d0764579a4)
- [search] 天天看书（优+++） (src_2e69e222325a50ab5fcacc3a) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403 (traceId: 68251a1f1d5973057a58fb8353c39fa1)
- [search] 得奇小说（优+++） (src_e65b7afd81062e9b55ab40f1) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://www.deqibook.com/tag/?key=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": tls: failed to verify certificate: x509: certificate signed by unknown authority (traceId: 3992139201fe16ca8950ded49541befd)
- [search] 篱笆文学（优+++） (src_f4cb64d3ffc3a27ef0d0d39c) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403 (traceId: 43588ab55637af34c0dbf7ea32979706)
- [search] 我的书城（优++） (src_525b28d5f4d56bd8e94e9911) [network_dns_failure]: empty search result; warnings: search_failed: Post "https://wodushu.com/search/": lookup wodushu.com on 223.5.5.5:53: server misbehaving (traceId: a52b39a1c1e58a7ac82d03ff0f0fbc01)
- [search] 八零小说（优++） (src_9e9340fd762b536ea8e2bf42) [source_search_empty_result]: empty search result (traceId: 3c99d9d51f16d937c6b5f6af83e9248b)
- [toc] 猫眼看书（优++） (src_e86c92fc3572d5388ba96a75) [source_toc_empty]: toc_empty (traceId: 9656c005190a445777908d574ac68fa5)
- [search] 鲸云轻说（优++） (src_556c281e6af98e47021261b4) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://jyapi.jyacg.com/web/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1&type=1": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-10-16T15:59:09+08:00 is after 2026-02-10T15:32:03Z (traceId: 6f8ffc50f602d60ae6f57167a8d9aac9)
- [search] 神凑轻说（优++） (src_acd7f6523e1a63a37769680b) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403 (traceId: f6b4fd7203e78fe1d2a5ab2b422cfd39)
- [search] 话本小说（优++） (src_e80901f6c7a641a9c6e88eae) [source_search_empty_result]: empty search result (traceId: 14dee7de76befee7f99fd81b5085f41e)
- [content] 全本小说（优++） (src_1d2910b6b9aa99e5188a9965) [network_connection_reset]: Post "http://119.45.176.116:5006/chapterContent": EOF (traceId: 85f40b7da36c37ae48ebc76535ddfe3b)
- [search] 番薯小说（优++） (src_8c4cda972439a6c3e8125fbd) [network_dns_failure]: empty search result; warnings: search_failed: Post "https://ggs.manmeng168.com/v1/search/do_search": lookup ggs.manmeng168.com on 202.118.1.29:53: no such host (traceId: e7f45e65ceccba90c7f0043230350369)
- [search] 番茄小说（优++） (src_802a30ceb32c39a84c74e34b) [network_dns_failure]: empty search result; warnings: search_failed: Get "https://fanqie.hnxianxin.cn/search?query=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=0&tab_type=3": lookup fanqie.hnxianxin.cn on 202.118.1.29:53: no such host (traceId: 4d0ce10b4fa90b1d9744e3cd82701080)
- [search] 有度中文（优++） (src_d38a730ccbdfbba899b5221e) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403 (traceId: cad0a22ae7518c129a7917523f724c7f)
- [content] 七猫小说（优++） (src_226e9a79d6623f6145729ae8) [source_upstream_non_json_response]: script_failed: stage=content source=src_226e9a79d6623f6145729ae8: SyntaxError: invalid character 'G' looking for beginning of value at parse (native) (traceId: c4823ade6b76afaab7b456f8efa7d869)
- [search] 番茄小说（优+） (src_548b68eea2029ceef58f7a05) [source_search_empty_result]: empty search result (traceId: 58f7c42324f82a2bddb3615524199138)
- [search] 番茄小说（优+） (src_ae48dc2981afcd5dbeea4255) [source_rule_script_failed]: empty search result; warnings: search_failed: script_failed: stage=search source=src_ae48dc2981afcd5dbeea4255: TypeError: Cannot read property 'join' of undefined or null at <eval>:4:12(22) (traceId: 25ba11de112c0c07c6aa91aab4f9b680)
- [info] 番茄小说（优+） (src_f8a3a2e7e8df8d9df945f590) [source_upstream_empty_json_response]: script_failed: stage=info source=src_f8a3a2e7e8df8d9df945f590: SyntaxError: Unexpected end of JSON input (EOF) at parse (native) (traceId: 219ce1b5a676fa90c5ef5743100beb6f)
- [search] 有度轻说（优+） (src_818a053544bc9270a08fd51f) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://www.yodu.org/sa/all-%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9-1.html": net/http: TLS handshake timeout (traceId: bfb86394272d15ecc905981c35e565a6)
- [search] 轻说机翻（优+） (src_835ebbfd39f44f6aa68a2a63) [source_authorization_failed]: empty search result; warnings: search_failed: unexpected status: 401 (traceId: ffcbeb3d0496733aa13fd0902b745dfe)
- [search] 天天书吧（优+） (src_dfc5e2c079ea84a464b13e25) [source_upstream_http_500]: empty search result; warnings: search_failed: unexpected status: 500 (traceId: 6bc9602a9e7b671dfb9410042447b980)
- [search] 天天书吧（优+） (src_1bb8d072276f4ad3708a5d1e) [security_policy_blocked]: empty search result; warnings: search_failed: fetch blocked: unsupported scheme (traceId: 6d7d2317ccb4c3d04a473919010957bf)
- [search] 阅友小说（优+） (src_be2d391cdcf230b310be3ecb) [network_timeout]: empty search result; warnings: search_failed: Get "http://sma.yueyouxs.com/api/book/search?keyword=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) (traceId: 463a0aacd4720dd3dd10a2d4cc4d5cf6)
- [search] 酷匠网吧（优+） (src_f3d1750edb38632a08acb5ff) [source_upstream_empty_json_response]: empty search result; warnings: search_failed: invalid json: unexpected end of JSON input (traceId: f4e0e9735a5fa8159d33609b01a9de46)
- [search] 国学書库（优+） (src_fd0d561a90307d803dc40dca) [network_connection_reset]: empty search result; warnings: search_failed: Get "https://book.mywebos.cn/bk/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": EOF (traceId: c9ab3b318c9f8bfbf48c7f7f12b184f3)
- [search] 国学经典（优+） (src_f1ed7753d419f7b1c84c4aae) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403 (traceId: eb7239dfbc39a591aabb0f70f6cfb60f)
- [search] 中华典藏（优+） (src_35b7e7ed637987d3126f0936) [network_tls_invalid]: empty search result; warnings: search_failed: Post "https://www.diancang.xyz/e/search/index.php": net/http: TLS handshake timeout (traceId: 6a2bfb075a5d8a4ee43917b76dff3e0f)
- [search] 中华典藏（优+） (src_0b3ad25c91471566070ebddc) [network_tls_invalid]: empty search result; warnings: search_failed: Post "https://www.diancang.xyz/e/search/index.php": net/http: TLS handshake timeout (traceId: 6e5fc61508f5fdda180c771d44d8fa05)
- [search] 中华典藏（优+） (src_fb904277e1befe2368640f00) [network_tls_invalid]: empty search result; warnings: search_failed: Post "https://www.diancang.xyz/e/search/index.php": net/http: TLS handshake timeout (traceId: d4a1479cddcd240d93563f9e4fa540b0)
- [search] 金庸小说（优+） (src_a868a503411048c51d887329) [source_upstream_http_400]: empty search result; warnings: search_failed: unexpected status: 400 (traceId: 7f8b4772d85a3dcebf75c286f2f74802)
- [search] 金庸小说（优+） (src_a311aba6335e1e00f27d7d6a) [source_upstream_http_400]: empty search result; warnings: search_failed: unexpected status: 400 (traceId: 20feb951edf4b0652b9f0609e6b788d9)
- [search] 盗墓笔记（优+） (src_348a4f1ee3be6f78cfd14bd6) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404 (traceId: 28b2ac72f8bc02a12aed348e903c81cf)
- [search] 盐选文库（优+） (src_324fefedd44d5637bb733eff) [source_search_empty_result]: empty search result (traceId: 2eac99ea4e167f2f5774ac3b8acc1d3b)
- [search] 盐选文库（优+） (src_fe929c1e56fad544e2dee30d) [source_search_empty_result]: empty search result (traceId: b3170ecf40bf0415a5716814efea5b99)
- [search] 古诗文网（优+） (src_a0345050588428ebb29f8b65) [source_search_empty_result]: empty search result (traceId: a32b02f5535fa54174372da6b57f78aa)
- [search] 古诗词网（优+） (src_8c754868647d40e20e00fcd6) [source_search_empty_result]: empty search result (traceId: a3f8714b697dd2dce3f35758d8c67d9e)
- [search] 古诗文网（优+） (src_5072a6eed2bf13219c7df90e) [source_search_empty_result]: empty search result (traceId: a8845e5a2b522ee6c13882e95ba78b0a)
- [toc] 天涯知识（优+） (src_844c516e0716b6d1b0ae021d) [source_toc_empty]: toc_empty (traceId: ffe22815653931ee58bd65c2aa7c1732)
- [search] 熊猫文学（优+） (src_85204b4c8ade867fdf3470e6) [source_search_empty_result]: empty search result; warnings: search_failed: Get "https://www.dxmwx.org/list/%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9.html": too many redirects (traceId: c704ddb1858863d295407536fdf9ab31)
- [search] 百合爱会（优+） (src_4c0af541a551d1445adec5e6) [source_search_empty_result]: empty search result (traceId: a527d1fbb4660fe19084a4963c99758e)
- [search] 全本小说（优+） (src_371d847bd9e984de7bdbd525) [source_search_empty_result]: empty search result; warnings: search_failed: Post "https://m.qbxsba.com/modules/article/search.php": http: server gave HTTP response to HTTPS client (traceId: 2e122d3b351476670132e21550d38067)
- [search] 旭日小说（优+） (src_be6bc224a0d554d82c4ddb39) [source_authorization_failed]: empty search result; warnings: search_failed: unexpected status: 401 (traceId: eb2ba6f9163005d1fb32733b93e425fd)
- [search] 狸猫故事（优+） (src_b28018fc62ce8d744ffcb9d8) [source_search_empty_result]: empty search result; warnings: search_failed: unexpected status: 503 (traceId: 101382536dda233c2f0b373b215c38b4)
- [search] 书香阁子（优+） (src_e35e1e2cd1f510c8bf994084) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://www.sxgread.com/s/?sword=%B6%B7%C6%C6%B2%D4%F1%B7": net/http: TLS handshake timeout (traceId: 4705109437c614cdca7339ac4ab86ec7)
- [search] 风云小说（优+） (src_efffd3d64d374730dc38dffb) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403 (traceId: 14e87d7cfa39619ffb6c620f1b5c7058)
- [search] 八三中文（优+） (src_94ec0165cde467ca225d0db6) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404 (traceId: 508791c674a9d3af8b1846032f4f4f68)
- [search] 阅读库子（优+） (src_8312a4da2e972018e6c1be17) [network_connection_reset]: empty search result; warnings: search_failed: Get "http://www.yuedsk.com/modules/article/search.php?q=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": EOF (traceId: 3770233705d29eaf3882442e0bb7732f)
- [search] 安读书网（优+） (src_b80cb5f33f6635be64c13a52) [network_dns_failure]: empty search result; warnings: search_failed: Get "https://www.88haoshu.com/sss/?searchkey=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": lookup www.88haoshu.com on 223.5.5.5:53: server misbehaving (traceId: 3211af85f61d56be389a8e87ef3b27c6)
- [search] 小米阅读（优+） (src_5a3717c98dc35415a2e67187) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404 (traceId: e765537bf8c136d6644d3cb24a658284)
- [search] 小米书城（优+） (src_7956897ace18c66b4b8e5c47) [source_upstream_http_404]: empty search result; warnings: search_failed: unexpected status: 404 (traceId: 53baf8d59ee614d231f5315a91f4e7a8)
- [search] 神凑轻说（优+） (src_ed5d11ff9ad445ffef371bc9) [source_upstream_http_403]: empty search result; warnings: search_failed: unexpected status: 403 (traceId: 8c0da7ed3182ddf5825c8cbb94a4ef70)
- [search] 鲸云轻说（优+） (src_a38faf7e5285445cef87a57c) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://jyapi.jyacg.com/web/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1&type=1": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-10-16T16:07:50+08:00 is after 2026-02-10T15:32:03Z (traceId: cdc942c88d7498bb48c713f854e0e88c)
- [search] 鲸云轻说（优+） (src_ded92cad885a53cc49f5aacc) [network_tls_invalid]: empty search result; warnings: search_failed: Get "https://jyapi.jyacg.com/web/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1&type=1": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-10-16T16:07:50+08:00 is after 2026-02-10T15:32:03Z (traceId: 23ac6a604c6df094c703601c6dedbe29)
- [search] 炫动小说（优+） (src_f6eb03cad223579556ead442) [source_search_empty_result]: empty search result; warnings: search_failed: script_timeout: stage=search source=src_f6eb03cad223579556ead442: script_timeout at <eval>:4:12(22) (traceId: e99882f22cc6ad834fd74691ff2e8844)
- [search] 爱久久网（优+） (src_cf66bfeee51cfb09c24b1c4c) [source_search_empty_result]: empty search result (traceId: 89e147decd1ff2938fb2c91c09e847b2)
- [search] 桔纸书屋（优+） (src_c91c18f448f64e89aa7cb82a) [source_search_empty_result]: empty search result (traceId: 9e76a5643bf168904791b9484014eb63)
- [search] 有度中文（优+） (src_2ccb91753b7c52e0802265c3) [network_tls_invalid]: empty search result; warnings: search_failed: Post "https://www.yodu.org/sa": net/http: TLS handshake timeout (traceId: 29419053b88c4adc37903b9b5c9bff3f)
- [search] 笔下文学（优+） (src_07f2191f96e9e77274a19da2) [source_search_empty_result]: empty search result (traceId: d68f716c4c4ba9ad32e15761c18a8af5)
