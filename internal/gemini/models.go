package gemini

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Model 表示账号启动时发现的网页模型
type Model struct {
	ID           string
	DisplayName  string
	Description  string
	Hash         string
	Header       string
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
func parseModelCatalog(payload []any, clientID string) (ModelCatalog, error) {
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
		header, err := buildModelHeader(hash, mode, ThinkingStandard, clientID)
		if err != nil {
			return ModelCatalog{}, fmt.Errorf("encode model header: %w", err)
		}
		models = append(models, Model{
			ID:           modelID(displayName),
			DisplayName:  displayName,
			Description:  description,
			Hash:         hash,
			Header:       header,
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

// buildModelHeader 构造包含模型与思考策略的请求头
func buildModelHeader(hash string, mode int, thinkingMode ThinkingMode, clientID string) (string, error) {
	header, err := json.Marshal([]any{
		1, nil, nil, nil, hash, nil, nil, 0,
		[]int{4, 5, 6, 8, 4, 5, 6, 8}, nil, nil, 2, nil, nil, mode, int(thinkingMode) + 1, clientID,
	})
	if err != nil {
		return "", err
	}
	return string(header), nil
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
