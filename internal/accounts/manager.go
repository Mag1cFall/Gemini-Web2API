// Package accounts 管理账号的初始化、恢复、Cookie 轮换与运行期增删
package accounts

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/proxypool"
)

// Settings 是账号生命周期使用的配置
type Settings struct {
	// InitTimeout 是单个账号一次初始化的超时
	InitTimeout time.Duration
	// RetryInterval 是初始化失败后再次初始化的间隔
	RetryInterval time.Duration
	// Proxy 是以逗号分隔的代理池，未设置固定代理的账号与访客会话从中分配
	Proxy string
	// SaveHistory 表示生成请求写入官网历史记录
	SaveHistory bool
	// AuthDir 是管理端导入的账号写入的目录
	AuthDir string
	// Concurrency 是每个账号同时进行的上游请求数
	Concurrency int
}

// Manager 为每个账号运行初始化、失败恢复与 Cookie 轮换，并支持运行期增删
type Manager struct {
	ctx      context.Context
	pool     *balancer.AccountPool
	mu       sync.Mutex
	settings Settings
	accounts map[string]*managed
	order    []string
	// guest 是访客会话的生命周期
	guest *managed
	// operations 串行化同一账号的停止与重启
	operations sync.Map
	// proxies 是未设置固定代理的账号共用的代理池，proxyCheck 请求立即检测代理池
	proxies    *proxypool.Pool
	proxyCheck chan struct{}
	// guestMu 串行化访客会话的重启
	guestMu sync.Mutex
}

type managed struct {
	account auth.LoadedAccount
	cancel  context.CancelFunc
	done    chan struct{}
}

