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
	ConversationID     string          `json:"conversation_id,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	// ContextManagement 为本服务在本地执行的上下文编辑策略
	ContextManagement *ContextManagement `json:"context_management,omitempty"`
}

// ContextManagement 是 context_management 的上下文编辑策略
type ContextManagement struct {
	Edits []ContextEdit `json:"edits"`
}

// ContextEdit 是一条上下文编辑策略，字段按 type 取用
type ContextEdit struct {
	Type                 string          `json:"type"`
	Trigger              *EditAmount     `json:"trigger"`
	Keep                 json.RawMessage `json:"keep"`
	ClearAtLeast         *EditAmount     `json:"clear_at_least"`
	ExcludeTools         []string        `json:"exclude_tools"`
	ClearToolInputs      json.RawMessage `json:"clear_tool_inputs"`
	PauseAfterCompaction bool            `json:"pause_after_compaction"`
	Instructions         string          `json:"instructions"`
}

// EditAmount 是触发条件、保留数量与最少清理量的类型与数值
type EditAmount struct {
	Type  string `json:"type"`
	Value int    `json:"value"`
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
	Citations []map[string]any        `json:"citations,omitempty"`
	// EncryptedContent 是 compaction 块的加密内容，本服务写为 null
	EncryptedContent json.RawMessage `json:"encrypted_content,omitempty"`
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
	// MaxUses 是服务端工具在一次请求中的调用次数上限
	MaxUses int `json:"max_uses,omitempty"`
	// AllowedDomains 与 BlockedDomains 是 web_search 与 web_fetch 工具允许与屏蔽的域名
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
	// UserLocation 是 web_search 工具按其搜索的用户位置
	UserLocation json.RawMessage `json:"user_location,omitempty"`
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
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        *Usage         `json:"usage,omitempty"`
	// ContextManagement 是请求带 context_management 时生效的编辑
	ContextManagement map[string]any `json:"context_management,omitempty"`
	ConversationID    string         `json:"conversation_id,omitempty"`
	// ProviderModel 是上游实际回答的模型 ID
	ProviderModel string `json:"provider_model,omitempty"`
}

type Usage struct {
	InputTokens              int            `json:"input_tokens"`
	CacheCreationInputTokens *int           `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     *int           `json:"cache_read_input_tokens,omitempty"`
	OutputTokens             int            `json:"output_tokens"`
	ServerToolUse            *ServerToolUse `json:"server_tool_use,omitempty"`
	// Iterations 是请求带 compact_20260112 时压缩与回答各次采样的用量
	Iterations []map[string]any `json:"iterations,omitempty"`
}

// ServerToolUse 统计服务端工具的调用次数
type ServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests"`
	WebFetchRequests  int `json:"web_fetch_requests"`
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
