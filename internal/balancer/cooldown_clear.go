package balancer

import (
	"errors"
	"slices"
)

// ErrAccountNotFound 表示账号池中没有该账号
var ErrAccountNotFound = errors.New("账号不存在")

// ClearCooldown 清除账号的一项冷却并保存冷却记录，model 为空时清除账号级冷却，否则清除该模型的冷却
func (p *AccountPool) ClearCooldown(accountID string, model string) error {
	p.mu.Lock()
	entry := p.entry(accountID)
	if entry == nil {
		p.mu.Unlock()
		return ErrAccountNotFound
	}
	if model == "" {
		entry.Cooldown = cooldown{}
	} else {
		delete(entry.ModelCooldowns, model)
	}
	if remaining := slices.DeleteFunc(p.restored[accountID], func(value Cooldown) bool { return value.Model == model }); len(remaining) > 0 {
		p.restored[accountID] = remaining
	} else {
		delete(p.restored, accountID)
	}
	p.notifyLocked()
	p.mu.Unlock()
	p.persistCooldowns()
	return nil
}
