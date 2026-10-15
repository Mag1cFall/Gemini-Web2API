package gemini

import (
	"strings"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"golang.org/x/net/publicsuffix"
)

const (
	defaultLanguage = "en-US,en;q=0.9"
	// ChromeUserAgent 是所有上游请求使用的 Chrome 稳定版 User-Agent
	ChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36"
)

// chromeLowEntropyHints 是 Chrome 稳定版发往所有 HTTPS 站点的客户端提示
var chromeLowEntropyHints = map[string]string{
	"sec-ch-ua":          `"Google Chrome";v="155", "Chromium";v="155", "Not(A:Brand";v="24"`,
	"sec-ch-ua-mobile":   "?0",
	"sec-ch-ua-platform": `"Windows"`,
}

// chromeGoogleHeaders 是 Chrome 稳定版发往 Google 域名的高熵客户端提示与浏览器标识
var chromeGoogleHeaders = map[string]string{
	"sec-ch-ua-arch":              `"x86"`,
	"sec-ch-ua-bitness":           `"64"`,
	"sec-ch-ua-form-factors":      `"Desktop"`,
	"sec-ch-ua-full-version":      `"155.0.8059.41"`,
	"sec-ch-ua-full-version-list": `"Google Chrome";v="155.0.8059.41", "Chromium";v="155.0.8059.41", "Not(A:Brand";v="24.0.0.0"`,
	"sec-ch-ua-model":             `""`,
	"sec-ch-ua-platform-version":  `"10.0.0"`,
	"sec-ch-ua-wow64":             "?0",
	"x-browser-channel":           "stable",
	"x-browser-year":              "2026",
	"x-browser-validation":        "lYqVr4pshcjeOP5q5htEarLm87A=",
	"x-browser-copyright":         "Copyright 2026 Google LLC. All Rights Reserved.",
}

// NewHTTPClient 创建 TLS 与 HTTP/2 握手和 Chrome 稳定版一致的客户端
func NewHTTPClient(timeoutSeconds int, proxyURL string) (tls_client.HttpClient, error) {
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(timeoutSeconds),
		tls_client.WithClientProfile(profiles.Chrome_152),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(newCookieJar()),
	}
	if strings.TrimSpace(proxyURL) != "" {
		options = append(options, tls_client.WithProxyUrl(strings.TrimSpace(proxyURL)))
	}
	return tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
}

// newCookieJar 创建按 RFC 6265 匹配域、host-only 与路径的 Cookie jar
func newCookieJar() http.CookieJar {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return jar
}

// applyChromeHeaders 写入 User-Agent、低熵客户端提示与账号语言，google 为真时同时写入 Google 域名专用请求头
func applyChromeHeaders(req *http.Request, language string, google bool) {
	req.Header.Set("user-agent", ChromeUserAgent)
	req.Header.Set("accept-language", language)
	for name, value := range chromeLowEntropyHints {
		req.Header.Set(name, value)
	}
	if !google {
		return
	}
	for name, value := range chromeGoogleHeaders {
		req.Header.Set(name, value)
	}
}
