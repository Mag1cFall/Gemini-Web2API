package chromeauth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// CookieAccount 是 Cookie 中一个已登录会话的账号：会话序号与登录邮箱
type CookieAccount struct {
	AuthUser int    `json:"auth_user"`
	Email    string `json:"email"`
}

// maxCookieAuthUsers 是识别多登录 Cookie 时尝试的会话序号上限，与同一浏览器可同时登录的 Google 账号数相同
const maxCookieAuthUsers = 10

// DiscoverCookieAccounts 依次以 Cookie 的各会话序号打开 Gemini 首页并读取登录邮箱，序号超出已登录的会话时首页回到已列出的账号或无法初始化，识别到此结束
func DiscoverCookieAccounts(ctx context.Context, text string, proxy string, fallback string) ([]CookieAccount, error) {
	_, requestProxy, err := importProxies(proxy, fallback)
	if err != nil {
		return nil, err
	}
	cookies, err := parseCookies(text)
	if err != nil {
		return nil, err
	}
	var accounts []CookieAccount
	for authUser := range maxCookieAuthUsers {
		source := gemini.AccountSource{
			ID: "cookie", Cookies: auth.GeminiCookies(cookies), Language: defaultImportLanguage, AuthUser: authUser,
			Save: func([]gemini.Cookie) error { return nil },
		}
		client, err := gemini.NewClient(source, requestProxy, false)
		if err != nil {
			return nil, err
		}
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = client.Init(attempt)
		cancel()
		client.CloseIdleConnections()
		email := strings.ToLower(client.Email())
		if authUser > 0 && (err != nil || email == "" || slices.ContainsFunc(accounts, func(account CookieAccount) bool { return account.Email == email })) {
			break
		}
		if err != nil {
			return nil, withMissingCookieHint(cookies, withImportProxyHint(requestProxy, err))
		}
		if email == "" {
			return nil, errors.New("Gemini 首页没有登录账号，Cookie 未登录或已失效")
		}
		accounts = append(accounts, CookieAccount{AuthUser: authUser, Email: email})
	}
	return accounts, nil
}

// withMissingCookieHint 在官网按未登录拒绝 Cookie 时指出缺少的 gemini.google.com 站点 Cookie 或 __Secure-1PSIDTS
func withMissingCookieHint(cookies []auth.StateCookie, err error) error {
	if !errors.Is(err, gemini.ErrSignedOut) {
		return err
	}
	hasAccounts := hasAccountsSessionCookie(cookies)
	hasOSID := hasAccounts || hasGeminiCookie(cookies, "OSID")
	hasSecureOSID := hasAccounts || hasGeminiCookie(cookies, "__Secure-OSID")
	hasPSIDTS := hasGoogleCookie(cookies, "__Secure-1PSIDTS")
	switch {
	case !hasOSID && !hasSecureOSID && !hasPSIDTS:
		return fmt.Errorf("%w：Cookie 中没有 OSID、__Secure-OSID、__Secure-1PSIDTS，官网按未登录应答，请复制完整的 Cookie", err)
	case !hasOSID && !hasSecureOSID:
		return fmt.Errorf("%w：Cookie 中没有 OSID、__Secure-OSID，官网按未登录应答，请复制完整的 Cookie", err)
	case !hasOSID:
		return fmt.Errorf("%w：Cookie 中没有 OSID，官网按未登录应答，请复制完整的 Cookie", err)
	case !hasSecureOSID:
		return fmt.Errorf("%w：Cookie 中没有 __Secure-OSID，官网按未登录应答，请复制完整的 Cookie", err)
	case !hasPSIDTS:
		return fmt.Errorf("%w：Cookie 中没有 __Secure-1PSIDTS，官网按未登录应答，复制 Cookie 时带上这一项", err)
	default:
		return err
	}
}

// hasAccountsSessionCookie 报告 Cookie 列表中是否包含 accounts.google.com 的登录会话 Cookie（有该会话时打开首页可经 ServiceLogin/SetOSID 自动签发 gemini.google.com 的 OSID）
func hasAccountsSessionCookie(cookies []auth.StateCookie) bool {
	return slices.ContainsFunc(cookies, func(cookie auth.StateCookie) bool {
		if cookie.Name != "LSID" && cookie.Name != "__Host-1PLSID" && cookie.Name != "__Host-3PLSID" {
			return false
		}
		domain := strings.ToLower(strings.TrimSpace(cookie.Domain))
		return domain == "accounts.google.com" || domain == ".accounts.google.com"
	})
}

// hasGeminiCookie 报告 Cookie 列表中是否包含发往 gemini.google.com 的指定 Cookie（排除 console.cloud.google.com 等其他子域的同名 Cookie）
func hasGeminiCookie(cookies []auth.StateCookie, name string) bool {
	return slices.ContainsFunc(cookies, func(cookie auth.StateCookie) bool {
		if cookie.Name != name {
			return false
		}
		domain := strings.ToLower(strings.TrimSpace(cookie.Domain))
		return domain == "gemini.google.com" || domain == ".gemini.google.com" || domain == ".google.com" || domain == "google.com"
	})
}

// hasGoogleCookie 报告 Cookie 列表中是否包含发往 google.com 或 gemini.google.com 的指定 Cookie
func hasGoogleCookie(cookies []auth.StateCookie, name string) bool {
	return hasGeminiCookie(cookies, name)
}