// operate 占用账号的操作锁，返回释放函数
func (m *Manager) operate(accountID string) func() {
	lock, _ := m.operations.LoadOrStore(accountID, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

// New 创建账号管理器，ctx 结束时全部账号停止
func New(ctx context.Context, pool *balancer.AccountPool, settings Settings) *Manager {
	manager := &Manager{
		ctx: ctx, pool: pool, settings: settings, accounts: make(map[string]*managed),
		proxies: proxypool.New(config.SplitList(settings.Proxy)), proxyCheck: make(chan struct{}, 1),
	}
	pool.OnTransportError(manager.requestProxyCheck)
	pool.OnProxyBlocked(manager.MarkProxyBlocked)
	pool.SetReconnector(func(ctx context.Context, accountID string) error {
		return manager.Verify(accountID)
	})
	return manager
}

// Pool 返回账号池
func (m *Manager) Pool() *balancer.AccountPool {
	return m.pool
}

// Settings 返回当前账号配置
func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

// Start 登记并启动全部账号与访客会话，返回后首轮初始化全部结束
func (m *Manager) Start(accounts []auth.LoadedAccount) {
	var wait sync.WaitGroup
	for _, account := range accounts {
		wait.Add(1)
		m.launch(account, wait.Done)
	}
	wait.Add(1)
	m.launchGuest(wait.Done)
	wait.Wait()
	go m.watchProxies()
	go m.watchQuotas()
}

// guestReloadInterval 是访客会话重读首页与模型目录的间隔，与账号的默认轮换间隔相同
const guestReloadInterval = 10 * time.Minute

// launchGuest 创建访客客户端并启动其生命周期，firstDone 在首轮初始化结束时调用
func (m *Manager) launchGuest(firstDone func()) {
	settings := m.Settings()
	proxy, source := m.poolProxy(gemini.GuestID)
	m.pool.Register(gemini.GuestID, balancer.AccountInfo{Source: balancer.SourceGuest, Proxy: proxy, ProxySource: source}, false)
	client, err := gemini.NewGuestClient(proxy, settings.Concurrency)
	if err != nil {
		m.pool.MarkFailed(gemini.GuestID, err)
		logging.Error(gemini.GuestID, "访客会话创建失败", "错误", err)
		firstDone()
		return
	}
	m.pool.SetClient(gemini.GuestID, client)
	ctx, cancel := context.WithCancel(m.ctx)
	entry := &managed{cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.guest = entry
	m.mu.Unlock()
	go func() {
		defer close(entry.done)
		m.runGuest(ctx, client, firstDone)
	}()
}

// runGuest 初始化访客会话直到成功，之后定期重读首页与模型目录，重读失败时保留原有参数
func (m *Manager) runGuest(ctx context.Context, client *gemini.Client, firstDone func()) {
	var once sync.Once
	first := func() { once.Do(firstDone) }
	defer first()
	for {
		settings := m.Settings()
		started := time.Now()
		initContext, cancel := context.WithTimeout(ctx, settings.InitTimeout)
		err := client.Init(initContext)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			m.pool.MarkReady(gemini.GuestID)
			logging.Info(gemini.GuestID, "访客会话就绪", "模型", len(client.Models()), "耗时", time.Since(started))
			break
		}
		if m.pool.MarkFailed(gemini.GuestID, err) {
			logging.Warn(gemini.GuestID, "访客会话初始化失败", "耗时", time.Since(started), "下次重试", settings.RetryInterval, "错误", err)
		}
		first()
		if !sleep(ctx, settings.RetryInterval) {
			return
		}
	}
	first()
	for sleep(ctx, guestReloadInterval) {
		before := catalogOf(client)
		initContext, cancel := context.WithTimeout(ctx, m.Settings().InitTimeout)
		err := client.Init(initContext)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			m.pool.RecordError(gemini.GuestID, err)
			logging.Warn(gemini.GuestID, "访客会话重读失败", "下次", guestReloadInterval, "错误", err)
			continue
		}
		logCatalogChange(gemini.GuestID, before, catalogOf(client))
	}
}

// stopGuest 停止访客会话的生命周期协程并等待其退出
func (m *Manager) stopGuest() {
	m.mu.Lock()
	entry := m.guest
	m.guest = nil
	m.mu.Unlock()
	if entry != nil {
		entry.cancel()
		<-entry.done
	}
}

// Add 登记并启动一个新导入的账号，返回后首轮初始化结束
func (m *Manager) Add(account auth.LoadedAccount) error {
	m.mu.Lock()
	_, exists := m.accounts[account.ID]
	m.mu.Unlock()
	if exists {
		return fmt.Errorf("账号 %q 已存在", account.ID)
	}
	done := make(chan struct{})
	var once sync.Once
	m.launch(account, func() { once.Do(func() { close(done) }) })
	select {
	case <-done:
	case <-m.ctx.Done():
	}
	return nil
}

// launch 创建账号客户端并启动其生命周期，firstDone 在首轮初始化结束或账号停用时调用
func (m *Manager) launch(account auth.LoadedAccount, firstDone func()) {
	proxy, source := m.accountProxy(account)
	account.ProxyURL = proxy
	accountSource := "cookie"
	if account.Browser != "" {
		accountSource = account.Browser
	} else if account.OAuth != nil {
		accountSource = "chrome"
	}
	info := balancer.AccountInfo{Source: accountSource, Profile: account.Profile, Proxy: proxy, ProxySource: source}
	if account.File != nil {
		info.Path = account.File.Path()
	}
	m.pool.Register(account.ID, info, account.Disabled)
	m.mu.Lock()
	if _, exists := m.accounts[account.ID]; !exists {
		m.order = append(m.order, account.ID)
	}
	entry := &managed{account: account}
	m.accounts[account.ID] = entry
	settings := m.settings
	m.mu.Unlock()
	if account.Disabled {
		firstDone()
		return
	}

	if account.OAuth != nil {
		account.Source.Refresh = newChromeRenewal(account).refresh
	} else if account.Browser != "" && account.Profile != "" {
		account.Source.Refresh = newBrowserRenewal(account).refresh
	}
	account.Source.Concurrency = settings.Concurrency
	client, err := gemini.NewClient(account.Source, account.ProxyURL, settings.SaveHistory)
	if err != nil {
		m.pool.MarkFailed(account.ID, err)
		logging.Error(account.ID, "账号创建失败", "错误", err)
		firstDone()
		return
	}
	m.pool.SetClient(account.ID, client)
	ctx, cancel := context.WithCancel(m.ctx)
	entry.cancel, entry.done = cancel, make(chan struct{})
	go func() {
		defer close(entry.done)
		m.run(ctx, account, client, firstDone)
	}()
}

// run 初始化账号直到成功，之后按轮换页给出的间隔轮换 Cookie 直到 ctx 结束
func (m *Manager) run(ctx context.Context, account auth.LoadedAccount, client *gemini.Client, firstDone func()) {
	var once sync.Once
	first := func() { once.Do(firstDone) }
	defer first()
	for {
		settings := m.Settings()
		started := time.Now()
		err := initialize(ctx, client, settings.InitTimeout)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			m.pool.MarkReady(account.ID)
			logReady(account, client, time.Since(started))
			break
		}
		if m.pool.MarkFailed(account.ID, err) {
			logging.Error(account.ID, "账号初始化失败", "耗时", time.Since(started), "下次重试", settings.RetryInterval, "错误", err)
		} else {
			logging.Debug(account.ID, "账号初始化失败", "错误", err)
		}
		first()
		if !sleep(ctx, settings.RetryInterval) {
			return
		}
	}
	first()

	rotator := gemini.NewCookieRotator(client)
	for {
		started := time.Now()
		before := catalogOf(client)
		next, err := rotator.Rotate(ctx)
		if ctx.Err() != nil {
			return
		}
		state := m.state(account.ID)
		switch {
		case err == nil:
			logCatalogChange(account.ID, before, catalogOf(client))
			if state == balancer.StateAuthRequired || state == balancer.StateUnavailable {
				m.pool.MarkReady(account.ID)
				logging.Info(account.ID, "账号恢复就绪", "耗时", time.Since(started))
			}
			logging.Debug(account.ID, "Cookie 轮换完成", "耗时", time.Since(started), "下次", next)
		case rotationTooSoon(err):
			logging.Debug(account.ID, "Cookie 轮换过早", "下次", next)
		case gemini.IsAuthenticationError(err):
			if m.pool.MarkFailed(account.ID, err) {
				logging.Warn(account.ID, "账号会话失效", "下次检查", next, "错误", err)
			}
		case errors.Is(err, gemini.ErrAccountRestricted):
			if m.pool.MarkFailed(account.ID, err) {
				logging.Warn(account.ID, "账号不可用", "下次检查", next, "错误", err)
			}
		default:
			m.pool.RecordError(account.ID, err)
			logging.Warn(account.ID, "Cookie 轮换失败", "下次", next, "错误", err)
		}
		if !sleep(ctx, next) {
			return
		}
	}
}

