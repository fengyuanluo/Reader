package script

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/text/encoding/htmlindex"
)

// readerAndroidID 是 java.androidId() 的固定返回值：服务端没有设备 ID，固定值保证同一书源签名稳定。
const readerAndroidID = "reader0000000000"

// Android Base64 flags（android.util.Base64）。
const (
	base64FlagNoPadding = 1
	base64FlagNoWrap    = 2
	base64FlagCRLF      = 4
	base64FlagURLSafe   = 8
)

// base64EncodeFlags 按 Android Base64.encodeToString 的 flags 语义编码；DEFAULT 每 76 字符换行并以换行结尾。
func base64EncodeFlags(data []byte, flags int) string {
	enc := base64.StdEncoding
	if flags&base64FlagURLSafe != 0 {
		enc = base64.URLEncoding
	}
	if flags&base64FlagNoPadding != 0 {
		enc = enc.WithPadding(base64.NoPadding)
	}
	encoded := enc.EncodeToString(data)
	if flags&base64FlagNoWrap != 0 || encoded == "" {
		return encoded
	}
	newline := "\n"
	if flags&base64FlagCRLF != 0 {
		newline = "\r\n"
	}
	var b strings.Builder
	for len(encoded) > 76 {
		b.WriteString(encoded[:76])
		b.WriteString(newline)
		encoded = encoded[76:]
	}
	b.WriteString(encoded)
	b.WriteString(newline)
	return b.String()
}

// base64DecodeLenient 兼容标准/URL 安全字母表、缺省填充与换行；非法输入返回 nil。
func base64DecodeLenient(text string) []byte {
	text = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		case '-':
			return '+'
		case '_':
			return '/'
		}
		return r
	}, text)
	text = strings.TrimRight(text, "=")
	decoded, err := base64.RawStdEncoding.DecodeString(text)
	if err != nil {
		return nil
	}
	return decoded
}

