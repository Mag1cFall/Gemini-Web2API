package balancer

import (
	"context"
	"testing"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

func TestUnifiedRoutingQuotaAwareness(t *testing.T) {
	pool := NewAccountPool(1*time.Minute, 30*time.Minute)

	acc1 := "acc1-exhausted@gmail.com"
	acc2 := "acc2-with-quota@gmail.com"

	c1, err := gemini.NewGuestClient("", 1)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := gemini.NewGuestClient("", 1)
	if err != nil {
		t.Fatal(err)
	}

	argonModel := gemini.Model{ID: "gemini-4-argon-task", DisplayName: "4 Argon Task", Capabilities: []string{"generateContent"}}
	catalog := gemini.NewModelCatalogForTest([]gemini.Model{argonModel})
	c1.SetBootstrapForTest(&gemini.Bootstrap{Catalog: catalog})
	c2.SetBootstrapForTest(&gemini.Bootstrap{Catalog: catalog})

	// 模拟 acc1 额度已 100% 耗尽，ResetAt 在 2 小时后
	c1.SetUsageForTest(&gemini.UsageInfo{
		Tier: "pro",
		Current: &gemini.UsageWindow{
			UsedRatio: 1.0,
			ResetAt:   time.Now().Add(2 * time.Hour),
		},
	})

	// 模拟 acc2 额度只用了 17% (尚有 83% 额度)
	c2.SetUsageForTest(&gemini.UsageInfo{
		Tier: "pro",
		Current: &gemini.UsageWindow{
			UsedRatio: 0.17,
			ResetAt:   time.Now().Add(3 * time.Hour),
		},
	})

	pool.Register(acc1, AccountInfo{Source: "brave"}, false)
	pool.SetClient(acc1, c1)
	pool.MarkReady(acc1)

	pool.Register(acc2, AccountInfo{Source: "brave"}, false)
	pool.SetClient(acc2, c2)
	pool.MarkReady(acc2)

	// 请求 Pro 模型 gemini-4-argon-task
	modelID := "gemini-4-argon-task"
	client, chosenID, release, err := pool.AcquireForModel(context.Background(), "", modelID, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatalf("expected to acquire a client, got nil")
	}
	defer release()

	// 调度器必须精准绕过 100% 耗尽的 acc1，直接选择还有额度的 acc2！
	if chosenID != acc2 {
		t.Fatalf("expected acc2 (%s) with 17%% quota to be chosen, but got %s", acc2, chosenID)
	}
}

func TestUnifiedRoutingSessionAffinity(t *testing.T) {
	pool := NewAccountPool(1*time.Minute, 30*time.Minute)

	acc1 := "acc1@gmail.com"
	acc2 := "acc2@gmail.com"

	c1, _ := gemini.NewGuestClient("", 1)
	c2, _ := gemini.NewGuestClient("", 1)

	affinityModel := gemini.Model{ID: "gemini-4-argon-task", DisplayName: "4 Argon Task", Capabilities: []string{"generateContent"}}
	affinityCatalog := gemini.NewModelCatalogForTest([]gemini.Model{affinityModel})
	c1.SetBootstrapForTest(&gemini.Bootstrap{Catalog: affinityCatalog})
	c2.SetBootstrapForTest(&gemini.Bootstrap{Catalog: affinityCatalog})

	c1.SetUsageForTest(&gemini.UsageInfo{Tier: "pro", Current: &gemini.UsageWindow{UsedRatio: 0.3, ResetAt: time.Now().Add(1 * time.Hour)}})
	c2.SetUsageForTest(&gemini.UsageInfo{Tier: "pro", Current: &gemini.UsageWindow{UsedRatio: 0.1, ResetAt: time.Now().Add(1 * time.Hour)}})

	pool.Register(acc1, AccountInfo{Source: "brave"}, false)
	pool.SetClient(acc1, c1)
	pool.MarkReady(acc1)

	pool.Register(acc2, AccountInfo{Source: "brave"}, false)
	pool.SetClient(acc2, c2)
	pool.MarkReady(acc2)

	// 会话绑定到 acc2
	sessionKey := "test-session-key"
	pool.BindSession(sessionKey, acc2)

	// 请求该会话时，即使 acc1 使用率较高，也必须优先沿用已绑定的 acc2，以复用上下文节省 Token
	client, chosenID, release, err := pool.AcquireForModel(context.Background(), sessionKey, "gemini-4-argon-task", nil)
	if err != nil || client == nil {
		t.Fatalf("failed to acquire client: %v", err)
	}
	defer release()

	if chosenID != acc2 {
		t.Fatalf("expected session affinity to stay on %s, got %s", acc2, chosenID)
	}
}
