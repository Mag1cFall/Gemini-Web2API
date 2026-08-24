package balancer

import (
	"context"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

type accountEntry struct {
	Client           *gemini.Client
	AccountID        string
	LastUsed         time.Time
	CooldownUntil    time.Time
	ConsecutiveFails int
	Unavailable      bool
}

// AccountStatus 是健康检查使用的脱敏账号状态
type AccountStatus struct {
	AccountID        string     `json:"account_id"`
	State            string     `json:"state"`
	LastUsed         *time.Time `json:"last_used,omitempty"`
	CooldownUntil    *time.Time `json:"cooldown_until,omitempty"`
	ConsecutiveFails int        `json:"consecutive_failures"`
}

// PoolStatus 汇总账号池状态
type PoolStatus struct {
	Total       int             `json:"total"`
	Available   int             `json:"available"`
	CoolingDown int             `json:"cooling_down"`
	Unavailable int             `json:"unavailable"`
	Accounts    []AccountStatus `json:"accounts"`
}

// AccountUsageStatus 表示单个账号的实时官网用量
type AccountUsageStatus struct {
	AccountID string            `json:"account_id"`
	State     string            `json:"state"`
	Usage     *gemini.UsageInfo `json:"usage,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// PoolUsageStatus 汇总账号池实时官网用量
type PoolUsageStatus struct {
	Accounts []AccountUsageStatus `json:"accounts"`
}

type sessionBinding struct {
	AccountID string
	ExpiresAt time.Time
}

// AccountPool 在健康账号之间调度请求并维护显式会话粘性
type AccountPool struct {
	entries       []accountEntry
	sessions      map[string]sessionBinding
	index         uint64
	cooldown      time.Duration
	sessionTTL    time.Duration
	lastSessionGC time.Time
	mu            sync.Mutex
}

// NewAccountPool 创建账号池
func NewAccountPool(cooldown time.Duration, sessionTTL time.Duration) *AccountPool {
	return &AccountPool{
		entries:    make([]accountEntry, 0),
		sessions:   make(map[string]sessionBinding),
		cooldown:   cooldown,
		sessionTTL: sessionTTL,
	}
}

// Add 添加一个已经完成协议初始化的账号
func (p *AccountPool) Add(client *gemini.Client, accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.entries = append(p.entries, accountEntry{
		Client:    client,
		AccountID: accountID,
	})
}

// AddUnavailable 记录一个启动失败的账号
func (p *AccountPool) AddUnavailable(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.entries = append(p.entries, accountEntry{
		AccountID:   accountID,
		Unavailable: true,
	})
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
			entry := p.availableEntry(binding.AccountID, now)
			if entry != nil && entry.supportsModel(modelID) {
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

// ReportSuccess 清除账号的连续失败状态
func (p *AccountPool) ReportSuccess(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry := p.entry(accountID); entry != nil {
		entry.ConsecutiveFails = 0
		entry.CooldownUntil = time.Time{}
	}
}

// ReportFailure 将账号移入冷却
func (p *AccountPool) ReportFailure(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry := p.entry(accountID)
	if entry == nil {
		return
	}

	entry.ConsecutiveFails++
	entry.CooldownUntil = time.Now().Add(p.cooldown)
}

// BindSession 将成功响应标识绑定到产生它的账号
func (p *AccountPool) BindSession(sessionKey string, accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if sessionKey == "" {
		return
	}
	now := time.Now()
	if p.availableEntry(accountID, now) == nil {
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

// Size 返回当前可接收请求的账号数量
func (p *AccountPool) Size() int {
	return p.Status().Available
}

// Clients 返回当前健康客户端快照
func (p *AccountPool) Clients() []*gemini.Client {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	clients := make([]*gemini.Client, 0, len(p.entries))
	for _, entry := range p.entries {
		if entry.Client != nil && !entry.Unavailable && !now.Before(entry.CooldownUntil) {
			clients = append(clients, entry.Client)
		}
	}
	return clients
}

// HasModel 报告已初始化账号是否发现目标模型
func (p *AccountPool) HasModel(modelID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, entry := range p.entries {
		if !entry.Unavailable && entry.supportsModel(modelID) {
			return true
		}
	}
	return false
}

// HasReadyModel 报告当前是否有健康账号可以运行目标模型
func (p *AccountPool) HasReadyModel(modelID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for _, entry := range p.entries {
		if entry.Client != nil && !entry.Unavailable && !now.Before(entry.CooldownUntil) && entry.supportsModel(modelID) {
			return true
		}
	}
	return false
}

// Status 返回账号池健康快照
func (p *AccountPool) Status() PoolStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	status := PoolStatus{
		Total:    len(p.entries),
		Accounts: make([]AccountStatus, 0, len(p.entries)),
	}
	for _, entry := range p.entries {
		state := "ready"
		switch {
		case entry.Client == nil || entry.Unavailable:
			state = "unavailable"
			status.Unavailable++
		case now.Before(entry.CooldownUntil):
			state = "cooldown"
			status.CoolingDown++
		default:
			status.Available++
		}
		status.Accounts = append(status.Accounts, AccountStatus{
			AccountID:        entry.AccountID,
			State:            state,
			LastUsed:         timePointer(entry.LastUsed),
			CooldownUntil:    timePointer(entry.CooldownUntil),
			ConsecutiveFails: entry.ConsecutiveFails,
		})
	}
	return status
}

// Usage 并发读取全部已初始化账号的官网用量
func (p *AccountPool) Usage(ctx context.Context) PoolUsageStatus {
	p.mu.Lock()
	now := time.Now()
	accounts := make([]AccountUsageStatus, len(p.entries))
	clients := make([]*gemini.Client, len(p.entries))
	for index, entry := range p.entries {
		state := "ready"
		switch {
		case entry.Client == nil || entry.Unavailable:
			state = "unavailable"
		case now.Before(entry.CooldownUntil):
			state = "cooldown"
		}
		accounts[index] = AccountUsageStatus{AccountID: entry.AccountID, State: state}
		clients[index] = entry.Client
	}
	p.mu.Unlock()

	var wait sync.WaitGroup
	for index, client := range clients {
		if client == nil {
			continue
		}
		wait.Add(1)
		go func(index int, client *gemini.Client) {
			defer wait.Done()
			release := client.AcquireRequest()
			defer release()
			usage, err := client.FetchUsage(ctx)
			if err != nil {
				accounts[index].Error = err.Error()
				return
			}
			accounts[index].Usage = &usage
		}(index, client)
	}
	wait.Wait()
	return PoolUsageStatus{Accounts: accounts}
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func (p *AccountPool) nextAvailable(now time.Time, modelID string, excludedAccountIDs map[string]struct{}) *accountEntry {
	if len(p.entries) == 0 {
		return nil
	}

	eligible := func(entry *accountEntry) bool {
		_, excluded := excludedAccountIDs[entry.AccountID]
		return !excluded && entry.Client != nil && !entry.Unavailable && !now.Before(entry.CooldownUntil) && entry.supportsModel(modelID)
	}
	maxContextWindow := -1
	for i := range p.entries {
		entry := &p.entries[i]
		if eligible(entry) && entry.Client.ContextWindow() > maxContextWindow {
			maxContextWindow = entry.Client.ContextWindow()
		}
	}
	if maxContextWindow < 0 {
		return nil
	}

	for offset := uint64(0); offset < uint64(len(p.entries)); offset++ {
		idx := (p.index + offset) % uint64(len(p.entries))
		entry := &p.entries[idx]
		if eligible(entry) && entry.Client.ContextWindow() == maxContextWindow {
			p.index = idx + 1
			return entry
		}
	}
	return nil
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

func (p *AccountPool) availableEntry(accountID string, now time.Time) *accountEntry {
	entry := p.entry(accountID)
	if entry == nil || entry.Client == nil || entry.Unavailable || now.Before(entry.CooldownUntil) {
		return nil
	}
	return entry
}

func (p *AccountPool) entry(accountID string) *accountEntry {
	for i := range p.entries {
		if p.entries[i].AccountID == accountID {
			return &p.entries[i]
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
