package gemini

import (
	"net/url"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

// TestPrepareGeneratedMediaURL 验证生成图预览请求变换
func TestPrepareGeneratedMediaURL(t *testing.T) {
	mediaURL, err := url.Parse("https://lh3.googleusercontent.com/gg-dl/opaque")
	if err != nil {
		t.Fatal(err)
	}
	if !prepareGeneratedMediaURL(mediaURL) {
		t.Fatal("generated media URL was not recognized")
	}
	if mediaURL.Path != "/gg-dl/opaque=s1024-rj" || mediaURL.Query().Get("alr") != "yes" {
		t.Fatalf("media URL = %s", mediaURL.Redacted())
	}
}

// TestApplyGeneratedMediaCookies 验证同站中继收到 Google 认证 Cookie
func TestApplyGeneratedMediaCookies(t *testing.T) {
	client := &Client{cookies: map[string]Cookie{
		"google": {Name: "SAPISID", Value: "secret", Domain: ".google.com", Path: "/"},
		"gemini": {Name: "COMPASS", Value: "other", Domain: ".gemini.google.com", Path: "/"},
	}}
	req, err := http.NewRequest(http.MethodGet, "https://work.fife.usercontent.google.com/rd-gg-dl/opaque", nil)
	if err != nil {
		t.Fatal(err)
	}
	client.applyGeneratedMediaCookies(req)
	cookieHeader := req.Header.Get("Cookie")
	if !strings.Contains(cookieHeader, "SAPISID=secret") || strings.Contains(cookieHeader, "COMPASS=other") {
		t.Fatalf("cookie header = %q", cookieHeader)
	}
	other, err := http.NewRequest(http.MethodGet, "https://other.usercontent.google.com/rd-gg-dl/opaque", nil)
	if err != nil {
		t.Fatal(err)
	}
	client.applyGeneratedMediaCookies(other)
	if other.Header.Get("Cookie") != "" {
		t.Fatalf("unexpected relay cookie = %q", other.Header.Get("Cookie"))
	}
}

// TestParseGeneratedMediaRelay 验证生成图下载链域名和路径
func TestParseGeneratedMediaRelay(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "work relay", value: "https://work.fife.usercontent.google.com/rd-gg-dl/opaque=s1024-rj?alr=yes"},
		{name: "media relay", value: "https://lh3.googleusercontent.com/rd-gg-dl/opaque=s1024-rj?alr=yes"},
		{name: "unexpected host", value: "https://example.com/rd-gg-dl/opaque", wantErr: true},
		{name: "unexpected path", value: "https://lh3.googleusercontent.com/gg-dl/opaque", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseGeneratedMediaRelay([]byte(test.value))
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
