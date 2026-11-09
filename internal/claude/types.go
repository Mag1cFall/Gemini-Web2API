package claude

import (
	"encoding/json"
	"strings"
)

type ClaudeRequest struct {
	Model              string          `json:"model"`
	Messages           []Message       `json:"messages"`
	System             json.RawMessage `json:"system,omitempty"`
	Tools              []Tool          `json:"tools,omitempty"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty"`
	Stream             bool            `json:"stream"`
	MaxTokens          *int            `json:"max_tokens,omitempty"`
	StopSequences      []string        `json:"stop_sequences,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	TopK               *int            `json:"top_k,omitempty"`
	Thinking           *ThinkingConfig `json:"thinking,omitempty"`
	OutputConfig       *OutputConfig   `json:"output_config,omitempty"`
	OutputFormat       *OutputFormat   `json:"output_format,omitempty"`
	Metadata           *Metadata       `json:"metadata,omitempty"`
	ConversationID     string          `json:"conversation_id,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
}

type ThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
	Display      string `json:"display,omitempty"`
}

// OutputConfig 表示 Claude 输出强度与结构化输出配置
type OutputConfig struct {
	Effort string        `json:"effort,omitempty"`
	Format *OutputFormat `json:"format,omitempty"`
}

// OutputFormat 表示 output_config.format 与 beta output_format 的结构化输出格式
type OutputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

type Metadata struct {
	UserID string `json:"user_id,omitempty"`
}

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type ContentBlock struct {
	Type      string                  `json:"type"`
	Text      string                  `json:"text,omitempty"`
	Thinking  string                  `json:"thinking,omitempty"`
	Signature string                  `json:"signature,omitempty"`
	ID        string                  `json:"id,omitempty"`
	Name      string                  `json:"name,omitempty"`
	Input     *map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                  `json:"tool_use_id,omitempty"`
	Content   json.RawMessage         `json:"content,omitempty"`
	IsError   *bool                   `json:"is_error,omitempty"`
	Source    *ImageSource            `json:"source,omitempty"`
	Data      string                  `json:"data,omitempty"`
	Citations []map[string]any        `json:"citations,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type Tool struct {
	Type        *string         `json:"type,omitempty"`
	Name        *string         `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	// DisplayWidthPx、DisplayHeightPx 与 EnableZoom 是 computer 工具的屏幕尺寸与 zoom 动作开关
	DisplayWidthPx  int  `json:"display_width_px,omitempty"`
	DisplayHeightPx int  `json:"display_height_px,omitempty"`
	EnableZoom      bool `json:"enable_zoom,omitempty"`
}

func (t Tool) IsWebSearch() bool {
	if t.Type != nil && strings.HasPrefix(*t.Type, "web_search") {
		return true
	}
	if t.Name != nil && (*t.Name == "web_search" || *t.Name == "google_search") && len(t.InputSchema) == 0 {
		return true
	}
	return false
}

type ClaudeResponse struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Role           string         `json:"role"`
	Model          string         `json:"model"`
	Content        []ContentBlock `json:"content"`
	StopReason     string         `json:"stop_reason"`
	StopSequence   *string        `json:"stop_sequence"`
	Usage          *Usage         `json:"usage,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
}

type Usage struct {
	InputTokens              int            `json:"input_tokens"`
	OutputTokens             int            `json:"output_tokens"`
	CacheReadInputTokens     *int           `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *int           `json:"cache_creation_input_tokens,omitempty"`
	ServerToolUse            *ServerToolUse `json:"server_tool_use,omitempty"`
}

// ServerToolUse 统计服务端工具的调用次数
type ServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests"`
}

func ParseMessageContent(raw json.RawMessage) ([]ContentBlock, string, error) {
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return nil, str, nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, "", err
	}
	return blocks, "", nil
}

func ParseSystemPrompt(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str, nil
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", err
	}

	var result string
	for _, b := range blocks {
		if b.Type == "text" {
			result += b.Text + "\n"
		}
	}
	return result, nil
}
