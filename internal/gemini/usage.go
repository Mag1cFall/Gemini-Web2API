package gemini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

const usageRPC = "jSf9Qc"

// ErrUsageUnavailable 表示账号已登录，但官网用量接口不返回该账号的用量数据（例如 Google Workspace 账号）
var ErrUsageUnavailable = errors.New("官网不提供该账号的用量数据")

var errUsageEmpty = errors.New("官网用量响应为空")

// AccountTier 是从模型目录 otAQ7b 的 payload[16] 与 payload[17] 推导出的账号层级
type AccountTier struct {
	Tier           string
	TierCode       int
	EnterpriseTier int
}

// parseAccountTier 从模型目录 otAQ7b 的 payload[16] 与 payload[17] 推导账号层级与企业层级
func parseAccountTier(payload []any) AccountTier {
	if len(payload) <= 16 {
		return AccountTier{}
	}
	attrs, _ := arrayAt(payload, 16)
	caps, _ := arrayAt(payload, 17)
	has := func(values []any, target int) bool {
		for _, item := range values {
			if value, ok := item.(float64); ok && int(value) == target {
				return true
			}
		}
		return false
	}
	code := 1
	switch {
	case has(attrs, 16) || has(caps, 106):
		code = 3
	case has(caps, 198):
		code = 6
	case has(attrs, 8) || (has(caps, 19) && !has(caps, 106)):
		code = 2
	case has(caps, 115):
		code = 4
	}
	enterprise := 0
	switch {
	case has(attrs, 21):
		enterprise = 1
	case has(attrs, 22):
		enterprise = 2
	}
	tier := usageTier(code)
	if enterprise != 0 && code == 1 {
		tier = "pro"
	}
	return AccountTier{Tier: tier, TierCode: code, EnterpriseTier: enterprise}
}

// buildAccountModelHeader 构造生成会话的模型扩展头 protobuf，企业非 Ultra 账号将企业层级写入第 12 槽，其余将账号层级写入第 11 槽
func buildAccountModelHeader(features []int, model Model, language string, chatMode any, tier int, enterpriseTier int, thinkingLevel ThinkingLevel, clientID string, buildLabel string, timing []any) []byte {
	var tierSlot, enterpriseSlot, levelSlot any
	if enterpriseTier != 0 && tier != 3 && tier != 6 {
		enterpriseSlot = enterpriseTier
	} else {
		tierSlot = tier
	}
	if thinkingLevel != 0 {
		levelSlot = int(thinkingLevel)
	}
	return encodeJSPB(withVariantAndTimezone([]any{
		1, nil, nil, nil, model.Hash, nil, language, chatMode,
		features, nil, nil, tierSlot, enterpriseSlot, nil, model.Mode, levelSlot, clientID,
		[]string{buildLabel}, nil, timing,
	}, model.Variant))
}

// Tier 返回当前账号的套餐名称，优先取用量接口，其次取模型目录 otAQ7b 推导出的层级；访客会话为空
func (c *Client) Tier() string {
	if c.guest {
		return ""
	}
	if usage := c.usage.Load(); usage != nil && usage.Tier != "" {
		return usage.Tier
	}
	if bootstrap, err := c.bootstrapSnapshot(); err == nil {
		return bootstrap.AccountTier.Tier
	}
	return ""
}

// tierFor 结合已读取的 bootstrap 快照返回当前账号的套餐名称，访客会话为空
func (c *Client) tierFor(bootstrap Bootstrap) string {
	if c.guest {
		return ""
	}
	if usage := c.usage.Load(); usage != nil && usage.Tier != "" {
		return usage.Tier
	}
	return bootstrap.AccountTier.Tier
}

// headerTier 结合已读取的 bootstrap 快照返回生成请求模型头的账号层级编码与企业层级编码
func (c *Client) headerTier(bootstrap Bootstrap) (int, int) {
	if c.guest {
		return 1, 0
	}
	tier := bootstrap.AccountTier.TierCode
	if usage := c.usage.Load(); usage != nil {
		tier = usage.TierCode
	}
	return tier, bootstrap.AccountTier.EnterpriseTier
}

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
	usage, err := c.fetchUsage(ctx, true)
	if err != nil {
		if errors.Is(err, ErrUsageUnavailable) {
			c.usage.Store(nil)
		}
		return UsageInfo{}, err
	}
	c.usage.Store(&usage)
	return usage, nil
}

