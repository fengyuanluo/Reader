package fetcher

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html/charset"

	"reader/internal/domain/fetch"
)

// HTTPFetcher 执行书源 HTTP 请求。
// SSRF 防护在拨号阶段完成：每次建立连接都只解析一次域名，校验全部解析结果后直接连接已校验的 IP，
// 因此 DNS rebinding 与重定向都无法绕过校验；环境代理变量不生效，代理只来自请求自身的 Proxy 字段。
type HTTPFetcher struct {
	timeout          time.Duration
	maxRedirects     int
	defaultUserAgent string
	maxBodyBytes     int64
	allowPrivate     bool
	resolver         func(ctx context.Context, host string) ([]net.IP, error)
	// dial 负责建立到已校验地址的 TCP 连接；测试可替换以观察实际连接目标。
	dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	cookies *CookieManager
	// rateLimiter 按书源 concurrentRate 限速；这是全仓唯一的按源速率限速点。
	rateLimiter *sourceRateLimiter

	mu      sync.Mutex
	clients map[routeKey]*http.Client
}

// routeKey 区分不同出口路由；连接池不感知 DNS 固定与代理，必须按路由隔离 Transport。
type routeKey struct {
	proxy   string
	pinHost string
	pinIP   string
}

type Option func(*HTTPFetcher)

