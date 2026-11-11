package adapter

import (
	"context"
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
	// temporary 为不写入网页历史的账号以普通会话发送的会话，键为 CID，不再被续接后从网页历史删除
	temporary map[string]temporaryChat
	mu        sync.Mutex
}

// temporaryChat 是待删除的网页会话所属账号、模型模式与最近一次生成结束的时间
type temporaryChat struct {
	accountID string
	mode      int
	touched   time.Time
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
	states:    make(map[string]conversationEntry),
	locks:     make(map[string]*conversationLock),
	ttl:       30 * time.Minute,
	temporary: make(map[string]temporaryChat),
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
	if _, temporary := r.temporary[entry.snapshot.CID]; temporary {
		store = nil
	}
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
	store.SaveSessions(sessions)
}

// markTemporary 登记一个结束生成后需要从网页历史删除的会话
func (r *conversationRegistry) markTemporary(snapshot gemini.ConversationSnapshot, accountID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.temporary[snapshot.CID] = temporaryChat{accountID: accountID, mode: snapshot.ModelMode, touched: time.Now()}
}

// takeTemporary 取出不再被续接的待删除会话：生成结束超过一分钟且没有未过期的续接条目指向它，all 为真时取出全部
func (r *conversationRegistry) takeTemporary(now time.Time, all bool) map[string]temporaryChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	live := make(map[string]bool)
	for _, entry := range r.states {
		if now.Before(entry.expiresAt) {
			live[entry.snapshot.CID] = true
		}
	}
	taken := make(map[string]temporaryChat)
	for cid, chat := range r.temporary {
		if all || !live[cid] && now.Sub(chat.touched) > time.Minute {
			taken[cid] = chat
			delete(r.temporary, cid)
		}
	}
	return taken
}

// deleteChats 从各会话所属账号的网页历史中删除会话
func deleteChats(ctx context.Context, pool *balancer.AccountPool, chats map[string]temporaryChat) {
	for cid, chat := range chats {
		client := pool.Client(chat.accountID)
		if client == nil {
			continue
		}
		if err := client.DeleteConversation(ctx, cid, chat.mode); err != nil {
			logging.Warn(chat.accountID, "删除网页会话失败", "会话", cid, "错误", err)
			continue
		}
		logging.Info(chat.accountID, "已删除网页会话", "会话", cid)
	}
}

// CleanupTemporaryChats 每分钟从网页历史删除不再被续接的临时会话，直到 ctx 结束
func CleanupTemporaryChats(ctx context.Context, pool *balancer.AccountPool) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			deleteChats(ctx, pool, conversations.takeTemporary(now, false))
		}
	}
}

// DeleteTemporaryChats 从网页历史删除全部临时会话，服务停止时调用
func DeleteTemporaryChats(ctx context.Context, pool *balancer.AccountPool) {
	deleteChats(ctx, pool, conversations.takeTemporary(time.Now(), true))
}

// configureCooldownStore 让账号与模型冷却写入账本库，并恢复上次运行保存的未结束冷却
func configureCooldownStore(pool *balancer.AccountPool, store *requestdb.Store) {
	if store == nil {
		return
	}
	stored, err := store.Cooldowns(context.Background(), time.Now())
	if err != nil {
		logging.Warn(logging.SourceService, "读取冷却失败", "错误", err)
	}
	cooldowns := make([]balancer.Cooldown, 0, len(stored))
	for _, value := range stored {
		cooldowns = append(cooldowns, balancer.Cooldown{Account: value.Account, Model: value.Model, Until: value.Until, Reason: value.Reason})
	}
	pool.RestoreCooldowns(cooldowns)
	pool.OnCooldownChange(func(cooldowns []balancer.Cooldown) {
		values := make([]requestdb.StoredCooldown, 0, len(cooldowns))
		for _, value := range cooldowns {
			values = append(values, requestdb.StoredCooldown{Account: value.Account, Model: value.Model, Until: value.Until, Reason: value.Reason})
		}
		if err := store.SaveCooldowns(context.Background(), values); err != nil {
			logging.Warn(logging.SourceService, "保存冷却失败", "错误", err)
		}
	})
	if len(cooldowns) > 0 {
		logging.Info(logging.SourceService, "恢复冷却", "数量", len(cooldowns))
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

func conversationKey(previousResponseID string, conversationID string, headerValue string) string {
	if previousResponseID != "" {
		return previousResponseID
	}
	if conversationID != "" {
		return conversationID
	}
	return headerValue
}
