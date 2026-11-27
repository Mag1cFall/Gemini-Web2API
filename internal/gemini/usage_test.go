package gemini

import (
	"errors"
	"strings"
	"testing"
)

// 企业/Workspace 账号：用量 RPC 返回空串，应归为无用量数据，而非未登录
func TestDecodeUsageResponseEmptyPayloadIsUnavailable(t *testing.T) {
	body := `[["wrb.fr","jSf9Qc","",null,null]]`
	_, err := decodeUsageResponse(strings.NewReader(body))
	if !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("expected ErrUsageUnavailable, got %v", err)
	}
	if errors.Is(err, errUsageNotSignedIn) {
		t.Fatalf("empty usage payload must not be treated as signed out")
	}
}

// 企业/Workspace 账号：响应里没有用量 RPC 记录，应归为无用量数据
func TestDecodeUsageResponseNoRecordIsUnavailable(t *testing.T) {
	body := `[["wrb.fr","otherRPC","[]",null,null]]`
	_, err := decodeUsageResponse(strings.NewReader(body))
	if !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("expected ErrUsageUnavailable, got %v", err)
	}
}

// 有套餐、无用量窗口行：视为无用量并保留套餐，不报错
func TestParseUsageInfoTierWithoutWindows(t *testing.T) {
	usage, err := parseUsageInfo([]any{float64(2), nil})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage.Tier != "pro" || usage.Current != nil || usage.Weekly != nil {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

// 正常个人账号用量仍能解析出窗口
func TestParseUsageInfoWithWindow(t *testing.T) {
	row := []any{nil, float64(0.5), float64(1), []any{[]any{float64(1730000000), float64(0)}}}
	usage, err := parseUsageInfo([]any{float64(1), []any{row}})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage.Current == nil || usage.Current.UsedRatio != 0.5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}
