package proxypool

import (
	"context"
	"testing"
	"time"
)

func TestPoolCooldownAndRebind(t *testing.T) {
	pool := New([]string{
		"http://127.0.0.1:8001",
		"http://127.0.0.1:8002",
	})

	// 初始状态下两代理均健康
	acc1 := "acc1@gmail.com"
	acc2 := "acc2@gmail.com"

	proxy1 := pool.Assign(acc1)
	proxy2 := pool.Assign(acc2)

	if proxy1 == proxy2 {
		t.Fatalf("expected acc1 and acc2 to be distributed across different proxies, got both %s", proxy1)
	}

	// 模拟 proxy1 遭 Google 302 拦截触发隔离冷却
	moved := pool.MarkCooldown(proxy1, 30*time.Minute, "遭遇 Google 人机验证拦截 (302)")
	if len(moved) != 1 || moved[0] != acc1 {
		t.Fatalf("expected acc1 to be moved, got %v", moved)
	}

	// 此时 acc1 应该被改绑至唯一健康的 proxy2
	newProxy1 := pool.Assign(acc1)
	if newProxy1 != proxy2 {
		t.Fatalf("expected acc1 to be re-bound to proxy2 (%s), got %s", proxy2, newProxy1)
	}

	// 检查代理状态列表，proxy1 应处于 CooldownUntil 期间且 healthy=false
	statuses := pool.Status()
	for _, status := range statuses {
		switch status.URL {
		case proxy1:
			if status.Healthy {
				t.Errorf("expected %s to be marked unhealthy during cooldown", proxy1)
			}
			if status.CooldownUntil.IsZero() {
				t.Errorf("expected %s to have CooldownUntil set", proxy1)
			}
		case proxy2:
			if !status.Healthy {
				t.Errorf("expected %s to be healthy", proxy2)
			}
		}
	}

	// 测试 Check probe 在冷却期内不应破坏隔离
	pool.Check(context.Background(), func(ctx context.Context, s string) (time.Duration, error) {
		return 50 * time.Millisecond, nil
	})

	for _, status := range pool.Status() {
		if status.URL == proxy1 && status.Healthy {
			t.Errorf("expected %s to remain in cooldown isolation after probe during cooldown window", proxy1)
		}
	}

	// 模拟冷却时间过后再次 Check，应恢复健康
	pool.mu.Lock()
	for _, state := range pool.proxies {
		state.cooldownUntil = time.Now().Add(-1 * time.Minute)
	}
	pool.mu.Unlock()

	pool.Check(context.Background(), func(ctx context.Context, s string) (time.Duration, error) {
		return 50 * time.Millisecond, nil
	})

	for _, status := range pool.Status() {
		if !status.Healthy {
			t.Errorf("expected all proxies to be restored to healthy after cooldown expired, but %s was not", status.URL)
		}
	}
}
