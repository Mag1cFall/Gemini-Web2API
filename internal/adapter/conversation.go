package adapter

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"golang.org/x/sync/semaphore"
)

// conversationRegistry 保存不可变响应快照和线性会话锁，配置账本库后会话写入 sessions 表并在重启后恢复
type conversationRegistry struct {
	states map[string]conversationEntry
	locks  map[string]*conversationLock
	ttl    time.Duration
	lastGC time.Time
	store  *requestdb.Store
	mu     sync.Mutex
}

type conversationEntry struct {
	snapshot      gemini.ConversationSnapshot
	accountID     string
	contextTokens int
	expiresAt     time.Time
}

type conversationLock struct {
	slot *semaphore.Weighted
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

// isLatest 判断快照是否为其网页会话中最新一轮的回答
func (r *conversationRegistry) isLatest(snapshot gemini.ConversationSnapshot) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	latest, ok := r.states[snapshot.CID]
	return !ok || latest.snapshot.RCID == snapshot.RCID
}

func (r *conversationRegistry) put(state *gemini.ConversationState, accountID string, contextTokens int, keys ...string) {
	if state == nil {
		return
	}
	r.mu.Lock()
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
	store := r.store
	r.mu.Unlock()
	if store == nil {
		return
	}
	sessions := make([]requestdb.StoredSession, 0, len(keys))
	for _, key := range keys {
		if key != "" {
			sessions = append(sessions, requestdb.StoredSession{
				Key: key, CID: entry.snapshot.CID, RID: entry.snapshot.RID, RCID: entry.snapshot.RCID, Token: entry.snapshot.Token,
				ModelMode: entry.snapshot.ModelMode, Turns: entry.snapshot.Turns, Account: accountID, ContextTokens: contextTokens, ExpiresAt: entry.expiresAt,
			})
		}
	}
	if err := store.SaveSessions(context.Background(), sessions); err != nil {
		logging.Warn(logging.SourceService, "保存会话失败", "错误", err)
	}
}

// configureSessionStore 让会话登记写入账本库，并恢复上次运行保存的未过期会话
func configureSessionStore(pool *balancer.AccountPool, store *requestdb.Store) {
	if store == nil {
		return
	}
	conversations.mu.Lock()
	if conversations.store != nil {
		conversations.mu.Unlock()
		return
	}
	conversations.store = store
	conversations.mu.Unlock()
	sessions, err := store.Sessions(context.Background(), time.Now())
	if err != nil {
		logging.Warn(logging.SourceService, "读取会话失败", "错误", err)
		return
	}
	conversations.mu.Lock()
	for _, session := range sessions {
		conversations.states[session.Key] = conversationEntry{
			snapshot: gemini.ConversationSnapshot{
				CID: session.CID, RID: session.RID, RCID: session.RCID, Token: session.Token, ModelMode: session.ModelMode, Turns: session.Turns,
			},
			accountID: session.Account, contextTokens: session.ContextTokens, expiresAt: session.ExpiresAt,
		}
	}
	conversations.mu.Unlock()
	if len(sessions) > 0 {
		logging.Info(logging.SourceService, "恢复会话", "数量", len(sessions))
		go bindRestoredSessions(pool, sessions)
	}
}

// bindRestoredSessions 在所属账号的客户端创建后把恢复的会话绑定回该账号，会话到期后不再等待
func bindRestoredSessions(pool *balancer.AccountPool, sessions []requestdb.StoredSession) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for len(sessions) > 0 {
		now := time.Now()
		pending := sessions[:0]
		for _, session := range sessions {
			switch {
			case now.After(session.ExpiresAt):
			case pool.Client(session.Account) != nil:
				pool.BindSession(session.Key, session.Account)
			default:
				pending = append(pending, session)
			}
		}
		sessions = pending
		if len(sessions) > 0 {
			<-ticker.C
		}
	}
}

// acquire 在请求生命周期内取得线性会话的独占权
func (r *conversationRegistry) acquire(ctx context.Context, key string) (func(), error) {
	if key == "" {
		return func() {}, nil
	}

	r.mu.Lock()
	lock := r.locks[key]
	if lock == nil {
		lock = &conversationLock{slot: semaphore.NewWeighted(1)}
		r.locks[key] = lock
	}
	lock.refs++
	r.mu.Unlock()

	releaseReference := func() {
		r.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(r.locks, key)
		}
		r.mu.Unlock()
	}
	if err := lock.slot.Acquire(ctx, 1); err != nil {
		releaseReference()
		return nil, err
	}
	return func() {
		lock.slot.Release(1)
		releaseReference()
	}, nil
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
	if store := r.store; store != nil {
		go func() {
			if err := store.PruneSessions(context.Background(), now); err != nil {
				logging.Warn(logging.SourceService, "清理会话失败", "错误", err)
			}
		}()
	}
}

func (r *conversationRegistry) has(key string) bool {
	if key == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.states[key]
	return ok && time.Now().Before(entry.expiresAt)
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

func extractHeaderSessionKey(c interface{ GetHeader(string) string }) string {
	if c == nil {
		return ""
	}
	for _, header := range []string{
		"X-Conversation-ID",
		"Conversation-ID",
		"X-Session-ID",
		"Session-ID",
		"X-Chat-ID",
		"Chat-ID",
		"anthropic-session-id",
	} {
		if val := strings.TrimSpace(c.GetHeader(header)); val != "" {
			return val
		}
	}
	return ""
}