// initialize 读取首页参数、模型目录、偏好与用量
func initialize(ctx context.Context, client *gemini.Client, timeout time.Duration) error {
	initContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := client.Init(initContext); err != nil {
		return err
	}
	_, err := client.FetchUsage(initContext)
	return err
}

// rotationTooSoon 判断轮换失败是否为距上次轮换过近的 429
func rotationTooSoon(err error) bool {
	var protocolErr *gemini.ProtocolError
	return errors.As(err, &protocolErr) && protocolErr.Code == 429
}

func logReady(account auth.LoadedAccount, client *gemini.Client, duration time.Duration) {
	fields := []any{"模型", len(client.Models()), "窗口", client.ContextWindow()}
	if usage := client.CachedUsage(); usage != nil {
		fields = append([]any{"层级", usage.Tier}, fields...)
	}
	if email := client.Email(); email != "" {
		fields = append([]any{"邮箱", email}, fields...)
	}
	if account.ProxyURL != "" {
		fields = append(fields, "代理", maskProxy(account.ProxyURL))
	}
	fields = append(fields, "耗时", duration)
	logging.Info(account.ID, "账号就绪", fields...)
}

// accountCatalog 是账号的层级与模型 ID，用于发现定期重读后的变化
type accountCatalog struct {
	tier   string
	models []string
}

