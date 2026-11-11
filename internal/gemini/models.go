package gemini

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Model 表示账号启动时发现的网页模型
type Model struct {
	ID           string
	DisplayName  string
	Description  string
	Hash         string
	Capabilities []string
	Mode         int
	Default      bool
	// ThinkingLevels 为官网模型菜单中该模型可选的思考强度
	ThinkingLevels []ThinkingLevel
	// Feature 为该 ID 在生成请求中选中的官网工具，普通对话模型为零
	Feature Feature
	// Gem 为 Gem 条目的 Gem ID，写入生成请求 19 槽
	Gem string
	// Variant 为模型策略中与其他模型共用 mode 的模型的变体键，这类模型的 Hash 也是该键
	Variant string
}

// ImageModelID 是官网 Create image 工具的公开模型 ID
const ImageModelID = "gemini-3.1-flash-image"

// featureTool 是以独立公开 ID 提供、由账户默认模型协调的官网输入框工具
type featureTool struct {
	id          string
	displayName string
	description string
	feature     Feature
	// capabilities 为账户能力表 payload[17] 中启用该工具需要的全部能力号
	capabilities []int
}

// featureTools 是公开为独立模型 ID 的官网工具
var featureTools = []featureTool{
	{ImageModelID, "Nano Banana 2", "Gemini Web image generation and editing", FeatureImage, []int{82}},
	{"gemini-video", "Video", "Gemini Web video generation", FeatureVideo, []int{71, 140}},
	{"gemini-music", "Music", "Gemini Web music generation", FeatureMusic, []int{139}},
	{"gemini-canvas", "Canvas", "Gemini Web Canvas documents and code", FeatureCanvas, []int{51}},
	{"gemini-deep-research", "Deep Research", "Gemini Web Deep Research reports", FeatureDeepResearch, []int{50}},
	{"gemini-guided-learning", "Guided Learning", "Gemini Web Guided Learning", FeatureLearning, []int{117}},
}

// ModelCatalog 表示单个账号当前可用的模型目录
type ModelCatalog struct {
	models       []Model
	lookup       map[string]int
	defaultIndex int
	// options 为 payload[19] 中各工具的选项组
	options map[Feature][]toolOption
	// personalIntelligence 表示 payload[21] 给出了 Personal Intelligence 开关，访客与未开放该功能的账号没有
	personalIntelligence bool
}

// toolOption 是官网工具选项组中的一项，id 写入生成请求 55 槽；source 表示属于 Sources 多选组（组槽 2 为 2）
type toolOption struct {
	id       int
	group    string
	label    string
	selected bool
	source   bool
}

// aspectRatioPattern 匹配 16:9、1536x1024 形式的宽高比
var aspectRatioPattern = regexp.MustCompile(`(\d+)\s*[:x×]\s*(\d+)`)

// parseToolOptions 读取 payload[19] 中各工具的选项组
func parseToolOptions(payload []any) map[Feature][]toolOption {
	options := make(map[Feature][]toolOption)
	groups, _ := arrayAt(payload, 19)
	for _, rawGroup := range groups {
		group, _ := rawGroup.([]any)
		feature, ok := intAt(group, 0)
		kind, _ := intAt(group, 2)
		name, _ := stringAt(group, 3)
		items, _ := arrayAt(group, 7)
		if !ok {
			continue
		}
		for _, rawItem := range items {
			item, _ := rawItem.([]any)
			id, ok := intAt(item, 0)
			if !ok {
				continue
			}
			label, _ := stringAt(item, 3)
			selected, _ := valuePath(item, 2)
			options[Feature(feature)] = append(options[Feature(feature)], toolOption{id: id, group: name, label: label, selected: selected == true, source: kind == 2})
		}
	}
	return options
}

// nativeImageRatios 是官网菜单之外上游按原值生成的图片宽高比
var nativeImageRatios = []string{"2:3", "21:9", "1:8", "8:1"}

// toolChoice 是生成请求选中的工具选项：options 写入 55 槽，sources 为其中 Sources 组的选项，aspectRatio 为请求宽高比时选中的标签，aspectOrder 与 durationOrder 为选中的宽高比与视频时长在官网菜单中的序号（从 1 起）
type toolChoice struct {
	options       []int
	sources       []int
	aspectRatio   string
	aspectOrder   int
	durationOrder int
}

