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
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/transform"

	"reader/internal/domain/fetch"
)

// HTTPFetcher 执行书源 HTTP 请求。
// SSRF 防护在拨号阶段完成：每次建立连接只解析一次域名（结果按 host 缓存 dnsCacheTTL），校验全部解析结果后
// 才连接已校验的 IP，因此重定向无法绕过校验；缓存只存解析结果，每次拨号仍对当次使用的 IP 重新校验，
// 不缓存允许/拒绝结论。环境代理变量不生效，代理只来自请求自身的 Proxy 字段。
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
	// transports 记录每个路由对应的 Transport，路由被淘汰时回收其空闲连接（S08-12）。
	transports map[routeKey]*http.Transport
	// clientOrder 记录 client 插入顺序，超出 maxRouteClients 时按最旧淘汰。
	clientOrder []routeKey

	// dnsCache 按 host 缓存 DNS 解析结果（TTL dnsCacheTTL），避免每条新连接重复解析（C-024/C-025）。
	// 只缓存解析结果：每次拨号仍对当次使用的 IP 重新执行 checkIPs 校验，SSRF 语义不变。
	dnsCacheMu sync.Mutex
	dnsCache   map[string]dnsCacheEntry
	// resolveInflight 合并同一 host 的并发解析（S08-11），避免缓存 miss 时重复发起 DNS 查询。
	resolveInflight map[string]*resolveCall
}

// resolveCall 表示一次进行中的 DNS 解析；并发调用者共享同一结果。
type resolveCall struct {
	done chan struct{}
	ips  []net.IP
	err  error
}

// maxRouteClients 限制按路由缓存的客户端数量，防止代理路由组合无界增长（S08-12）。
const maxRouteClients = 64

// maxDNSCacheEntries 限制 DNS 缓存条目数量（S08-11）。
const maxDNSCacheEntries = 1024

type dnsCacheEntry struct {
	ips       []net.IP
	expiresAt time.Time
}

// dnsCacheTTL 是 DNS 解析结果的缓存时长。
const dnsCacheTTL = 60 * time.Second

// clearDNSCache 清空 DNS 缓存，供测试使用。
func (f *HTTPFetcher) clearDNSCache() {
	f.dnsCacheMu.Lock()
	defer f.dnsCacheMu.Unlock()
	f.dnsCache = map[string]dnsCacheEntry{}
}

// routeKey 区分不同出口路由；连接池不感知 DNS 固定与代理，必须按路由隔离 Transport。
type routeKey struct {
	proxy   string
	pinHost string
	pinIP   string
}

type Option func(*HTTPFetcher)

