package gemini

import (
	"fmt"
	"strings"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	defaultLanguage = "en-US,en;q=0.9"
	defaultFamily   = "chrome"
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
	profile        profiles.ClientProfile
	browser        string
	version        string
	userAgent      string
	secCHUA        string
	browserHeaders [][2]string
}

// fingerprintPresets 按浏览器家族保存当前 TLS 模板与请求头身份
var fingerprintPresets = map[string]fingerprintPreset{
	"chrome": {
		profile:   profiles.Chrome_152,
		browser:   "Chrome",
		version:   "153",
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36",
		secCHUA:   `"Google Chrome";v="153", "Not_A Brand";v="8", "Chromium";v="153"`,
		browserHeaders: [][2]string{
			{"x-browser-channel", "stable"},
			{"x-browser-year", "2026"},
			{"x-browser-validation", "6sWHb8G4ZxDIKZivt/PCtKQKFEk="},
			{"x-browser-copyright", "Copyright 2026 Google LLC. All Rights Reserved."},
		},
	},
	"edge": {
		profile:   profiles.Chrome_146,
		browser:   "Edge",
		version:   "146",
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36 Edg/146.0.0.0",
		secCHUA:   `"Not=A?Brand";v="99", "Microsoft Edge";v="146", "Chromium";v="146"`,
	},
	"firefox": {
		profile:   profiles.Firefox_148,
		browser:   "Firefox",
		version:   "148",
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0",
	},
}

// normalizeFingerprint 按浏览器家族选择当前 TLS 模板并让 HTTP 身份与模板一致
func normalizeFingerprint(value Fingerprint) (Fingerprint, fingerprintPreset, error) {
	family := strings.ToLower(strings.TrimSpace(value.TLSProfile))
	family, _, _ = strings.Cut(family, "_")
	if family == "" {
		family = defaultFamily
	}
	preset, ok := fingerprintPresets[family]
	if !ok {
		return Fingerprint{}, fingerprintPreset{}, fmt.Errorf("unsupported TLS profile %q", value.TLSProfile)
	}

	value.TLSProfile = family
	value.Browser = preset.browser
	value.Version = preset.version
	value.UserAgent = preset.userAgent
	value.Platform = "Windows"
	if strings.TrimSpace(value.Language) == "" {
		value.Language = defaultLanguage
	}
	return value, preset, nil
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

// clientHints 返回与固定 TLS 模板一致的 Chromium 客户端提示
func (p fingerprintPreset) clientHints() map[string]string {
	if p.secCHUA == "" {
		return nil
	}
	return map[string]string{
		"Sec-CH-UA":          p.secCHUA,
		"Sec-CH-UA-Mobile":   "?0",
		"Sec-CH-UA-Platform": `"Windows"`,
	}
}