// toolSelection 返回工具选中的选项：各组默认项，请求宽高比时 Aspect ratio 组换成比例最接近的一项；工具没有选项组时为 nil
func (c ModelCatalog) toolSelection(feature Feature, aspectRatio string) *toolChoice {
	options, ok := c.options[feature]
	if !ok {
		return nil
	}
	requested, hasRatio := parseAspectRatio(aspectRatio)
	choice := &toolChoice{options: make([]int, 0)}
	closest, closestDistance, closestOrder := toolOption{id: -1}, math.Inf(1), 0
	order := make(map[string]int)
	for _, option := range options {
		order[option.group]++
		ratio, isRatio := parseAspectRatio(option.label)
		if hasRatio && option.group == "Aspect ratio" && isRatio {
			if distance := math.Abs(math.Log(ratio / requested)); distance < closestDistance {
				closest, closestDistance, closestOrder = option, distance, order[option.group]
			}
			continue
		}
		if option.selected {
			choice.options = append(choice.options, option.id)
			if option.source {
				choice.sources = append(choice.sources, option.id)
			}
			switch option.group {
			case "Aspect ratio":
				choice.aspectOrder = order[option.group]
			case "Duration":
				choice.durationOrder = order[option.group]
			}
		}
	}
	if closest.id >= 0 {
		choice.options = append(choice.options, closest.id)
		choice.aspectRatio, choice.aspectOrder = closest.label, closestOrder
		if slices.Contains(nativeImageRatios, aspectRatio) {
			choice.aspectRatio = aspectRatio
		}
	}
	return choice
}

// parseAspectRatio 解析文本中第一个宽高比
func parseAspectRatio(value string) (float64, bool) {
	match := aspectRatioPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, false
	}
	width, _ := strconv.ParseFloat(match[1], 64)
	height, _ := strconv.ParseFloat(match[2], 64)
	if width == 0 || height == 0 {
		return 0, false
	}
	return width / height, true
}

// List 返回稳定排序的模型副本
func (c ModelCatalog) List() []Model {
	models := append([]Model(nil), c.models...)
	for index := range models {
		models[index].Capabilities = append([]string(nil), models[index].Capabilities...)
	}
	return models
}

// Resolve 根据公开 ID、官方名称或 hash 查找模型，公开 ID 可带 Gemini API 的 models/ 前缀
func (c ModelCatalog) Resolve(value string) (Model, error) {
	index, ok := c.lookup[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "models/"))]
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

// catalogLanguage 是模型目录请求的界面语言，使模型名与公开 ID 不随账户界面语言变化
const catalogLanguage = "en"

// modelID 从官方显示名生成稳定公开 ID
func modelID(displayName string) string {
	slug := strings.Trim(modelSlugPattern.ReplaceAllString(strings.ToLower(displayName), "-"), "-")
	if strings.HasPrefix(slug, "gemini-") {
		return slug
	}
	return "gemini-" + slug
}

// modelName 返回官网菜单项的型号名与说明：标题与副标题中含短名（如 Flash）的一项是型号名，另一项是说明，副标题不含短名时标题是型号名
func modelName(short, title, subtitle string) (name, description string) {
	if strings.Contains(subtitle, short) && !strings.Contains(title, short) {
		return subtitle, title
	}
	return title, subtitle
}

// parseModelCatalog 从 otAQ7b 响应生成唯一模型目录：payload[15] 的模型行加上 payload[24] 策略中带变体键的模型，默认模型为 payload[0] 指定 mode 的行，没有该行时为首行
func parseModelCatalog(payload []any) (ModelCatalog, error) {
	rows, ok := arrayAt(payload, 15)
	if !ok || len(rows) == 0 {
		return ModelCatalog{}, fmt.Errorf("model catalog payload has no models")
	}

	defaultMode, _ := intAt(payload, 0)
	chatPolicy := parsePolicy(payload, 1)
	models := make([]Model, 0, len(rows))
	for _, rawRow := range rows {
		row, ok := rawRow.([]any)
		if !ok {
			continue
		}
		hash, _ := stringAt(row, 0)
		short, _ := stringAt(row, 1)
		title, _ := stringAt(row, 11)
		if title == "" {
			title, _ = stringAt(row, 19)
		}
		mode, _ := intAt(row, 17)
		if hash == "" || title == "" || mode == 0 {
			continue
		}
		subtitle, _ := stringAt(row, 12)
		displayName, description := modelName(short, title, subtitle)
		levels, _ := policyLevels(chatPolicy, mode, "")
		models = append(models, Model{
			ID:             modelID(displayName),
			DisplayName:    displayName,
			Description:    description,
			Hash:           hash,
			Capabilities:   []string{"generateContent", "streamGenerateContent"},
			Mode:           mode,
			Default:        mode == defaultMode,
			ThinkingLevels: levels,
		})
	}
	if len(models) == 0 {
		return ModelCatalog{}, fmt.Errorf("model catalog rows are invalid")
	}
	if !slices.ContainsFunc(models, func(model Model) bool { return model.Default }) {
		models[0].Default = true
	}
	for _, entry := range chatPolicy {
		if entry.variant == "" || entry.upgrade {
			continue
		}
		displayName, description := modelName(entry.short, entry.title, entry.subtitle)
		models = append(models, Model{
			ID:             modelID(displayName),
			DisplayName:    displayName,
			Description:    description,
			Hash:           entry.variant,
			Capabilities:   []string{"generateContent", "streamGenerateContent"},
			Mode:           entry.mode,
			ThinkingLevels: entry.levels,
			Variant:        entry.variant,
		})
	}

	sort.SliceStable(models, func(left, right int) bool {
		return models[left].ID < models[right].ID
	})
	_, personalIntelligence := arrayAt(payload, 21)
	catalog := ModelCatalog{models: models, lookup: make(map[string]int), defaultIndex: -1, options: parseToolOptions(payload), personalIntelligence: personalIntelligence}
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
	defaultModel := models[catalog.defaultIndex]
	accountCapabilities, _ := arrayAt(payload, 17)
	for _, tool := range featureTools {
		if slices.ContainsFunc(tool.capabilities, func(capability int) bool { return !slices.Contains(accountCapabilities, any(float64(capability))) }) {
			continue
		}
		entry := defaultModel
		entry.ID, entry.DisplayName, entry.Description, entry.Default, entry.Feature = tool.id, tool.displayName, tool.description, false, tool.feature
		if tool.feature == FeatureVideo {
			entry.Capabilities = append(slices.Clone(entry.Capabilities), "predictLongRunning")
		}
		catalog.add(entry)
	}
	taskPolicy := parsePolicy(payload, 2)
	for _, entry := range models {
		levels, ok := policyLevels(taskPolicy, entry.Mode, entry.Variant)
		if !ok {
			continue
		}
		entry.ID, entry.DisplayName, entry.Default, entry.Feature, entry.ThinkingLevels = entry.ID+"-task", entry.DisplayName+" Task", false, FeatureTask, levels
		catalog.add(entry)
	}
	return catalog, nil
}

