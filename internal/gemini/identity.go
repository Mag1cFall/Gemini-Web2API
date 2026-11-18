package gemini

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"math/bits"
	"math/rand/v2"
	"slices"
	"strconv"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
	"golang.org/x/net/publicsuffix"
)

const defaultLanguage = "en-US,en;q=0.9"

// chromeBuild 是版本池中一个 Windows x64 Chrome 稳定版构建的请求身份，取值来自该构建的官网现场
type chromeBuild struct {
	major int
	// version 是完整版本号
	version string
	// brands 是 sec-ch-ua
	brands string
	// fullBrands 是 sec-ch-ua-full-version-list
	fullBrands string
	// accept 是页面与 iframe 导航的 accept
	accept     string
	validation string
	// year 是 x-browser-year
	year string
	// copyright 是 x-browser-copyright
	copyright string
	// lowEntropy 表示新 profile 以 x-client-data 报告低熵源对应的变体 ID
	lowEntropy bool
	// seed 是 x-sec-ch-ua-not-a-border 的种子，为空时不发送
	seed string
	// anchors 是 trust_anchors 扩展内容的十六进制，为空时不发送该扩展
	anchors string
	// grease 表示签名算法以一个 GREASE 值开头
	grease bool
	// mldsa 表示签名算法含 ML-DSA（0x0904–0x0906）
	mldsa bool
	// share 是该版本在 Chrome 用户中的占比
	share float64
}

// chromeLowEntropyBase 是 Chromium 把低熵源（0–7999）报告为变体 ID 时的起点 kLowEntropySourceVariationIdRangeMin
const chromeLowEntropyBase = 3320978

// chromeAPIKey 是 Chrome 稳定版内置的 Google API key，参与 x-sec-ch-ua-not-a-border 的计算
const chromeAPIKey = "AIzaSyA2KlwBX3mkFo30om9LUFYQhpqLoa_BNhE"

// chromeLatest 是版本池中最新的构建，用于不属于任何账号的请求
var chromeLatest = &chromeBuilds[len(chromeBuilds)-1]

// IsChromeVersion 判断 major 是否为版本池中的 Chrome 主版本
func IsChromeVersion(major int) bool {
	_, err := chromeBuildFor(major)
	return major != 0 && err == nil
}

// PickChromeVersion 按版本池各版本的用户占比加权随机返回一个 Chrome 主版本
func PickChromeVersion() int {
	total := 0.0
	for _, build := range chromeBuilds {
		total += build.share
	}
	point := rand.Float64() * total
	for _, build := range chromeBuilds {
		if point -= build.share; point < 0 {
			return build.major
		}
	}
	return chromeLatest.major
}

// chromeBuildFor 返回主版本 major 的构建，major 为 0 时返回版本池中最新的构建
func chromeBuildFor(major int) (*chromeBuild, error) {
	if major == 0 {
		return chromeLatest, nil
	}
	for index := range chromeBuilds {
		if chromeBuilds[index].major == major {
			return &chromeBuilds[index], nil
		}
	}
	return nil, fmt.Errorf("Chrome %d 不在版本池中", major)
}

// ChromeUserAgent 返回主版本 major 的 Chrome User-Agent，major 为 0 时为版本池中最新的版本
func ChromeUserAgent(major int) string {
	if major == 0 {
		major = chromeLatest.major
	}
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + strconv.Itoa(major) + ".0.0.0 Safari/537.36"
}

// NewHTTPClient 创建经代理 proxyURL 访问、TLS 与 HTTP/2 和主版本 major 的 Chrome 稳定版一致的客户端，proxyURL 为空时直连，major 为 0 时为版本池中最新的版本
func NewHTTPClient(timeoutSeconds int, proxyURL string, major int) (tls_client.HttpClient, error) {
	build, err := chromeBuildFor(major)
	if err != nil {
		return nil, err
	}
	return build.httpClient(timeoutSeconds, tls_client.WithProxyUrl(proxyURL))
}

