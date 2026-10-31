package accounts

import (
	"context"
	"testing"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
)

func TestTestProxyFormatValidation(t *testing.T) {
	mgr := New(context.Background(), balancer.NewAccountPool(0, 0), Settings{})

	// 1. 无效的 scheme
	res := mgr.TestProxy(context.Background(), "ftp://127.0.0.1:21")
	if res.OK || res.Status != "invalid_format" {
		t.Fatalf("expected invalid_format, got ok=%v, status=%s", res.OK, res.Status)
	}

	// 2. 无法解析的主机
	res = mgr.TestProxy(context.Background(), "http://")
	if res.OK || res.Status != "invalid_format" {
		t.Fatalf("expected invalid_format, got ok=%v, status=%s", res.OK, res.Status)
	}
}

func TestPoolReconnectHook(t *testing.T) {
	pool := balancer.NewAccountPool(0, 0)
	mgr := New(context.Background(), pool, Settings{})

	// 验证 pool.Reconnect 是否已绑定
	err := pool.Reconnect(context.Background(), "non-existent-account")
	if err == nil {
		t.Fatalf("expected error for non-existent account, got nil")
	}
	// 确认错误不是 "未配置重新连接器"
	if err.Error() == "未配置重新连接器" {
		t.Fatalf("expected manager.Verify to be registered as reconnector")
	}
	_ = mgr
}