// removeTools 去掉目录末尾的官网工具与 Task 条目，用于官网工具菜单全部禁用的访客会话
func (c *ModelCatalog) removeTools() {
	for index := len(c.models) - 1; index >= 0 && c.models[index].Feature != 0; index-- {
		delete(c.lookup, strings.ToLower(c.models[index].ID))
		c.models = c.models[:index]
	}
}

// add 追加只按公开 ID 查找的官网工具条目
func (c *ModelCatalog) add(model Model) {
	model.Capabilities = append([]string(nil), model.Capabilities...)
	c.models = append(c.models, model)
	c.lookup[strings.ToLower(model.ID)] = len(c.models) - 1
}

// policyEntry 是 payload[24] 模型策略在一个界面下的一项：mode、同一 mode 下区分模型的变体键、菜单名称与可选的思考强度
type policyEntry struct {
	mode    int
	variant string
	// upgrade 表示该项是升级入口，账号层级不含该模型，官网点击后弹出升级对话框
	upgrade                bool
	short, title, subtitle string
	levels                 []ThinkingLevel
}

// parsePolicy 读取 payload[24] 的模型策略在指定界面（1 为 Chat，2 为 Task）下的各项
func parsePolicy(payload []any, surface int) []policyEntry {
	var entries []policyEntry
	policy, _ := arrayAt(payload, 24)
	rawEntries, _ := arrayAt(policy, 1)
	for _, rawEntry := range rawEntries {
		entry, _ := rawEntry.([]any)
		header, _ := arrayAt(entry, 0)
		mode, ok := intAt(header, 1)
		surfaces, _ := arrayAt(entry, 3)
		if value, _ := intAt(surfaces, 0); !ok || value != surface {
			continue
		}
		item := policyEntry{mode: mode}
		item.variant, _ = stringAt(header, 3)
		item.short, _ = stringAt(header, 4)
		item.subtitle, _ = stringAt(header, 5)
		item.title, _ = stringAt(header, 12)
		_, item.upgrade = arrayAt(header, 14)
		options, _ := arrayAt(entry, 1)
		for _, rawOption := range options {
			option, _ := rawOption.([]any)
			if level, ok := intAt(option, 0); ok {
				item.levels = append(item.levels, ThinkingLevel(level))
			}
		}
		entries = append(entries, item)
	}
	return entries
}

// policyLevels 返回策略中指定 mode 与变体键的一项的思考强度，没有该项时 ok 为 false
func policyLevels(entries []policyEntry, mode int, variant string) (levels []ThinkingLevel, ok bool) {
	for _, entry := range entries {
		if entry.mode == mode && entry.variant == variant {
			return entry.levels, true
		}
	}
	return nil, false
}

// thinkingRank 按 Low、Medium、High 的强弱顺序返回思考强度序号
func thinkingRank(level ThinkingLevel) int {
	switch level {
	case ThinkingMedium:
		return 1
	case ThinkingHigh:
		return 2
	default:
		return 0
	}
}