func catalogOf(client *gemini.Client) accountCatalog {
	var catalog accountCatalog
	if usage := client.CachedUsage(); usage != nil {
		catalog.tier = usage.Tier
	}
	for _, model := range client.Models() {
		catalog.models = append(catalog.models, model.ID)
	}
	return catalog
}

// watchQuotas 定时检查各账号的用量重置时间，到期时自动向 Google 刷新用量
func (m *Manager) watchQuotas() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.refreshDueQuotas()
		}
	}
}

// refreshDueQuotas 为配额重置时间已过或用量已耗尽的账号自动向 Google 刷新最新用量
func (m *Manager) refreshDueQuotas() {
	m.mu.Lock()
	entries := make([]*managed, 0, len(m.accounts))
	for _, entry := range m.accounts {
		if entry != nil && !entry.account.Disabled {
			entries = append(entries, entry)
		}
	}
	m.mu.Unlock()

	now := time.Now()
	for _, entry := range entries {
		client := m.pool.Client(entry.account.ID)
		if client == nil {
			continue
		}
		usage := client.CachedUsage()
		shouldRefresh := false
		if usage != nil {
			if usage.Current != nil && usage.Current.UsedRatio >= 1 && now.After(usage.Current.ResetAt) {
				shouldRefresh = true
			}
			if usage.Weekly != nil && usage.Weekly.UsedRatio >= 1 && now.After(usage.Weekly.ResetAt) {
				shouldRefresh = true
			}
		}
		if shouldRefresh {
			accountID := entry.account.ID
			go func(id string, c *gemini.Client) {
				ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
				defer cancel()
				newUsage, err := c.FetchUsage(ctx)
				if err == nil {
					logging.Info(id, "账号配额已到期自动刷新", "层级", newUsage.Tier)
					if newUsage.Current != nil && newUsage.Current.UsedRatio < 1 {
						_ = m.pool.ClearCooldown(id, "")
					}
				}
			}(accountID, client)
		}
	}
}

// logCatalogChange 在账号层级或模型目录变化时写一行日志，列出新旧层级与增减的模型
func logCatalogChange(accountID string, before, after accountCatalog) {
	var fields []any
	if before.tier != after.tier {
		fields = append(fields, "层级", before.tier+" → "+after.tier)
	}
	if added := missingFrom(after.models, before.models); len(added) > 0 {
		fields = append(fields, "新增", strings.Join(added, ","))
	}
	if removed := missingFrom(before.models, after.models); len(removed) > 0 {
		fields = append(fields, "移除", strings.Join(removed, ","))
	}
	if len(fields) > 0 {
		logging.Info(accountID, "账号目录变化", fields...)
	}
}

// missingFrom 返回 ids 中不在 others 里的 ID
func missingFrom(ids, others []string) []string {
	var missing []string
	for _, id := range ids {
		if !slices.Contains(others, id) {
			missing = append(missing, id)
		}
	}
	return missing
}

func (m *Manager) state(accountID string) string {
	for _, account := range m.pool.Status().Accounts {
		if account.AccountID == accountID {
			return account.State
		}
	}
	return ""
}

// stop 停止账号的生命周期协程并等待其退出
func (m *Manager) stop(accountID string) (*managed, error) {
	m.mu.Lock()
	entry := m.accounts[accountID]
	m.mu.Unlock()
	if entry == nil {
		return nil, fmt.Errorf("账号 %q 不存在", accountID)
	}
	if entry.cancel != nil {
		entry.cancel()
		<-entry.done
		entry.cancel = nil
	}
	return entry, nil
}

// Remove 停止并删除账号与其认证状态文件
func (m *Manager) Remove(accountID string) error {
	defer m.operate(accountID)()
	entry, err := m.stop(accountID)
	if err != nil {
		return err
	}
	m.pool.Remove(accountID)
	m.proxies.Release(accountID)
	m.mu.Lock()
	delete(m.accounts, accountID)
	for index, id := range m.order {
		if id == accountID {
			m.order = append(m.order[:index], m.order[index+1:]...)
			break
		}
	}
	m.mu.Unlock()
	if entry.account.File != nil {
		path := entry.account.File.Path()
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除认证状态 %s: %w", path, err)
		}
		_ = os.Remove(filepath.Dir(path))
	}
	logging.Info(accountID, "账号已删除")
	return nil
}

