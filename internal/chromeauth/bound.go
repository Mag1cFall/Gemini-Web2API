package chromeauth

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// boundRefreshThreshold 是 Chromium kProactiveRefreshThreshold，绑定 Cookie 剩余寿命不超过它时主动刷新 DBSC 会话
const boundRefreshThreshold = 2 * time.Minute

// errBoundSessionRejected 表示 Google 拒绝刷新保存的 DBSC 会话
var errBoundSessionRejected = errors.New("Google 拒绝刷新设备绑定会话")

// boundChallengePattern 匹配 Secure-Session-Challenge 结构化列表中的挑战与可选会话 id
var boundChallengePattern = regexp.MustCompile(`"([^"]+)"(?:;id="([^"]*)")?`)

// boundRefreshHeaderOrder 是 Chrome RegistrationFetcher 发出 RotateBoundCookies 的请求头顺序
var boundRefreshHeaderOrder = []string{
	"content-length", "sec-fetch-mode", "sec-fetch-dest", "sec-fetch-site",
	"origin", "sec-secure-session-id", "secure-session-response",
	"user-agent", "accept-encoding", "accept-language", "cookie", "priority",
}

// RotateBoundCookies 在绑定 Cookie 剩余寿命不超过主动刷新阈值时按 Chromium RegistrationFetcher 的流程刷新 DBSC 会话，返回距下次刷新的等待时间；cookies 与 send 是账号的 Cookie 与请求通道
func RotateBoundCookies(ctx context.Context, session auth.BoundSession, wrappedKey []byte, chrome int, language string, cookies func() []gemini.Cookie, send func(*http.Request) (int, http.Header, error)) (time.Duration, error) {
	lifetime := boundCookieLifetime(cookies(), session, time.Now())
	if lifetime > boundRefreshThreshold {
		return lifetime - boundRefreshThreshold, nil
	}
	priority := "u=4, i"
	if lifetime == 0 {
		priority = "u=0, i"
	}
	request, err := boundRefreshRequest(ctx, session, chrome, language, priority, "")
	if err != nil {
		return 0, err
	}
	status, header, err := send(request)
	if err != nil {
		return 0, err
	}
	if status/100 != 2 {
		challenge := boundChallenge(header.Get("Secure-Session-Challenge"), session.ID)
		if status != http.StatusForbidden || challenge == "" {
			return 0, boundRefreshStatusError(status)
		}
		signed, err := signBoundChallenge(wrappedKey, challenge)
		if err != nil {
			return 0, err
		}
		if request, err = boundRefreshRequest(ctx, session, chrome, language, priority, signed); err != nil {
			return 0, err
		}
		if status, _, err = send(request); err != nil {
			return 0, err
		}
		if status/100 != 2 {
			return 0, boundRefreshStatusError(status)
		}
	}
	return boundCookieLifetime(cookies(), session, time.Now()) - boundRefreshThreshold, nil
}

// boundCookieLifetime 按 Chromium Session::MinimumBoundCookieLifetime 返回会话绑定 Cookie 中最早到期者的剩余寿命，缺失或已到期时为 0
func boundCookieLifetime(cookies []gemini.Cookie, session auth.BoundSession, now time.Time) time.Duration {
	lifetime := time.Duration(math.MaxInt64)
	for _, name := range session.Credentials {
		index := slices.IndexFunc(cookies, func(cookie gemini.Cookie) bool { return cookie.Name == name && cookie.Domain == ".google.com" })
		if index < 0 {
			return 0
		}
		if cookies[index].Expires > 0 {
			lifetime = min(lifetime, max(time.Unix(cookies[index].Expires, 0).Sub(now), 0))
		}
	}
	return lifetime
}

// boundRefreshRequest 按 Chrome RegistrationFetcher 的请求头构造一次 DBSC 刷新请求，signed 为刷新证明，首次请求为空
func boundRefreshRequest(ctx context.Context, session auth.BoundSession, chrome int, language string, priority string, signed string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, session.RefreshURL, strings.NewReader(""))
	if err != nil {
		return nil, fmt.Errorf("创建设备绑定刷新请求: %w", err)
	}
	request.Header.Set("Sec-Fetch-Mode", "no-cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("Sec-Fetch-Site", "same-site")
	request.Header.Set("Origin", "https://google.com")
	request.Header.Set("Sec-Secure-Session-Id", session.ID)
	if signed != "" {
		request.Header.Set("Secure-Session-Response", signed)
	}
	request.Header.Set("User-Agent", gemini.ChromeUserAgent(chrome))
	request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	request.Header.Set("Accept-Language", language)
	request.Header.Set("Priority", priority)
	request.Header[http.HeaderOrderKey] = boundRefreshHeaderOrder
	return request, nil
}

// boundChallenge 返回 Secure-Session-Challenge 中属于 sessionID 或未指明会话的挑战
func boundChallenge(header string, sessionID string) string {
	for _, match := range boundChallengePattern.FindAllStringSubmatch(header, -1) {
		if match[2] == "" || match[2] == sessionID {
			return match[1]
		}
	}
	return ""
}

// signBoundChallenge 以设备绑定密钥为挑战签发刷新证明
func signBoundChallenge(wrappedKey []byte, challenge string) (string, error) {
	bindingKey, err := openDeviceBindingKey(wrappedKey)
	if err != nil {
		return "", err
	}
	defer bindingKey.Close()
	return signBoundRefresh(bindingKey, challenge)
}

// signBoundRefresh 按 DBSC 规范与 Chromium CreateKeyRefreshHeaderAndPayload 签发 dbsc+jwt 刷新证明
func signBoundRefresh(bindingKey deviceBindingKey, challenge string) (string, error) {
	return signJWT(bindingKey, struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}{"ES256", "dbsc+jwt"}, struct {
		JWTID string `json:"jti"`
	}{challenge})
}

// boundRefreshStatusError 按 Chromium RegistrationFetcher 把 5xx 与 429 归为传输层错误，其余非 2xx 状态归为会话被拒
func boundRefreshStatusError(status int) error {
	if status >= http.StatusInternalServerError || status == http.StatusTooManyRequests {
		return fmt.Errorf("%w：设备绑定会话刷新返回 HTTP %d", gemini.ErrTransport, status)
	}
	return fmt.Errorf("%w（HTTP %d）", errBoundSessionRejected, status)
}
