package adapter

import (
	"testing"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// TestConversationRegistryKeepsResponseSnapshotsImmutable 验证旧响应分叉不会被后续轮次改写
func TestConversationRegistryKeepsResponseSnapshotsImmutable(t *testing.T) {
	registry := conversationRegistry{
		states: make(map[string]conversationEntry),
		locks:  make(map[string]*conversationLock),
		ttl:    time.Hour,
	}
	first := gemini.NewConversationStateFrom(gemini.ConversationSnapshot{CID: "cid", RID: "rid-1", RCID: "rcid-1"})
	registry.put(first, "account", 10, "response-1", "cid")

	branch, _, branchTokens, ok := registry.get("response-1")
	if !ok {
		t.Fatal("response-1 is missing")
	}
	if branchTokens != 10 {
		t.Fatalf("response-1 context tokens = %d", branchTokens)
	}
	branch.Update(gemini.ConversationSnapshot{CID: "cid", RID: "rid-2", RCID: "rcid-2"})
	registry.put(branch, "account", 20, "response-2", "cid")

	oldState, _, oldTokens, ok := registry.get("response-1")
	if !ok || oldState.Snapshot().RID != "rid-1" || oldState.Snapshot().RCID != "rcid-1" {
		t.Fatalf("old response snapshot = %#v", oldState.Snapshot())
	}
	if oldTokens != 10 {
		t.Fatalf("old response context tokens = %d", oldTokens)
	}
	latest, _, latestTokens, ok := registry.get("cid")
	if !ok || latest.Snapshot().RID != "rid-2" || latest.Snapshot().RCID != "rcid-2" {
		t.Fatalf("linear conversation snapshot = %#v", latest.Snapshot())
	}
	if latestTokens != 20 {
		t.Fatalf("linear conversation context tokens = %d", latestTokens)
	}
}

// TestConversationRegistrySerializesLinearSession 验证同一显式会话严格串行
func TestConversationRegistrySerializesLinearSession(t *testing.T) {
	registry := conversationRegistry{
		states: make(map[string]conversationEntry),
		locks:  make(map[string]*conversationLock),
		ttl:    time.Hour,
	}
	releaseFirst := registry.acquire("conversation")
	started := make(chan struct{})
	acquired := make(chan struct{})
	go func() {
		close(started)
		releaseSecond := registry.acquire("conversation")
		close(acquired)
		releaseSecond()
	}()
	<-started

	select {
	case <-acquired:
		t.Fatal("second request entered the same conversation concurrently")
	case <-time.After(20 * time.Millisecond):
	}
	releaseFirst()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second request did not continue after release")
	}
}