// SetEnabled 启用或停用账号并写回认证状态
func (m *Manager) SetEnabled(accountID string, enabled bool) error {
	defer m.operate(accountID)()
	entry, err := m.stop(accountID)
	if err != nil {
		return err
	}
	if entry.account.File != nil {
		if err := entry.account.File.SetDisabled(!enabled); err != nil {
			return err
		}
	}
	entry.account.Disabled = !enabled
	if !enabled {
		m.pool.SetDisabled(accountID, true)
		logging.Info(accountID, "账号已停用")
		return nil
	}
	m.pool.SetDisabled(accountID, false)
	logging.Info(accountID, "账号已启用")
	return m.restart(entry.account)
}

// SetProxy 修改账号固定代理并重新初始化账号，空值表示从代理池分配，direct 表示直连
func (m *Manager) SetProxy(accountID string, proxy string) error {
	proxy = strings.TrimSpace(proxy)
	if proxy != "" && proxy != proxypool.Direct {
		if _, err := chromeauth.ValidateProxy(proxy); err != nil {
			return err
		}
	}
	defer m.operate(accountID)()
	entry, err := m.stop(accountID)
	if err != nil {
		return err
	}
	if entry.account.File != nil {
		if err := entry.account.File.SetProxy(proxy); err != nil {
			return err
		}
	}
	entry.account.ProxyURL, _ = m.accountProxy(entry.account)
	logging.Info(accountID, "账号代理已更新", "代理", cmp.Or(maskProxy(entry.account.ProxyURL), "直连"))
	return m.restart(entry.account)
}

// Verify 重新初始化账号并返回结果
func (m *Manager) Verify(accountID string) error {
	defer m.operate(accountID)()
	entry, err := m.stop(accountID)
	if err != nil {
		return err
	}
	if entry.account.Disabled {
		return fmt.Errorf("账号 %q 已停用", accountID)
	}
	logging.Info(accountID, "账号重新验证")
	if entry.account.OAuth != nil {
		if cookies, err := newChromeRenewal(entry.account).refresh(m.ctx); err == nil && len(cookies) > 0 {
			entry.account.Source.Cookies = cookies
		}
	} else if entry.account.Browser != "" && entry.account.Profile != "" {
		if cookies, err := newBrowserRenewal(entry.account).refresh(m.ctx); err == nil && len(cookies) > 0 {
			entry.account.Source.Cookies = cookies
		}
	} else if entry.account.File != nil {
		if cookies := entry.account.File.Cookies(); len(cookies) > 0 {
			entry.account.Source.Cookies = cookies
		}
	}
	if err := m.restart(entry.account); err != nil {
		return err
	}
	for _, account := range m.pool.Status().Accounts {
		if account.AccountID == accountID && account.State != balancer.StateReady && account.State != balancer.StateCooldown {
			return errors.New(account.LastError)
		}
	}
	return nil
}

// SetSettings 修改账号配置并并行重新初始化受影响的账号：代理池变化时为分到的代理有变化的账号与访客会话，历史记录变化时为全部账号，并发设置变化时为全部账号与访客会话
func (m *Manager) SetSettings(settings Settings) {
	m.mu.Lock()
	previous := m.settings
	m.settings = settings
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	if previous.Proxy != settings.Proxy {
		m.proxies.SetProxies(config.SplitList(settings.Proxy))
		m.requestProxyCheck()
	}
	var wait sync.WaitGroup
	for _, id := range ids {
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer m.operate(id)()
			m.mu.Lock()
			entry := m.accounts[id]
			m.mu.Unlock()
			if entry == nil || entry.account.Disabled {
				return
			}
			account := entry.account
			proxyChanged := usesGlobalProxy(account) && m.proxies.Assign(id) != account.ProxyURL
			if proxyChanged || previous.SaveHistory != settings.SaveHistory || previous.Concurrency != settings.Concurrency {
				if _, err := m.stop(id); err == nil {
					_ = m.restart(account)
				}
			}
		}()
	}
	if previous.Proxy != settings.Proxy || previous.Concurrency != settings.Concurrency {
		wait.Add(1)
		go func() {
			defer wait.Done()
			m.relaunchGuest()
		}()
	}
	wait.Wait()
}

