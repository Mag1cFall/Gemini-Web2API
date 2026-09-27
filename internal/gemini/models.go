package gemini

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Model 表示账号启动时发现的网页模型
type Model struct {
	ID          string
	DisplayName string
	Description string
	Hash        string
	// Tier 为模型行第 4 槽的账号层级，生成请求模型头第 11 项回传该值
	Tier         int
	Capabilities []string
	Mode         int
	Default      bool
}

// ModelCatalog 表示单个账号当前可用的模型目录
type ModelCatalog struct {
	models       []Model
	lookup       map[string]int
	defaultIndex int
}

// List 返回稳定排序的模型副本
func (c ModelCatalog) List() []Model {
	models := append([]Model(nil), c.models...)
	for index := range models {
		models[index].Capabilities = append([]string(nil), models[index].Capabilities...)
	}
	return models
}

// Resolve 根据公开 ID、官方名称或 hash 查找模型
func (c ModelCatalog) Resolve(value string) (Model, error) {
	index, ok := c.lookup[strings.ToLower(strings.TrimSpace(value))]
	if !ok {
		return Model{}, fmt.Errorf("model %q is unavailable for this account", value)
	}
	return c.models[index], nil
}

// Default 返回官方标记的默认模型
func (c ModelCatalog) Default() (Model, error) {
	if c.defaultIndex < 0 || c.defaultIndex >= len(c.models) {
		return Model{}, fmt.Errorf("model catalog has no official default")
	}
	return c.models[c.defaultIndex], nil
}

var modelSlugPattern = regexp.MustCompile(`[^a-z0-9.]+`)

// modelID 从官方显示名生成稳定公开 ID
func modelID(displayName string) string {
	slug := strings.Trim(modelSlugPattern.ReplaceAllString(strings.ToLower(displayName), "-"), "-")
	if strings.HasPrefix(slug, "gemini-") {
		return slug
	}
	return "gemini-" + slug
}

// parseModelCatalog 从 otAQ7b 响应生成唯一模型目录
func parseModelCatalog(payload []any) (ModelCatalog, error) {
	rows, ok := arrayAt(payload, 15)
	if !ok || len(rows) == 0 {
		return ModelCatalog{}, fmt.Errorf("model catalog payload has no models")
	}

	models := make([]Model, 0, len(rows))
	for _, rawRow := range rows {
		row, ok := rawRow.([]any)
		if !ok {
			continue
		}
		hash, _ := stringAt(row, 0)
		displayName, _ := stringAt(row, 11)
		if displayName == "" {
			displayName, _ = stringAt(row, 19)
		}
		mode, _ := intAt(row, 17)
		if hash == "" || displayName == "" || mode == 0 {
			continue
		}
		description, _ := stringAt(row, 12)
		isDefault, _ := boolAt(row, 7)
		if alternateDefault, ok := boolAt(row, 15); ok {
			isDefault = isDefault || alternateDefault
		}
		tier, _ := intAt(row, 4)
		models = append(models, Model{
			ID:           modelID(displayName),
			DisplayName:  displayName,
			Description:  description,
			Hash:         hash,
			Tier:         tier,
			Capabilities: []string{"generateContent", "streamGenerateContent"},
			Mode:         mode,
			Default:      isDefault,
		})
	}
	if len(models) == 0 {
		return ModelCatalog{}, fmt.Errorf("model catalog rows are invalid")
	}

	sort.SliceStable(models, func(left, right int) bool {
		return models[left].ID < models[right].ID
	})
	catalog := ModelCatalog{models: models, lookup: make(map[string]int), defaultIndex: -1}
	for index, model := range models {
		catalog.lookup[strings.ToLower(model.ID)] = index
		catalog.lookup[strings.ToLower(model.DisplayName)] = index
		catalog.lookup[strings.ToLower(model.Hash)] = index
		if model.Default {
			if catalog.defaultIndex >= 0 {
				return ModelCatalog{}, fmt.Errorf("model catalog has multiple official defaults")
			}
			catalog.defaultIndex = index
		}
	}
	if catalog.defaultIndex < 0 {
		return ModelCatalog{}, fmt.Errorf("model catalog has no official default")
	}
	return catalog, nil
}

// buildModelHeader 构造包含模型、会话类型、思考策略与请求计时的生成请求头
func buildModelHeader(model Model, temporaryChat bool, thinkingMode ThinkingMode, clientID string, timing []any) (string, error) {
	chatMode := 0
	if temporaryChat {
		chatMode = 1
	}
	header, err := json.Marshal([]any{
		1, nil, nil, nil, model.Hash, nil, nil, chatMode,
		[]int{4, 5, 6, 8, 16, 4, 5, 6, 8, 16}, nil, nil, model.Tier, nil, nil, model.Mode, int(thinkingMode) + 1, clientID,
		nil, nil, timing,
	})
	if err != nil {
		return "", err
	}
	return string(header), nil
}

// buildGenericHeader 构造 batchexecute 请求使用的客户端头
func buildGenericHeader(clientID string, timing []any) string {
	header, _ := json.Marshal([]any{1, nil, nil, nil, nil, nil, nil, nil, []int{4, 5, 6, 8, 16}, nil, nil, nil, nil, nil, nil, nil, clientID, nil, nil, timing})
	return string(header)
}

// requestTiming 返回网页客户端头末尾的耗时与发送时间，秒为零时写空值
func requestTiming(elapsed time.Duration, now time.Time) []any {
	var seconds any
	if whole := int64(elapsed / time.Second); whole > 0 {
		seconds = whole
	}
	return []any{
		[]any{seconds, int64(elapsed % time.Second)},
		[]any{now.Unix(), int64(now.Nanosecond()) / int64(time.Millisecond) * int64(time.Millisecond)},
	}
}

func arrayAt(values []any, index int) ([]any, bool) {
	if index < 0 || index >= len(values) {
		return nil, false
	}
	value, ok := values[index].([]any)
	return value, ok
}

func stringAt(values []any, index int) (string, bool) {
	if index < 0 || index >= len(values) {
		return "", false
	}
	value, ok := values[index].(string)
	return value, ok
}

func intAt(values []any, index int) (int, bool) {
	if index < 0 || index >= len(values) {
		return 0, false
	}
	value, ok := values[index].(float64)
	return int(value), ok
}

func boolAt(values []any, index int) (bool, bool) {
	if index < 0 || index >= len(values) {
		return false, false
	}
	value, ok := values[index].(bool)
	return value, ok
}
