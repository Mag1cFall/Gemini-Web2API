package balancer

import (
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// TestCooldownRules 验证冷却规则：冷却期间的并发失败不计次，较短冷却不覆盖较长冷却，截止时刻按墙上时钟比较，连续 1060 依次冷却 ACCOUNT_COOLDOWN、5、15、30、60 分钟并在成功后复位
func TestCooldownRules(t *testing.T) {
	rateLimited := &gemini.ProtocolError{HTTPStatus: 429, Code: 1095}
	restricted := &gemini.ProtocolError{HTTPStatus: 403, Code: 1060}
	pool := NewAccountPool(2*time.Minute, time.Minute)
	for _, id := range []string{"concurrent", "longer", "ladder"} {
		pool.Register(id, AccountInfo{}, false)
		pool.MarkReady(id)
	}
	remaining := func(id string) time.Duration {
		return time.Until(pool.entry(id).Cooldown.until).Round(time.Minute)
	}

	for range 3 {
		pool.ReportFailure("concurrent", "m", rateLimited)
	}
	if got := remaining("concurrent"); got != time.Minute || pool.entry("concurrent").ConsecutiveFails != 1 {
		t.Fatalf("冷却期间的并发失败计次：冷却 %v，连续 %d 次", got, pool.entry("concurrent").ConsecutiveFails)
	}
	if until := pool.entry("concurrent").Cooldown.until; strings.Contains(until.String(), "m=") {
		t.Fatalf("冷却截止时刻带单调时钟读数：%v", until)
	}

	pool.ReportFailure("longer", "m", restricted)
	pool.ReportFailure("longer", "m", rateLimited)
	if got, reason := remaining("longer"), pool.entry("longer").Cooldown.reason; got != 2*time.Minute || reason != "ip_restricted" {
		t.Fatalf("较短冷却覆盖了较长冷却：冷却 %v，原因 %s", got, reason)
	}

	for _, want := range []time.Duration{2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, time.Hour} {
		pool.entry("ladder").Cooldown.until = time.Now().Add(-time.Second)
		pool.ReportFailure("ladder", "m", restricted)
		if got := remaining("ladder"); got != want {
			t.Fatalf("连续 1060 第 %d 次冷却 %v，应为 %v", pool.entry("ladder").ConsecutiveFails, got, want)
		}
	}
	pool.ReportSuccess("ladder", "m")
	pool.ReportFailure("ladder", "m", restricted)
	if got := remaining("ladder"); got != 2*time.Minute {
		t.Fatalf("成功后 1060 冷却 %v，应复位为 2m0s", got)
	}
}