// usesGlobalProxy 报告账号没有固定代理、从代理池分配
func usesGlobalProxy(account auth.LoadedAccount) bool {
	return fixedProxy(account) == ""
}

// restart 以新客户端重新启动账号并等待首轮初始化结束
func (m *Manager) restart(account auth.LoadedAccount) error {
	done := make(chan struct{})
	var once sync.Once
	m.launch(account, func() { once.Do(func() { close(done) }) })
	select {
	case <-done:
		return nil
	case <-m.ctx.Done():
		return m.ctx.Err()
	}
}

// IDs 返回按登记顺序排列的账号 ID
func (m *Manager) IDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.order...)
}

// BrowserAccounts 列出本机指定浏览器中可导入的账号，并标出已导入的账号
func (m *Manager) BrowserAccounts(browser string) ([]BrowserAccount, error) {
	if browser == "" {
		browser = chromeauth.BrowserChrome
	}
	root, err := chromeauth.DefaultBrowserRoot(browser)
	if err != nil {
		return nil, err
	}
	discovered, err := chromeauth.DiscoverBrowser(browser, root)
	if err != nil {
		return nil, err
	}
	importedEmails := make(map[string]bool)
	importedIDs := make(map[string]bool)
	importedNoEmailProfiles := make(map[string]bool)
	m.mu.Lock()
	for id, entry := range m.accounts {
		importedIDs[strings.ToLower(id)] = true
		var email string
		if client := m.pool.Client(id); client != nil && client.Email() != "" {
			email = client.Email()
		}
		if email != "" {
			importedEmails[strings.ToLower(strings.TrimSpace(email))] = true
			importedIDs[chromeauth.AccountID(email)] = true
		}
		if entry != nil {
			b := entry.account.Browser
			if b == "" && entry.account.OAuth != nil {
				b = chromeauth.BrowserChrome
			}
			prof := strings.ToLower(entry.account.Profile)
			if email == "" && b != "" && prof != "" {
				importedNoEmailProfiles[b+"/"+prof] = true
			}
		}
	}
	m.mu.Unlock()

	result := make([]BrowserAccount, 0, len(discovered))
	for _, account := range discovered {
		isImported := false
		email := strings.ToLower(strings.TrimSpace(account.Email))
		targetID := chromeauth.AccountID(email)

		if email != "" && strings.Contains(email, "@") {
			if importedEmails[email] || (targetID != "" && importedIDs[targetID]) {
				isImported = true
			}
		} else {
			profID := chromeauth.AccountID(account.Profile)
			b := strings.ToLower(account.Browser)
			if b == "" {
				b = browser
			}
			profKey := b + "/" + strings.ToLower(account.Profile)
			if (profID != "" && importedIDs[profID]) || importedNoEmailProfiles[profKey] {
				isImported = true
			}
		}
		result = append(result, BrowserAccount{Account: account, Imported: isImported})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Profile < result[j].Profile })
	return result, nil
}

// ChromeAccounts 列出本机 Chrome 中可导入的账号，并标出已导入的账号
func (m *Manager) ChromeAccounts() ([]ChromeAccount, error) {
	return m.BrowserAccounts(chromeauth.BrowserChrome)
}

// BrowserAccount 是本机浏览器中的一个 Google 账号及其导入状态
type BrowserAccount struct {
	chromeauth.Account
	Imported bool `json:"imported"`
}

// ChromeAccount 是本机 Chrome 中的一个 Google 账号及其导入状态
type ChromeAccount = BrowserAccount