// thinkingLevel 返回模型可选思考强度中不高于请求强度的最强一档，都高于请求时取最弱一档
func (m Model) thinkingLevel(requested ThinkingLevel) ThinkingLevel {
	if len(m.ThinkingLevels) == 0 || slices.Contains(m.ThinkingLevels, requested) {
		return requested
	}
	selected, found := m.ThinkingLevels[0], false
	for _, level := range m.ThinkingLevels {
		if thinkingRank(level) <= thinkingRank(requested) && (!found || thinkingRank(level) > thinkingRank(selected)) {
			selected, found = level, true
		}
		if !found && thinkingRank(level) < thinkingRank(selected) {
			selected = level
		}
	}
	return selected
}

// clientFeatures 是登录页面在客户端扩展头第 8 槽声明的功能编号
var clientFeatures = []int{4, 33, 22, 5, 6, 8, 16, 19, 20}

// guestClientFeatures 是访客页面在客户端扩展头第 8 槽声明的功能编号
var guestClientFeatures = []int{4, 33, 22, 6, 16, 19, 20}

// fallbackTimezone 是读不到本机时区名时写入客户端扩展头的时区
const fallbackTimezone = "UTC"

// hostTimezone 是客户端扩展头计时槽末尾的本机 IANA 时区名
var hostTimezone = localTimezone()

// buildModelHeader 构造包含模型、界面语言、会话类型、功能编号、账号层级编码、思考强度、构建标签与请求计时的生成请求头，功能编号与官网一样写两遍，chatMode 为 Temporary Chat 1、普通会话 0，确认 Deep Research 计划时为空
func buildModelHeader(features []int, model Model, language string, chatMode any, tier int, thinkingLevel ThinkingLevel, clientID string, buildLabel string, timing []any) (string, error) {
	var levelSlot any
	if thinkingLevel != 0 {
		levelSlot = int(thinkingLevel)
	}
	header, err := json.Marshal(withVariant([]any{
		1, nil, nil, nil, model.Hash, nil, language, chatMode,
		slices.Concat(features, features), nil, nil, tier, nil, nil, model.Mode, levelSlot, clientID,
		[]string{buildLabel}, nil, timing,
	}, model.Variant))
	if err != nil {
		return "", err
	}
	return string(header), nil
}

// withVariant 返回在第 24 槽写了变体键的客户端头，variant 为空时原样返回
func withVariant(header []any, variant string) []any {
	if variant == "" {
		return header
	}
	return append(header, nil, nil, nil, nil, variant)
}

// buildGenericHeader 构造 batchexecute 请求使用的客户端头，mode 与 level 为零时对应槽为空，variant 非空时写在第 24 槽
func buildGenericHeader(features []int, language string, clientID string, buildLabel string, mode int, variant string, level ThinkingLevel, timing []any) string {
	var modeSlot, levelSlot any
	if mode != 0 {
		modeSlot = mode
	}
	if level != 0 {
		levelSlot = int(level)
	}
	header, _ := json.Marshal(withVariant([]any{1, nil, nil, nil, nil, nil, language, nil, features, nil, nil, nil, nil, nil, modeSlot, levelSlot, clientID, []string{buildLabel}, nil, timing}, variant))
	return string(header)
}

// buildConversationHeader 构造会话内 batchexecute 请求的客户端头，第 7 槽为 Temporary Chat 状态，不写模型 hash 与层级
func buildConversationHeader(features []int, language string, clientID string, buildLabel string, temporaryChat bool, mode int, variant string, level ThinkingLevel, timing []any) string {
	chatMode := 0
	if temporaryChat {
		chatMode = 1
	}
	header, _ := json.Marshal(withVariant([]any{
		1, nil, nil, nil, nil, nil, language, chatMode, slices.Concat(features, features),
		nil, nil, nil, nil, nil, mode, int(level), clientID, []string{buildLabel}, nil, timing,
	}, variant))
	return string(header)
}

// requestTiming 返回网页客户端头末尾的耗时、发送时间与本机时区，elapsed 为毫秒，发送时间取 now 的整毫秒
func requestTiming(elapsed float64, now time.Time) []any {
	return []any{pageDuration(elapsed), pageDuration(float64(now.UnixMilli())), hostTimezone}
}

// pageDuration 按官网页面脚本把毫秒数拆为 [秒,纳秒]，值为 0 的项写 null，末尾的 null 省略
func pageDuration(milliseconds float64) []any {
	seconds := math.Trunc(milliseconds / 1000)
	value := []any{int64(seconds), int64(math.Trunc((milliseconds - seconds*1000) * 1e6))}
	for index, item := range value {
		if item == int64(0) {
			value[index] = nil
		}
	}
	for len(value) > 0 && value[len(value)-1] == nil {
		value = value[:len(value)-1]
	}
	return value
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
