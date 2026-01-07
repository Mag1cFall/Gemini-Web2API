package claude

import (
	"encoding/json"
)

type ClaudeRequest struct {
	Model       string          `json:"model"`
	Messages    []Message       `json:"messages"`
	System      json.RawMessage `json:"system,omitempty"`
	Tools       []Tool          `json:"tools,omitempty"`
	Stream      bool            `json:"stream"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	TopK        *int            `json:"top_k,omitempty"`
	Thinking    *ThinkingConfig `json:"thinking,omitempty"`
	Metadata    *Metadata       `json:"metadata,omitempty"`
}

type ThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
}

type Metadata struct {
	UserID string `json:"user_id,omitempty"`
}

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type ContentBlock struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text,omitempty"`
	Thinking  string                 `json:"thinking,omitempty"`
	Signature string                 `json:"signature,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   json.RawMessage        `json:"content,omitempty"`
	IsError   *bool                  `json:"is_error,omitempty"`
	Source    *ImageSource           `json:"source,omitempty"`
	Data      string                 `json:"data,omitempty"`
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
}

func (t Tool) IsWebSearch() bool {
	if t.Type != nil && (*t.Type == "web_search" || *t.Type == "web_search_20250305") {
		return true
	}
	if t.Name != nil && (*t.Name == "web_search" || *t.Name == "google_search") {
		return true
	}
	return false
}

type ClaudeResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence,omitempty"`
	Usage        Usage          `json:"usage"`
}

type Usage struct {
	InputTokens              int  `json:"input_tokens"`
	OutputTokens             int  `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text             *string           `json:"text,omitempty"`
	Thought          *bool             `json:"thought,omitempty"`
	ThoughtSignature *string           `json:"thoughtSignature,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *InlineData       `json:"inlineData,omitempty"`
}

type FunctionCall struct {
	Name string                 `json:"name"`
	ID   *string                `json:"id,omitempty"`
	Args map[string]interface{} `json:"args,omitempty"`
}

type FunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
	ID       *string                `json:"id,omitempty"`
}

type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GeminiResponse struct {
	Candidates    []Candidate    `json:"candidates,omitempty"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  *string        `json:"modelVersion,omitempty"`
	ResponseID    *string        `json:"responseId,omitempty"`
}

type Candidate struct {
	Content           *GeminiContent     `json:"content,omitempty"`
	FinishReason      *string            `json:"finishReason,omitempty"`
	Index             *int               `json:"index,omitempty"`
	GroundingMetadata *GroundingMetadata `json:"groundingMetadata,omitempty"`
}

type UsageMetadata struct {
	PromptTokenCount        *int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    *int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         *int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount *int `json:"cachedContentTokenCount,omitempty"`
}

type GroundingMetadata struct {
	WebSearchQueries  []string           `json:"webSearchQueries,omitempty"`
	GroundingChunks   []GroundingChunk   `json:"groundingChunks,omitempty"`
	GroundingSupports []GroundingSupport `json:"groundingSupports,omitempty"`
}

type GroundingChunk struct {
	Web *WebSource `json:"web,omitempty"`
}

type WebSource struct {
	URI   *string `json:"uri,omitempty"`
	Title *string `json:"title,omitempty"`
}

type GroundingSupport struct {
	Segment               *TextSegment `json:"segment,omitempty"`
	GroundingChunkIndices []int        `json:"groundingChunkIndices,omitempty"`
	ConfidenceScores      []float64    `json:"confidenceScores,omitempty"`
}

type TextSegment struct {
	StartIndex *int    `json:"startIndex,omitempty"`
	EndIndex   *int    `json:"endIndex,omitempty"`
	Text       *string `json:"text,omitempty"`
}

type GeminiRequest struct {
	Project     string                 `json:"project"`
	RequestID   string                 `json:"requestId"`
	Model       string                 `json:"model"`
	UserAgent   string                 `json:"userAgent"`
	RequestType string                 `json:"requestType"`
	Request     map[string]interface{} `json:"request"`
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
