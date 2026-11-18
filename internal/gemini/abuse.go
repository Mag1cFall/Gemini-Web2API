package gemini

import (
	"context"
	"strconv"
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

// parseAbuseStatus 解析 GPRiHf 载荷：正常账号为 []，有异常标记时 [1][1] 除以 1000000 为状态码，[1][3][1] 为信号
func parseAbuseStatus(payload []any) AbuseStatus {
	info, _ := arrayAt(payload, 1)
	if len(info) == 0 {
		return AbuseStatus{}
	}
	status := AbuseStatus{Restricted: true}
	raw, _ := valuePath(info, 1)
	switch raw := raw.(type) {
	case float64:
		status.Code = int(raw) / 1000000
	case string:
		if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
			status.Code = int(value / 1000000)
		}
	}
	status.Signal, _ = stringPath(info, 3, 1)
	return status
}
