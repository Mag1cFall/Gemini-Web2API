package adapter

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/claude"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// reasoningOptions 表示 OpenAI reasoning 配置
type reasoningOptions struct {
	Effort  string `json:"effort"`
	Summary string `json:"summary"`
}

// geminiThinkingConfig 表示 Gemini 原生思考配置
type geminiThinkingConfig struct {
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
}

// reasoningEffortLevel 将外部思考强度映射到网页模型菜单的三档思考强度
func reasoningEffortLevel(effort string) (gemini.ThinkingLevel, error) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none", "minimal", "low", "auto", "thinking_level_unspecified":
		return gemini.ThinkingLow, nil
	case "medium":
		return gemini.ThinkingMedium, nil
	case "high", "xhigh", "max":
		return gemini.ThinkingHigh, nil
	default:
		return gemini.ThinkingLow, fmt.Errorf("不支持的思考强度 %q", effort)
	}
}

// reasoningRawOptions 读取 OpenAI reasoning 配置
func reasoningRawOptions(raw json.RawMessage) (reasoningOptions, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return reasoningOptions{}, nil
	}
	var options reasoningOptions
	if err := json.Unmarshal(raw, &options); err == nil {
		return options, nil
	}
	var effort string
	if err := json.Unmarshal(raw, &effort); err != nil {
		return reasoningOptions{}, fmt.Errorf("思考配置格式无效")
	}
	return reasoningOptions{Effort: effort}, nil
}

// openAIChatThinkingSettings 读取 Chat Completions 思考强度和摘要策略
func openAIChatThinkingSettings(request ChatRequest) (gemini.ThinkingLevel, bool, error) {
	options, err := reasoningRawOptions(request.Reasoning)
	if err != nil {
		return gemini.ThinkingLow, false, err
	}
	effort := request.ReasoningEffort
	if effort == "" {
		effort = options.Effort
	}
	mode, err := reasoningEffortLevel(effort)
	if err != nil {
		return gemini.ThinkingLow, false, err
	}
	includeThoughts := !strings.EqualFold(strings.TrimSpace(effort), "none")
	specified, requested := effort != "", false
	if request.IncludeReasoning != nil {
		includeThoughts, requested = *request.IncludeReasoning, *request.IncludeReasoning
	}
	switch strings.ToLower(strings.TrimSpace(options.Summary)) {
	case "":
	case "none":
		includeThoughts = false
	case "auto", "concise", "detailed":
	default:
		return gemini.ThinkingLow, false, fmt.Errorf("不支持的 reasoning.summary %q", options.Summary)
	}
	if thinking := request.ExtraBody.Google.ThinkingConfig; thinking != nil {
		if thinking.ThinkingLevel != "" && thinking.ThinkingBudget != nil {
			return gemini.ThinkingLow, false, fmt.Errorf("thinking_level 与 thinking_budget 不能同时使用")
		}
		if thinking.ThinkingLevel != "" {
			if mode, err = reasoningEffortLevel(thinking.ThinkingLevel); err != nil {
				return gemini.ThinkingLow, false, err
			}
			specified = true
		}
		if thinking.IncludeThoughts != nil {
			includeThoughts, requested = *thinking.IncludeThoughts, *thinking.IncludeThoughts
		}
		if budget := thinking.ThinkingBudget; budget != nil {
			mode, includeThoughts, specified = thinkingBudgetLevel(*budget), includeThoughts && *budget != 0, true
		}
	}
	if !specified {
		mode = unspecifiedThinkingLevel(requested)
	}
	return mode, includeThoughts, nil
}

// thinkingBudgetLevel 把思考预算映射到网页思考强度：不超过 1024 为 Low，不超过 8192 为 Medium，更高为 High，0 与 -1 为 Low
func thinkingBudgetLevel(budget int) gemini.ThinkingLevel {
	switch {
	case budget <= 1024:
		return gemini.ThinkingLow
	case budget <= 8192:
		return gemini.ThinkingMedium
	default:
		return gemini.ThinkingHigh
	}
}

// responsesThinkingSettings 读取 Responses API 思考强度和摘要策略
func responsesThinkingSettings(request ResponsesRequest) (gemini.ThinkingLevel, bool, error) {
	options, err := reasoningRawOptions(request.Reasoning)
	if err != nil {
		return gemini.ThinkingLow, false, err
	}
	mode, err := reasoningEffortLevel(options.Effort)
	if err != nil {
		return gemini.ThinkingLow, false, err
	}
	switch strings.ToLower(strings.TrimSpace(options.Summary)) {
	case "", "none":
		return mode, false, nil
	case "auto", "concise", "detailed":
		if options.Effort == "" {
			mode = unspecifiedThinkingLevel(true)
		}
		return mode, true, nil
	default:
		return gemini.ThinkingLow, false, fmt.Errorf("不支持的 reasoning.summary %q", options.Summary)
	}
}

