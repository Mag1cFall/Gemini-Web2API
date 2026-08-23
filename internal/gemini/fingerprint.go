package gemini

import (
	"fmt"
	"strings"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	defaultLanguage   = "en-US,en;q=0.9"
	defaultTLSProfile = "chrome_146"
)

// Fingerprint 表示与账号长期绑定的 HTTP 客户端指纹
type Fingerprint struct {
	Browser    string `json:"browser"`
	Version    string `json:"version"`
	Platform   string `json:"platform"`
	UserAgent  string `json:"user_agent"`
	Language   string `json:"language"`
	TLSProfile string `json:"tls_profile"`
}

type fingerprintPreset struct {
	profile   profiles.ClientProfile
	browser   string
	version   string
	userAgent string
}

var fingerprintPresets = map[string]fingerprintPreset{
	"chrome_146": {
		profile:   profiles.Chrome_146,
		browser:   "Chrome",
		version:   "146",
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
	},
	"edge_146": {
		profile:   profiles.Chrome_146,
		browser:   "Edge",
		version:   "146",
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36 Edg/146.0.0.0",
	},
	"firefox_148": {
		profile:   profiles.Firefox_148,
		browser:   "Firefox",
		version:   "148",
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0",
	},
}

// normalizeFingerprint 固定 TLS 模板并让 HTTP 身份与模板版本一致
func normalizeFingerprint(value Fingerprint) (Fingerprint, profiles.ClientProfile, error) {
	if strings.TrimSpace(value.TLSProfile) == "" {
		value.TLSProfile = defaultTLSProfile
	}
	preset, ok := fingerprintPresets[strings.ToLower(strings.TrimSpace(value.TLSProfile))]
	if !ok {
		return Fingerprint{}, profiles.ClientProfile{}, fmt.Errorf("unsupported TLS profile %q", value.TLSProfile)
	}

	value.Browser = preset.browser
	value.Version = preset.version
	value.UserAgent = preset.userAgent
	value.Platform = "Windows"
	if strings.TrimSpace(value.Language) == "" {
		value.Language = defaultLanguage
	}
	return value, preset.profile, nil
}

// getClientOptions 返回账号固定的 TLS 客户端配置
func getClientOptions(profile profiles.ClientProfile, proxyURL string) []tls_client.HttpClientOption {
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(600),
		tls_client.WithClientProfile(profile),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
	}
	if strings.TrimSpace(proxyURL) != "" {
		options = append(options, tls_client.WithProxyUrl(strings.TrimSpace(proxyURL)))
	}
	return options
}

// languageCode 返回协议载荷使用的语言代码
func (f Fingerprint) languageCode() string {
	language := strings.TrimSpace(strings.Split(f.Language, ",")[0])
	language = strings.TrimSpace(strings.Split(language, ";")[0])
	if language == "" {
		return "en"
	}
	return language
}

// clientHints 返回与固定 TLS 模板一致的 Chromium 客户端提示
func (f Fingerprint) clientHints() map[string]string {
	if f.Browser == "Firefox" {
		return nil
	}
	brand := "Google Chrome"
	if f.Browser == "Edge" {
		brand = "Microsoft Edge"
	}
	return map[string]string{
		"Sec-CH-UA":          fmt.Sprintf(`"Not=A?Brand";v="99", "%s";v="%s", "Chromium";v="%s"`, brand, f.Version, f.Version),
		"Sec-CH-UA-Mobile":   "?0",
		"Sec-CH-UA-Platform": fmt.Sprintf("%q", f.Platform),
	}
}