func (c *Client) fetchUsage(ctx context.Context, allowRefresh bool) (UsageInfo, error) {
	version := c.cookieVersion.Load()
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return UsageInfo{}, err
	}
	body, err := c.postBatch(ctx, bootstrap, batchRequest{rpcID: usageRPC, payload: "[]", sourcePath: "/usage", language: bootstrap.Language, extension: "[0]"})
	renew := false
	switch {
	case err != nil:
		renew = renewable(err)
	case bytes.Contains(body, []byte("accounts.google.com")):
		err, renew = retryableProtocolError(ErrSignedOut.Error(), ErrSignedOut), true
	default:
		var usage UsageInfo
		usage, err = decodeUsageResponse(body)
		if !errors.Is(err, errUsageEmpty) {
			return usage, err
		}
		if _, err = c.fetchPreferences(ctx, bootstrap); err == nil {
			return UsageInfo{}, ErrUsageUnavailable
		}
		renew = renewable(err)
	}
	if !renew || !allowRefresh || c.refresh == nil {
		return UsageInfo{}, err
	}
	if err := c.refreshCookies(ctx, version); err != nil {
		return UsageInfo{}, err
	}
	if err := c.init(ctx, false); err != nil {
		return UsageInfo{}, err
	}
	return c.fetchUsage(ctx, false)
}

// decodeUsageResponse 解析用量 RPC 的响应正文，没有用量载荷时返回 errUsageEmpty
func decodeUsageResponse(body []byte) (UsageInfo, error) {
	payload, err := rpcPayload(bytes.NewReader(body), usageRPC, func(record []any) error {
		if code, ok := intAt(record, 5, 0); ok && code != 7 {
			return rpcStatusError("读取用量", record)
		}
		return errUsageEmpty
	})
	if err != nil {
		return UsageInfo{}, err
	}
	return parseUsageInfo(payload)
}

func parseUsageInfo(payload []any) (UsageInfo, error) {
	tierCode, ok := intAt(payload, 0)
	if !ok {
		return UsageInfo{}, fmt.Errorf("官网用量数据缺少套餐")
	}
	var rows []any
	if len(payload) > 1 && payload[1] != nil {
		rows, ok = arrayAt(payload, 1)
		if !ok {
			return UsageInfo{}, fmt.Errorf("官网用量数据缺少用量窗口")
		}
	}
	usage := UsageInfo{Tier: usageTier(tierCode), TierCode: tierCode, FetchedAt: time.Now().UTC()}
	usage.UseOverageAICredits, _ = boolAt(payload, 2)
	for _, rawRow := range rows {
		row, ok := rawRow.([]any)
		if !ok {
			return UsageInfo{}, fmt.Errorf("官网用量窗口格式无法识别")
		}
		kind, ok := intAt(row, 2)
		if !ok {
			return UsageInfo{}, fmt.Errorf("官网用量窗口缺少类型")
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
				return UsageInfo{}, fmt.Errorf("官网 AI 额度缺少剩余值")
			}
			usage.RemainingCredits = &remaining
		}
	}
	return usage, nil
}

func parseUsageWindow(row []any) (*UsageWindow, error) {
	ratio, ok := numberAt(row, 1)
	if !ok {
		return nil, fmt.Errorf("官网用量窗口缺少已用比例")
	}
	resetOuter, ok := arrayAt(row, 3)
	if !ok || len(resetOuter) != 1 {
		return nil, fmt.Errorf("官网用量窗口缺少重置时间")
	}
	reset, ok := resetOuter[0].([]any)
	if !ok {
		return nil, fmt.Errorf("官网用量重置时间无法识别")
	}
	seconds, secondsOK := numberAt(reset, 0)
	nanos, nanosOK := numberAt(reset, 1)
	if !secondsOK || !nanosOK {
		return nil, fmt.Errorf("官网用量重置时间不完整")
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

// usageContextWindow 返回套餐的上下文窗口 tokens，未知套餐为 0，表示不在本地检查窗口
func usageContextWindow(tier string) int {
	switch tier {
	case "free":
		return 32 * 1024
	case "plus":
		return 128 * 1024
	case "pro", "ultra":
		return 1024 * 1024
	}
	return 0
}