// claudeThinkingSettings 读取 Claude Messages 思考强度和摘要策略
func claudeThinkingSettings(request claude.ClaudeRequest) (gemini.ThinkingLevel, bool, error) {
	if request.Thinking == nil {
		if request.OutputConfig == nil {
			return gemini.ThinkingLow, false, nil
		}
		mode, err := reasoningEffortLevel(request.OutputConfig.Effort)
		return mode, false, err
	}
	switch strings.ToLower(strings.TrimSpace(request.Thinking.Type)) {
	case "disabled":
		return gemini.ThinkingLow, false, nil
	case "enabled", "adaptive":
		if request.OutputConfig == nil || request.OutputConfig.Effort == "" {
			if request.Thinking.BudgetTokens != nil {
				return thinkingBudgetLevel(*request.Thinking.BudgetTokens), true, nil
			}
			return gemini.ThinkingHigh, true, nil
		}
		mode, err := reasoningEffortLevel(request.OutputConfig.Effort)
		return mode, err == nil, err
	default:
		return gemini.ThinkingLow, false, fmt.Errorf("不支持的 thinking.type %q", request.Thinking.Type)
	}
}

// geminiRequestThinkingSettings 读取 Gemini generationConfig 思考强度和摘要策略
func geminiRequestThinkingSettings(request GeminiGenerateContentRequest) (gemini.ThinkingLevel, bool, error) {
	if len(request.GenerationConfig) == 0 || string(request.GenerationConfig) == "null" {
		return gemini.ThinkingLow, false, nil
	}
	var config struct {
		ThinkingConfig *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
	}
	if err := json.Unmarshal(request.GenerationConfig, &config); err != nil {
		return gemini.ThinkingLow, false, err
	}
	if config.ThinkingConfig == nil {
		return gemini.ThinkingLow, false, nil
	}
	if config.ThinkingConfig.ThinkingLevel != "" && config.ThinkingConfig.ThinkingBudget != nil {
		return gemini.ThinkingLow, false, fmt.Errorf("thinkingLevel 与 thinkingBudget 不能同时使用")
	}
	if config.ThinkingConfig.ThinkingLevel != "" {
		mode, err := reasoningEffortLevel(config.ThinkingConfig.ThinkingLevel)
		return mode, config.ThinkingConfig.IncludeThoughts && err == nil, err
	}
	if budget := config.ThinkingConfig.ThinkingBudget; budget != nil {
		return thinkingBudgetLevel(*budget), config.ThinkingConfig.IncludeThoughts && *budget != 0, nil
	}
	return unspecifiedThinkingLevel(config.ThinkingConfig.IncludeThoughts), config.ThinkingConfig.IncludeThoughts, nil
}

// unspecifiedThinkingLevel 返回请求未指定思考强度时的网页强度，要求输出思考摘要时为 High，否则为官网默认的 Low
func unspecifiedThinkingLevel(includeThoughts bool) gemini.ThinkingLevel {
	if includeThoughts {
		return gemini.ThinkingHigh
	}
	return gemini.ThinkingLow
}

// modelSuffix 是模型名末尾 (值) 给出的思考设置，level 为 0 表示没有后缀
type modelSuffix struct {
	level      gemini.ThinkingLevel
	noThoughts bool
}

// takeModelSuffix 从模型名中去掉末尾的思考后缀并返回其思考设置
func takeModelSuffix(model *string) (modelSuffix, error) {
	open := strings.LastIndex(*model, "(")
	if open < 0 || !strings.HasSuffix(*model, ")") {
		return modelSuffix{}, nil
	}
	value := strings.ToLower(strings.TrimSpace((*model)[open+1 : len(*model)-1]))
	*model = strings.TrimSpace((*model)[:open])
	if budget, err := strconv.Atoi(value); err == nil && budget >= -1 {
		return modelSuffix{level: thinkingBudgetLevel(budget), noThoughts: budget == 0}, nil
	}
	level, err := reasoningEffortLevel(value)
	if err != nil || value == "" || value == "thinking_level_unspecified" {
		return modelSuffix{}, fmt.Errorf("模型名后缀 (%s) 必须是 none、auto、minimal、low、medium、high、xhigh、max、-1 或非负整数思考预算", value)
	}
	return modelSuffix{level: level, noThoughts: value == "none"}, nil
}

// apply 用后缀覆盖请求体给出的思考强度与思考摘要设置
func (s modelSuffix) apply(level gemini.ThinkingLevel, includeThoughts bool) (gemini.ThinkingLevel, bool) {
	if s.level == 0 {
		return level, includeThoughts
	}
	return s.level, includeThoughts && !s.noThoughts
}
