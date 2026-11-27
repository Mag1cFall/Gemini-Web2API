package proxypool

import (
	"context"
	"net/url"
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
	// fixed 记录各账号设为固定代理的地址，统计各代理账号数时一并计入
	fixed map[string]string
}

// proxyState 是代理池中一个代理的检测结果
type proxyState struct {
	url     string
	healthy bool
	checked time.Time
	latency time.Duration
	err     string
	// blockedUntil 是出口被人机验证页拦截后停用到的时间
	blockedUntil time.Time
}

// usable 报告代理检测可用且没有因人机验证拦截停用
func (s *proxyState) usable() bool {
	return s.healthy && !time.Now().Before(s.blockedUntil)
}

// Status 是代理池中一个代理的当前状态与绑定的账号
type Status struct {
	URL       string    `json:"url"`
	Healthy   bool      `json:"healthy"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	LatencyMS int64     `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
	// BlockedUntil 是出口被人机验证页拦截后停用到的时间，没有停用时省略
	BlockedUntil time.Time `json:"blocked_until,omitzero"`
	Accounts     []string  `json:"accounts"`
}

// New 创建代理池，代理在首次检测前视为可用
func New(urls []string) *Pool {
	pool := &Pool{bindings: make(map[string]*proxyState), fixed: make(map[string]string)}
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

// rebalance 每次把账号最多的可用代理上 ID 最大的账号改绑到账号最少的可用代理，直到两者相差不超过一个
func (p *Pool) rebalance() {
	anyHealthy := p.anyHealthy()
	accounts := make(map[*proxyState][]string, len(p.proxies))
	fixed := p.fixedCounts()
	for _, state := range p.proxies {
		if !anyHealthy || state.usable() {
			accounts[state] = []string{}
		}
	}
	for id, state := range p.bindings {
		if ids, ok := accounts[state]; ok {
			accounts[state] = append(ids, id)
		}
	}
	load := func(state *proxyState) int { return len(accounts[state]) + fixed[state] }
	for {
		var most, least *proxyState
		for _, state := range p.proxies {
			ids, ok := accounts[state]
			if !ok {
				continue
			}
			if len(ids) > 0 && (most == nil || load(state) > load(most)) {
				most = state
			}
			if least == nil || lessLoaded(load(state), fixed[state], load(least), fixed[least]) {
				least = state
			}
		}
		if most == nil || load(most)-load(least) <= 1 {
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
	for _, state := range p.proxies {
		if state == target {
			return true
		}
	}
	return false
}

// Assign 返回账号使用的代理：已绑定的代理可用时沿用，否则绑定可用代理中账号最少的一个；全部不可用时保持原绑定，代理池为空时返回系统代理
func (p *Pool) Assign(accountID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.fixed, accountID)
	if len(p.proxies) == 0 {
		delete(p.bindings, accountID)
		return System()
	}
	if current := p.bindings[accountID]; current != nil && (current.usable() || !p.anyHealthy()) {
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
	for _, state := range p.proxies {
		if state.usable() {
			return true
		}
	}
	return false
}

// fixedCounts 返回池中每个代理当前被设为固定代理的账号数
func (p *Pool) fixedCounts() map[*proxyState]int {
	counts := make(map[*proxyState]int, len(p.proxies))
	byURL := make(map[string]*proxyState, len(p.proxies))
	for _, state := range p.proxies {
		byURL[state.url] = state
	}
	for _, proxyURL := range p.fixed {
		if state := byURL[proxyURL]; state != nil {
			counts[state]++
		}
	}
	return counts
}

// lessLoaded 报告候选代理负载是否少于当前项，负载相同时固定账号数少者优先
func lessLoaded(candidateLoad, candidateFixed, currentLoad, currentFixed int) bool {
	if candidateLoad != currentLoad {
		return candidateLoad < currentLoad
	}
	return candidateFixed < currentFixed
}

// leastBound 返回除 accountID 外绑定与固定账号合计最少的代理，有可用代理时只在可用代理中选，同数时优先未作固定代理的一项，再取列表中靠前的一个
func (p *Pool) leastBound(accountID string) *proxyState {
	fixed := p.fixedCounts()
	counts := make(map[*proxyState]int, len(p.proxies))
	for state, n := range fixed {
		counts[state] = n
	}
	for id, state := range p.bindings {
		if id != accountID {
			counts[state]++
		}
	}
	anyHealthy := p.anyHealthy()
	var best *proxyState
	for _, state := range p.proxies {
		if anyHealthy && !state.usable() {
			continue
		}
		if best == nil || lessLoaded(counts[state], fixed[state], counts[best], fixed[best]) {
			best = state
		}
	}
	return best
}

// SetFixed 登记或清除账号的固定代理：proxyURL 非空且不为 direct 时记为固定代理并解除池绑定，否则清除固定代理登记
func (p *Pool) SetFixed(accountID string, proxyURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if proxyURL == "" || proxyURL == Direct {
		delete(p.fixed, accountID)
		return
	}
	delete(p.bindings, accountID)
	p.fixed[accountID] = proxyURL
}

// Release 解除账号与代理的绑定及固定代理登记
func (p *Pool) Release(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.bindings, accountID)
	delete(p.fixed, accountID)
}

// Empty 报告代理池是否没有代理
func (p *Pool) Empty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.proxies) == 0
}

// Check 以 probe 并行检测未处于人机验证停用期的全部代理并更新状态，返回绑定在不可用代理上、且有可用代理可改绑的账号
func (p *Pool) Check(ctx context.Context, probe func(context.Context, string) (time.Duration, error)) []string {
	now := time.Now()
	p.mu.Lock()
	targets := make([]*proxyState, 0, len(p.proxies))
	for _, state := range p.proxies {
		if !now.Before(state.blockedUntil) {
			targets = append(targets, state)
		}
	}
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
	now = time.Now()
	for index, state := range targets {
		state.checked, state.latency, state.err = now, results[index].latency, ""
		state.healthy = results[index].err == nil
		if !state.healthy {
			state.latency, state.err = 0, WithLoopbackHint(state.url, results[index].err).Error()
		}
	}
	if !p.anyHealthy() {
		return nil
	}
	moved := []string{}
	for id, state := range p.bindings {
		if !state.usable() {
			moved = append(moved, id)
		}
	}
	sort.Strings(moved)
	return moved
}

// Block 在 until 之前不使用该代理，返回绑定在它上面且有其他可用代理可改绑的账号
func (p *Pool) Block(url string, until time.Time) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	moved := []string{}
	for _, state := range p.proxies {
		if state.url != url {
			continue
		}
		state.blockedUntil = until
		if !p.anyHealthy() {
			return nil
		}
		for id, bound := range p.bindings {
			if bound == state {
				moved = append(moved, id)
			}
		}
	}
	sort.Strings(moved)
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
	result := make([]Status, 0, len(p.proxies))
	for _, state := range p.proxies {
		ids := append([]string{}, accounts[state]...)
		sort.Strings(ids)
		result = append(result, Status{
			URL: Redact(state.url), Healthy: state.healthy, CheckedAt: state.checked,
			LatencyMS: state.latency.Milliseconds(), Error: state.err, Accounts: ids,
		})
		if time.Now().Before(state.blockedUntil) {
			result[len(result)-1].BlockedUntil = state.blockedUntil
		}
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
