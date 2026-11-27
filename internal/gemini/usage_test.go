package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
)

// TestDecodeUsageResponseEmptyPayload 验证 jSf9Qc 空载荷与缺记录返回 errUsageEmpty，明确帧错误与非字符串/非法 JSON 载荷返回对应错误
func TestDecodeUsageResponseEmptyPayload(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"null_status7", ")]}'\n\n103\n[[\"wrb.fr\",\"jSf9Qc\",null,null,null,[7],\"generic\"],[\"di\",39]]\n"},
		{"null_no_status", ")]}'\n\n74\n[[\"wrb.fr\",\"jSf9Qc\",null,null,null,null,\"generic\"],[\"di\",39]]\n"},
		{"empty_string", `[["wrb.fr","jSf9Qc","",null,null]]`},
		{"no_record", `[["wrb.fr","otherRPC","[]",null,null]]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeUsageResponse([]byte(tc.body))
			if !errors.Is(err, errUsageEmpty) {
				t.Fatalf("got %v, want errUsageEmpty", err)
			}
			if errors.Is(err, ErrUsageUnavailable) {
				t.Fatalf("decodeUsageResponse must not bypass session verification with ErrUsageUnavailable")
			}
		})
	}

	_, err := decodeUsageResponse([]byte(")]}'\n\n80\n[[\"wrb.fr\",\"jSf9Qc\",null,null,null,[13],\"generic\"]]\n"))
	if err == nil || errors.Is(err, errUsageEmpty) || errors.Is(err, ErrUsageUnavailable) || !strings.Contains(err.Error(), "13") {
		t.Fatalf("frame status [13] returned %v, want RPC status error containing 13", err)
	}

	_, err = decodeUsageResponse([]byte(`[["wrb.fr","jSf9Qc",123,null,null]]`))
	if err == nil || errors.Is(err, errUsageEmpty) || errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("non-string payload returned %v, want parse error", err)
	}

	_, err = decodeUsageResponse([]byte(`[["wrb.fr","jSf9Qc","{invalid",null,null]]`))
	if err == nil || errors.Is(err, errUsageEmpty) || errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("malformed JSON payload returned %v, want parse error", err)
	}
}

// TestParseUsageInfoZeroWindows 验证 JSPB 空 repeated 字段（省略第 1 槽或为 null）按零个窗口解析，仍读取槽位 2，非法类型仍报错
func TestParseUsageInfoZeroWindows(t *testing.T) {
	for _, tc := range []struct {
		name        string
		raw         string
		wantTier    string
		wantCode    int
		wantOverage bool
	}{
		{"omitted_windows", `[3]`, "ultra", 3, false},
		{"null_windows_false_overage", `[2,null,false]`, "pro", 2, false},
		{"null_windows_true_overage", `[3,null,true]`, "ultra", 3, true},
		{"empty_array_windows", `[4,[]]`, "plus", 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload []any
			if err := json.Unmarshal([]byte(tc.raw), &payload); err != nil {
				t.Fatal(err)
			}
			usage, err := parseUsageInfo(payload)
			if err != nil {
				t.Fatalf("parseUsageInfo(%s) failed: %v", tc.raw, err)
			}
			if usage.Tier != tc.wantTier || usage.TierCode != tc.wantCode || usage.UseOverageAICredits != tc.wantOverage {
				t.Fatalf("got %+v, want tier=%s code=%d overage=%v", usage, tc.wantTier, tc.wantCode, tc.wantOverage)
			}
			if usage.Current != nil || usage.Weekly != nil || usage.RemainingCredits != nil {
				t.Fatalf("zero-window payload unexpectedly populated windows: %+v", usage)
			}
		})
	}

	for _, tc := range []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"missing_tier", `[]`, "官网用量数据缺少套餐"},
		{"non_array_windows", `[2,"bad"]`, "官网用量数据缺少用量窗口"},
		{"malformed_window_row", `[2,["bad"]]`, "官网用量窗口格式无法识别"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload []any
			if err := json.Unmarshal([]byte(tc.raw), &payload); err != nil {
				t.Fatal(err)
			}
			if _, err := parseUsageInfo(payload); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("parseUsageInfo(%s) = %v, want error containing %q", tc.raw, err, tc.wantErr)
			}
		})
	}
}

// TestParseUsageInfoWindows 验证 jSf9Qc 五小时窗口、周窗口、AI 额度与超额开关解析
func TestParseUsageInfoWindows(t *testing.T) {
	raw := `[2,[[null,0.25,1,[[1730000000,0]]],[null,0.75,2,[[1730500000,500]]],[120.5,null,3]],true]`
	var payload []any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	usage, err := parseUsageInfo(payload)
	if err != nil {
		t.Fatalf("parseUsageInfo failed: %v", err)
	}
	if usage.Tier != "pro" || usage.TierCode != 2 || !usage.UseOverageAICredits {
		t.Fatalf("unexpected header fields: %+v", usage)
	}
	if usage.Current == nil || usage.Current.UsedRatio != 0.25 || !usage.Current.ResetAt.Equal(time.Unix(1730000000, 0).UTC()) {
		t.Fatalf("unexpected current window: %+v", usage.Current)
	}
	if usage.Weekly == nil || usage.Weekly.UsedRatio != 0.75 || !usage.Weekly.ResetAt.Equal(time.Unix(1730500000, 500).UTC()) {
		t.Fatalf("unexpected weekly window: %+v", usage.Weekly)
	}
	if usage.RemainingCredits == nil || *usage.RemainingCredits != 120.5 {
		t.Fatalf("unexpected remaining credits: %+v", usage.RemainingCredits)
	}
}

// TestParseAccountTier 验证从 otAQ7b payload[16] 与 payload[17] 推导套餐、层级码与企业层级
func TestParseAccountTier(t *testing.T) {
	makePayload := func(attrs, caps []int) []any {
		p := make([]any, 18)
		toAny := func(nums []int) []any {
			out := make([]any, len(nums))
			for i, v := range nums {
				out[i] = float64(v)
			}
			return out
		}
		p[16] = toAny(attrs)
		p[17] = toAny(caps)
		return p
	}
	for _, tc := range []struct {
		name     string
		attrs    []int
		caps     []int
		wantTier string
		wantCode int
		wantEnt  int
	}{
		{"empty_attrs_and_caps", []int{}, []int{}, "free", 1, 0},
		{"free", []int{38}, []int{82}, "free", 1, 0},
		{"plus", []int{38}, []int{115}, "plus", 4, 0},
		{"pro", []int{8}, []int{19}, "pro", 2, 0},
		{"ultra_3", []int{16}, []int{19, 106}, "ultra", 3, 0},
		{"ultra_6", []int{38}, []int{198}, "ultra", 6, 0},
		{"workspace_work_pro", []int{3, 38}, []int{19}, "pro", 2, 0},
		{"workspace_enterprise_1", []int{3, 21}, []int{82}, "pro", 1, 1},
		{"workspace_enterprise_2", []int{3, 22}, []int{19}, "pro", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAccountTier(makePayload(tc.attrs, tc.caps))
			if got.Tier != tc.wantTier || got.TierCode != tc.wantCode || got.EnterpriseTier != tc.wantEnt {
				t.Fatalf("got %+v, want tier=%s code=%d ent=%d", got, tc.wantTier, tc.wantCode, tc.wantEnt)
			}
		})
	}
	if empty := parseAccountTier([]any{float64(1)}); empty != (AccountTier{}) {
		t.Fatalf("short payload got %+v, want zero", empty)
	}
}

type usageTestTransport struct {
	tls_client.HttpClient
	usageBody string
}

func (u *usageTestTransport) Do(req *http.Request) (*http.Response, error) {
	switch req.URL.Query().Get("rpcids") {
	case usageRPC:
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(u.usageBody)), Request: req}, nil
	case preferencesRPC:
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(")]}'\n\n45\n[[\"wrb.fr\",\"ESY5D\",\"[[null,true]]\"]]\n")), Request: req}, nil
	default:
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}
}

// TestFetchUsageClearsStaleCacheOnUnavailable 验证确认官网不提供用量时清除旧用量缓存并回退到目录层级窗口
func TestFetchUsageClearsStaleCacheOnUnavailable(t *testing.T) {
	client, err := NewClient(AccountSource{ID: "stale-usage"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	client.bootstrap = &Bootstrap{SNlM0e: "AT", BL: "BUILD", FSID: "SID", Language: "en", AccountTier: AccountTier{Tier: "pro", TierCode: 2}}
	transport := &usageTestTransport{
		HttpClient: client.httpClient,
		usageBody:  ")]}'\n\n120\n[[\"wrb.fr\",\"jSf9Qc\",\"[1,[[null,1.0,1,[[4102444800,0]]]]]\",null,null,null,\"generic\"]]\n",
	}
	client.httpClient = transport
	if _, err := client.FetchUsage(context.Background()); err != nil {
		t.Fatalf("initial FetchUsage failed: %v", err)
	}
	if client.CachedUsage() == nil || client.ContextWindow() != 32*1024 {
		t.Fatalf("initial usage=%v window=%d, want non-nil and 32768", client.CachedUsage(), client.ContextWindow())
	}
	transport.usageBody = ")]}'\n\n103\n[[\"wrb.fr\",\"jSf9Qc\",null,null,null,[7],\"generic\"]]\n"
	_, err = client.FetchUsage(context.Background())
	if !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("second FetchUsage = %v, want ErrUsageUnavailable", err)
	}
	if client.CachedUsage() != nil {
		t.Fatalf("stale usage cache was not cleared: %+v", client.CachedUsage())
	}
	if client.Tier() != "pro" || client.ContextWindow() != 1024*1024 {
		t.Fatalf("after clearing cache, tier=%q window=%d, want pro / 1048576", client.Tier(), client.ContextWindow())
	}
}
