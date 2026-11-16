package gemini

import (
	"context"
	"testing"
)

func TestAwaitTurnRespectsAlreadyReleasedSlotYieldController(t *testing.T) {
	client, err := NewClient(AccountSource{Concurrency: 1}, "", false)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	release, err := client.AcquireRequest(context.Background())
	if err != nil {
		t.Fatalf("AcquireRequest failed: %v", err)
	}
	// 模拟外层 yieldingRelease 已经提前释放了唯一 1 个请求槽
	release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = WithSlotYieldController(ctx, func() (func(), bool) {
		return func() {}, true
	})

	// 若 awaitTurn 未检查 alreadyReleased，此处会触发 panic: semaphore: released more than held
	err = client.awaitTurn(ctx, NewConversationState(), nil, Model{ID: "gemini-video"}, 0, nil, nil, func(Event) error {
		return nil
	})
	if err == nil {
		t.Fatalf("expected context canceled error, got nil")
	}
}

func TestAwaitTurnReleasesAndRestoresWhenSlotStillHeld(t *testing.T) {
	client, err := NewClient(AccountSource{Concurrency: 1}, "", false)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	release, err := client.AcquireRequest(context.Background())
	if err != nil {
		t.Fatalf("AcquireRequest failed: %v", err)
	}

	restored := false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = WithSlotYieldController(ctx, func() (func(), bool) {
		return func() { restored = true }, false
	})

	_ = client.awaitTurn(ctx, NewConversationState(), nil, Model{ID: "gemini-video"}, 0, nil, nil, func(Event) error {
		return nil
	})
	if !restored {
		t.Fatalf("expected restore callback to be invoked")
	}
	// 结束后仍持有 1 个槽位，由外层统一释放，不应 panic
	release()
}