// ImportBrowser 按邮箱或 Profile 从本机指定浏览器导入账号并启动
func (m *Manager) ImportBrowser(ctx context.Context, browser string, targets []string, proxy string) ([]string, []chromeauth.ImportFailure, error) {
	if browser == "" {
		browser = chromeauth.BrowserChrome
	}
	root, err := chromeauth.DefaultBrowserRoot(browser)
	if err != nil {
		return nil, nil, err
	}
	settings := m.Settings()
	res, err := chromeauth.ImportBrowserWithDetails(ctx, chromeauth.BrowserImportOptions{
		Browser:      browser,
		BrowserRoot:  root,
		Output:       settings.AuthDir,
		Proxy:        proxy,
		RequestProxy: m.importProxy(),
		Targets:      targets,
	})
	if err != nil && len(res.Results) == 0 {
		return nil, res.Failures, err
	}
	ids := make([]string, 0, len(res.Results))
	for _, result := range res.Results {
		id, addErr := m.addFile(result.Path)
		if addErr != nil {
			target := result.Email
			if target == "" {
				target = result.Profile
			}
			res.Failures = append(res.Failures, chromeauth.ImportFailure{Target: target, Error: addErr.Error()})
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 && len(res.Failures) > 0 {
		return nil, res.Failures, fmt.Errorf("所有所选账号均导入失败: %s", res.Failures[0].Error)
	}
	return ids, res.Failures, nil
}

// ImportChrome 按邮箱从本机 Chrome 导入账号并启动
func (m *Manager) ImportChrome(ctx context.Context, emails []string, proxy string) ([]string, error) {
	ids, _, err := m.ImportBrowser(ctx, chromeauth.BrowserChrome, emails, proxy)
	return ids, err
}

// ImportCookie 从 Cookie Header、浏览器导出的 JSON 或 Netscape cookies.txt 导入账号并启动
func (m *Manager) ImportCookie(ctx context.Context, cookies string, id string, proxy string, authUser int) (string, error) {
	settings := m.Settings()
	result, _, err := chromeauth.ImportCookies(ctx, chromeauth.CookieImportOptions{Cookies: cookies, ID: id, Output: settings.AuthDir, Proxy: proxy, RequestProxy: m.importProxy(), AuthUser: authUser})
	if err != nil {
		return "", err
	}
	return m.addFile(result.Path)
}

// ImportState 校验 setup --remote 上传的认证状态，写入认证目录后新增或更新账号
func (m *Manager) ImportState(data []byte) (string, error) {
	var state auth.StorageState
	if err := json.Unmarshal(data, &state); err != nil {
		return "", fmt.Errorf("认证状态无效: %w", err)
	}
	id := chromeauth.AccountID(state.Metadata.ID)
	if id == "" || len(state.Cookies) == 0 {
		return "", errors.New("认证状态缺少账号 ID 或 Cookie")
	}
	path := filepath.Join(m.Settings().AuthDir, id, "storage-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("写入认证状态: %w", err)
	}
	return m.addFile(path)
}

// addFile 读取认证状态文件，账号已存在时以新文件重启该账号，否则新增
func (m *Manager) addFile(path string) (string, error) {
	file, err := auth.Load(path)
	if err != nil {
		return "", err
	}
	account := file.Account()
	m.mu.Lock()
	_, exists := m.accounts[account.ID]
	m.mu.Unlock()
	if exists {
		defer m.operate(account.ID)()
		if _, err := m.stop(account.ID); err != nil {
			return "", err
		}
		logging.Info(account.ID, "账号认证已更新")
		return account.ID, m.restart(account)
	}
	logging.Info(account.ID, "账号已导入")
	return account.ID, m.Add(account)
}

// maskProxy 隐藏代理 URL 中的密码
func maskProxy(value string) string {
	if at := strings.LastIndex(value, "@"); at > 0 {
		if scheme := strings.Index(value, "://"); scheme >= 0 && scheme < at {
			credentials := value[scheme+3 : at]
			if user, _, found := strings.Cut(credentials, ":"); found {
				return value[:scheme+3] + user + ":***" + value[at:]
			}
		}
	}
	return value
}

func sleep(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
