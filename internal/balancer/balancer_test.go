package balancer

import (
	"testing"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// TestAccountPoolRoundRobinAndStickySession 验证轮询和显式会话粘性
func TestAccountPoolRoundRobinAndStickySession(t *testing.T) {
	pool := NewAccountPool(time.Minute, time.Hour)
	first := &gemini.Client{}
	second := &gemini.Client{}
	pool.Add(first, "first", nil)
	pool.Add(second, "second", nil)

	client, accountID := pool.NextForModel("conversation-a", "")
	if client != first || accountID != "first" {
		t.Fatalf("first selection = %p %q", client, accountID)
	}
	client, accountID = pool.NextForModel("conversation-a", "")
	if client != first || accountID != "first" {
		t.Fatalf("sticky selection = %p %q", client, accountID)
	}
	client, accountID = pool.NextForModel("", "")
	if client != second || accountID != "second" {
		t.Fatalf("round robin selection = %p %q", client, accountID)
	}
}

// TestAccountPoolBindsResponseAlias 验证首轮响应标识会粘住原账号
func TestAccountPoolBindsResponseAlias(t *testing.T) {
	pool := NewAccountPool(time.Minute, time.Hour)
	first := &gemini.Client{}
	second := &gemini.Client{}
	pool.Add(first, "first", nil)
	pool.Add(second, "second", nil)

	client, accountID := pool.NextForModel("", "")
	if client != first || accountID != "first" {
		t.Fatalf("first selection = %p %q", client, accountID)
	}
	pool.BindSession("response-1", accountID)

	client, accountID = pool.NextForModel("response-1", "")
	if client != first || accountID != "first" {
		t.Fatalf("bound selection = %p %q", client, accountID)
	}
}

// TestAccountPoolCooldownPreservesStickySession 验证失败账号冷却期间保留会话绑定
func TestAccountPoolCooldownPreservesStickySession(t *testing.T) {
	pool := NewAccountPool(time.Minute, time.Hour)
	first := &gemini.Client{}
	second := &gemini.Client{}
	pool.Add(first, "first", nil)
	pool.Add(second, "second", nil)

	_, _ = pool.NextForModel("conversation-a", "")
	pool.ReportFailure("first")

	client, accountID := pool.NextForModel("conversation-a", "")
	if client != nil || accountID != "first" {
		t.Fatalf("selection after cooldown = %p %q", client, accountID)
	}
	client, accountID = pool.NextForModel("", "")
	if client != second || accountID != "second" {
		t.Fatalf("new request selection = %p %q", client, accountID)
	}
	status := pool.Status()
	if status.Available != 1 || status.CoolingDown != 1 {
		t.Fatalf("status = %#v", status)
	}
	clients := pool.Clients()
	if len(clients) != 1 || clients[0] != second {
		t.Fatalf("clients = %#v", clients)
	}
	pool.ReportSuccess("first")
	client, accountID = pool.NextForModel("conversation-a", "")
	if client != first || accountID != "first" {
		t.Fatalf("selection after recovery = %p %q", client, accountID)
	}
}

// TestAccountPoolUnavailableStatus 验证初始化失败账号不可被调度
func TestAccountPoolUnavailableStatus(t *testing.T) {
	pool := NewAccountPool(time.Minute, time.Hour)
	pool.AddUnavailable("broken")

	if client, _ := pool.NextForModel("", ""); client != nil {
		t.Fatal("NextForModel returned an unavailable account")
	}
	status := pool.Status()
	if status.Total != 1 || status.Unavailable != 1 || status.Available != 0 {
		t.Fatalf("status = %#v", status)
	}
}

// TestAccountPoolSelectsAccountWithRequestedModel 验证模型灰度账号选择
func TestAccountPoolSelectsAccountWithRequestedModel(t *testing.T) {
	pool := NewAccountPool(time.Minute, time.Hour)
	first := &gemini.Client{}
	second := &gemini.Client{}
	pool.Add(first, "first", []gemini.Model{{ID: "model-a"}})
	pool.Add(second, "second", []gemini.Model{{ID: "model-b"}})

	client, accountID := pool.NextForModel("", "model-b")
	if client != second || accountID != "second" {
		t.Fatalf("model-aware selection = %p %q", client, accountID)
	}

	pool.BindSession("conversation-b", accountID)
	client, accountID = pool.NextForModel("conversation-b", "model-a")
	if client != nil || accountID != "second" {
		t.Fatalf("bound model selection = %p %q", client, accountID)
	}
	client, accountID = pool.NextForModel("", "model-a")
	if client != first || accountID != "first" {
		t.Fatalf("new model-aware selection = %p %q", client, accountID)
	}
	client, accountID = pool.NextForModel("conversation-b", "model-b")
	if client != second || accountID != "second" {
		t.Fatalf("preserved model binding = %p %q", client, accountID)
	}
	if !pool.HasModel("model-a") || pool.HasModel("model-missing") {
		t.Fatal("HasModel returned an incorrect result")
	}
}
