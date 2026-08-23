package adapter

import (
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// conversationRegistry 保存不可变响应快照和线性会话锁
type conversationRegistry struct {
	states map[string]conversationEntry
	locks  map[string]*conversationLock
	ttl    time.Duration
	lastGC time.Time
	mu     sync.Mutex
}

type conversationEntry struct {
	snapshot      gemini.ConversationSnapshot
	accountID     string
	contextTokens int
	expiresAt     time.Time
}

type conversationLock struct {
	mu   sync.Mutex
	refs int
}

var conversations = conversationRegistry{
	states: make(map[string]conversationEntry),
	locks:  make(map[string]*conversationLock),
	ttl:    30 * time.Minute,
}

// ConfigureSessionTTL 设置显式会话状态的有效期
func ConfigureSessionTTL(ttl time.Duration) {
	conversations.mu.Lock()
	defer conversations.mu.Unlock()
	conversations.ttl = ttl
}

func (r *conversationRegistry) get(key string) (*gemini.ConversationState, string, int, bool) {
	if key == "" {
		return nil, "", 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.states[key]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(r.states, key)
		return nil, "", 0, false
	}
	entry.expiresAt = time.Now().Add(r.ttl)
	r.states[key] = entry
	return gemini.NewConversationStateFrom(entry.snapshot), entry.accountID, entry.contextTokens, true
}

func (r *conversationRegistry) put(state *gemini.ConversationState, accountID string, contextTokens int, keys ...string) {
	if state == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.cleanupLocked(now)
	entry := conversationEntry{
		snapshot: state.Snapshot(), accountID: accountID, contextTokens: contextTokens, expiresAt: now.Add(r.ttl),
	}
	for _, key := range keys {
		if key != "" {
			r.states[key] = entry
		}
	}
}

func (r *conversationRegistry) acquire(key string) func() {
	if key == "" {
		return func() {}
	}

	r.mu.Lock()
	lock := r.locks[key]
	if lock == nil {
		lock = &conversationLock{}
		r.locks[key] = lock
	}
	lock.refs++
	r.mu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		r.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(r.locks, key)
		}
		r.mu.Unlock()
	}
}

func (r *conversationRegistry) cleanupLocked(now time.Time) {
	if !r.lastGC.IsZero() && now.Sub(r.lastGC) < time.Minute {
		return
	}
	for key, entry := range r.states {
		if now.After(entry.expiresAt) {
			delete(r.states, key)
		}
	}
	r.lastGC = now
}

func conversationKey(previousResponseID string, conversationID string, headerValue string) string {
	if previousResponseID != "" {
		return previousResponseID
	}
	if conversationID != "" {
		return conversationID
	}
	return headerValue
}
