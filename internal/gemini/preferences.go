package gemini

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
)

const preferencesRPC = "ESY5D"

// preferenceFields 是官网首页读取账号偏好时请求的字段号
var preferenceFields = []int{
	2, 3, 7, 8, 9, 10, 11, 16, 18, 19, 20, 23, 24, 25, 26, 27, 28, 29, 31, 32, 33, 34, 35, 37,
	39, 40, 41, 42, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 64,
	65, 66, 67, 68, 70, 72, 77, 78, 79, 87, 89, 91, 92, 93, 94, 96, 97, 99, 100, 101, 102, 103, 104, 105,
	106, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 121, 122, 123, 124, 126, 127, 128, 129, 130, 131, 132,
	133, 136, 137, 138, 139, 140, 141, 145, 146, 147, 148, 152, 154, 155, 158, 160, 161, 162, 163, 165, 166, 171, 174, 175,
	176, 177, 178, 179, 180, 181, 182, 183, 184, 185, 187, 188, 189, 191, 192, 193, 194, 195, 197, 198, 199, 200, 201, 202,
	203, 204, 205, 206, 207, 210, 211, 215, 217, 221, 222, 223, 228, 232, 233, 234, 235, 236, 238, 239, 244, 245, 247, 248,
	249, 250, 251, 252, 254, 255, 259, 260, 261, 265, 266, 270, 272, 280, 287, 291, 293, 296, 297, 301,
}

const (
	// preferenceSlot41 是决定生成载荷 41 槽取值的账号偏好字段
	preferenceSlot41 = 25
	// preferenceSlot91 是决定生成载荷 91 槽取值的账号偏好字段
	preferenceSlot91 = 261
	// preferenceUploadTerms 是账号最近一次同意上传条款的 Unix 秒数
	preferenceUploadTerms = 145
	// preferenceTaskOnboarded 是账号已完成 Task 首次使用确认的偏好字段
	preferenceTaskOnboarded = 222
	// preferenceSelectedModel 是官网模型选择器当前选中模型的 hash，页面打开时预选该模型
	preferenceSelectedModel = 100
)

// Preferences 是 ESY5D 返回的账号偏好消息，字段号超出数组的值位于末尾的稀疏对象
type Preferences []any

// Field 按字段号返回偏好值
func (p Preferences) Field(number int) any {
	values := []any(p)
	if len(values) > 0 {
		if sparse, ok := values[len(values)-1].(map[string]any); ok {
			if value, ok := sparse[strconv.Itoa(number)]; ok {
				return value
			}
			values = values[:len(values)-1]
		}
	}
	if number < 1 || number > len(values) {
		return nil
	}
	return values[number-1]
}

// with 返回把字段号设为 value 的偏好副本
func (p Preferences) with(number int, value any) Preferences {
	values := slices.Clone([]any(p))
	if len(values) > 0 {
		if sparse, ok := values[len(values)-1].(map[string]any); ok {
			if number < len(values) {
				values[number-1] = value
				return values
			}
			sparse = maps.Clone(sparse)
			sparse[strconv.Itoa(number)] = value
			values[len(values)-1] = sparse
			return values
		}
	}
	for len(values) < number {
		values = append(values, nil)
	}
	values[number-1] = value
	return values
}

// Bool 按字段号返回布尔偏好，缺失时为 false
func (p Preferences) Bool(number int) bool {
	value, _ := p.Field(number).(bool)
	return value
}

// fetchPreferences 以官网首页的字段列表读取账号偏好
func (c *Client) fetchPreferences(ctx context.Context, bootstrap Bootstrap) (Preferences, error) {
	request, err := json.Marshal([]any{nil, preferenceFields})
	if err != nil {
		return nil, err
	}
	payload, err := c.batchExecute(ctx, bootstrap, batchRequest{rpcID: preferencesRPC, payload: string(request), sourcePath: "/app", language: bootstrap.Language, extension: "[0]"})
	if err != nil {
		return nil, err
	}
	preferences, _ := arrayAt(payload, 0)
	return Preferences(preferences), nil
}

// selectedModelMode 返回官网页面打开时预选模型的 mode，偏好字段 100 缺失或不在目录中时取账号默认模型
func selectedModelMode(catalog ModelCatalog, preferences Preferences) int {
	if hash, ok := preferences.Field(preferenceSelectedModel).(string); ok {
		if model, err := catalog.Resolve(hash); err == nil {
			return model.Mode
		}
	}
	if model, err := catalog.Default(); err == nil {
		return model.Mode
	}
	return 0
}