func NewHTTPFetcher(opts ...Option) *HTTPFetcher {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	f := &HTTPFetcher{
		timeout:          30 * time.Second,
		maxRedirects:     5,
		defaultUserAgent: "Reader/0.1.0",
		maxBodyBytes:     10 * 1024 * 1024,
		resolver: func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		},
		dial:        dialer.DialContext,
		clients:     map[routeKey]*http.Client{},
		rateLimiter: newSourceRateLimiter(),
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

func WithTimeout(timeout time.Duration) Option {
	return func(f *HTTPFetcher) {
		f.timeout = timeout
	}
}

func WithMaxRedirects(max int) Option {
	return func(f *HTTPFetcher) {
		f.maxRedirects = max
	}
}

func WithResolver(resolver func(ctx context.Context, host string) ([]net.IP, error)) Option {
	return func(f *HTTPFetcher) {
		f.resolver = resolver
	}
}

func WithCookieManager(manager *CookieManager) Option {
	return func(f *HTTPFetcher) {
		f.cookies = manager
	}
}

func WithDefaultUserAgent(userAgent string) Option {
	return func(f *HTTPFetcher) {
		f.defaultUserAgent = userAgent
	}
}

func WithMaxBodyBytes(max int64) Option {
	return func(f *HTTPFetcher) {
		f.maxBodyBytes = max
	}
}

func WithAllowPrivateNetwork(allow bool) Option {
	return func(f *HTTPFetcher) {
		f.allowPrivate = allow
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	if req.WebViewFallback {
		slog.WarnContext(ctx, "Legado webView request is using plain HTTP fallback")
	}
	// 按源 concurrentRate 限速（远端语义）；SkipRateLimit 的请求直接放行。
	if !req.SkipRateLimit && f.rateLimiter != nil {
		if err := f.rateLimiter.wait(ctx, req.SourceKey, req.ConcurrentRate); err != nil {
			return nil, err
		}
	}
	// 代理与 DNS 固定配置错误是确定性的，在重试循环外解析一次。
	client, err := f.clientFor(req)
	if err != nil {
		return nil, err
	}
	attempts := req.Retry + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastResp *fetch.Response
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		resp, err := f.fetchOnce(ctx, client, req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		lastResp = resp
		lastErr = nil
		if req.AllowNonSuccess || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
			return resp, nil
		}
	}
	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

// clientFor 按请求的代理 / DNS 固定配置返回缓存的 http.Client。
func (f *HTTPFetcher) clientFor(req fetch.Request) (*http.Client, error) {
	var key routeKey
	var proxyURL *url.URL
	var pinIP net.IP
	if raw := strings.TrimSpace(req.Proxy); raw != "" {
		if strings.TrimSpace(req.DNSIP) != "" {
			return nil, errors.New("proxy and dnsIp cannot be combined")
		}
		parsed, err := parseProxy(raw)
		if err != nil {
			return nil, err
		}
		proxyURL = parsed
		key.proxy = parsed.String()
	}
	if raw := strings.TrimSpace(req.DNSIP); raw != "" {
		pinIP = net.ParseIP(raw)
		if pinIP == nil {
			return nil, fmt.Errorf("invalid dnsIp: %q", raw)
		}
		parsed, err := url.Parse(req.URL)
		if err != nil {
			return nil, err
		}
		key.pinHost = strings.ToLower(parsed.Hostname())
		key.pinIP = pinIP.String()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if client, ok := f.clients[key]; ok {
		return client, nil
	}
	transport := &http.Transport{
		// 显式不读取 HTTP(S)_PROXY 环境变量，避免环境代理改变实际连接目标。
		Proxy:                 nil,
		DialContext:           f.dialContext(key.pinHost, pinIP),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if proxyURL != nil {
		transport.Proxy = f.proxyFunc(proxyURL)
	}
	client := &http.Client{Transport: transport, Timeout: f.timeout, CheckRedirect: f.checkRedirect}
	f.clients[key] = client
	return client, nil
}

func (f *HTTPFetcher) checkRedirect(_ *http.Request, via []*http.Request) error {
	if len(via) >= f.maxRedirects {
		return errors.New("too many redirects")
	}
	// 重定向目标会重新拨号，SSRF 校验由 dialContext / proxyFunc 完成。
	return nil
}

// dialContext 返回在连接前校验实际目标 IP 的拨号函数。pinHost 非空时该主机固定连接 pinIP（dnsIp 选项），
// Host 头与 TLS SNI 仍取自 URL。代理模式下拨号目标是代理服务器本身，同样受 SSRF 策略约束。
func (f *HTTPFetcher) dialContext(pinHost string, pinIP net.IP) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		var ips []net.IP
		if pinIP != nil && strings.EqualFold(host, pinHost) {
			ips = []net.IP{pinIP}
			if err := f.checkIPs(ips); err != nil {
				return nil, err
			}
		} else if ips, err = f.allowedIPs(ctx, host); err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := f.dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// proxyFunc 在每一跳（含重定向）发送前校验目标主机。代理负责远端 DNS 解析，
// 此处的本地解析校验只能尽力而为，无法排除代理侧解析结果与本地不同。
func (f *HTTPFetcher) proxyFunc(proxyURL *url.URL) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if f.allowPrivate {
			return proxyURL, nil
		}
		if _, err := f.allowedIPs(req.Context(), req.URL.Hostname()); err != nil {
			return nil, err
		}
		return proxyURL, nil
	}
}

// allowedIPs 解析主机并在禁止私网时校验全部结果；任一地址被封禁即拒绝。
func (f *HTTPFetcher) allowedIPs(ctx context.Context, host string) ([]net.IP, error) {
	if strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("%w: empty host", fetch.ErrBlocked)
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := f.resolver(ctx, host)
		if err != nil {
			return nil, err
		}
		ips = resolved
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses for host %s", host)
	}
	if err := f.checkIPs(ips); err != nil {
		return nil, err
	}
	return ips, nil
}

func (f *HTTPFetcher) checkIPs(ips []net.IP) error {
	if f.allowPrivate {
		return nil
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: private or reserved address %s", fetch.ErrBlocked, ip.String())
		}
	}
	return nil
}

// legadoProxyPattern 匹配 Legado 代理写法 scheme://host:port@user@pass（HttpHelper.kt getProxyClient）。
var legadoProxyPattern = regexp.MustCompile(`^(?i)([a-z0-9]+)://([^/@]+):(\d{1,5})@([^@]*)@(.*)$`)

