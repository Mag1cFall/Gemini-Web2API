package balancer

import (
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

// keyExcluded 返回并入请求 API key 绑定范围之外账号与访客会话的排除集合，key 没有绑定时原样返回
func (p *AccountPool) keyExcluded(ctx context.Context, excluded map[string]struct{}) map[string]struct{} {
	key, _ := ctx.Value(apiKeyContextKey{}).(string)
	p.mu.Lock()
	defer p.mu.Unlock()
	scope, bound := p.apiKeyAccounts[key]
	if key == "" || !bound {
		return excluded
	}
	result := maps.Clone(excluded)
	if result == nil {
		result = make(map[string]struct{})
	}
	for _, entry := range p.entries {
		if !slices.Contains(scope, entry.AccountID) {
			result[entry.AccountID] = struct{}{}
		}
	}
	return result
}

// TryAcquireIdle 立即占用一个没有进行中请求、可调度该模型且未排除的账号的请求槽，都没有时再试 fallback 账号的其余请求槽；没有可用请求槽时客户端为空
func (p *AccountPool) TryAcquireIdle(ctx context.Context, modelID string, excludedAccountIDs map[string]struct{}, fallback string) (*gemini.Client, string, func()) {
	outside := p.keyExcluded(ctx, nil)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, entry := range p.entries {
		_, excluded := excludedAccountIDs[entry.AccountID]
		_, out := outside[entry.AccountID]
		if excluded || out || entry.guest() || !entry.schedulable(now, modelID) || entry.Client.Pending() > 0 {
			continue
		}
		if release, ok := entry.Client.TryAcquireRequest(); ok {
			entry.LastUsed = now
			return entry.Client, entry.AccountID, release
		}
	}
	if _, out := outside[fallback]; !out {
		if entry := p.entry(fallback); entry != nil && entry.schedulable(now, modelID) {
			if release, ok := entry.Client.TryAcquireRequest(); ok {
				return entry.Client, entry.AccountID, release
			}
		}
	}
	return nil, "", nil
}
