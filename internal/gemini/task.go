package gemini

import (
	"context"
	"encoding/json"
)

// onboardTask 在账号未完成 Task 首次使用确认时，按官网 Continue 按钮把偏好 222 写为真；返回值表示生成请求之后还需发送服务条款同意
func (c *Client) onboardTask(ctx context.Context, bootstrap Bootstrap, mode int, level ThinkingLevel) (bool, error) {
	if bootstrap.Preferences.Bool(preferenceTaskOnboarded) {
		return false, nil
	}
	message := make([]any, preferenceTaskOnboarded)
	message[preferenceTaskOnboarded-1] = 1
	request, err := json.Marshal([]any{message, nil, []int{preferenceTaskOnboarded}})
	if err != nil {
		return false, err
	}
	if _, err := c.batchExecute(ctx, bootstrap, batchRequest{
		rpcID: "L5adhe", payload: string(request), sourcePath: "/app", language: bootstrap.Language, extension: "[0]", mode: mode, level: level,
	}); err != nil {
		return false, err
	}
	c.bootstrapMu.Lock()
	if c.bootstrap != nil {
		c.bootstrap.Preferences = c.bootstrap.Preferences.with(preferenceTaskOnboarded, true)
	}
	c.bootstrapMu.Unlock()
	return true, nil
}

// acceptTaskTerms 按官网 Task 首次使用流程发送 UVb0T 服务条款同意：条款版本 0、界面语言、首页 thykhd 令牌、条款类别 12
func (c *Client) acceptTaskTerms(ctx context.Context, bootstrap Bootstrap, mode int, level ThinkingLevel) error {
	request, err := json.Marshal([]any{0, bootstrap.Language, bootstrap.TermsToken, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, 12, nil, nil, 2})
	if err != nil {
		return err
	}
	_, err = c.batchExecute(ctx, bootstrap, batchRequest{
		rpcID: "UVb0T", payload: string(request), sourcePath: "/app", language: bootstrap.Language, extension: "[0]", mode: mode, level: level, untimed: true,
	})
	return err
}