// parseProxy 解析书源代理配置；同时支持 Legado 的 @user@pass 写法与标准 userinfo 写法。
// 返回的错误不回显原始配置，避免凭证进入日志。
func parseProxy(raw string) (*url.URL, error) {
	var proxyURL *url.URL
	if m := legadoProxyPattern.FindStringSubmatch(raw); m != nil {
		proxyURL = &url.URL{Scheme: strings.ToLower(m[1]), Host: net.JoinHostPort(strings.Trim(m[2], "[]"), m[3])}
		if m[4] != "" && m[5] != "" {
			proxyURL.User = url.UserPassword(m[4], m[5])
		}
	} else {
		parsed, err := url.Parse(raw)
		if err != nil {
			return nil, errors.New("invalid proxy: malformed url")
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		proxyURL = parsed
	}
	switch proxyURL.Scheme {
	case "http", "https", "socks5", "socks5h":
	case "socks4":
		return nil, errors.New("invalid proxy: socks4 is not supported, use socks5")
	default:
		return nil, fmt.Errorf("invalid proxy: unsupported scheme %q", proxyURL.Scheme)
	}
	if proxyURL.Hostname() == "" || proxyURL.Port() == "" {
		return nil, errors.New("invalid proxy: host and port are required")
	}
	return proxyURL, nil
}

func (f *HTTPFetcher) fetchOnce(ctx context.Context, client *http.Client, req fetch.Request) (*fetch.Response, error) {
	parsed, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "data" {
		body, err := decodeDataURI(req.URL)
		if err != nil {
			return nil, err
		}
		if req.EncodeBodyAsHex {
			body = []byte(hex.EncodeToString(body))
		}
		return &fetch.Response{StatusCode: 200, Headers: fetch.Headers{}, Body: body, URL: req.URL}, nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: unsupported scheme", fetch.ErrBlocked)
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	targetURL := req.URL
	if len(req.Query) > 0 {
		parsed.RawQuery = mergeQuery(parsed.Query(), req.Query)
		targetURL = parsed.String()
	}
	var body io.Reader
	// 与 Legado AnalyzeUrl 一致：GET（及 HEAD）只发送 URL 与查询串，忽略 body 选项。
	if len(req.Body) > 0 && method != http.MethodGet && method != http.MethodHead {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return nil, err
	}
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}
	if req.UseCookieJar && f.cookies != nil {
		cookie, err := f.cookies.Load(ctx, targetURL, httpReq.Header.Get("Cookie"))
		if err != nil {
			return nil, err
		}
		if cookie != "" {
			httpReq.Header.Set("Cookie", cookie)
		}
	}
	if req.RequestWithoutUA {
		httpReq.Header["User-Agent"] = []string{""}
	} else if f.defaultUserAgent != "" && httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", f.defaultUserAgent)
	}
	if httpReq.Header.Get("Accept-Encoding") == "" {
		httpReq.Header.Set("Accept-Encoding", "gzip")
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	reader := resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	respBody, err := io.ReadAll(io.LimitReader(reader, f.maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if resp.Header.Get("Content-Encoding") != "gzip" && hasGzipMagic(respBody) {
		gzipReader, err := gzip.NewReader(bytes.NewReader(respBody))
		if err != nil {
			return nil, err
		}
		respBody, err = io.ReadAll(io.LimitReader(gzipReader, f.maxBodyBytes+1))
		_ = gzipReader.Close()
		if err != nil {
			return nil, err
		}
	}
	if int64(len(respBody)) > f.maxBodyBytes {
		return nil, errors.New("response body too large")
	}
	if !req.EncodeBodyAsHex {
		if decoded, err := decodeTextResponseBody(respBody, resp.Header.Get("Content-Type"), f.maxBodyBytes); err == nil {
			respBody = decoded
		}
	}
	if req.EncodeBodyAsHex {
		respBody = []byte(hex.EncodeToString(respBody))
	}
	if !req.AllowNonSuccess && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	if req.UseCookieJar && f.cookies != nil {
		if err := f.cookies.Save(ctx, resp.Request.URL.String(), resp.Header); err != nil {
			return nil, err
		}
	}
	// domain 层不依赖 net/http：在适配层把传输层响应头转成 fetch.Headers。
	return &fetch.Response{StatusCode: resp.StatusCode, Headers: fetch.Headers(resp.Header), Body: respBody, URL: resp.Request.URL.String()}, nil
}

func hasGzipMagic(body []byte) bool {
	return len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b
}

func decodeTextResponseBody(body []byte, contentType string, maxBodyBytes int64) ([]byte, error) {
	if strings.TrimSpace(contentType) == "" {
		// 缺少 Content-Type 时按 Legado OkHttpUtils.text 的回退思路探测 BOM / HTML meta 声明的编码；
		// 探测不到时保持原字节，避免误伤二进制响应。
		label := sniffCharset(body)
		if label == "" {
			return body, nil
		}
		contentType = "text/html; charset=" + label
	} else if !looksLikeTextContent(contentType) {
		return body, nil
	}
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return body, err
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, maxBodyBytes+1))
	if err != nil {
		return body, err
	}
	if int64(len(decoded)) > maxBodyBytes {
		return nil, errors.New("response body too large")
	}
	return decoded, nil
}

func looksLikeTextContent(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(contentType, "text/"):
		return true
	case strings.HasPrefix(contentType, "application/json"):
		return true
	case strings.HasPrefix(contentType, "application/xml"):
		return true
	case strings.HasPrefix(contentType, "application/xhtml+xml"):
		return true
	case strings.HasPrefix(contentType, "application/javascript"):
		return true
	case strings.HasPrefix(contentType, "application/x-javascript"):
		return true
	default:
		return false
	}
}

