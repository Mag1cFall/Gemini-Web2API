package balancer

import (
	"gemini-web2api/internal/gemini"
	"sync"
	"sync/atomic"
)

type AccountEntry struct {
	Client    *gemini.Client
	AccountID string
	ProxyURL  string
}

type AccountPool struct {
	entries []AccountEntry
	index   uint64
	mu      sync.RWMutex
}

func NewAccountPool() *AccountPool {
	return &AccountPool{
		entries: make([]AccountEntry, 0),
	}
}

func (p *AccountPool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = make([]AccountEntry, 0)
	atomic.StoreUint64(&p.index, 0)
}

func (p *AccountPool) Add(client *gemini.Client, accountID string, proxyURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = append(p.entries, AccountEntry{
		Client:    client,
		AccountID: accountID,
		ProxyURL:  proxyURL,
	})
}

func (p *AccountPool) Next() (*gemini.Client, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.entries) == 0 {
		return nil, ""
	}
	idx := atomic.AddUint64(&p.index, 1) - 1
	entry := p.entries[idx%uint64(len(p.entries))]
	return entry.Client, entry.AccountID
}

func (p *AccountPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.entries)
}

func (p *AccountPool) ReplaceAccounts(newAccountIDs []string, changedEntries map[string]AccountEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldEntries := make(map[string]AccountEntry)
	for _, entry := range p.entries {
		oldEntries[entry.AccountID] = entry
	}

	p.entries = make([]AccountEntry, 0, len(newAccountIDs))
	for _, accountID := range newAccountIDs {
		if newEntry, changed := changedEntries[accountID]; changed {
			p.entries = append(p.entries, newEntry)
		} else if oldEntry, existed := oldEntries[accountID]; existed {
			p.entries = append(p.entries, oldEntry)
		}
	}
}