func NewHTTPFetcher(opts ...Option) *HTTPFetcher {
	// 传输层参数在 opts 应用后再确定（S08-02：dialer 超时须跟随 f.timeout）。
	f := &HTTPFetcher{
		timeout:          30 * time.Second,
		maxRedirects:     5,
		defaultUserAgent: "Reader/0.1.0",
		maxBodyBytes:     10 * 1024 * 1024,
		resolver: func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		},
		clients:         map[routeKey]*http.Client{},
		transports:      map[routeKey]*http.Transport{},
		dnsCache:        map[string]dnsCacheEntry{},
		resolveInflight: map[string]*resolveCall{},
		rateLimiter:     newSourceRateLimiter(),
	}
	for _, opt := range opts {
		opt(f)
	}
	dialTimeout := f.timeout
	if dialTimeout <= 0 || dialTimeout > 30*time.Second {
		dialTimeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	f.dial = dialer.DialContext
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
		if attempt > 0 && req.Retry > 0 {
			// 指数退避：100ms × 2^(attempt-1)，上限 2s，ctx 可取消。
			backoff := time.Duration(100) * time.Millisecond << (attempt - 1)
			if backoff > 2*time.Second {
				backoff = 2 * time.Second
			}
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		resp, err := f.fetchOnce(ctx, client, req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, err
			}
			if !retryable(req, err) {
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

// errRequestSent 表示服务器可能已收到并处理请求；非幂等方法重试会造成重复提交。
type errRequestSent struct {
	underlying error
}

func (e *errRequestSent) Error() string {
	if e.underlying != nil {
		return e.underlying.Error()
	}
	return "request may have been sent"
}

func (e *errRequestSent) Unwrap() error { return e.underlying }

// retryable 判断错误是否值得重试（S08-03）：非幂等方法（POST 等）在请求可能已发送时不重试，
// 确定性错误（超限、被封禁、不支持的 scheme、重定向过多、非 2xx 状态）直接返回。
func retryable(req fetch.Request, err error) bool {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead && errors.As(err, new(*errRequestSent)) {
		// 非幂等请求且连接可能已建立，重试有重复提交风险。
		return false
	}
	if errors.Is(err, fetch.ErrBlocked) {
		return false
	}
	// body 超限 / 重定向过多 / 不支持的 scheme 等确定性错误重试不会改变结果。
	// 非 2xx 状态按 Legado newCallResponse 语义仍重试（existing 行为）。
	var msg string
	if errors.As(err, new(*errRequestSent)) {
		msg = ""
	} else {
		msg = err.Error()
	}
	switch {
	case strings.Contains(msg, "response body too large"),
		strings.Contains(msg, "too many redirects"):
		return false
	}
	// gzip 截断产生的 unexpected EOF：内容已达 maxBodyBytes，重试只会重复下载。
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return false
	}
	return true
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
	if client, ok := f.clients[key]; ok {
		f.mu.Unlock()
		return client, nil
	}
	f.mu.Unlock()
	transport := &http.Transport{
		// 显式不读取 HTTP(S)_PROXY 环境变量，避免环境代理改变实际连接目标。
		Proxy:             nil,
		DialContext:       f.dialContext(key.pinHost, pinIP),
		ForceAttemptHTTP2: true,
		MaxIdleConns:      100,
		// 与并发下载的章节数同量级，避免同源并发时反复 TCP+TLS 握手（C-023）。
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		// 响应头独立超时，慢响应头不再占满整个 Client.Timeout（S08-01）。
		ResponseHeaderTimeout: f.timeout,
	}
	if proxyURL != nil {
		transport.Proxy = f.proxyFunc(proxyURL)
	}
	client := &http.Client{Transport: transport, Timeout: f.timeout, CheckRedirect: f.checkRedirect}
	f.mu.Lock()
	if existing, ok := f.clients[key]; ok {
		// 并发下已由其他 goroutine 创建，回收本 transport 避免重复连接池。
		f.mu.Unlock()
		transport.CloseIdleConnections()
		return existing, nil
	}
	f.clients[key] = client
	f.transports[key] = transport
	f.clientOrder = append(f.clientOrder, key)
	// 超出容量上限时按最旧淘汰路由并回收其空闲连接（S08-12）。
	for len(f.clientOrder) > maxRouteClients {
		oldest := f.clientOrder[0]
		f.clientOrder = f.clientOrder[1:]
		if evicted, ok := f.transports[oldest]; ok {
			evicted.CloseIdleConnections()
			delete(f.transports, oldest)
		}
		delete(f.clients, oldest)
	}
	f.mu.Unlock()
	return client, nil
}

// CloseIdleConnections 立即回收所有路由的空闲持久连接（S03-15），供校验任务收尾调用。
func (f *HTTPFetcher) CloseIdleConnections() {
	f.mu.Lock()
	transports := make([]*http.Transport, 0, len(f.transports))
	for _, t := range f.transports {
		transports = append(transports, t)
	}
	f.mu.Unlock()
	for _, t := range transports {
		t.CloseIdleConnections()
	}
}

func (f *HTTPFetcher) checkRedirect(_ *http.Request, via []*http.Request) error {
	if len(via) >= f.maxRedirects {
		return errors.New("too many redirects")
	}
	// 重定向目标 scheme 必须仍为 http/https（S08-13），防止回落到 file:/data:/ftp: 等。
	last := via[len(via)-1]
	if last != nil && last.URL != nil && last.URL.Scheme != "http" && last.URL.Scheme != "https" {
		return fmt.Errorf("%w: unsupported redirect scheme", fetch.ErrBlocked)
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
// DNS 解析结果按 host 缓存（TTL dnsCacheTTL）；无论命中缓存与否，每次调用都对当次使用的 IP 重新执行 checkIPs，
// 不缓存允许/拒绝结论，SSRF 防护不放宽。
func (f *HTTPFetcher) allowedIPs(ctx context.Context, host string) ([]net.IP, error) {
	if strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("%w: empty host", fetch.ErrBlocked)
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := f.cachedResolve(ctx, host)
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

// cachedResolve 返回主机的 DNS 解析结果；命中未过期缓存则不再次解析。
// 同一 host 的并发解析通过 in-flight 合并（S08-11），只发起一次 resolver 调用。
// 解析失败或结果为空时不写缓存，并把原始错误（含 context.Canceled）返回给调用方，避免吞掉 DNS 具体原因。
func (f *HTTPFetcher) cachedResolve(ctx context.Context, host string) ([]net.IP, error) {
	now := time.Now()
	f.dnsCacheMu.Lock()
	if entry, ok := f.dnsCache[host]; ok && now.Before(entry.expiresAt) {
		f.dnsCacheMu.Unlock()
		return entry.ips, nil
	}
	if call, ok := f.resolveInflight[host]; ok {
		f.dnsCacheMu.Unlock()
		select {
		case <-call.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if call.err != nil {
			return nil, call.err
		}
		return call.ips, nil
	}
	call := &resolveCall{done: make(chan struct{})}
	f.resolveInflight[host] = call
	f.dnsCacheMu.Unlock()
	defer func() {
		f.dnsCacheMu.Lock()
		delete(f.resolveInflight, host)
		f.dnsCacheMu.Unlock()
	}()
	resolved, err := f.resolver(ctx, host)
	if err != nil {
		// 解析失败不缓存，保持原有错误路径。
		call.err = err
		close(call.done)
		return nil, err
	}
	if len(resolved) == 0 {
		call.err = fmt.Errorf("no addresses for host %s", host)
		close(call.done)
		return nil, call.err
	}
	f.dnsCacheMu.Lock()
	// 超出容量上限时淘汰最旧条目（S08-11：map 无界增长）。
	if len(f.dnsCache) >= maxDNSCacheEntries {
		for k := range f.dnsCache {
			delete(f.dnsCache, k)
			break
		}
	}
	f.dnsCache[host] = dnsCacheEntry{ips: resolved, expiresAt: now.Add(dnsCacheTTL)}
	f.dnsCacheMu.Unlock()
	call.ips = resolved
	close(call.done)
	return resolved, nil
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
	// C-047：书源 header 可能含非法 HTTP token 字符（如 U+2011 连字符），原样写入会让传输层报
	// invalid header field name 而整源失效；此处统一清洗：非法 key 跳过并告警，value 去除 \r\n 防注入。
	for key, value := range req.Headers {
		if !validHeaderToken(key) {
			slog.WarnContext(ctx, "dropping invalid header name from source", "header", key)
			continue
		}
		httpReq.Header.Set(key, stripHeaderCRLF(value))
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
		// 标记请求可能已发送（连接/TLS 已建立），供重试幂等判断使用（S08-03）。
		return nil, &errRequestSent{underlying: err}
	}
	defer resp.Body.Close()
	rawBody, readErr := io.ReadAll(io.LimitReader(resp.Body, f.maxBodyBytes+1))
	if readErr != nil {
		return nil, readErr
	}
	if int64(len(rawBody)) > f.maxBodyBytes {
		// 超限是确定性错误，不重试（S08-05）。
		return nil, errors.New("response body too large")
	}
	respBody := rawBody
	decodeGzip := func(data []byte) ([]byte, error) {
		gzipReader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			// 非 gzip magic 或损坏头部：与 Legado 一致按原文本处理，不报错（S08-04）。
			return data, nil
		}
		out, err := io.ReadAll(io.LimitReader(gzipReader, f.maxBodyBytes+1))
		_ = gzipReader.Close()
		if err != nil && int64(len(out)) <= f.maxBodyBytes {
			// 解压中途出错（非超限），按原始错误返回。
			return nil, err
		}
		return out, nil
	}
	if resp.Header.Get("Content-Encoding") == "gzip" {
		decoded, err := decodeGzip(rawBody)
		if err != nil {
			return nil, err
		}
		respBody = decoded
		if int64(len(respBody)) > f.maxBodyBytes {
			return nil, errors.New("response body too large")
		}
	} else if hasGzipMagic(rawBody) {
		decoded, err := decodeGzip(rawBody)
		if err != nil {
			return nil, err
		}
		respBody = decoded
		if int64(len(respBody)) > f.maxBodyBytes {
			return nil, errors.New("response body too large")
		}
	}
	if !req.EncodeBodyAsHex {
		if decoded, err := decodeTextResponseBody(respBody, resp.Header.Get("Content-Type"), f.maxBodyBytes); err != nil {
			if strings.Contains(err.Error(), "response body too large") {
				// 超限错误必须向上传播，不得静默透传原始截断字节（S08-15）。
				return nil, err
			}
			// 真正的编码错误：降级为原始字节并告警（S08-15）。
			slog.WarnContext(ctx, "charset decode failed, falling back to raw body", "error", err)
		} else {
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

// validHeaderToken 判断 key 是否为 RFC 7230 合法 token（!#$%&'*+-.^_`|~ 与数字字母）。
func validHeaderToken(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

// stripHeaderCRLF 去除 value 中的 \r 与 \n，防止 header 注入。
func stripHeaderCRLF(value string) string {
	if !strings.ContainsAny(value, "\r\n") {
		return value
	}
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
}

func decodeTextResponseBody(body []byte, contentType string, maxBodyBytes int64) ([]byte, error) {
	if strings.TrimSpace(contentType) == "" {
		// 缺少 Content-Type 时按 Legado OkHttpUtils.text 的回退思路探测 BOM / HTML meta 声明的编码；
		// 探测不到时保持原字节，避免误伤二进制响应。
		contentType = "text/html; charset=" + sniffCharset(body)
	} else if !looksLikeTextContent(contentType) {
		return body, nil
	}
	label := charsetLabel(contentType)
	if label == "" {
		// Content-Type 未带 charset 参数（charset.NewReader 的 DetermineEncoding 同样如此处理）时
		// 回退到内容嗅探：BOM → 文档开头的 <meta charset> / http-equiv 声明；探测不到时保持原字节。
		if label = sniffCharset(body); label == "" {
			return body, nil
		}
	}
	// 用 charset.Lookup 直接取编码并包 LimitReader，一次 ReadAll 完成解码，避免中间整本拷贝。
	decoded, err := io.ReadAll(io.LimitReader(
		transform.NewReader(bytes.NewReader(body), encodingFromLabel(label).NewDecoder()), maxBodyBytes+1))
	if err != nil {
		return body, err
	}
	if int64(len(decoded)) > maxBodyBytes {
		return nil, errors.New("response body too large")
	}
	return decoded, nil
}

// charsetLabel 从 Content-Type 解析 charset 名，缺失时按 UTF-8 处理。
func charsetLabel(contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return params["charset"]
}

// encodingFromLabel 按 charset 标签名返回对应编码，未识别时按原字节处理（与 charset.NewReader 的 UTF-8 回退一致）。
func encodingFromLabel(label string) encoding.Encoding {
	if e, _ := charset.Lookup(label); e != nil {
		return e
	}
	return encoding.Nop
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

// mergeQuery 合并查询参数；不修改调用方传入的 base（S08-09），顺序按 base 原序 + extra 新键稳定排序。
func mergeQuery(base url.Values, extra map[string]string) string {
	merged := make(url.Values, len(base)+len(extra))
	for key, values := range base {
		merged[key] = append([]string(nil), values...)
	}
	for key, value := range extra {
		merged.Set(key, value)
	}
	return merged.Encode()
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
