package accounts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// chromeRenewal 用保存的续签材料签发 Cookie，材料被拒时从本机浏览器读取同一 Gaia ID 账号的新材料并写回认证状态
type chromeRenewal struct {
	mu       sync.Mutex
	id       string
	browser  string
	material auth.OAuthMaterial
	profile  string
	proxy    string
	file     *auth.File
	// tried 是浏览器中已确认被拒的 refresh token 密文
	tried []byte
}

// newChromeRenewal 返回账号的续签器
func newChromeRenewal(account auth.LoadedAccount) *chromeRenewal {
	browser := account.Browser
	if browser == "" {
		browser = chromeauth.BrowserChrome
	}
	return &chromeRenewal{id: account.ID, browser: browser, material: *account.OAuth, profile: account.Profile, proxy: account.ProxyURL, file: account.File}
}

// refresh 签发 Cookie，保存的材料被拒且浏览器中的材料已更新时改用浏览器中的材料
func (r *chromeRenewal) refresh(ctx context.Context) ([]gemini.Cookie, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cookies, err := chromeauth.Refresh(ctx, r.material, r.proxy)
	if !errors.Is(err, chromeauth.ErrCredentialsRejected) || r.file == nil {
		return cookies, err
	}
	current, findErr := chromeauth.FindBrowserMaterial(r.browser, r.material.GaiaID, r.profile)
	if findErr != nil {
		return nil, fmt.Errorf("%w；从 %s 重新导入失败: %v", err, r.browser, findErr)
	}
	if bytes.Equal(current.Encrypted, r.tried) {
		return nil, fmt.Errorf("%w；%s 中的材料未更新", err, r.browser)
	}
	logging.Info(r.id, "续签材料被拒，从浏览器重新导入", "Browser", r.browser, "Profile", current.Profile)
	material, importErr := current.Decrypt()
	if importErr == nil && material.RefreshToken == r.material.RefreshToken {
		importErr = fmt.Errorf("%w：%s 中的材料与被拒材料相同", chromeauth.ErrCredentialsRejected, r.browser)
	}
	if importErr == nil {
		cookies, importErr = chromeauth.Refresh(ctx, material, r.proxy)
	}
	if errors.Is(importErr, chromeauth.ErrCredentialsRejected) {
		r.tried = current.Encrypted
	}
	if importErr != nil {
		return nil, fmt.Errorf("%w；从 %s 重新导入失败: %w", err, r.browser, importErr)
	}
	if err := r.file.SetOAuth(material, current.Profile); err != nil {
		return nil, err
	}
	r.material, r.profile = material, current.Profile
	logging.Info(r.id, "已从浏览器重新导入续签材料", "Browser", r.browser, "Profile", current.Profile)
	return cookies, nil
}

// browserRenewal 在 Cookie 会话失效且没有 OAuth 续签材料时，从本机浏览器重新读取最新 Cookie 并写回认证状态
type browserRenewal struct {
	mu      sync.Mutex
	id      string
	browser string
	profile string
	file    *auth.File
}

// newBrowserRenewal 返回浏览器 Cookie 账号的续签器
func newBrowserRenewal(account auth.LoadedAccount) *browserRenewal {
	browser := account.Browser
	if browser == "" {
		browser = chromeauth.BrowserChrome
	}
	return &browserRenewal{id: account.ID, browser: browser, profile: account.Profile, file: account.File}
}

// refresh 从本机浏览器读取当前最新的 Cookie 并写回认证状态
func (r *browserRenewal) refresh(ctx context.Context) ([]gemini.Cookie, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil || r.profile == "" {
		return nil, gemini.ErrSignedOut
	}
	logging.Info(r.id, "Cookie 会话失效，从浏览器重新读取", "Browser", r.browser, "Profile", r.profile)
	stateCookies, err := chromeauth.ReadBrowserCookies(r.browser, r.profile)
	if err != nil {
		return nil, fmt.Errorf("从 %s 重新读取 Cookie 失败: %w", r.browser, err)
	}
	geminiCookies := auth.ToGeminiCookies(stateCookies)
	if len(geminiCookies) == 0 {
		return nil, fmt.Errorf("从 %s 未读取到任何 Cookie", r.browser)
	}
	if err := r.file.SaveCookies(geminiCookies); err != nil {
		logging.Warn(r.id, "写回重新读取的 Cookie 失败", "错误", err)
	}
	logging.Info(r.id, "已从浏览器重新读取最新 Cookie", "Browser", r.browser, "Count", len(geminiCookies))
	return geminiCookies, nil
}
