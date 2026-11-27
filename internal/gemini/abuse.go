package gemini

import (
	"context"
	"strconv"
	"time"
)

// AbuseStatus 是官网页面加载时 GPRiHf 返回的账号异常状态，Restricted 为假表示没有异常标记
type AbuseStatus struct {
	Restricted bool   `json:"restricted"`
	Code       int    `json:"code,omitempty"`
	Signal     string `json:"signal,omitempty"`
}

// AbuseStatus 返回账号最近一次初始化读取的异常状态，访客会话与尚未读取时为 nil
func (c *Client) AbuseStatus() *AbuseStatus {
	return c.abuse.Load()
}

// fetchAbuseStatus 按官网页面加载时的 GPRiHf 请求读取账号异常状态
func (c *Client) fetchAbuseStatus(ctx context.Context, bootstrap Bootstrap) (AbuseStatus, error) {
	payload, err := c.batchExecute(ctx, bootstrap, batchRequest{rpcID: "GPRiHf", payload: "[]", sourcePath: "/app", language: bootstrap.Language, extension: "[]"})
	if err != nil {
		return AbuseStatus{}, err
	}
	return parseAbuseStatus(payload), nil
}

// parseAbuseStatus 按官网前端解析 GPRiHf 载荷：正常账号为 []；存在《使用限制政策》警告时 payload[0] 为告警记录，[0][0] 为签发微秒时间戳，[0][2][0] 为违规类型码（两者非零时官网弹出使用限制警告卡片）
func parseAbuseStatus(payload []any) AbuseStatus {
	info, _ := arrayAt(payload, 0)
	if len(info) == 0 {
		return AbuseStatus{}
	}
	issued := unixSecondsMicro(info, 0)
	code, _ := intAt(info, 2, 0)
	if issued == 0 || code == 0 {
		return AbuseStatus{}
	}
	return AbuseStatus{
		Restricted: true,
		Code:       code,
		Signal:     time.Unix(issued, 0).UTC().Format(time.RFC3339),
	}
}

// unixSecondsMicro 读取微秒时间戳字段并换算为 Unix 秒，兼容 JSON 数字与字符串编码
func unixSecondsMicro(values []any, path ...int) int64 {
	raw, ok := valueAt(values, path...)
	if !ok {
		return 0
	}
	switch value := raw.(type) {
	case float64:
		return int64(value) / 1_000_000
	case string:
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0
		}
		return parsed / 1_000_000
	default:
		return 0
	}
}
