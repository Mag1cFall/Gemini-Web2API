package balancer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/url"
	"slices"
	"sort"
	"strings"
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
	proxyBlocked  func(proxyURL string, err error)
	reconnect     func(ctx context.Context, accountID string) error
	// changed 在账号状态、冷却或请求槽变化时关闭并替换，唤醒等待账号的请求
	changed chan struct{}
	// queues 为按排队键分组、按到达顺序排列的等待请求唤醒通道
	queues map[string][]chan struct{}
	// restored 为上次运行保存、账号尚未就绪的冷却，账号就绪时恢复
	restored map[string][]Cooldown
	// saveCooldowns 为冷却变化后保存全部冷却的函数
	saveCooldowns func([]Cooldown)
	// persistMu 串行化从取冷却快照到保存完成的过程
	persistMu sync.Mutex
}

// Cooldown 是一个账号或其某个模型的冷却，Model 为空表示整个账号
type Cooldown struct {
	Account string
	Model   string
	Until   time.Time
	Reason  string
}

// NewAccountPool 创建账号池，轮询从随机位置开始
func NewAccountPool(cooldown time.Duration, sessionTTL time.Duration) *AccountPool {
	return &AccountPool{
		sessions:   make(map[string]sessionBinding),
		cooldown:   cooldown,
		sessionTTL: sessionTTL,
		index:      rand.Uint64(),
		changed:    make(chan struct{}),
		queues:     make(map[string][]chan struct{}),
		restored:   make(map[string][]Cooldown),
	}
}

// RestoreCooldowns 恢复上次运行保存的冷却，账号尚未就绪时在就绪时恢复
func (p *AccountPool) RestoreCooldowns(cooldowns []Cooldown) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, value := range cooldowns {
		if entry := p.entry(value.Account); entry != nil && entry.State == StateReady {
			entry.restore(value)
			continue
		}
		p.restored[value.Account] = append(p.restored[value.Account], value)
	}
}

// OnCooldownChange 登记冷却变化后保存全部冷却的函数
func (p *AccountPool) OnCooldownChange(save func([]Cooldown)) {
	p.mu.Lock()
	p.saveCooldowns = save
	p.mu.Unlock()
}

// Cooldowns 返回尚未结束的账号与模型冷却，含尚未就绪账号待恢复的冷却
func (p *AccountPool) Cooldowns() []Cooldown {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cooldownsLocked(time.Now())
}

func (p *AccountPool) cooldownsLocked(now time.Time) []Cooldown {
	var cooldowns []Cooldown
	for _, values := range p.restored {
		for _, value := range values {
			if now.Before(value.Until) {
				cooldowns = append(cooldowns, value)
			}
		}
	}
	for _, entry := range p.entries {
		if now.Before(entry.Cooldown.until) {
			cooldowns = append(cooldowns, Cooldown{Account: entry.AccountID, Until: entry.Cooldown.until, Reason: entry.Cooldown.reason})
		}
		for model, value := range entry.ModelCooldowns {
			if now.Before(value.until) {
				cooldowns = append(cooldowns, Cooldown{Account: entry.AccountID, Model: model, Until: value.until, Reason: value.reason})
			}
		}
	}
	return cooldowns
}

// persistCooldowns 在冷却变化后把全部冷却交给 OnCooldownChange 登记的函数
func (p *AccountPool) persistCooldowns() {
	p.persistMu.Lock()
	defer p.persistMu.Unlock()
	p.mu.Lock()
	save, cooldowns := p.saveCooldowns, p.cooldownsLocked(time.Now())
	p.mu.Unlock()
	if save != nil {
		save(cooldowns)
	}
}

