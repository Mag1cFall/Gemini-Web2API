package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// TestStateRoundTrip 验证 Playwright 状态加载与完整 Cookie 写回
func TestStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "profile-40", "storage-state.json")
	state := StorageState{
		Cookies: []StateCookie{{
			Name:     "__Secure-1PSID",
			Value:    "secret",
			Domain:   ".google.com",
			Path:     "/",
			Expires:  1893456000,
			HTTPOnly: true,
			Secure:   true,
			SameSite: "None",
		}},
		Origins: []json.RawMessage{},
		Metadata: Metadata{
			ID:    "profile-40",
			Proxy: "http://127.0.0.1:8080",
			Fingerprint: gemini.Fingerprint{
				TLSProfile: "chrome_146",
				Language:   "en-US,en;q=0.9",
			},
		},
	}
	file, err := New(path, state)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Write(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.json"), []byte(`{"ignored":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	accounts, err := LoadFiles([]string{root}, "")
	if err != nil || len(accounts) != 1 || accounts[0].ID != "profile-40" {
		t.Fatalf("认证目录加载错误: accounts=%#v err=%v", accounts, err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	account := loaded.Account("")
	if account.ID != "profile-40" || account.ProxyURL != "http://127.0.0.1:8080" {
		t.Fatalf("账号元数据错误: %#v", account)
	}
	updated := append(account.Source.Cookies, gemini.Cookie{
		Name: "__Secure-1PSIDTS", Value: "rotated", Domain: ".google.com", Path: "/", Secure: true,
	})
	if err := account.Source.Save(updated); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted StorageState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Cookies) != 2 || persisted.Cookies[1].Value != "rotated" {
		t.Fatalf("Cookie 写回错误: %#v", persisted.Cookies)
	}
}
