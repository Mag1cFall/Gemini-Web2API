package gemini

import "sync"

// ConversationSnapshot 表示可序列化的网页会话三元组
type ConversationSnapshot struct {
	CID  string
	RID  string
	RCID string
}

// ConversationState 保存线程安全的网页会话状态
type ConversationState struct {
	mu   sync.RWMutex
	cid  string
	rid  string
	rcid string
}

// NewConversationState 创建空网页会话状态
func NewConversationState() *ConversationState {
	return &ConversationState{}
}

// NewConversationStateFrom 从已有三元组创建网页会话状态
func NewConversationStateFrom(snapshot ConversationSnapshot) *ConversationState {
	state := &ConversationState{}
	state.Update(snapshot)
	return state
}

// Snapshot 返回当前会话三元组副本
func (s *ConversationState) Snapshot() ConversationSnapshot {
	if s == nil {
		return ConversationSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ConversationSnapshot{CID: s.cid, RID: s.rid, RCID: s.rcid}
}

// Update 原子替换非空会话字段
func (s *ConversationState) Update(snapshot ConversationSnapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.CID != "" {
		s.cid = snapshot.CID
	}
	if snapshot.RID != "" {
		s.rid = snapshot.RID
	}
	if snapshot.RCID != "" {
		s.rcid = snapshot.RCID
	}
}