// restore 恢复一条保存的冷却
func (e *accountEntry) restore(value Cooldown) {
	if value.Model == "" {
		e.Cooldown = cooldown{until: value.Until, reason: value.Reason}
		return
	}
	e.ModelCooldowns[value.Model] = cooldown{until: value.Until, reason: value.Reason}
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
		client.SetReleaseHook(p.notify)
		p.notifyLocked()
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
	for _, value := range p.restored[accountID] {
		entry.restore(value)
	}
	delete(p.restored, accountID)
	p.notifyLocked()
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
	if errors.Is(err, gemini.ErrCaptcha) && entry.Info.ProxySource == "pool" {
		p.reportProxyBlocked(entry.Info.Proxy, err)
	}
	changed := entry.State != state || entry.LastError != errorText(err)
	entry.State = state
	entry.LastError, entry.LastErrorAt = errorText(err), time.Now()
	p.notifyLocked()
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

// OnProxyBlocked 设置账号遭遇 Google 人机验证拦截时调用的函数
func (p *AccountPool) OnProxyBlocked(fn func(proxyURL string, err error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proxyBlocked = fn
}

// SetReconnector 设置账号遇到认证失效时自动重新连接并初始化的函数
func (p *AccountPool) SetReconnector(fn func(ctx context.Context, accountID string) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reconnect = fn
}

// Reconnect 调用已设置的重新连接函数为指定账号重新连接并初始化
func (p *AccountPool) Reconnect(ctx context.Context, accountID string) error {
	p.mu.Lock()
	fn := p.reconnect
	p.mu.Unlock()
	if fn == nil {
		return errors.New("未配置重新连接器")
	}
	return fn(ctx, accountID)
}

// reportProxyBlocked 在代理遭 Google 人机验证拦截时通知外部
func (p *AccountPool) reportProxyBlocked(proxyURL string, err error) {
	if p.proxyBlocked != nil && proxyURL != "" {
		p.proxyBlocked(proxyURL, err)
	}
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
	defer p.notifyLocked()
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
	p.notifyLocked()
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

// AcquireForModel 选择可调度该模型的账号并占用一个请求槽，返回客户端、账号与释放函数；同一排队键的请求按到达顺序排队，队首在任一账号空出、就绪或状态变化后重新选择，没有可调度账号时客户端为空
func (p *AccountPool) AcquireForModel(ctx context.Context, sessionKey string, modelID string, excludedAccountIDs map[string]struct{}) (*gemini.Client, string, func(), error) {
	key, wake := p.joinQueue(sessionKey, modelID, excludedAccountIDs)
	defer p.leaveQueue(key, wake)
	accountID := ""
	for {
		if p.queueHead(key, wake) {
			var client *gemini.Client
			client, accountID = p.NextForModelExcluding(sessionKey, modelID, excludedAccountIDs)
			if client == nil {
				return nil, accountID, nil, nil
			}
			if release, ok := client.TryAcquireRequest(); ok {
				return client, accountID, release, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, accountID, nil, ctx.Err()
		case <-wake:
		}
	}
}

// joinQueue 把请求排到排队键的队尾，返回排队键与唤醒通道；排队键为模型、会话绑定的账号与排除的账号
func (p *AccountPool) joinQueue(sessionKey string, modelID string, excludedAccountIDs map[string]struct{}) (string, chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	bound := ""
	if binding, ok := p.sessions[sessionKey]; ok && sessionKey != "" && time.Now().Before(binding.ExpiresAt) {
		bound = binding.AccountID
	}
	key := modelID + "\x00" + bound + "\x00" + strings.Join(slices.Sorted(maps.Keys(excludedAccountIDs)), ",")
	wake := make(chan struct{}, 1)
	p.queues[key] = append(p.queues[key], wake)
	return key, wake
}

// leaveQueue 移除排队请求，它位于队首时唤醒下一个请求
func (p *AccountPool) leaveQueue(key string, wake chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	waiters := p.queues[key]
	index := slices.Index(waiters, wake)
	waiters = slices.Delete(waiters, index, index+1)
	if len(waiters) == 0 {
		delete(p.queues, key)
		return
	}
	p.queues[key] = waiters
	if index == 0 {
		signal(waiters[0])
	}
}

// queueHead 报告排队请求是否位于队首
func (p *AccountPool) queueHead(key string, wake chan struct{}) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.queues[key][0] == wake
}

// signal 不阻塞地唤醒排队请求
func signal(wake chan struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// notify 唤醒等待账号的请求
func (p *AccountPool) notify() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notifyLocked()
}

// notifyLocked 唤醒等待账号变化的调用方与每个排队键的队首，调用方持有 p.mu
func (p *AccountPool) notifyLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
	for _, waiters := range p.queues {
		signal(waiters[0])
	}
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
				if _, excluded := excludedAccountIDs[entry.AccountID]; !excluded {
					entry.LastUsed = now
					p.sessions[sessionKey] = sessionBinding{AccountID: entry.AccountID, ExpiresAt: now.Add(p.sessionTTL)}
					return entry.Client, entry.AccountID
				}
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

// SessionIdle 判断会话绑定的账号可调度该模型且有空闲请求槽
func (p *AccountPool) SessionIdle(sessionKey string, modelID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	binding, ok := p.sessions[sessionKey]
	if !ok || !now.Before(binding.ExpiresAt) {
		return false
	}
	entry := p.entry(binding.AccountID)
	return entry != nil && entry.schedulable(now, modelID) && !entry.Client.Busy()
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
	cleared := false
	defer func() {
		if cleared {
			p.persistCooldowns()
		}
	}()
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry := p.entry(accountID); entry != nil {
		_, modelCooling := entry.ModelCooldowns[modelID]
		cleared = !entry.Cooldown.until.IsZero() || modelCooling
		entry.ConsecutiveFails = 0
		entry.Cooldown = cooldown{}
		delete(entry.ModelCooldowns, modelID)
		p.notifyLocked()
	}
}

// ReportFailure 按错误类型把账号或该模型移入冷却，会话失效时把账号标为待认证，网络层失败只记录错误
func (p *AccountPool) ReportFailure(accountID string, modelID string, err error) {
	cooled := false
	defer func() {
		if cooled {
			p.persistCooldowns()
		}
	}()
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.notifyLocked()

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
	if errors.Is(err, gemini.ErrCaptcha) && entry.Info.ProxySource == "pool" {
		p.reportProxyBlocked(entry.Info.Proxy, err)
	}
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
	cooled = true
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
		case 1037, 1173:
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
	limited := errors.As(err, &protocolErr) && (protocolErr.Code == 1037 || protocolErr.Code == 1173) || errors.Is(err, gemini.ErrModelMismatch)
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

// CatalogAccount 是一个已就绪或已读取模型目录的账号，Ready 表示已就绪
type CatalogAccount struct {
	ID     string
	Client *gemini.Client
	Ready  bool
}

// CatalogAccounts 返回已就绪或已读取模型目录的账号，用于汇总模型目录
func (p *AccountPool) CatalogAccounts() []CatalogAccount {
	p.mu.Lock()
	defer p.mu.Unlock()

	accounts := make([]CatalogAccount, 0, len(p.entries))
	for _, entry := range p.entries {
		if entry.Client == nil || entry.State != StateReady && len(entry.Client.Models()) == 0 {
			continue
		}
		accounts = append(accounts, CatalogAccount{ID: entry.AccountID, Client: entry.Client, Ready: entry.State == StateReady})
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

// ModelAvailability 是目标模型在账号池中的调度情况
type ModelAvailability struct {
	// Catalog 表示至少一个账号已就绪或已读取模型目录
	Catalog bool
	// Known 表示至少一个账号的模型目录含该模型
	Known bool
	// Ready 表示有可调度该模型的账号
	Ready bool
	// Reasons 为不可调度时支持该模型的各账号的原因
	Reasons []string
	// Pending 为尚未读取模型目录的未停用账号的状态
	Pending []string
}

// ModelAvailability 按全部账号的模型目录返回目标模型的调度情况
func (p *AccountPool) ModelAvailability(modelID string) ModelAvailability {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	var availability ModelAvailability
	for _, entry := range p.entries {
		if entry.Client == nil || entry.State != StateReady && len(entry.Client.Models()) == 0 {
			if entry.State != StateDisabled {
				availability.Pending = append(availability.Pending, entry.unavailableReason(now, modelID))
			}
			continue
		}
		availability.Catalog = true
		if !entry.supportsModel(modelID) {
			continue
		}
		availability.Known = true
		if entry.schedulable(now, modelID) {
			availability.Ready = true
			continue
		}
		availability.Reasons = append(availability.Reasons, entry.unavailableReason(now, modelID))
	}
	if availability.Ready {
		availability.Reasons = nil
	}
	return availability
}

// AccountReason 返回账号不能调度该模型的原因：冷却截止时间或账号状态
func (p *AccountPool) AccountReason(accountID string, modelID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entry(accountID); entry != nil {
		return entry.unavailableReason(time.Now(), modelID)
	}
	return accountID + " 已移除"
}

// unavailableReason 返回账号不能调度该模型的原因，调用方持有 p.mu
func (e *accountEntry) unavailableReason(now time.Time, modelID string) string {
	if until := e.blockedUntil(now, modelID); e.State == StateReady && !until.IsZero() {
		return fmt.Sprintf("%s 冷却至 %s", e.AccountID, until.Local().Format(time.TimeOnly))
	}
	return e.AccountID + " " + string(e.State)
}

// Changed 返回账号状态下一次变化或请求槽释放时关闭的通道
func (p *AccountPool) Changed() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
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

// isQuotaLimitedModel 报告模型是否受 Pro/Advanced 5小时用量周期限制
func isQuotaLimitedModel(modelID string) bool {
	id := strings.ToLower(modelID)
	if strings.Contains(id, "flash") && !strings.Contains(id, "task") {
		return false
	}
	return strings.Contains(id, "argon") || strings.Contains(id, "pro") || strings.Contains(id, "deep-research") || strings.Contains(id, "task") || strings.Contains(id, "video")
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
	baseID := strings.TrimSuffix(modelID, "-task")
	if baseID != modelID {
		if value, ok := e.ModelCooldowns[baseID]; ok && now.Before(value.until) && value.until.After(until) {
			until = value.until
		}
	} else {
		taskID := modelID + "-task"
		if value, ok := e.ModelCooldowns[taskID]; ok && now.Before(value.until) && value.until.After(until) {
			until = value.until
		}
	}
	if isQuotaLimitedModel(modelID) && e.Client != nil {
		if usage := e.Client.CachedUsage(); usage != nil {
			for _, window := range []*gemini.UsageWindow{usage.Current, usage.Weekly} {
				if window != nil && window.UsedRatio >= 1 && now.Before(window.ResetAt) && window.ResetAt.After(until) {
					until = window.ResetAt
				}
			}
		}
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

// nextFrom 在账号或访客会话中按单账号配额耗尽优先与上下文窗口统一选择
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

	quotaRatio := func(entry *accountEntry) float64 {
		if entry.Client == nil || !isQuotaLimitedModel(modelID) {
			return 0
		}
		usage := entry.Client.CachedUsage()
		if usage == nil || usage.Current == nil {
			return 0
		}
		if now.After(usage.Current.ResetAt) {
			return 0
		}
		return usage.Current.UsedRatio
	}

	type candidate struct {
		entry     *accountEntry
		origIndex int
		usedRatio float64
		busy      bool
	}
	candidates := make([]candidate, 0, len(p.entries))
	for idx, entry := range p.entries {
		if eligible(entry) && entry.Client.ContextWindow() == maxContextWindow {
			candidates = append(candidates, candidate{
				entry:     entry,
				origIndex: idx,
				usedRatio: quotaRatio(entry),
				busy:      entry.Client.Busy(),
			})
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	// 统一调度策略：单账号配额耗尽优先（Exhaust-First）
	// 1. 空闲账号优先（!busy 优先于 busy）
	// 2. 正在消耗配额的账号优先（0 < usedRatio < 1 降序），优先完全耗尽单一账号额度，避免分散消耗多个账号的 5 小时窗口
	// 3. 配额消耗相同时，严格保持账号列表原始顺序（origIndex），逐个账号依次耗尽
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.busy != b.busy {
			return !a.busy && b.busy
		}
		aInProgress := a.usedRatio > 0 && a.usedRatio < 1
		bInProgress := b.usedRatio > 0 && b.usedRatio < 1
		if aInProgress != bInProgress {
			return aInProgress
		}
		if aInProgress && bInProgress && a.usedRatio != b.usedRatio {
			return a.usedRatio > b.usedRatio
		}
		return a.origIndex < b.origIndex
	})

	return candidates[0].entry
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
