package balancer

import (
	"context"
	"slices"
)

// requestAccountContextKey 是请求上下文中指定处理账号的键
type requestAccountContextKey struct{}

// ContextWithAccount 返回指定处理账号的上下文，账号池只在该账号上选号
func ContextWithAccount(ctx context.Context, accountID string) context.Context {
	return context.WithValue(ctx, requestAccountContextKey{}, accountID)
}

// requestAccount 返回请求指定的处理账号，没有指定时为空
func requestAccount(ctx context.Context) string {
	accountID, _ := ctx.Value(requestAccountContextKey{}).(string)
	return accountID
}

// AccountAllowed 报告账号是否存在，以及请求所用 API key 是否可以使用它
func (p *AccountPool) AccountAllowed(ctx context.Context, accountID string) (exists bool, allowed bool) {
	key, _ := ctx.Value(apiKeyContextKey{}).(string)
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entry(accountID)
	if entry == nil || entry.guest() {
		return false, false
	}
	scope, bound := p.apiKeyAccounts[key]
	return true, !bound || key == "" || slices.Contains(scope, accountID)
}
