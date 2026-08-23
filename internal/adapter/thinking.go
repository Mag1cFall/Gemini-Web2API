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
	Effort string `json:"effort"`
}

// geminiThinkingConfig 表示 Gemini 原生思考配置
type geminiThinkingConfig struct {
	ThinkingBudget *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel  string `json:"thinkingLevel,omitempty"`
}

// reasoningEffortMode 将外部思考强度映射到网页策略
func reasoningEffortMode(effort string) (gemini.ThinkingMode, error) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none", "minimal", "low", "auto", "thinking_level_unspecified":
		return gemini.ThinkingStandard, nil
	case "medium", "high", "xhigh", "max":
		return gemini.ThinkingExtended, nil
	default:
		return gemini.ThinkingStandard, fmt.Errorf("不支持的思考强度 %q", effort)
	}
}

// reasoningRawEffort 读取 OpenAI reasoning 配置
func reasoningRawEffort(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var options reasoningOptions
	if err := json.Unmarshal(raw, &options); err == nil {
		return options.Effort, nil
	}
	var effort string
	if err := json.Unmarshal(raw, &effort); err != nil {
		return "", fmt.Errorf("思考配置格式无效")
	}
	return effort, nil
}

// openAIChatThinkingMode 读取 Chat Completions 思考配置
func openAIChatThinkingMode(request ChatRequest) (gemini.ThinkingMode, error) {
	effort := request.ReasoningEffort
	if effort == "" {
		var err error
		effort, err = reasoningRawEffort(request.Reasoning)
		if err != nil {
			return gemini.ThinkingStandard, err
		}
	}
	return reasoningEffortMode(effort)
}

// responsesThinkingMode 读取 Responses API 思考配置
func responsesThinkingMode(request ResponsesRequest) (gemini.ThinkingMode, error) {
	effort, err := reasoningRawEffort(request.Reasoning)
	if err != nil {
		return gemini.ThinkingStandard, err
	}
	return reasoningEffortMode(effort)
}

// claudeThinkingMode 读取 Claude Messages 思考配置
func claudeThinkingMode(request claude.ClaudeRequest) (gemini.ThinkingMode, error) {
	if request.Thinking == nil {
		if request.OutputConfig == nil {
			return gemini.ThinkingStandard, nil
		}
		return reasoningEffortMode(request.OutputConfig.Effort)
	}
	switch strings.ToLower(strings.TrimSpace(request.Thinking.Type)) {
	case "disabled":
		return gemini.ThinkingStandard, nil
	case "enabled", "adaptive":
		if request.OutputConfig == nil || request.OutputConfig.Effort == "" {
			return gemini.ThinkingExtended, nil
		}
		return reasoningEffortMode(request.OutputConfig.Effort)
	default:
		return gemini.ThinkingStandard, fmt.Errorf("不支持的 thinking.type %q", request.Thinking.Type)
	}
}

// geminiRequestThinkingMode 读取 Gemini generationConfig 思考配置
func geminiRequestThinkingMode(request GeminiGenerateContentRequest) (gemini.ThinkingMode, error) {
	if len(request.GenerationConfig) == 0 || string(request.GenerationConfig) == "null" {
		return gemini.ThinkingStandard, nil
	}
	var config struct {
		ThinkingConfig *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
	}
	if err := json.Unmarshal(request.GenerationConfig, &config); err != nil {
		return gemini.ThinkingStandard, err
	}
	if config.ThinkingConfig == nil {
		return gemini.ThinkingStandard, nil
	}
	if config.ThinkingConfig.ThinkingLevel != "" && config.ThinkingConfig.ThinkingBudget != nil {
		return gemini.ThinkingStandard, fmt.Errorf("thinkingLevel 与 thinkingBudget 不能同时使用")
	}
	if config.ThinkingConfig.ThinkingLevel != "" {
		return reasoningEffortMode(config.ThinkingConfig.ThinkingLevel)
	}
	if config.ThinkingConfig.ThinkingBudget != nil && *config.ThinkingConfig.ThinkingBudget > 0 {
		return gemini.ThinkingExtended, nil
	}
	return gemini.ThinkingStandard, nil
}
