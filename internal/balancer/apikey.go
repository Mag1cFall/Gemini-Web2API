package balancer

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// apiKeyContextKey 是请求上下文中所用 API key 的键
type apiKeyContextKey struct{}

// ContextWithAPIKey 返回带有请求所用 API key 的上下文，账号池只在该 key 绑定的账号中选号
func ContextWithAPIKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, apiKeyContextKey{}, key)
}

// SetAPIKeyAccounts 设置各 API key 可用的账号 ID，未列出的 key 可用全部账号
func (p *AccountPool) SetAPIKeyAccounts(accounts map[string][]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.apiKeyAccounts = maps.Clone(accounts)
	p.notifyLocked()
}

// scopeExcluded 返回并入请求范围之外账号与访客会话的排除集合：请求指定了处理账号时只留该账号，否则按 API key 与目标模型的绑定排除，都没有绑定时原样返回
func (p *AccountPool) scopeExcluded(ctx context.Context, modelID string, excluded map[string]struct{}) map[string]struct{} {
	key, _ := ctx.Value(apiKeyContextKey{}).(string)
	p.mu.Lock()
	defer p.mu.Unlock()
	if requested := requestAccount(ctx); requested != "" {
		result := maps.Clone(excluded)
		if result == nil {
			result = make(map[string]struct{})
		}
		for _, entry := range p.entries {
			if entry.AccountID != requested {
				result[entry.AccountID] = struct{}{}
			}
		}
		return result
	}
	keyScope, keyBound := p.apiKeyAccounts[key]
	keyBound = keyBound && key != ""
	modelScope, modelBound := p.modelBinding(modelID)
	if !keyBound && !modelBound {
		return excluded
	}
	result := maps.Clone(excluded)
	if result == nil {
		result = make(map[string]struct{})
	}
	for _, entry := range p.entries {
		if keyBound && !slices.Contains(keyScope, entry.AccountID) || modelBound && !slices.Contains(modelScope, entry.AccountID) {
			result[entry.AccountID] = struct{}{}
		}
	}
	return result
}

// SetModelAccounts 设置各模型可用的账号 ID，未列出的模型可用全部账号
func (p *AccountPool) SetModelAccounts(accounts map[string][]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.modelAccounts = make(map[string][]string, len(accounts))
	for model, ids := range accounts {
		p.modelAccounts[gemini.NormalizeModelID(model)] = ids
	}
	p.notifyLocked()
}

// modelBinding 返回模型绑定的账号：绑定中的模型 ID 与 modelID 都换成改名后的当前 ID 再比较，调用方持有 p.mu
func (p *AccountPool) modelBinding(modelID string) ([]string, bool) {
	current := gemini.CanonicalModelID(modelID)
	for model, ids := range p.modelAccounts {
		if gemini.CanonicalModelID(model) == current {
			return ids, true
		}
	}
	return nil, false
}

// SetAccountOrder 设置 fill-first 依次使用的账号 ID，未列出的账号排在后面并保持登记顺序
func (p *AccountPool) SetAccountOrder(accountIDs []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.order = slices.Clone(accountIDs)
	p.sortLocked()
	p.notifyLocked()
}

// sortLocked 按 order 排列账号，未列出的账号按登记顺序排在后面，调用方持有 p.mu
func (p *AccountPool) sortLocked() {
	rank := func(entry *accountEntry) int {
		if index := slices.Index(p.order, entry.AccountID); index >= 0 {
			return index
		}
		return len(p.order)
	}
	slices.SortFunc(p.entries, func(left, right *accountEntry) int {
		return cmp.Or(rank(left)-rank(right), left.registered-right.registered)
	})
}

// TryAcquireIdle 立即占用一个没有进行中请求、可调度该模型且未排除的账号的请求槽，都没有时再试 fallback 账号的其余请求槽；没有可用请求槽时客户端为空
func (p *AccountPool) TryAcquireIdle(ctx context.Context, modelID string, excludedAccountIDs map[string]struct{}, fallback string) (*gemini.Client, string, func()) {
	outside := p.scopeExcluded(ctx, modelID, nil)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, entry := range p.entries {
		_, excluded := excludedAccountIDs[entry.AccountID]
		_, out := outside[entry.AccountID]
		if excluded || out || entry.guest() || !p.schedulable(entry, now, modelID) || entry.Client.Pending() > 0 {
			continue
		}
		if release, ok := entry.Client.TryAcquireRequest(); ok {
			entry.LastUsed = now
			return entry.Client, entry.AccountID, release
		}
	}
	if _, out := outside[fallback]; !out {
		if entry := p.entry(fallback); entry != nil && p.schedulable(entry, now, modelID) {
			if release, ok := entry.Client.TryAcquireRequest(); ok {
				return entry.Client, entry.AccountID, release
			}
		}
	}
	return nil, "", nil
}
