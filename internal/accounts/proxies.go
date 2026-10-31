package accounts

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/proxypool"
)

// proxyCheckInterval 是定时检测代理池的间隔
const proxyCheckInterval = 5 * time.Minute

// accountProxy 返回账号实际使用的代理与来源：固定代理优先，direct 表示直连，否则从代理池分配
func (m *Manager) accountProxy(account auth.LoadedAccount) (string, string) {
	switch fixed := fixedProxy(account); fixed {
	case "":
		return m.poolProxy(account.ID)
	case proxypool.Direct:
		m.proxies.Release(account.ID)
		return "", "direct"
	default:
		m.proxies.Release(account.ID)
		return fixed, "fixed"
	}
}

// poolProxy 返回代理池为 id 分配的代理与来源，代理池为空时返回系统代理
func (m *Manager) poolProxy(id string) (string, string) {
	if m.proxies.Empty() {
		return proxypool.System(), "system"
	}
	return m.proxies.Assign(id), "pool"
}

// fixedProxy 返回账号状态中的固定代理
func fixedProxy(account auth.LoadedAccount) string {
	if account.File == nil {
		return ""
	}
	return strings.TrimSpace(account.File.Proxy())
}

// importProxy 返回导入时没有指定账号代理的请求使用的代理
func (m *Manager) importProxy() string {
	return m.proxies.Pick()
}

// requestProxyCheck 请求立即检测代理池，已有待处理的请求时忽略
func (m *Manager) requestProxyCheck() {
	select {
	case m.proxyCheck <- struct{}{}:
	default:
	}
}

// watchProxies 启动后立即检测代理池，之后按间隔或收到请求时检测，直到服务停止
func (m *Manager) watchProxies() {
	ticker := time.NewTicker(proxyCheckInterval)
	defer ticker.Stop()
	for {
		m.CheckProxies(m.ctx)
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		case <-m.proxyCheck:
		}
	}
}

// CheckProxies 检测代理池，把绑定在不可用代理上的账号与访客会话改绑到可用代理并重新初始化，返回检测后的状态
func (m *Manager) CheckProxies(ctx context.Context) []proxypool.Status {
	if m.proxies.Empty() {
		return m.proxies.Status()
	}
	moved := m.proxies.Check(ctx, gemini.ProbeProxy)
	for _, status := range m.proxies.Status() {
		if !status.Healthy {
			logging.Warn(logging.SourceService, "代理不可用", "代理", status.URL, "原因", status.Error)
		}
	}
	var wait sync.WaitGroup
	for _, id := range moved {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if id == gemini.GuestID {
				m.relaunchGuest()
				return
			}
			m.rebind(id)
		}()
	}
	wait.Wait()
	return m.proxies.Status()
}

// ProxyStatus 返回代理池中每个代理的状态
func (m *Manager) ProxyStatus() []proxypool.Status {
	return m.proxies.Status()
}

// rebind 为从代理池分配代理的账号重新分配，代理有变化时以新代理重新初始化
func (m *Manager) rebind(accountID string) {
	defer m.operate(accountID)()
	m.mu.Lock()
	entry := m.accounts[accountID]
	m.mu.Unlock()
	if entry == nil || entry.account.Disabled || !usesGlobalProxy(entry.account) {
		return
	}
	previous := entry.account.ProxyURL
	if m.proxies.Assign(accountID) == previous {
		return
	}
	stopped, err := m.stop(accountID)
	if err != nil {
		return
	}
	stopped.account.ProxyURL, _ = m.accountProxy(stopped.account)
	logging.Info(accountID, "账号代理切换", "原代理", cmp.Or(maskProxy(previous), "直连"), "新代理", cmp.Or(maskProxy(stopped.account.ProxyURL), "直连"))
	_ = m.restart(stopped.account)
}

// relaunchGuest 以重新分配的代理重启访客会话并等待首轮初始化结束
func (m *Manager) relaunchGuest() {
	m.guestMu.Lock()
	defer m.guestMu.Unlock()
	m.stopGuest()
	done := make(chan struct{})
	var once sync.Once
	m.launchGuest(func() { once.Do(func() { close(done) }) })
	select {
	case <-done:
	case <-m.ctx.Done():
	}
}

// MarkProxyBlocked 将遭 Google 人机验证拦截的代理移入冷却隔离，并将绑定在此代理上的账号与访客会话改绑
func (m *Manager) MarkProxyBlocked(proxyURL string, err error) {
	if m.proxies.Empty() || proxyURL == "" {
		return
	}
	moved := m.proxies.MarkCooldown(proxyURL, 30*time.Minute, "遭遇 Google 人机验证拦截 (302)")
	if len(moved) == 0 {
		return
	}
	logging.Warn(logging.SourceService, "代理遭 Google 人机验证拦截，已自动隔离冷却并重新分配账号", "代理", maskProxy(proxyURL), "重分配账号数", len(moved))
	go func() {
		sem := make(chan struct{}, 3)
		var wg sync.WaitGroup
		for _, id := range moved {
			accountID := id
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() {
					<-sem
					wg.Done()
				}()
				if accountID == gemini.GuestID {
					m.relaunchGuest()
					return
				}
				m.rebind(accountID)
			}()
		}
		wg.Wait()
	}()
}

// TestProxy 测试指定代理的连通性、延迟以及是否被 Google 风控拦截
func (m *Manager) TestProxy(ctx context.Context, proxy string) gemini.ProxyDiagnostic {
	proxy = strings.TrimSpace(proxy)
	targetProxy := proxy
	if proxy == "" {
		targetProxy = m.importProxy()
	} else if proxy == proxypool.Direct {
		targetProxy = ""
	} else {
		fixed, err := chromeauth.ValidateProxy(proxy)
		if err != nil {
			return gemini.ProxyDiagnostic{
				OK:      false,
				Status:  "invalid_format",
				Message: err.Error(),
				Error:   err.Error(),
			}
		}
		targetProxy = fixed
	}

	diag := gemini.DiagnoseProxy(ctx, targetProxy)
	if proxy == "" {
		if targetProxy != "" {
			diag.Message = fmt.Sprintf("（代理池分配 %s）%s", maskProxy(targetProxy), diag.Message)
		} else {
			diag.Message = fmt.Sprintf("（代理池為空，測試本機直連）%s", diag.Message)
		}
	}
	return diag
}
