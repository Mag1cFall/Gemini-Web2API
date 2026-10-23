package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/adapter"
	"github.com/gin-gonic/gin"
)

// sessionCookie 是管理端登录会话的 Cookie 名
const sessionCookie = "gw_admin"

// sessionLifetime 是登录会话的有效期
const sessionLifetime = 12 * time.Hour

// loginLimit 是同一来源一分钟内允许的登录失败次数
const loginLimit = 5

// adminSession 是一个登录会话，ctx 在退出登录、会话撤销或到期时结束
type adminSession struct {
	expires time.Time
	ctx     context.Context
	cancel  context.CancelFunc
}

// sessionStore 保存已登录的管理端会话
type sessionStore struct {
	mu     sync.Mutex
	tokens map[string]*adminSession
}

func (store *sessionStore) create() string {
	data := make([]byte, 32)
	_, _ = rand.Read(data)
	token := hex.EncodeToString(data)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.tokens == nil {
		store.tokens = make(map[string]*adminSession)
	}
	now := time.Now()
	for key, session := range store.tokens {
		if now.After(session.expires) {
			session.cancel()
			delete(store.tokens, key)
		}
	}
	expires := now.Add(sessionLifetime)
	ctx, cancel := context.WithDeadline(context.Background(), expires)
	store.tokens[token] = &adminSession{expires: expires, ctx: ctx, cancel: cancel}
	return token
}

// lookup 返回未到期的会话
func (store *sessionStore) lookup(token string) *adminSession {
	store.mu.Lock()
	defer store.mu.Unlock()
	session := store.tokens[token]
	if session == nil || !time.Now().Before(session.expires) {
		return nil
	}
	return session
}

func (store *sessionStore) remove(token string) {
	store.mu.Lock()
	if session := store.tokens[token]; session != nil {
		session.cancel()
		delete(store.tokens, token)
	}
	store.mu.Unlock()
}

func (store *sessionStore) clear() {
	store.mu.Lock()
	for _, session := range store.tokens {
		session.cancel()
	}
	store.tokens = nil
	store.mu.Unlock()
}

// loginAttempts 记录各来源地址一分钟窗口内的登录失败次数
type loginAttempts struct {
	mu      sync.Mutex
	entries map[string]loginAttempt
}

// loginAttempt 是一个来源地址的失败次数与窗口结束时间
type loginAttempt struct {
	count int
	until time.Time
}

// blocked 报告来源地址在当前窗口内的失败次数已达上限，同时清理过期窗口
func (attempts *loginAttempts) blocked(peer string, now time.Time) bool {
	attempts.mu.Lock()
	defer attempts.mu.Unlock()
	for address, attempt := range attempts.entries {
		if !now.Before(attempt.until) {
			delete(attempts.entries, address)
		}
	}
	return attempts.entries[peer].count >= loginLimit
}

// fail 记一次失败，窗口从该来源的第一次失败开始
func (attempts *loginAttempts) fail(peer string, now time.Time) {
	attempts.mu.Lock()
	defer attempts.mu.Unlock()
	if attempts.entries == nil {
		attempts.entries = make(map[string]loginAttempt)
	}
	attempt := attempts.entries[peer]
	if attempt.count == 0 {
		attempt.until = now.Add(time.Minute)
	}
	attempt.count++
	attempts.entries[peer] = attempt
}

func (attempts *loginAttempts) reset(peer string) {
	attempts.mu.Lock()
	delete(attempts.entries, peer)
	attempts.mu.Unlock()
}

// sessionView 是管理端登录状态
type sessionView struct {
	Enabled       bool   `json:"enabled"`
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username"`
}

// guard 拒绝跨站请求；未开启登录时只接受本机访问，开启后要求有效会话
func (h *handler) guard(c *gin.Context) {
	if !sameOrigin(c.Request) {
		writeError(c, http.StatusForbidden, "admin_cross_origin", "管理端拒绝跨站请求")
		return
	}
	cfg := h.settings.Snapshot()
	if !cfg.AdminAuthEnabled {
		if !loopbackRequest(c.Request) {
			writeError(c, http.StatusForbidden, "admin_remote_forbidden", "管理端只接受本机访问；远程访问需设置 ADMIN_AUTH_ENABLED=true 与 ADMIN_PASSWORD")
			return
		}
		c.Next()
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/auth/") {
		c.Next()
		return
	}
	session := h.loginSession(c)
	if session == nil {
		writeError(c, http.StatusUnauthorized, "admin_login_required", "需要登录管理端")
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	stop := context.AfterFunc(session.ctx, cancel)
	defer stop()
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	c.Next()
}

// loginSession 返回请求 Cookie 对应的有效会话
func (h *handler) loginSession(c *gin.Context) *adminSession {
	token, err := c.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return h.sessions.lookup(token)
}

func (h *handler) authenticated(c *gin.Context) bool {
	return h.loginSession(c) != nil
}

func (h *handler) session(c *gin.Context) {
	cfg := h.settings.Snapshot()
	c.JSON(http.StatusOK, sessionView{
		Enabled: cfg.AdminAuthEnabled, Authenticated: !cfg.AdminAuthEnabled || h.authenticated(c), Username: cfg.AdminUsername,
	})
}

func (h *handler) login(c *gin.Context) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bindJSON(c, &input) {
		return
	}
	cfg := h.settings.Snapshot()
	if !cfg.AdminAuthEnabled {
		c.JSON(http.StatusOK, sessionView{Enabled: false, Authenticated: true, Username: cfg.AdminUsername})
		return
	}
	peer, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
	now := time.Now()
	if h.attempts.blocked(peer, now) {
		c.Header("Retry-After", "60")
		writeError(c, http.StatusTooManyRequests, "admin_login_rate_limited", "登录失败次数过多，请一分钟后再试")
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(input.Username)), []byte(cfg.AdminUsername)) == 1
	passwordOK := subtle.ConstantTimeCompare([]byte(input.Password), []byte(cfg.AdminPassword)) == 1
	if !userOK || !passwordOK {
		h.attempts.fail(peer, now)
		writeError(c, http.StatusUnauthorized, "admin_login_failed", "用户名或密码错误")
		return
	}
	h.attempts.reset(peer)
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: h.sessions.create(), Path: "/", MaxAge: int(sessionLifetime / time.Second),
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: c.Request.TLS != nil,
	})
	c.JSON(http.StatusOK, sessionView{Enabled: true, Authenticated: true, Username: cfg.AdminUsername})
}

func (h *handler) logout(c *gin.Context) {
	if token, err := c.Cookie(sessionCookie); err == nil {
		h.sessions.remove(token)
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	c.Status(http.StatusNoContent)
}

// sameOrigin 报告浏览器请求的 Origin 与 Host 一致，非浏览器请求没有 Origin
func sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, request.Host)
}

// loopbackRequest 报告请求来自本机且 Host 为回环地址，防止 DNS 重绑定
func loopbackRequest(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && adapter.LoopbackHost(request.Host)
}
