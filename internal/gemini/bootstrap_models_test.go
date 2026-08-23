package gemini

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

// TestParseBootstrap 验证首页动态参数不使用静态回退
func TestParseBootstrap(t *testing.T) {
	bootstrap, err := parseBootstrap(`{"SNlM0e":"AT","bl":"BUILD","FdrFJe":"SID"}`)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.SNlM0e != "AT" || bootstrap.BL != "BUILD" || bootstrap.FSID != "SID" {
		t.Fatalf("bootstrap = %+v", bootstrap)
	}
	if _, err := parseBootstrap(`{"SNlM0e":"AT","bl":"BUILD"}`); err == nil {
		t.Fatal("missing f.sid must fail")
	}
}

// TestModelIDDoesNotDuplicatePrefix 验证官方显示名已带 Gemini 时不会重复前缀
func TestModelIDDoesNotDuplicatePrefix(t *testing.T) {
	if got := modelID("Gemini 3.7 Flash"); got != "gemini-3.7-flash" {
		t.Fatalf("模型 ID 错误: %q", got)
	}
	if got := modelID("3.7 Flash"); got != "gemini-3.7-flash" {
		t.Fatalf("模型 ID 错误: %q", got)
	}
}

// TestParseModelCatalog 验证模型目录和请求头只来自启动响应
func TestParseModelCatalog(t *testing.T) {
	fixture, err := os.ReadFile("testdata/otAQ7b-models.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload []any
	if err := json.Unmarshal(fixture, &payload); err != nil {
		t.Fatal(err)
	}
	catalog, err := parseModelCatalog(payload, "SESSION-ID")
	if err != nil {
		t.Fatal(err)
	}
	models := catalog.List()
	if len(models) != 3 {
		t.Fatalf("models = %d", len(models))
	}
	want := []struct {
		id          string
		hash        string
		mode        int
		isDefault   bool
		displayName string
	}{
		{"gemini-3.5-flash-lite", "8c46e95b1a07cecc", 6, false, "3.5 Flash-Lite"},
		{"gemini-3.7-flash", "56fdd199312815e2", 1, true, "3.7 Flash"},
		{"gemini-3.1-pro", "e6fa609c3fa255c0", 3, false, "3.1 Pro"},
	}
	for _, expected := range want {
		model, err := catalog.Resolve(expected.id)
		if err != nil {
			t.Fatal(err)
		}
		if model.Hash != expected.hash || model.Mode != expected.mode || model.Default != expected.isDefault || model.DisplayName != expected.displayName {
			t.Fatalf("model %s = %+v", expected.id, model)
		}
		var header []any
		if err := json.Unmarshal([]byte(model.Header), &header); err != nil {
			t.Fatal(err)
		}
		if hash, _ := stringAt(header, 4); hash != expected.hash {
			t.Fatalf("header hash = %q", hash)
		}
		if mode, _ := intAt(header, 14); mode != expected.mode {
			t.Fatalf("header mode = %d", mode)
		}
		if thinkingMode, _ := intAt(header, 15); thinkingMode != 1 {
			t.Fatalf("header thinking mode = %d", thinkingMode)
		}
		if sessionID, _ := stringAt(header, 16); sessionID != "SESSION-ID" {
			t.Fatalf("header session = %q", sessionID)
		}
	}
	extendedHeader, err := buildModelHeader("56fdd199312815e2", 1, ThinkingExtended, "SESSION-ID")
	if err != nil {
		t.Fatal(err)
	}
	var header []any
	if err := json.Unmarshal([]byte(extendedHeader), &header); err != nil {
		t.Fatal(err)
	}
	if thinkingMode, _ := intAt(header, 15); thinkingMode != 2 {
		t.Fatalf("extended header thinking mode = %d", thinkingMode)
	}
	defaultModel, err := catalog.Default()
	if err != nil || defaultModel.ID != "gemini-3.7-flash" {
		t.Fatalf("default model = %+v error = %v", defaultModel, err)
	}
	if _, err := catalog.Resolve("unknown"); err == nil {
		t.Fatal("unknown model must fail")
	}
}

// TestNormalizeFingerprint 验证 HTTP 身份与 TLS 模板版本一致
func TestNormalizeFingerprint(t *testing.T) {
	fingerprint, _, err := normalizeFingerprint(Fingerprint{
		Version:    "151",
		Platform:   "Linux",
		UserAgent:  "Chrome/151",
		TLSProfile: "chrome_146",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint.Version != "146" || fingerprint.Platform != "Windows" {
		t.Fatalf("fingerprint = %+v", fingerprint)
	}
	if strings.Contains(fingerprint.UserAgent, "151") || !strings.Contains(fingerprint.UserAgent, "Chrome/146") {
		t.Fatalf("user agent = %q", fingerprint.UserAgent)
	}
}

// TestNextReqIDConcurrent 验证并发请求不会复用 reqid
func TestNextReqIDConcurrent(t *testing.T) {
	client := &Client{}
	client.reqID.Store(100000)
	values := make(chan int64, 256)
	var wait sync.WaitGroup
	for index := 0; index < 256; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			values <- client.nextReqID()
		}()
	}
	wait.Wait()
	close(values)
	seen := make(map[int64]struct{}, 256)
	for value := range values {
		if _, exists := seen[value]; exists {
			t.Fatalf("duplicate reqid %d", value)
		}
		seen[value] = struct{}{}
	}
	if len(seen) != 256 {
		t.Fatalf("reqid count = %d", len(seen))
	}
}

// TestParseUsageInfo 验证官网用量窗口按 kind 映射
func TestParseUsageInfo(t *testing.T) {
	usage, err := parseUsageInfo([]any{
		float64(2),
		[]any{
			[]any{float64(48289), 0.00196025, float64(2), []any{[]any{float64(1788113617), float64(857365000)}}},
			[]any{float64(2305), 0.04, float64(1), []any{[]any{float64(1787526817), float64(857315000)}}},
			[]any{float64(120), nil, float64(3)},
			[]any{nil, nil, float64(99)},
		},
		false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Tier != "pro" || usage.TierCode != 2 {
		t.Fatalf("tier = %+v", usage)
	}
	if usage.Current == nil || usage.Current.UsedRatio != 0.04 || usage.Current.ResetAt.Unix() != 1787526817 {
		t.Fatalf("current = %+v", usage.Current)
	}
	if usage.Weekly == nil || usage.Weekly.UsedRatio != 0.00196025 || usage.Weekly.ResetAt.Unix() != 1788113617 {
		t.Fatalf("weekly = %+v", usage.Weekly)
	}
	if usage.RemainingCredits == nil || *usage.RemainingCredits != 120 {
		t.Fatalf("remaining credits = %v", usage.RemainingCredits)
	}
	if _, err := parseUsageInfo([]any{float64(2), []any{[]any{0.0, 0.1, float64(1), []any{[]any{float64(1), float64(0)}}}}}); err == nil {
		t.Fatal("missing weekly window was accepted")
	}
}
