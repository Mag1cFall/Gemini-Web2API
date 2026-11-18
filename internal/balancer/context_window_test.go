package balancer

import (
	"testing"
	"time"
)

// TestContextTooNarrow 验证上下文窗口与请求 token 数的比较：窗口小于请求时为真，两者相等时仍可装下，窗口未知（0）时按未知处理
func TestContextTooNarrow(t *testing.T) {
	cases := []struct {
		window int
		tokens int
		narrow bool
	}{
		{32768, 56508, true},
		{32768, 32768, false},
		{128 * 1024, 56508, false},
		{0, 56508, false},
		{32768, 0, false},
	}
	for _, tc := range cases {
		if got := contextTooNarrow(tc.window, tc.tokens); got != tc.narrow {
			t.Fatalf("窗口 %d、请求 %d tokens：%v，应为 %v", tc.window, tc.tokens, got, tc.narrow)
		}
	}
}

// TestExcludeNarrowContextKeepsUnknownWindow 验证排除装不下请求的账号时的边界：请求 token 数不为正时不改动，窗口未知的账号不排除，已排除的账号不重复加入
func TestExcludeNarrowContextKeepsUnknownWindow(t *testing.T) {
	pool := NewAccountPool(2*time.Minute, time.Minute)
	pool.Register("account", AccountInfo{}, false)
	pool.Register("guest", AccountInfo{Source: SourceGuest}, false)
	pool.MarkReady("account")
	pool.MarkReady("guest")
	excluded := map[string]struct{}{"account": {}}
	if added := pool.ExcludeNarrowContext(excluded, 0); len(added) != 0 {
		t.Fatalf("请求 token 数为 0 时不应排除账号，实际排除 %v", added)
	}
	if added := pool.ExcludeNarrowContext(excluded, 56508); len(added) != 0 {
		t.Fatalf("窗口未知的账号不应排除，实际排除 %v", added)
	}
	if len(excluded) != 1 {
		t.Fatalf("排除集被改动：%v", excluded)
	}
}

// TestExcludeNarrowContextReportsAddedAccounts 验证排除装不下请求的账号时返回新增的账号 ID 并写入排除集，账号池没有排除集之外的改动
func TestExcludeNarrowContextReportsAddedAccounts(t *testing.T) {
	pool := NewAccountPool(2*time.Minute, time.Minute)
	pool.Register("guest", AccountInfo{Source: SourceGuest}, false)
	pool.MarkReady("guest")
	excluded := map[string]struct{}{"other": {}}
	if added := pool.ExcludeNarrowContext(excluded, 1); len(added) != 0 {
		t.Fatalf("窗口未知的访客会话不应排除：%v", added)
	}
	if _, ok := excluded["other"]; !ok || len(excluded) != 1 {
		t.Fatalf("排除集应只含原有的账号：%v", excluded)
	}
}