// decodeCharset 把字节按字符集解码；字符集为空时按 UTF-8。
func decodeCharset(data []byte, charsetName string) (string, error) {
	charsetName = strings.TrimSpace(charsetName)
	if charsetName == "" || strings.EqualFold(charsetName, "utf-8") || strings.EqualFold(charsetName, "utf8") {
		return string(data), nil
	}
	enc, err := htmlindex.Get(charsetName)
	if err != nil {
		return "", fmt.Errorf("unsupported charset %s", charsetName)
	}
	decoded, err := enc.NewDecoder().Bytes(data)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// encodeCharset 把字符串按字符集编码；字符集为空时按 UTF-8。
func encodeCharset(text string, charsetName string) ([]byte, error) {
	charsetName = strings.TrimSpace(charsetName)
	if charsetName == "" || strings.EqualFold(charsetName, "utf-8") || strings.EqualFold(charsetName, "utf8") {
		return []byte(text), nil
	}
	enc, err := htmlindex.Get(charsetName)
	if err != nil {
		return nil, fmt.Errorf("unsupported charset %s", charsetName)
	}
	return enc.NewEncoder().Bytes([]byte(text))
}

// md5Encode16 对齐 MD5Utils.md5Encode16：32 位 md5 的中间 16 位。
func md5Encode16(text string) string {
	sum := md5.Sum([]byte(text))
	return hex.EncodeToString(sum[:])[8:24]
}

// timeFormat 对齐 JsExtensions.timeFormat：AppConst.dateFormat（yyyy/MM/dd HH:mm，本地时区）。
func timeFormat(value any) string {
	t, ok := coerceTimeValue(value)
	if !ok {
		return ""
	}
	return t.Local().Format("2006/01/02 15:04")
}

var (
	htmlNbspPattern      = regexp.MustCompile(`(&nbsp;)+`)
	htmlEspPattern       = regexp.MustCompile(`(&ensp;|&emsp;)`)
	htmlNoPrintPattern   = regexp.MustCompile("(&thinsp;|&zwnj;|&zwj;|\u2009|\u200C|\u200D)")
	htmlWrapPattern      = regexp.MustCompile(`</?(?:div|p|br|hr|h\d|article|dd|dl)[^>]*>`)
	htmlCommentPattern   = regexp.MustCompile(`<!--[^>]*-->`)
	htmlTagPattern       = regexp.MustCompile(`</?([a-zA-Z]+)[ >][^<>]*>|</?[a-zA-Z]+>`)
	htmlTagNamePattern   = regexp.MustCompile(`^</?([a-zA-Z]+)`)
	htmlIndent1Pattern   = regexp.MustCompile(`\s*\n+\s*`)
	htmlIndent2Pattern   = regexp.MustCompile(`^[\n\s]+`)
	htmlLastPattern      = regexp.MustCompile(`[\n\s]+$`)
	htmlImagePattern     = regexp.MustCompile(`(?i)<img[^>]*\ssrc\s*=\s*['"]([^'"{>]*\{(?:[^{}]|\{[^}>]+\})+\})['"][^>]*>|<img[^>]*\s(?:data-src|src)\s*=\s*['"]([^'">]+)['"][^>]*>|<img[^>]*\sdata-[^=>]*=\s*['"]([^'">]*)['"][^>]*>`)
	htmlURLParamPattern  = regexp.MustCompile(`\s*,\s*\{`)
	titleNumPattern      = regexp.MustCompile(`(第)(.+?)(章)`)
	whitespaceRunPattern = regexp.MustCompile(`\s+`)
)

// htmlFormatKeepImg 移植 HtmlFormatter.formatKeepImg：去除除 img 外的标签并规范缩进，img 统一为 <img src="...">。
// Go regexp 不支持前瞻，标签名判断改在替换回调里完成。
func htmlFormatKeepImg(html string) string {
	text := htmlNbspPattern.ReplaceAllString(html, " ")
	text = htmlEspPattern.ReplaceAllString(text, " ")
	text = htmlNoPrintPattern.ReplaceAllString(text, "")
	text = htmlWrapPattern.ReplaceAllString(text, "\n")
	text = htmlCommentPattern.ReplaceAllString(text, "")
	text = htmlTagPattern.ReplaceAllStringFunc(text, func(tag string) string {
		name := htmlTagNamePattern.FindStringSubmatch(tag)
		if len(name) == 2 && strings.HasPrefix(name[1], "img") {
			return tag
		}
		return ""
	})
	text = htmlIndent1Pattern.ReplaceAllString(text, "\n　　")
	text = htmlIndent2Pattern.ReplaceAllString(text, "　　")
	text = htmlLastPattern.ReplaceAllString(text, "")
	return htmlImagePattern.ReplaceAllStringFunc(text, func(tag string) string {
		groups := htmlImagePattern.FindStringSubmatch(tag)
		src := ""
		switch {
		case groups[1] != "":
			src = groups[1]
			// 带 URL 选项的地址保留 ",{...}" 原样，与 AnalyzeUrl.paramPattern 拆分后拼回一致。
			if loc := htmlURLParamPattern.FindStringIndex(src); loc != nil {
				src = src[:loc[0]] + "," + src[loc[1]-1:]
			}
		case groups[2] != "":
			src = groups[2]
		default:
			src = groups[3]
		}
		return `<img src="` + src + `">`
	})
}

// toNumChapter 移植 JsExtensions.toNumChapter：把 "第X章" 中的中文/全角数字转为阿拉伯数字。
func toNumChapter(text string) string {
	match := titleNumPattern.FindStringSubmatchIndex(text)
	if match == nil {
		return text
	}
	number := stringToInt(text[match[4]:match[5]])
	return text[:match[0]] + "第" + strconv.Itoa(number) + "章" + text[match[1]:]
}

// stringToInt 移植 StringUtils.stringToInt：先全角转半角并去空白，整数解析失败再按中文数字解析。
func stringToInt(text string) int {
	num := whitespaceRunPattern.ReplaceAllString(fullToHalf(text), "")
	if parsed, err := strconv.ParseInt(num, 10, 32); err == nil {
		return int(parsed)
	}
	return chineseNumToInt(num)
}

func fullToHalf(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == 12288:
			return 32
		case r >= 65281 && r <= 65374:
			return r - 65248
		}
		return r
	}, text)
}

