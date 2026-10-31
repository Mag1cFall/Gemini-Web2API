package proxypool

import (
	"context"
	"net/url"
	"slices"
	"sort"
	"sync"
	"time"
)

// Direct 是账号固定代理中表示直连的值
const Direct = "direct"

// checkTimeout 是检测单个代理的最长时间
const checkTimeout = 20 * time.Second

// Pool 是未设置固定代理的账号共用的代理池：每个账号绑定一个代理，代理不可用时改绑到可用代理中账号最少的一个，代理列表变化时在可用代理间均衡
type Pool struct {
	mu       sync.Mutex
	proxies  []*proxyState
	bindings map[string]*proxyState
}

// proxyState 是代理池中一个代理的检测结果
type proxyState struct {
	url           string
	healthy       bool
	checked       time.Time
	latency       time.Duration
	err           string
	cooldownUntil time.Time
}

// Status 是代理池中一个代理的当前状态与绑定的账号
type Status struct {
	URL           string    `json:"url"`
	Healthy       bool      `json:"healthy"`
	CheckedAt     time.Time `json:"checked_at,omitzero"`
	LatencyMS     int64     `json:"latency_ms"`
	Error         string    `json:"error,omitempty"`
	Accounts      []string  `json:"accounts"`
	CooldownUntil time.Time `json:"cooldown_until,omitzero"`
}

// New 创建代理池，代理在首次检测前视为可用
func New(urls []string) *Pool {
	pool := &Pool{bindings: make(map[string]*proxyState)}
	pool.SetProxies(urls)
	return pool
}

// SetProxies 替换代理列表，仍在列表中的代理保留检测结果与绑定，已删除代理上的账号改绑到账号最少的可用代理，再以最少的改绑使可用代理绑定的账号数相差不超过一个
func (p *Pool) SetProxies(urls []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := make(map[string]*proxyState, len(p.proxies))
	for _, state := range p.proxies {
		previous[state.url] = state
	}
	p.proxies = nil
	for _, value := range urls {
		state := previous[value]
		if state == nil {
			state = &proxyState{url: value, healthy: true}
		}
		if !p.contains(state) {
			p.proxies = append(p.proxies, state)
		}
	}
	orphans := []string{}
	for id, state := range p.bindings {
		if !p.contains(state) {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	for _, id := range orphans {
		if len(p.proxies) == 0 {
			delete(p.bindings, id)
			continue
		}
		p.bindings[id] = p.leastBound(id)
	}
	p.rebalance()
}

// isAvailable 报告代理当前是否健康且未处于冷却隔离期
func isAvailable(state *proxyState, now time.Time) bool {
	if state == nil || !state.healthy {
		return false
	}
	if !state.cooldownUntil.IsZero() && now.Before(state.cooldownUntil) {
		return false
	}
	return true
}

// rebalance 每次把账号最多的可用代理上 ID 最大的账号改绑到账号最少的可用代理，直到两者相差不超过一个
func (p *Pool) rebalance() {
	anyHealthy := p.anyHealthy()
	now := time.Now()
	accounts := make(map[*proxyState][]string, len(p.proxies))
	for _, state := range p.proxies {
		if !anyHealthy || isAvailable(state, now) {
			accounts[state] = []string{}
		}
	}
	for id, state := range p.bindings {
		if ids, ok := accounts[state]; ok {
			accounts[state] = append(ids, id)
		}
	}
	for {
		var most, least *proxyState
		for _, state := range p.proxies {
			ids, ok := accounts[state]
			if !ok {
				continue
			}
			if most == nil || len(ids) > len(accounts[most]) {
				most = state
			}
			if least == nil || len(ids) < len(accounts[least]) {
				least = state
			}
		}
		if most == nil || len(accounts[most])-len(accounts[least]) <= 1 {
			return
		}
		ids := accounts[most]
		sort.Strings(ids)
		id := ids[len(ids)-1]
		accounts[most], accounts[least] = ids[:len(ids)-1], append(accounts[least], id)
		p.bindings[id] = least
	}
}

// contains 报告代理是否仍在列表中
func (p *Pool) contains(target *proxyState) bool {
	return slices.Contains(p.proxies, target)
}

// Assign 返回账号使用的代理：已绑定的代理可用时沿用，否则绑定可用代理中账号最少的一个；全部不可用时保持原绑定，代理池为空时返回系统代理
func (p *Pool) Assign(accountID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.proxies) == 0 {
		delete(p.bindings, accountID)
		return System()
	}
	now := time.Now()
	if current := p.bindings[accountID]; current != nil && (isAvailable(current, now) || !p.anyHealthy()) {
		return current.url
	}
	best := p.leastBound(accountID)
	p.bindings[accountID] = best
	return best.url
}

// Pick 返回可用代理中账号最少的一个且不绑定，代理池为空时返回系统代理
func (p *Pool) Pick() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.proxies) == 0 {
		return System()
	}
	return p.leastBound("").url
}

