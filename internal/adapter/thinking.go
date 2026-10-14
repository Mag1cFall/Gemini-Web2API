package adapter

import (
	"encoding/json"
	"fmt"
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
	switch strings.ToLower(strings.TrimSpace(options.Summary)) {
	case "":
	case "none":
		includeThoughts = false
	case "auto", "concise", "detailed":
	default:
		return gemini.ThinkingLow, false, fmt.Errorf("不支持的 reasoning.summary %q", options.Summary)
	}
	return mode, includeThoughts, nil
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
	if config.ThinkingConfig.ThinkingBudget != nil {
		if *config.ThinkingConfig.ThinkingBudget == 0 {
			return gemini.ThinkingLow, false, nil
		}
		if *config.ThinkingConfig.ThinkingBudget > 0 {
			return gemini.ThinkingHigh, config.ThinkingConfig.IncludeThoughts, nil
		}
	}
	return gemini.ThinkingLow, config.ThinkingConfig.IncludeThoughts, nil
}
