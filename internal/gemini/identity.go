package gemini

import (
	"encoding/binary"
	"math/bits"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"golang.org/x/net/publicsuffix"
)

const (
	defaultLanguage = "en-US,en;q=0.9"
	// ChromeUserAgent 是所有上游请求使用的 Chrome 稳定版 User-Agent
	ChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"
)

// chromeLowEntropyHints 是 Chrome 稳定版发往所有 HTTPS 站点的客户端提示
var chromeLowEntropyHints = map[string]string{
	"sec-ch-ua":          `"Not-A.Brand";v="99", "Google Chrome";v="157", "Chromium";v="157"`,
	"sec-ch-ua-mobile":   "?0",
	"sec-ch-ua-platform": `"Windows"`,
}

// chromeGoogleHeaders 是 Chrome 稳定版发往 Google 域名的高熵客户端提示与浏览器标识
var chromeGoogleHeaders = map[string]string{
	"sec-ch-ua-arch":              `"x86"`,
	"sec-ch-ua-bitness":           `"64"`,
	"sec-ch-ua-form-factors":      `"Desktop"`,
	"sec-ch-ua-full-version":      `"157.0.8103.40"`,
	"sec-ch-ua-full-version-list": `"Not-A.Brand";v="99.0.0.0", "Google Chrome";v="157.0.8103.40", "Chromium";v="157.0.8103.40"`,
	"sec-ch-ua-model":             `""`,
	"sec-ch-ua-platform-version":  `"10.0.0"`,
	"sec-ch-ua-wow64":             "?0",
	"x-browser-channel":           "stable",
	"x-browser-year":              "2026",
	"x-browser-validation":        "XLAS5gg5sZEDe2LzGC1NUVw7aB4=",
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
	req.Header.Set("x-sec-ch-ua-not-a-border", notABorder(time.Now()))
}

// chromeAPIKey 是 Chrome 稳定版内置的 Google API key，参与 x-sec-ch-ua-not-a-border 的计算
const chromeAPIKey = "AIzaSyA2KlwBX3mkFo30om9LUFYQhpqLoa_BNhE"

// notABorderSeed 是 Chrome 的 chromecompaneros.dll 计算 x-sec-ch-ua-not-a-border 时的初始状态
const notABorderSeed = "f369e73779169d07"

// notABorder 按 Chrome 的 GetCompaneroValue 返回 x-sec-ch-ua-not-a-border：以种子的四个大端字为状态，依次逐字节吸收 API key、User-Agent 与 Unix 毫秒除以 300000 的十进制字符串，再把四个字按小端逐字节、低半字节在前映射到 qazwsxedcrfvtgby
func notABorder(now time.Time) string {
	state := [4]uint32{}
	for index := range state {
		state[index] = binary.BigEndian.Uint32([]byte(notABorderSeed[index*4 : index*4+4]))
	}
	a, b, c, d := state[0], state[1], state[2], state[3]
	absorb := func(data string) {
		for index := 0; index < len(data); index++ {
			mixed := bits.RotateLeft32(uint32(data[index])+a, 7)
			b ^= mixed
			c += b
			next := bits.RotateLeft32(c, 13)
			folded := d ^ next
			a, b, c, d = b+folded, next^mixed, folded+mixed, next
		}
	}
	absorb(chromeAPIKey)
	absorb(ChromeUserAgent)
	absorb(strconv.FormatInt(now.UnixMilli()/300000, 10))
	const alphabet = "qazwsxedcrfvtgby"
	out := make([]byte, 0, 32)
	for _, word := range [4]uint32{a, b, c, d} {
		for shift := 0; shift < 32; shift += 8 {
			value := byte(word >> shift)
			out = append(out, alphabet[value&15], alphabet[value>>4])
		}
	}
	return string(out)
}
