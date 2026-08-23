package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

const usageRPC = "jSf9Qc"

// UsageWindow 表示官网展示的一段用量窗口
type UsageWindow struct {
	UsedRatio float64   `json:"used_ratio"`
	ResetAt   time.Time `json:"reset_at"`
}

// UsageInfo 表示单个账号的官网用量信息
type UsageInfo struct {
	Tier                string       `json:"tier"`
	TierCode            int          `json:"tier_code"`
	Current             *UsageWindow `json:"current,omitempty"`
	Weekly              *UsageWindow `json:"weekly,omitempty"`
	RemainingCredits    *float64     `json:"remaining_credits,omitempty"`
	UseOverageAICredits bool         `json:"use_overage_ai_credits"`
	FetchedAt           time.Time    `json:"fetched_at"`
}

// FetchUsage 读取官网当前账号用量
func (c *Client) FetchUsage(ctx context.Context) (UsageInfo, error) {
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return UsageInfo{}, err
	}
	encodedRequest, err := json.Marshal([]any{[]any{[]any{usageRPC, "[]", nil, "generic"}}})
	if err != nil {
		return UsageInfo{}, err
	}
	form := url.Values{"f.req": {string(encodedRequest)}, "at": {bootstrap.SNlM0e}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointBatch, strings.NewReader(form.Encode()))
	if err != nil {
		return UsageInfo{}, err
	}
	query := req.URL.Query()
	query.Set("rpcids", usageRPC)
	query.Set("source-path", "/usage")
	query.Set("bl", bootstrap.BL)
	query.Set("f.sid", bootstrap.FSID)
	query.Set("hl", c.fingerprint.languageCode())
	query.Set("_reqid", fmt.Sprintf("%d", c.nextReqID()))
	query.Set("rt", "c")
	req.URL.RawQuery = query.Encode()
	c.applyXHRHeaders(req)
	genericHeader, _ := json.Marshal([]any{1, nil, nil, nil, nil, nil, nil, nil, []int{4, 5, 6, 8}, nil, nil, nil, nil, nil, nil, nil, c.clientID})
	req.Header.Set("x-goog-ext-525001261-jspb", string(genericHeader))
	req.Header.Set("x-goog-ext-73010989-jspb", "[0]")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UsageInfo{}, fmt.Errorf("usage request: %w", err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		resp.Body.Close()
		return UsageInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UsageInfo{}, httpStatusError(resp.StatusCode, "usage")
	}
	return decodeUsageResponse(resp.Body)
}

func decodeUsageResponse(reader io.Reader) (UsageInfo, error) {
	var usage UsageInfo
	found := false
	err := scanProtocolRecords(reader, func(record []any) error {
		tag, _ := stringAt(record, 0)
		rpcID, _ := stringAt(record, 1)
		if tag != "wrb.fr" || rpcID != usageRPC {
			return nil
		}
		encoded, _ := stringAt(record, 2)
		var payload []any
		if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
			return fmt.Errorf("decode usage payload: %w", err)
		}
		parsed, err := parseUsageInfo(payload)
		if err != nil {
			return err
		}
		usage = parsed
		found = true
		return nil
	})
	if err != nil {
		return UsageInfo{}, err
	}
	if !found {
		return UsageInfo{}, fmt.Errorf("usage response has no %s payload", usageRPC)
	}
	return usage, nil
}

func parseUsageInfo(payload []any) (UsageInfo, error) {
	tierCode, ok := intAt(payload, 0)
	if !ok {
		return UsageInfo{}, fmt.Errorf("usage payload has no tier")
	}
	rows, ok := arrayAt(payload, 1)
	if !ok {
		return UsageInfo{}, fmt.Errorf("usage payload has no windows")
	}
	usage := UsageInfo{Tier: usageTier(tierCode), TierCode: tierCode, FetchedAt: time.Now().UTC()}
	usage.UseOverageAICredits, _ = boolAt(payload, 2)
	for _, rawRow := range rows {
		row, ok := rawRow.([]any)
		if !ok {
			return UsageInfo{}, fmt.Errorf("usage window is invalid")
		}
		kind, ok := intAt(row, 2)
		if !ok {
			return UsageInfo{}, fmt.Errorf("usage window has no kind")
		}
		switch kind {
		case 1:
			window, err := parseUsageWindow(row)
			if err != nil {
				return UsageInfo{}, err
			}
			usage.Current = window
		case 2:
			window, err := parseUsageWindow(row)
			if err != nil {
				return UsageInfo{}, err
			}
			usage.Weekly = window
		case 3:
			remaining, ok := numberAt(row, 0)
			if !ok {
				return UsageInfo{}, fmt.Errorf("usage credits have no remaining value")
			}
			usage.RemainingCredits = &remaining
		}
	}
	if usage.Current == nil || usage.Weekly == nil {
		return UsageInfo{}, fmt.Errorf("usage payload has no current or weekly window")
	}
	return usage, nil
}

func parseUsageWindow(row []any) (*UsageWindow, error) {
	ratio, ok := numberAt(row, 1)
	if !ok {
		return nil, fmt.Errorf("usage window has no ratio")
	}
	resetOuter, ok := arrayAt(row, 3)
	if !ok || len(resetOuter) != 1 {
		return nil, fmt.Errorf("usage window has no reset time")
	}
	reset, ok := resetOuter[0].([]any)
	if !ok {
		return nil, fmt.Errorf("usage reset time is invalid")
	}
	seconds, secondsOK := numberAt(reset, 0)
	nanos, nanosOK := numberAt(reset, 1)
	if !secondsOK || !nanosOK {
		return nil, fmt.Errorf("usage reset time is incomplete")
	}
	return &UsageWindow{UsedRatio: ratio, ResetAt: time.Unix(int64(seconds), int64(nanos)).UTC()}, nil
}

func usageTier(code int) string {
	switch code {
	case 1:
		return "free"
	case 2:
		return "pro"
	case 3, 6:
		return "ultra"
	case 4:
		return "plus"
	default:
		return "unknown"
	}
}

func numberAt(values []any, index int) (float64, bool) {
	if index < 0 || index >= len(values) {
		return 0, false
	}
	value, ok := values[index].(float64)
	return value, ok
}