var chineseNumMap = func() map[rune]int {
	m := map[rune]int{}
	for i, r := range []rune("零一二三四五六七八九十") {
		m[r] = i
	}
	for i, r := range []rune("〇壹贰叁肆伍陆柒捌玖拾") {
		m[r] = i
	}
	m['两'] = 2
	m['百'] = 100
	m['佰'] = 100
	m['千'] = 1000
	m['仟'] = 1000
	m['万'] = 10000
	m['亿'] = 100000000
	return m
}()

// chineseNumToInt 移植 StringUtils.chineseNumToInt；含未知字符时返回 -1。
func chineseNumToInt(text string) int {
	cn := []rune(text)
	result, tmp, billion := 0, 0, 0
	for i, r := range cn {
		value, ok := chineseNumMap[r]
		if !ok {
			return -1
		}
		switch {
		case value == 100000000:
			result += tmp
			result *= value
			billion = billion*100000000 + result
			result, tmp = 0, 0
		case value == 10000:
			result += tmp
			result *= value
			tmp = 0
		case value >= 10:
			if tmp == 0 {
				tmp = 1
			}
			result += value * tmp
			tmp = 0
		default:
			if i >= 2 && i == len(cn)-1 && chineseNumMap[cn[i-1]] > 10 {
				tmp = value * chineseNumMap[cn[i-1]] / 10
			} else {
				tmp = tmp*10 + value
			}
		}
	}
	return result + tmp + billion
}

// newJSURL 对应 Legado JsURL：只暴露 host/origin/pathname/searchParams。
func newJSURL(raw string, base string) (map[string]any, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(base) != "" {
		baseURL, err := url.Parse(strings.TrimSpace(base))
		if err != nil {
			return nil, err
		}
		target = baseURL.ResolveReference(target)
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("no protocol: %s", raw)
	}
	origin := target.Scheme + "://" + target.Hostname()
	if port := target.Port(); port != "" {
		origin += ":" + port
	}
	out := map[string]any{
		"host":     target.Hostname(),
		"origin":   origin,
		"pathname": target.EscapedPath(),
	}
	if target.RawQuery != "" || target.ForceQuery {
		params := map[string]string{}
		for _, part := range strings.Split(target.RawQuery, "&") {
			key, value, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			if decoded, err := url.QueryUnescape(value); err == nil {
				value = decoded
			}
			params[key] = value
		}
		out["searchParams"] = params
	} else {
		out["searchParams"] = nil
	}
	return out, nil
}

// isAbsURL 对齐 Legado String.isAbsUrl：仅 http/https 绝对地址。
func isAbsURL(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// cacheFile 对齐 JsExtensions.cacheFile：以 md5Encode16(url) 为键缓存文本内容，saveTime 秒后过期（0 为不过期）。
func (r *Runtime) cacheFile(runCtx evalContext, vm *goja.Runtime, rawURL string, saveTime int64) (string, error) {
	key := "cacheFile:" + md5Encode16(rawURL)
	if text, ok := r.cache.Get(runCtx.sourceKey, key); ok && text != "" {
		return text, nil
	}
	resp, err := r.fetchScriptURL(runCtx, vm, rawURL, nil)
	if err != nil {
		return "", err
	}
	text := string(resp.Body)
	r.log("首次下载 " + rawURL)
	var ttl time.Duration
	if saveTime > 0 {
		ttl = time.Duration(saveTime) * time.Second
	}
	r.cache.PutTTL(runCtx.sourceKey, key, text, ttl)
	return text, nil
}
