package balancer

import (
	"gemini-web2api/internal/gemini"
	"sync"
	"sync/atomic"
)

type AccountEntry struct {
	Client    *gemini.Client
	AccountID string
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

func (p *AccountPool) Add(client *gemini.Client, accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = append(p.entries, AccountEntry{
		Client:    client,
		AccountID: accountID,
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

func (p *AccountPool) ReplaceAccounts(newAccountIDs []string, changedClients map[string]*gemini.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldEntries := make(map[string]*gemini.Client)
	for _, entry := range p.entries {
		oldEntries[entry.AccountID] = entry.Client
	}

	p.entries = make([]AccountEntry, 0, len(newAccountIDs))
	for _, accountID := range newAccountIDs {
		if newClient, changed := changedClients[accountID]; changed {
			p.entries = append(p.entries, AccountEntry{
				Client:    newClient,
				AccountID: accountID,
			})
		} else if oldClient, existed := oldEntries[accountID]; existed {
			p.entries = append(p.entries, AccountEntry{
				Client:    oldClient,
				AccountID: accountID,
			})
		}
	}
}
