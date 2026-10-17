package balancer

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// SourceGuest 是访客会话的来源
const SourceGuest = "guest"

// 账号状态
const (
	StateInitializing = "initializing"
	StateReady        = "ready"
	StateCooldown     = "cooldown"
	StateAuthRequired = "auth_required"
	StateUnavailable  = "unavailable"
	StateDisabled     = "disabled"
)

// AccountInfo 是账号的来源与出口
type AccountInfo struct {
	// Source 为 chrome（Chrome Profile 导入，可续签）、cookie（Cookie Header 导入）或 guest（未登录的访客会话）
	Source string
	// Profile 为 Chrome 导入时的 Profile 目录名
	Profile string
	// Proxy 为账号实际使用的代理 URL
	Proxy string
	// ProxySource 为代理来源：fixed（账号固定代理）、direct（直连）、pool（代理池）或 system（系统代理）
	ProxySource string
	// Path 为认证状态文件路径
	Path string
}

type cooldown struct {
	until  time.Time
	reason string
}

type accountEntry struct {
	Client           *gemini.Client
	AccountID        string
	Info             AccountInfo
	State            string
	LastError        string
	LastErrorAt      time.Time
	LastUsed         time.Time
	ReadyAt          time.Time
	Cooldown         cooldown
	ModelCooldowns   map[string]cooldown
	ConsecutiveFails int
}

// ModelCooldown 是单个模型的冷却
type ModelCooldown struct {
	Model  string    `json:"model"`
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}

// AccountStatus 是账号的调度状态、层级、冷却与最近错误
type AccountStatus struct {
	AccountID        string            `json:"account_id"`
	State            string            `json:"state"`
	Enabled          bool              `json:"enabled"`
	Source           string            `json:"source,omitempty"`
	Profile          string            `json:"profile,omitempty"`
	Proxy            string            `json:"proxy,omitempty"`
	ProxySource      string            `json:"proxy_source,omitempty"`
	Tier             string            `json:"tier,omitempty"`
	ContextWindow    int               `json:"context_window,omitempty"`
	Models           []string          `json:"models"`
	Pending          int               `json:"pending"`
	LastUsed         *time.Time        `json:"last_used,omitempty"`
	ReadyAt          *time.Time        `json:"ready_at,omitempty"`
	CooldownUntil    *time.Time        `json:"cooldown_until,omitempty"`
	CooldownReason   string            `json:"cooldown_reason,omitempty"`
	ModelCooldowns   []ModelCooldown   `json:"model_cooldowns,omitempty"`
	ConsecutiveFails int               `json:"consecutive_failures"`
	LastError        string            `json:"last_error,omitempty"`
	LastErrorAt      *time.Time        `json:"last_error_at,omitempty"`
	Usage            *gemini.UsageInfo `json:"usage,omitempty"`
}

// PoolStatus 汇总账号池状态
type PoolStatus struct {
	Total        int             `json:"total"`
	Available    int             `json:"available"`
	CoolingDown  int             `json:"cooling_down"`
	Initializing int             `json:"initializing"`
	AuthRequired int             `json:"auth_required"`
	Unavailable  int             `json:"unavailable"`
	Disabled     int             `json:"disabled"`
	Accounts     []AccountStatus `json:"accounts"`
	// Guest 是访客会话的状态，不计入账号数
	Guest *AccountStatus `json:"guest,omitempty"`
}

// Ready 报告是否有就绪的账号或访客会话可以接收请求
func (s PoolStatus) Ready() bool {
	return s.Available > 0 || s.Guest != nil && s.Guest.State == StateReady
}