// anyHealthy 报告是否有可用代理
func (p *Pool) anyHealthy() bool {
	now := time.Now()
	for _, state := range p.proxies {
		if isAvailable(state, now) {
			return true
		}
	}
	return false
}

// leastBound 返回除 accountID 外绑定账号最少的代理，有可用代理时只在可用代理中选，同数时取列表中靠前的一个
func (p *Pool) leastBound(accountID string) *proxyState {
	counts := make(map[*proxyState]int, len(p.proxies))
	for id, state := range p.bindings {
		if id != accountID {
			counts[state]++
		}
	}
	anyHealthy := p.anyHealthy()
	now := time.Now()
	var best *proxyState
	for _, state := range p.proxies {
		if anyHealthy && !isAvailable(state, now) {
			continue
		}
		if best == nil || counts[state] < counts[best] {
			best = state
		}
	}
	return best
}

// Release 解除账号与代理的绑定
func (p *Pool) Release(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.bindings, accountID)
}

// Empty 报告代理池是否没有代理
func (p *Pool) Empty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.proxies) == 0
}

// Check 以 probe 并行检测全部代理并更新状态，返回绑定在不可用代理上、且有可用代理可改绑的账号
func (p *Pool) Check(ctx context.Context, probe func(context.Context, string) (time.Duration, error)) []string {
	p.mu.Lock()
	targets := append([]*proxyState(nil), p.proxies...)
	p.mu.Unlock()
	type result struct {
		latency time.Duration
		err     error
	}
	results := make([]result, len(targets))
	var wait sync.WaitGroup
	for index, state := range targets {
		wait.Add(1)
		go func() {
			defer wait.Done()
			checkContext, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			latency, err := probe(checkContext, state.url)
			results[index] = result{latency, err}
		}()
	}
	wait.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	anyHealthy := false
	for index, state := range targets {
		state.checked = now
		state.healthy = results[index].err == nil
		if !state.healthy {
			state.latency, state.err = 0, results[index].err.Error()
		} else {
			state.latency = results[index].latency
			if now.After(state.cooldownUntil) {
				state.cooldownUntil = time.Time{}
				state.err = ""
			}
		}
		anyHealthy = anyHealthy || isAvailable(state, now)
	}
	if !anyHealthy {
		return nil
	}
	moved := []string{}
	for id, state := range p.bindings {
		if !isAvailable(state, now) {
			moved = append(moved, id)
		}
	}
	sort.Strings(moved)
	return moved
}

// MarkCooldown 将指定代理设为冷却隔离状态（如遭遇 Google 302 人机验证），并返回需要改绑的账号 ID
func (p *Pool) MarkCooldown(proxyURL string, duration time.Duration, reason string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var target *proxyState
	for _, state := range p.proxies {
		if state.url == proxyURL {
			target = state
			break
		}
	}
	if target == nil {
		return nil
	}
	if !target.cooldownUntil.IsZero() && now.Before(target.cooldownUntil) {
		return nil
	}
	target.cooldownUntil = now.Add(duration)
	target.err = reason

	moved := []string{}
	for id, state := range p.bindings {
		if state == target {
			moved = append(moved, id)
		}
	}
	sort.Strings(moved)
	if p.anyHealthy() {
		for _, id := range moved {
			p.bindings[id] = p.leastBound(id)
		}
		p.rebalance()
	}
	return moved
}

// Status 返回代理池中每个代理的状态，代理 URL 隐去密码
func (p *Pool) Status() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	accounts := make(map[*proxyState][]string, len(p.proxies))
	for id, state := range p.bindings {
		accounts[state] = append(accounts[state], id)
	}
	now := time.Now()
	result := make([]Status, 0, len(p.proxies))
	for _, state := range p.proxies {
		ids := append([]string{}, accounts[state]...)
		sort.Strings(ids)
		result = append(result, Status{
			URL: Redact(state.url), Healthy: isAvailable(state, now), CheckedAt: state.checked,
			LatencyMS: state.latency.Milliseconds(), Error: state.err, Accounts: ids,
			CooldownUntil: state.cooldownUntil,
		})
	}
	return result
}

// Redact 返回隐去密码的代理 URL
func Redact(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	return parsed.Redacted()
}
