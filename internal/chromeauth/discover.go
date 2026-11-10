package chromeauth

import (
	"context"
	"errors"
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
			return nil, err
		}
		if email == "" {
			return nil, errors.New("Gemini 首页没有登录账号，Cookie 未登录或已失效")
		}
		accounts = append(accounts, CookieAccount{AuthUser: authUser, Email: email})
	}
	return accounts, nil
}