// AccountUsageStatus 表示单个账号的官网用量
type AccountUsageStatus struct {
	AccountID string            `json:"account_id"`
	State     string            `json:"state"`
	Usage     *gemini.UsageInfo `json:"usage,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// PoolUsageStatus 汇总账号池官网用量
type PoolUsageStatus struct {
	Accounts []AccountUsageStatus `json:"accounts"`
}

type sessionBinding struct {
	AccountID string
	ExpiresAt time.Time
}

// 调度策略
const (
	// RoundRobin 在账号间轮流分配
	RoundRobin = "round-robin"
	// FillFirst 优先使用排在前面且有空闲请求槽的账号
	FillFirst = "fill-first"
)

// AccountPool 在健康账号之间调度请求并维护账号状态、冷却与显式会话粘性
type AccountPool struct {
	strategy      string
	entries       []*accountEntry
	sessions      map[string]sessionBinding
	index         uint64
	cooldown      time.Duration
	sessionTTL    time.Duration
	lastSessionGC time.Time
	mu            sync.Mutex
	transport     func()
}

// NewAccountPool 创建账号池，轮询从随机位置开始
func NewAccountPool(cooldown time.Duration, sessionTTL time.Duration) *AccountPool {
	return &AccountPool{
		sessions:   make(map[string]sessionBinding),
		cooldown:   cooldown,
		sessionTTL: sessionTTL,
		index:      rand.Uint64(),
	}
}

// SetStrategy 修改无会话请求的调度策略
func (p *AccountPool) SetStrategy(strategy string) {
	p.mu.Lock()
	p.strategy = strategy
	p.mu.Unlock()
}

// SetTimings 修改失败冷却时长与显式会话存活时长
func (p *AccountPool) SetTimings(cooldown time.Duration, sessionTTL time.Duration) {
	p.mu.Lock()
	p.cooldown, p.sessionTTL = cooldown, sessionTTL
	p.mu.Unlock()
}

// Register 登记账号，disabled 为 true 时登记为停用，否则为初始化中
func (p *AccountPool) Register(accountID string, info AccountInfo, disabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := StateInitializing
	if disabled {
		state = StateDisabled
	}
	if entry := p.entry(accountID); entry != nil {
		entry.Info, entry.State = info, state
		return
	}
	p.entries = append(p.entries, &accountEntry{AccountID: accountID, Info: info, State: state, ModelCooldowns: make(map[string]cooldown)})
}

// Add 添加一个已经完成协议初始化的账号
func (p *AccountPool) Add(client *gemini.Client, accountID string) {
	p.Register(accountID, AccountInfo{}, false)
	p.SetClient(accountID, client)
	p.MarkReady(accountID)
}

// SetClient 设置账号的协议客户端
func (p *AccountPool) SetClient(accountID string, client *gemini.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entry(accountID); entry != nil {
		entry.Client = client
	}
}

// MarkReady 把账号标为就绪并清除错误与冷却
func (p *AccountPool) MarkReady(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entry(accountID)
	if entry == nil || entry.State == StateDisabled {
		return
	}
	entry.State, entry.ReadyAt = StateReady, time.Now()
	entry.LastError, entry.LastErrorAt = "", time.Time{}
	entry.Cooldown, entry.ConsecutiveFails = cooldown{}, 0
}

// MarkFailed 把账号标为不可用或待认证并记录原因，返回状态是否变化
func (p *AccountPool) MarkFailed(accountID string, err error) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entry(accountID)
	if entry == nil || entry.State == StateDisabled {
		return false
	}
	state := StateUnavailable
	if gemini.IsAuthenticationError(err) {
		state = StateAuthRequired
	}
	p.reportTransport(err)
	changed := entry.State != state || entry.LastError != errorText(err)
	entry.State = state
	entry.LastError, entry.LastErrorAt = errorText(err), time.Now()
	return changed
}

// RecordError 记录账号最近一次后台操作错误，不改变调度状态
func (p *AccountPool) RecordError(accountID string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entry(accountID); entry != nil {
		entry.LastError, entry.LastErrorAt = errorText(err), time.Now()
	}
	p.reportTransport(err)
}

// OnTransportError 设置账号遇到连接、TLS 或代理错误时调用的函数
func (p *AccountPool) OnTransportError(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.transport = fn
}

// reportTransport 在 err 为连接、TLS 或代理错误时调用 OnTransportError 设置的函数，调用方持有 p.mu
func (p *AccountPool) reportTransport(err error) {
	if p.transport != nil && errors.Is(err, gemini.ErrTransport) {
		p.transport()
	}
}

// SetDisabled 停用或启用账号，启用后账号回到初始化中
func (p *AccountPool) SetDisabled(accountID string, disabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entry(accountID)
	if entry == nil {
		return
	}
	if disabled {
		entry.State = StateDisabled
		return
	}
	if entry.State == StateDisabled {
		entry.State = StateInitializing
	}
}

// SetProxy 更新账号显示的代理
func (p *AccountPool) SetProxy(accountID string, proxy string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entry(accountID); entry != nil {
		entry.Info.Proxy = proxy
	}
}

// Remove 删除账号并解除其会话绑定
func (p *AccountPool) Remove(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for index, entry := range p.entries {
		if entry.AccountID == accountID {
			p.entries = append(p.entries[:index], p.entries[index+1:]...)
			break
		}
	}
	for key, binding := range p.sessions {
		if binding.AccountID == accountID {
			delete(p.sessions, key)
		}
	}
}

// Client 返回账号的协议客户端
func (p *AccountPool) Client(accountID string) *gemini.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entry(accountID); entry != nil {
		return entry.Client
	}
	return nil
}

// NextForModel 为新请求选择真正支持目标模型的健康账号
func (p *AccountPool) NextForModel(sessionKey string, modelID string) (*gemini.Client, string) {
	return p.NextForModelExcluding(sessionKey, modelID, nil)
}

// NextForModelExcluding 为无会话重试选择未排除的同能力账号
func (p *AccountPool) NextForModelExcluding(sessionKey string, modelID string, excludedAccountIDs map[string]struct{}) (*gemini.Client, string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	p.cleanupSessions(now)

	if sessionKey != "" {
		if binding, ok := p.sessions[sessionKey]; ok && now.Before(binding.ExpiresAt) {
			entry := p.entry(binding.AccountID)
			if entry != nil && entry.schedulable(now, modelID) {
				entry.LastUsed = now
				p.sessions[sessionKey] = sessionBinding{AccountID: entry.AccountID, ExpiresAt: now.Add(p.sessionTTL)}
				return entry.Client, entry.AccountID
			}
			return nil, binding.AccountID
		}
	}

	entry := p.nextAvailable(now, modelID, excludedAccountIDs)
	if entry == nil {
		return nil, ""
	}

	entry.LastUsed = now
	if sessionKey != "" {
		p.sessions[sessionKey] = sessionBinding{AccountID: entry.AccountID, ExpiresAt: now.Add(p.sessionTTL)}
	}
	return entry.Client, entry.AccountID
}

// RetryAfter 返回目标模型最早结束冷却的时间，没有冷却中的账号时为零值
func (p *AccountPool) RetryAfter(modelID string) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var earliest time.Time
	for _, entry := range p.entries {
		if entry.Client == nil || entry.State != StateReady || !entry.supportsModel(modelID) {
			continue
		}
		until := entry.blockedUntil(now, modelID)
		if until.IsZero() {
			return time.Time{}
		}
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	return earliest
}

// ReportSuccess 清除账号与该模型的失败状态
func (p *AccountPool) ReportSuccess(accountID string, modelID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry := p.entry(accountID); entry != nil {
		entry.ConsecutiveFails = 0
		entry.Cooldown = cooldown{}
		delete(entry.ModelCooldowns, modelID)
	}
}

// ReportFailure 按错误类型把账号或该模型移入冷却，会话失效时把账号标为待认证，网络层失败只记录错误
func (p *AccountPool) ReportFailure(accountID string, modelID string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry := p.entry(accountID)
	if entry == nil {
		return
	}
	now := time.Now()
	entry.LastError, entry.LastErrorAt = errorText(err), now
	if errors.Is(err, gemini.ErrTransport) {
		logging.Warn(accountID, "网络错误，账号不冷却", "原因", err)
		p.reportTransport(err)
		return
	}
	entry.ConsecutiveFails++
	if gemini.IsAuthenticationError(err) {
		entry.State = StateAuthRequired
		logging.Warn(accountID, "账号待认证", "原因", err)
		return
	}
	modelScope, reason := failureScope(err)
	until := now.Add(p.cooldown)
	if reset := usageReset(entry.Client, err); !reset.IsZero() {
		until = reset
	}
	value := cooldown{until: until, reason: reason}
	if modelScope && modelID != "" {
		entry.ModelCooldowns[modelID] = value
		logging.Warn(accountID, "模型冷却", "模型", modelID, "至", until, "原因", reason)
		return
	}
	entry.Cooldown = value
	logging.Warn(accountID, "账号冷却", "至", until, "原因", reason)
}

// failureScope 返回错误是否只影响当前模型与冷却原因 code
func failureScope(err error) (bool, string) {
	var protocolErr *gemini.ProtocolError
	if errors.As(err, &protocolErr) {
		switch protocolErr.Code {
		case 1037:
			return true, "usage_limit"
		case 1095:
			return true, "rate_limited"
		case 1060:
			return false, "ip_restricted"
		}
	}
	switch {
	case errors.Is(err, gemini.ErrCaptcha):
		return false, "captcha"
	case errors.Is(err, gemini.ErrModelMismatch):
		return true, "model_mismatch"
	case errors.As(err, &protocolErr) && protocolErr.HTTPStatus == 429:
		return true, "rate_limited"
	}
	return false, "upstream_error"
}

// usageReset 对用量上限与模型降级返回官网已用满的用量窗口的重置时间
func usageReset(client *gemini.Client, err error) time.Time {
	var protocolErr *gemini.ProtocolError
	limited := errors.As(err, &protocolErr) && protocolErr.Code == 1037 || errors.Is(err, gemini.ErrModelMismatch)
	if !limited || client == nil {
		return time.Time{}
	}
	usage := client.CachedUsage()
	if usage == nil {
		return time.Time{}
	}
	var reset time.Time
	for _, window := range []*gemini.UsageWindow{usage.Current, usage.Weekly} {
		if window != nil && window.UsedRatio >= 1 && window.ResetAt.After(time.Now()) && window.ResetAt.After(reset) {
			reset = window.ResetAt
		}
	}
	return reset
}

// BindSession 将成功响应标识绑定到产生它的账号
func (p *AccountPool) BindSession(sessionKey string, accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if sessionKey == "" {
		return
	}
	now := time.Now()
	if entry := p.entry(accountID); entry == nil || entry.Client == nil {
		return
	}
	p.sessions[sessionKey] = sessionBinding{AccountID: accountID, ExpiresAt: now.Add(p.sessionTTL)}
}

// ClearSession 结束显式会话的账号粘性
func (p *AccountPool) ClearSession(sessionKey string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sessions, sessionKey)
}

// Size 返回当前可接收请求的账号与访客会话数量
func (p *AccountPool) Size() int {
	status := p.Status()
	if status.Guest != nil && status.Guest.State == StateReady {
		return status.Available + 1
	}
	return status.Available
}

// ReadyAccount 是一个已就绪账号的 ID 与客户端
type ReadyAccount struct {
	ID     string
	Client *gemini.Client
}

// ReadyAccounts 返回已就绪账号，用于汇总模型目录
func (p *AccountPool) ReadyAccounts() []ReadyAccount {
	p.mu.Lock()
	defer p.mu.Unlock()

	accounts := make([]ReadyAccount, 0, len(p.entries))
	for _, entry := range p.entries {
		if entry.Client != nil && entry.State == StateReady {
			accounts = append(accounts, ReadyAccount{ID: entry.AccountID, Client: entry.Client})
		}
	}
	return accounts
}

// ModelAccounts 返回每个模型当前可调度的账号数
func (p *AccountPool) ModelAccounts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	counts := make(map[string]int)
	for _, entry := range p.entries {
		if entry.Client == nil || entry.State != StateReady {
			continue
		}
		for _, model := range entry.Client.Models() {
			if entry.blockedUntil(now, model.ID).IsZero() {
				counts[model.ID]++
			}
		}
	}
	return counts
}

// HasModel 报告已就绪账号是否发现目标模型
func (p *AccountPool) HasModel(modelID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, entry := range p.entries {
		if entry.Client != nil && entry.State == StateReady && entry.supportsModel(modelID) {
			return true
		}
	}
	return false
}

// HasReadyModel 报告当前是否有可调度账号可以运行目标模型
func (p *AccountPool) HasReadyModel(modelID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for _, entry := range p.entries {
		if entry.schedulable(now, modelID) {
			return true
		}
	}
	return false
}

// Status 返回账号池状态快照
func (p *AccountPool) Status() PoolStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	status := PoolStatus{Accounts: make([]AccountStatus, 0, len(p.entries))}
	for _, entry := range p.entries {
		account := entry.status(now)
		if entry.guest() {
			status.Guest = &account
			continue
		}
		status.Total++
		switch account.State {
		case StateReady:
			status.Available++
		case StateCooldown:
			status.CoolingDown++
		case StateInitializing:
			status.Initializing++
		case StateAuthRequired:
			status.AuthRequired++
		case StateDisabled:
			status.Disabled++
		default:
			status.Unavailable++
		}
		status.Accounts = append(status.Accounts, account)
	}
	return status
}

// Usage 返回各账号最近读取的官网用量，refresh 为 true 时并发重新读取
func (p *AccountPool) Usage(ctx context.Context, refresh bool) PoolUsageStatus {
	p.mu.Lock()
	now := time.Now()
	accounts := make([]AccountUsageStatus, 0, len(p.entries))
	clients := make([]*gemini.Client, 0, len(p.entries))
	for _, entry := range p.entries {
		if entry.guest() {
			continue
		}
		account := AccountUsageStatus{AccountID: entry.AccountID, State: entry.status(now).State}
		var client *gemini.Client
		if entry.Client != nil && entry.State != StateDisabled {
			client = entry.Client
			account.Usage = entry.Client.CachedUsage()
		}
		accounts, clients = append(accounts, account), append(clients, client)
	}
	p.mu.Unlock()
	if !refresh {
		return PoolUsageStatus{Accounts: accounts}
	}

	var wait sync.WaitGroup
	for index, client := range clients {
		if client == nil {
			continue
		}
		wait.Add(1)
		go func(index int, client *gemini.Client) {
			defer wait.Done()
			usage, err := client.FetchUsage(ctx)
			if err != nil {
				accounts[index].Error = logging.SanitizeError(err.Error())
				return
			}
			accounts[index].Usage = &usage
		}(index, client)
	}
	wait.Wait()
	return PoolUsageStatus{Accounts: accounts}
}

func (e *accountEntry) status(now time.Time) AccountStatus {
	status := AccountStatus{
		AccountID: e.AccountID, State: e.State, Enabled: e.State != StateDisabled, Source: e.Info.Source, Profile: e.Info.Profile,
		Proxy: maskProxy(e.Info.Proxy), ProxySource: e.Info.ProxySource, Models: []string{}, ConsecutiveFails: e.ConsecutiveFails, LastError: e.LastError,
		LastUsed: timePointer(e.LastUsed), ReadyAt: timePointer(e.ReadyAt), LastErrorAt: timePointer(e.LastErrorAt),
	}
	if e.State == StateReady && now.Before(e.Cooldown.until) {
		status.State = StateCooldown
		status.CooldownUntil, status.CooldownReason = timePointer(e.Cooldown.until), e.Cooldown.reason
	}
	for model, value := range e.ModelCooldowns {
		if now.Before(value.until) {
			status.ModelCooldowns = append(status.ModelCooldowns, ModelCooldown{Model: model, Until: value.until, Reason: value.reason})
		}
	}
	sort.Slice(status.ModelCooldowns, func(i, j int) bool { return status.ModelCooldowns[i].Model < status.ModelCooldowns[j].Model })
	if e.Client != nil {
		status.Pending = e.Client.Pending()
		if e.State != StateInitializing && e.State != StateDisabled {
			for _, model := range e.Client.Models() {
				status.Models = append(status.Models, model.ID)
			}
			status.ContextWindow = e.Client.ContextWindow()
		}
		if usage := e.Client.CachedUsage(); usage != nil {
			status.Tier, status.Usage = usage.Tier, usage
		}
	}
	return status
}

// schedulable 报告账号此刻能否接收目标模型的请求
func (e *accountEntry) schedulable(now time.Time, modelID string) bool {
	return e.Client != nil && e.State == StateReady && e.blockedUntil(now, modelID).IsZero() && e.supportsModel(modelID)
}

// blockedUntil 返回账号或目标模型冷却的结束时间，没有冷却时为零值
func (e *accountEntry) blockedUntil(now time.Time, modelID string) time.Time {
	var until time.Time
	if now.Before(e.Cooldown.until) {
		until = e.Cooldown.until
	}
	if value, ok := e.ModelCooldowns[modelID]; ok && now.Before(value.until) && value.until.After(until) {
		until = value.until
	}
	return until
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

// maskProxy 隐藏代理 URL 中的密码
func maskProxy(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	if _, ok := parsed.User.Password(); ok {
		parsed.User = url.UserPassword(parsed.User.Username(), "***")
	}
	return parsed.String()
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return logging.SanitizeError(err.Error())
}

// nextAvailable 优先在账号中选择，没有可调度的账号时使用访客会话
func (p *AccountPool) nextAvailable(now time.Time, modelID string, excludedAccountIDs map[string]struct{}) *accountEntry {
	if entry := p.nextFrom(now, modelID, excludedAccountIDs, false); entry != nil {
		return entry
	}
	return p.nextFrom(now, modelID, excludedAccountIDs, true)
}

// nextFrom 在账号或访客会话中按上下文窗口与调度策略选择
func (p *AccountPool) nextFrom(now time.Time, modelID string, excludedAccountIDs map[string]struct{}, guest bool) *accountEntry {
	if len(p.entries) == 0 {
		return nil
	}

	eligible := func(entry *accountEntry) bool {
		_, excluded := excludedAccountIDs[entry.AccountID]
		return !excluded && entry.guest() == guest && entry.schedulable(now, modelID)
	}
	maxContextWindow := -1
	for _, entry := range p.entries {
		if eligible(entry) && entry.Client.ContextWindow() > maxContextWindow {
			maxContextWindow = entry.Client.ContextWindow()
		}
	}
	if maxContextWindow < 0 {
		return nil
	}

	start := p.index
	if p.strategy == FillFirst {
		start = 0
	}
	for _, idleOnly := range []bool{true, false} {
		for offset := uint64(0); offset < uint64(len(p.entries)); offset++ {
			idx := (start + offset) % uint64(len(p.entries))
			entry := p.entries[idx]
			if eligible(entry) && entry.Client.ContextWindow() == maxContextWindow && (!idleOnly || !entry.Client.Busy()) {
				p.index = idx + 1
				return entry
			}
		}
	}
	return nil
}

// guest 报告条目是否为访客会话
func (e *accountEntry) guest() bool {
	return e.Info.Source == SourceGuest
}

func (e *accountEntry) supportsModel(modelID string) bool {
	if modelID == "" {
		return true
	}
	if e.Client == nil {
		return false
	}
	_, err := e.Client.ResolveModel(modelID)
	return err == nil
}

func (p *AccountPool) entry(accountID string) *accountEntry {
	for _, entry := range p.entries {
		if entry.AccountID == accountID {
			return entry
		}
	}
	return nil
}

func (p *AccountPool) cleanupSessions(now time.Time) {
	if !p.lastSessionGC.IsZero() && now.Sub(p.lastSessionGC) < time.Minute {
		return
	}
	for key, binding := range p.sessions {
		if !now.Before(binding.ExpiresAt) {
			delete(p.sessions, key)
		}
	}
	p.lastSessionGC = now
}
