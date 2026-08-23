package gemini

import (
	"net/url"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

// TestInstallCookiesTargetsGeminiHost 验证 Google 域 Cookie 会发送到 Gemini
func TestInstallCookiesTargetsGeminiHost(t *testing.T) {
	client, err := NewClient(AccountSource{
		ID: "test",
		Cookies: []Cookie{{
			Name: "__Secure-1PSID", Value: "secret", Domain: ".google.com", Path: "/", Secure: true,
		}},
		Fingerprint: Fingerprint{TLSProfile: "chrome_146"},
	}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse(endpointInit)
	for _, cookie := range client.httpClient.GetCookies(target) {
		if cookie.Name == "__Secure-1PSID" && cookie.Value == "secret" {
			return
		}
	}
	t.Fatal("Gemini 请求缺少 Google 认证 Cookie")
}

// TestAbsorbResponseCookies 验证完整 Set-Cookie 合并删除和写回
func TestAbsorbResponseCookies(t *testing.T) {
	var saved []Cookie
	client := &Client{
		cookies: make(map[string]Cookie),
		save: func(cookies []Cookie) error {
			saved = append([]Cookie(nil), cookies...)
			return nil
		},
	}
	requestURL, _ := url.Parse("https://gemini.google.com/app")
	response := &http.Response{Header: http.Header{
		"Set-Cookie": {
			"A=one; Path=/; Domain=.google.com; Secure; HttpOnly; SameSite=None",
			"B=two; Path=/app; Domain=gemini.google.com; SameSite=Lax",
		},
	}}
	if err := client.absorbResponseCookies(requestURL, response); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("saved cookies = %d", len(saved))
	}
	if !saved[0].HTTPOnly || !saved[0].Secure || saved[0].SameSite != "None" {
		t.Fatalf("cookie A = %+v", saved[0])
	}
	deleteResponse := &http.Response{Header: http.Header{
		"Set-Cookie": {"A=; Path=/; Domain=.google.com; Max-Age=0"},
	}}
	if err := client.absorbResponseCookies(requestURL, deleteResponse); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Name != "B" {
		t.Fatalf("cookies after deletion = %+v", saved)
	}
}