// charsetSniffLimit 限制 meta 探测范围，编码声明按 HTML 规范应位于文档开头。
const charsetSniffLimit = 4096

var metaCharsetPattern = regexp.MustCompile(`(?is)<meta[^>]*?charset\s*=\s*["']?\s*([a-z0-9_.:\-]+)`)

// sniffCharset 依次检查 BOM 与文档开头的 <meta charset> / http-equiv 声明，返回可识别的编码名。
func sniffCharset(body []byte) string {
	switch {
	case bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}):
		return "utf-8"
	case bytes.HasPrefix(body, []byte{0xFE, 0xFF}):
		return "utf-16be"
	case bytes.HasPrefix(body, []byte{0xFF, 0xFE}):
		return "utf-16le"
	}
	head := body
	if len(head) > charsetSniffLimit {
		head = head[:charsetSniffLimit]
	}
	m := metaCharsetPattern.FindSubmatch(head)
	if m == nil {
		return ""
	}
	label := strings.ToLower(string(m[1]))
	if enc, _ := charset.Lookup(label); enc == nil {
		return ""
	}
	return label
}

func mergeQuery(base url.Values, extra map[string]string) string {
	for key, value := range extra {
		base.Set(key, value)
	}
	return base.Encode()
}

func decodeDataURI(raw string) ([]byte, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	data := parsed.Opaque
	if data == "" {
		data = strings.TrimPrefix(raw, "data:")
	}
	parts := strings.SplitN(data, ",", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid data uri")
	}
	meta, payload := parts[0], parts[1]
	if strings.HasSuffix(strings.ToLower(meta), ";base64") {
		return base64.StdEncoding.DecodeString(payload)
	}
	decoded, err := url.QueryUnescape(payload)
	if err != nil {
		return nil, err
	}
	return []byte(decoded), nil
}

// blockedPrefixes 是标准库谓词未覆盖的特殊用途地址段（RFC 6890 等）。
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fec0::/10"),
}

func isBlockedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
