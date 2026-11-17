package gemini

import (
	"context"
	"testing"
)

// TestAwaitTurnAfterSlotYield 验证请求槽已被首事件等待让出时轮询不再释放，账号仍只有一个请求槽
func TestAwaitTurnAfterSlotYield(t *testing.T) {
	client, err := NewClient(AccountSource{Concurrency: 1}, "", false)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	release, err := client.AcquireRequest(context.Background())
	if err != nil {
		t.Fatalf("AcquireRequest failed: %v", err)
	}
	release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = WithSlotYieldController(ctx, func() (func(), bool) {
		return func() {}, true
	})
	if err := client.awaitTurn(ctx, NewConversationState(), nil, Model{ID: "gemini-video"}, 0, nil, nil, func(Event) error { return nil }); err == nil {
		t.Fatal("expected context canceled error")
	}
	if _, ok := client.TryAcquireRequest(); !ok {
		t.Fatal("the yielded request slot should be free for other requests")
	}
	if _, ok := client.TryAcquireRequest(); ok {
		t.Fatal("the account should still have exactly one request slot")
	}
}

// TestAwaitTurnKeepsHeldSlot 验证调用方持有请求槽时轮询返回前重新占用并调用恢复函数，请求结束后请求槽空闲
func TestAwaitTurnKeepsHeldSlot(t *testing.T) {
	client, err := NewClient(AccountSource{Concurrency: 1}, "", false)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	release, err := client.AcquireRequest(context.Background())
	if err != nil {
		t.Fatalf("AcquireRequest failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	restored := false
	ctx = WithSlotYieldController(ctx, func() (func(), bool) {
		return func() { restored = true }, false
	})
	_ = client.awaitTurn(ctx, NewConversationState(), nil, Model{ID: "gemini-video"}, 0, nil, nil, func(Event) error { return nil })
	if !restored {
		t.Fatal("the yield controller should be restored so the request releases its slot when it ends")
	}
	if _, ok := client.TryAcquireRequest(); ok {
		t.Fatal("the request should hold its slot again after polling")
	}
	release()
	if _, ok := client.TryAcquireRequest(); !ok {
		t.Fatal("the slot should be free after the request ends")
	}
}
