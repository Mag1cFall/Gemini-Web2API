package gemini

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/Mag1cFall/fhttp"
	"github.com/Mag1cFall/fhttp/http2"
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
	"golang.org/x/net/proxy"
)

// chromeTLSHandshakeTimeout 是 Chromium SSLConnectJob 的 kSSLHandshakeTimeout
const chromeTLSHandshakeTimeout = 30 * time.Second

// chromeIdleConnTimeout 是空闲 HTTP/2 连接保留的时长
const chromeIdleConnTimeout = 90 * time.Second

// chromeWeights 是 Chrome 按 priority 请求头的紧急度 u 写入 HEADERS 帧的权重减一，即 Chromium Spdy3PriorityToHttp2Weight(u) - 1
var chromeWeights = [8]uint8{255, 219, 182, 146, 109, 73, 36, 0}

// chromeStreamPriority 返回 Chrome 为 priority 请求头写入 HEADERS 帧的独占优先级，紧急度缺省为 3，依赖由传输按连接上仍打开的流选择
func chromeStreamPriority(priority string) http2.PriorityParam {
	urgency := 3
	for _, item := range strings.Split(priority, ",") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(item), "u="); ok {
			urgency, _ = strconv.Atoi(value)
		}
	}
	return http2.PriorityParam{Exclusive: true, Weight: chromeWeights[urgency]}
}

// chromeClient 是按 Chrome 网络栈发出请求的客户端：https 请求经 client 的 Chrome HTTP/2 连接池，明文 http 请求与 Chrome 一样以 HTTP/1.1 经内嵌的 tls-client 客户端发出，两者共用 Cookie jar 与代理拨号器
type chromeClient struct {
	tls_client.HttpClient
	client http.Client
}

func (c *chromeClient) Do(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "http" {
		return c.HttpClient.Do(req)
	}
	return c.client.Do(req)
}

func (c *chromeClient) CloseIdleConnections() {
	c.client.CloseIdleConnections()
	c.HttpClient.CloseIdleConnections()
}

func (c *chromeClient) SetCookieJar(jar http.CookieJar) {
	c.HttpClient.SetCookieJar(jar)
	c.client.Jar = jar
}

// chromeTransport 把请求转为 Mag1cFall/fhttp 请求，以 Chrome 的 TLS 握手、HTTP/2 设置与按请求的 HEADERS 优先级发出
type chromeTransport struct {
	transport *http2.Transport
}

// newChromeTransport 创建经 dialer 建立 TCP 连接、以 profile 的 ClientHello 与 HTTP/2 设置建立连接的传输，HEADERS 帧按 Chrome 网络栈依赖于连接上仍打开的流
func newChromeTransport(dialer proxy.ContextDialer, profile profiles.ClientProfile) *chromeTransport {
	settings := make(map[http2.SettingID]uint32, len(profile.GetSettings()))
	for id, value := range profile.GetSettings() {
		settings[http2.SettingID(id)] = value
	}
	order := make([]http2.SettingID, 0, len(profile.GetSettingsOrder()))
	for _, id := range profile.GetSettingsOrder() {
		order = append(order, http2.SettingID(id))
	}
	hello := profile.GetClientHelloId()
	sessions := tls.NewLRUClientSessionCache(32)
	return &chromeTransport{transport: &http2.Transport{
		DialTLS: func(network, address string, _ *tls.Config) (net.Conn, error) {
			return dialChromeTLS(dialer, sessions, hello, network, address)
		},
		Settings:             settings,
		SettingsOrder:        order,
		PseudoHeaderOrder:    profile.GetPseudoHeaderOrder(),
		ConnectionFlow:       profile.GetConnectionFlow(),
		IdleConnTimeout:      chromeIdleConnTimeout,
		PriorityDependencies: true,
	}}
}

// dialChromeTLS 建立 TLS 连接并要求协商 HTTP/2，扩展顺序与 Chrome 一样每条连接随机排列
func dialChromeTLS(dialer proxy.ContextDialer, sessions tls.ClientSessionCache, hello tls.ClientHelloID, network, address string) (net.Conn, error) {
	raw, err := dialer.DialContext(context.Background(), network, address)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(address)
	conn := tls.UClient(raw, &tls.Config{ServerName: host, ClientSessionCache: sessions, OmitEmptyPsk: true}, hello, true, false, false)
	ctx, cancel := context.WithTimeout(context.Background(), chromeTLSHandshakeTimeout)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if protocol := conn.ConnectionState().NegotiatedProtocol; protocol != http2.NextProtoTLS {
		_ = conn.Close()
		return nil, fmt.Errorf("连接 %s 没有协商出 HTTP/2（实际为 %q），代理需要原样转发 HTTPS 连接", address, protocol)
	}
	return conn, nil
}

func (t *chromeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := req.Body
	if body == http.NoBody {
		body = nil
	}
	upstream := (&fhttp.Request{
		Method:        req.Method,
		URL:           req.URL,
		Header:        fhttp.Header(req.Header),
		Body:          body,
		GetBody:       req.GetBody,
		ContentLength: req.ContentLength,
		Host:          req.Host,
	}).WithContext(http2.WithHeaderPriority(req.Context(), chromeStreamPriority(req.Header.Get("priority"))))
	resp, err := t.transport.RoundTrip(upstream)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		Proto:         resp.Proto,
		ProtoMajor:    resp.ProtoMajor,
		ProtoMinor:    resp.ProtoMinor,
		Header:        http.Header(resp.Header),
		Body:          resp.Body,
		ContentLength: resp.ContentLength,
		Uncompressed:  resp.Uncompressed,
		Request:       req,
		TLS:           resp.TLS,
	}, nil
}

func (t *chromeTransport) CloseIdleConnections() {
	t.transport.CloseIdleConnections()
}
