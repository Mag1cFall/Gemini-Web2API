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
	pool.Add(first, "first")
	pool.Add(second, "second")

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
	pool.Add(first, "first")
	pool.Add(second, "second")

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
	pool.Add(first, "first")
	pool.Add(second, "second")

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

// TestAccountPoolRejectsUnknownModel 验证账号模型能力来自客户端目录
func TestAccountPoolRejectsUnknownModel(t *testing.T) {
	pool := NewAccountPool(time.Minute, time.Hour)
	first := &gemini.Client{}
	second := &gemini.Client{}
	pool.Add(first, "first")
	pool.Add(second, "second")

	client, accountID := pool.NextForModel("", "model-missing")
	if client != nil || accountID != "" {
		t.Fatalf("unknown model selection = %p %q", client, accountID)
	}
	if pool.HasModel("model-missing") {
		t.Fatal("HasModel returned an unknown model")
	}
}