// httpClient 创建该构建的客户端，connect 给出代理或建立 TCP 连接的方式：https 请求经 Chrome HTTP/2 传输，扩展顺序与 Chrome 一样每条连接随机排列，HEADERS 帧的权重按 priority 请求头、依赖按连接上仍打开的流与 Chrome 网络栈一致
func (b *chromeBuild) httpClient(timeoutSeconds int, connect tls_client.HttpClientOption) (tls_client.HttpClient, error) {
	profile := b.clientProfile()
	jar := newCookieJar()
	base, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(timeoutSeconds),
		tls_client.WithClientProfile(profile),
		tls_client.WithRandomTLSExtensionOrder(),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(jar),
		connect,
	)
	if err != nil {
		return nil, err
	}
	return &chromeClient{HttpClient: base, client: http.Client{
		Transport:     newChromeTransport(base.GetDialer(), profile),
		Jar:           jar,
		Timeout:       time.Duration(timeoutSeconds) * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// clientProfile 返回以 tls-client 的 Chrome 152 档案为底、签名算法与 trust_anchors 按该构建现场的档案；该档案的签名算法依次为 GREASE、ML-DSA 与其余算法
func (b *chromeBuild) clientProfile() profiles.ClientProfile {
	base := profiles.Chrome_152
	anchors, _ := hex.DecodeString(b.anchors)
	hello := base.GetClientHelloId()
	hello.Version = strconv.Itoa(b.major)
	hello.SpecFactory = func() (tls.ClientHelloSpec, error) {
		spec, err := base.GetClientHelloSpec()
		if err != nil {
			return tls.ClientHelloSpec{}, err
		}
		extensions := spec.Extensions[:0]
		for _, extension := range spec.Extensions {
			switch typed := extension.(type) {
			case *tls.SignatureAlgorithmsExtension:
				typed.SupportedSignatureAlgorithms = slices.DeleteFunc(typed.SupportedSignatureAlgorithms, func(scheme tls.SignatureScheme) bool {
					return !b.grease && scheme&0x0f0f == 0x0a0a || !b.mldsa && scheme >= 0x0904 && scheme <= 0x0906
				})
			case *tls.GenericExtension:
				if typed.Id == 0xca34 {
					if len(anchors) == 0 {
						continue
					}
					typed.Data = anchors
				}
			}
			extensions = append(extensions, extension)
		}
		spec.Extensions = extensions
		return spec, nil
	}
	return profiles.NewClientProfile(hello, base.GetSettings(), base.GetSettingsOrder(), base.GetPseudoHeaderOrder(), base.GetConnectionFlow(),
		base.GetPriorities(), base.GetHeaderPriority(), base.GetStreamID(), base.GetAllowHTTP(),
		base.GetHttp3Settings(), base.GetHttp3SettingsOrder(), base.GetHttp3PriorityParam(), base.GetHttp3PseudoHeaderOrder(), base.GetHttp3SendGreaseFrames())
}

// newCookieJar 创建按 RFC 6265 匹配域、host-only 与路径的 Cookie jar
func newCookieJar() http.CookieJar {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return jar
}

// chromeBuild 返回客户端账号绑定的构建，不属于任何账号的客户端为版本池中最新的构建
func (c *Client) chromeBuild() *chromeBuild {
	if c.chrome == nil {
		return chromeLatest
	}
	return c.chrome
}

// applyChromeHeaders 写入账号所绑 Chrome 版本的 User-Agent、低熵客户端提示与账号语言，google 为真时同时写入 Google 域名专用请求头
func (c *Client) applyChromeHeaders(req *http.Request, google bool) {
	b := c.chromeBuild()
	req.Header.Set("user-agent", ChromeUserAgent(b.major))
	req.Header.Set("accept-language", c.language)
	req.Header.Set("sec-ch-ua", b.brands)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	if !google {
		return
	}
	req.Header.Set("sec-ch-ua-arch", `"x86"`)
	req.Header.Set("sec-ch-ua-bitness", `"64"`)
	req.Header.Set("sec-ch-ua-form-factors", `"Desktop"`)
	req.Header.Set("sec-ch-ua-full-version", strconv.Quote(b.version))
	req.Header.Set("sec-ch-ua-full-version-list", b.fullBrands)
	req.Header.Set("sec-ch-ua-model", `""`)
	req.Header.Set("sec-ch-ua-platform-version", `"10.0.0"`)
	req.Header.Set("sec-ch-ua-wow64", "?0")
	req.Header.Set("x-browser-channel", "stable")
	req.Header.Set("x-browser-year", b.year)
	req.Header.Set("x-browser-validation", b.validation)
	req.Header.Set("x-browser-copyright", b.copyright)
	if b.lowEntropy {
		req.Header.Set("x-client-data", lowEntropyClientData(c.accountID))
	}
	if b.seed != "" {
		req.Header.Set("x-sec-ch-ua-not-a-border", b.notABorder(time.Now()))
	}
}

// lowEntropyClientData 返回只含低熵源变体 ID 的 x-client-data（protobuf 字段 1 的 varint 再 Base64）；低熵源每个 Chrome 安装随机，账号取账号 ID 的 FNV-1a 哈希除以 8000 的余数
func lowEntropyClientData(accountID string) string {
	hash := fnv.New32a()
	hash.Write([]byte(accountID))
	return base64.StdEncoding.EncodeToString(binary.AppendUvarint([]byte{0x08}, uint64(chromeLowEntropyBase+hash.Sum32()%8000)))
}

// notABorder 按 Chrome 的 GetCompaneroValue 返回 x-sec-ch-ua-not-a-border：以种子的四个大端字为状态，依次逐字节吸收 API key、User-Agent 与 Unix 毫秒除以 300000 的十进制字符串，再把四个字按小端逐字节、低半字节在前映射到 qazwsxedcrfvtgby
func (b *chromeBuild) notABorder(now time.Time) string {
	state := [4]uint32{}
	for index := range state {
		state[index] = binary.BigEndian.Uint32([]byte(b.seed[index*4 : index*4+4]))
	}
	w0, w1, w2, w3 := state[0], state[1], state[2], state[3]
	absorb := func(data string) {
		for index := 0; index < len(data); index++ {
			mixed := bits.RotateLeft32(uint32(data[index])+w0, 7)
			w1 ^= mixed
			w2 += w1
			next := bits.RotateLeft32(w2, 13)
			folded := w3 ^ next
			w0, w1, w2, w3 = w1+folded, next^mixed, folded+mixed, next
		}
	}
	absorb(chromeAPIKey)
	absorb(ChromeUserAgent(b.major))
	absorb(strconv.FormatInt(now.UnixMilli()/300000, 10))
	const alphabet = "qazwsxedcrfvtgby"
	out := make([]byte, 0, 32)
	for _, word := range [4]uint32{w0, w1, w2, w3} {
		for shift := 0; shift < 32; shift += 8 {
			value := byte(word >> shift)
			out = append(out, alphabet[value&15], alphabet[value>>4])
		}
	}
	return string(out)
}
