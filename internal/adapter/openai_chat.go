package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
)

// ChatMessage 表示 OpenAI Chat 消息
type ChatMessage struct {
	Role                string            `json:"role"`
	Content             json.RawMessage   `json:"content"`
	Name                string            `json:"name,omitempty"`
	ToolCallID          string            `json:"tool_call_id,omitempty"`
	ToolCalls           []OpenAIToolCall  `json:"tool_calls,omitempty"`
	ReasoningContent    json.RawMessage   `json:"reasoning_content,omitempty"`
	TranscriptReasoning json.RawMessage   `json:"-"`
	TranscriptItems     []json.RawMessage `json:"-"`
}

// OpenAIToolCall 表示 OpenAI 函数调用
type OpenAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// OpenAITool 表示 OpenAI 函数定义
type OpenAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// ChatRequest 表示 OpenAI Chat 请求
type ChatRequest struct {
	Messages            []ChatMessage      `json:"messages"`
	Stream              bool               `json:"stream"`
	StreamOptions       *ChatStreamOptions `json:"stream_options,omitempty"`
	Model               string             `json:"model"`
	Tools               []OpenAITool       `json:"tools,omitempty"`
	ToolChoice          json.RawMessage    `json:"tool_choice,omitempty"`
	ResponseFormat      json.RawMessage    `json:"response_format,omitempty"`
	ReasoningEffort     string             `json:"reasoning_effort,omitempty"`
	Reasoning           json.RawMessage    `json:"reasoning,omitempty"`
	ConversationID      string             `json:"conversation_id,omitempty"`
	PreviousResponseID  string             `json:"previous_response_id,omitempty"`
	MaxTokens           *int               `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int               `json:"max_completion_tokens,omitempty"`
	Temperature         *float64           `json:"temperature,omitempty"`
	TopP                *float64           `json:"top_p,omitempty"`
	Stop                json.RawMessage    `json:"stop,omitempty"`
	PresencePenalty     *float64           `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64           `json:"frequency_penalty,omitempty"`
	Seed                *int               `json:"seed,omitempty"`
	N                   *int               `json:"n,omitempty"`
}

// ChatStreamOptions 表示 OpenAI Chat 流选项
type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatCompletionHandler 处理 OpenAI Chat Completions
func ChatCompletionHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request ChatRequest
		err := decodeRequest(c, &request)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.Model == "" || len(request.Messages) == 0 {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "model and messages are required")
			return
		}
		if request.N != nil && *request.N != 1 {
			writeOpenAIError(c, http.StatusBadRequest, "unsupported_parameter", "Only n=1 is supported")
			return
		}
		knownModel, readyModel := modelStatus(pool, request.Model)
		if !knownModel {
			writeOpenAIError(c, http.StatusNotFound, "model_not_found", fmt.Sprintf("model %q is unavailable", request.Model))
			return
		}
		if !readyModel {
			writeOpenAIError(c, http.StatusServiceUnavailable, "upstream_error", "No account is ready for the requested model")
			return
		}
		thinkingMode, includeThoughts, err := openAIChatThinkingSettings(request)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}

		unsupported := unsupportedChatParameters(request)
		setUnsupportedParameters(c, unsupported)
		bridge, err := openAIToolBridge(request)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}

		sessionKey := conversationKey(request.PreviousResponseID, request.ConversationID, c.GetHeader("X-Conversation-ID"))
		allowNewSession := request.PreviousResponseID == ""
		responseID := fmt.Sprintf("chatcmpl_%d", time.Now().UnixNano())
		created := time.Now().Unix()

		if request.Stream {
			setSSEHeaders(c)
			streamModel := config.MapModel(request.Model)
			roleSent := false
			writeRole := func() error {
				if roleSent {
					return nil
				}
				roleSent = true
				return writeOpenAIRole(c.Writer, responseID, created, streamModel)
			}
			if err := writeRole(); err != nil {
				return
			}
			projection := newStreamProjection(bridge, includeThoughts)
			result, accountID, err := runGeneration(
				c.Request.Context(), pool, sessionKey, allowNewSession, request.Model, responseID, false, thinkingMode,
				func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
					return buildOpenAIChatPrompt(c, client, request, continuation, bridge)
				},
				func(event gemini.Event) error {
					return projection.project(event, func(event gemini.Event) error {
						if err := writeRole(); err != nil {
							return err
						}
						delta := gin.H{"content": event.Delta}
						if event.Kind == gemini.EventThought {
							delta = gin.H{"reasoning_content": event.Delta}
						}
						return writeOpenAIDelta(c.Writer, responseID, created, streamModel, delta)
					}, func(err error) error {
						return writeOpenAIStreamError(c.Writer, "upstream_rewrite", err)
					})
				},
				projection.hasVisibleOutput,
			)
			c.Set("account_id", accountID)
			if err != nil {
				if !projection.errorSent {
					_ = writeOpenAIStreamError(c.Writer, openAIUpstreamCode(err), err)
				}
				return
			}
			streamModel = result.Model
			if err := writeRole(); err != nil {
				return
			}
			if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
				_ = writeOpenAIStreamError(c.Writer, "media_download_error", err)
				return
			}
			if err := writeProjectedOpenAI(c.Writer, responseID, created, streamModel, result.Accumulator, bridge, projection); err != nil {
				_ = writeOpenAIStreamError(c.Writer, "output_validation_error", err)
				return
			}
			_ = writeOpenAIFinish(c.Writer, responseID, created, streamModel, result, bridge)
			if request.StreamOptions != nil && request.StreamOptions.IncludeUsage && result.Accumulator.Usage != nil {
				_ = writeOpenAIUsageChunk(c.Writer, responseID, created, streamModel, result.Accumulator.Usage)
			}
			writeSSEDone(c.Writer)
			return
		}

		result, accountID, err := runGeneration(
			c.Request.Context(), pool, sessionKey, allowNewSession, request.Model, responseID, false, thinkingMode,
			func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
				return buildOpenAIChatPrompt(c, client, request, continuation, bridge)
			},
			nil, nil,
		)
		c.Set("account_id", accountID)
		if err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}

		if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
			writeOpenAIError(c, http.StatusBadGateway, "media_download_error", err.Error())
			return
		}
		response, err := buildOpenAIResponse(responseID, created, result, bridge, includeThoughts)
		if err != nil {
			writeOpenAIError(c, http.StatusBadGateway, "output_validation_error", err.Error())
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

func buildOpenAIChatPrompt(c *gin.Context, client *gemini.Client, request ChatRequest, continuation bool, bridge ToolBridge) (string, []gemini.FileData, error) {
	messages := request.Messages
	if continuation {
		messages = continuationMessages(messages)
	}
	records := make([]map[string]interface{}, 0, len(messages))
	var files []gemini.FileData
	for _, message := range messages {
		content, attachments, err := decodeMessageContent(c.Request.Context(), message.Content, client)
		if err != nil {
			return "", nil, err
		}
		files = append(files, attachments...)
		role := transcriptRole(message.Role)
		record := map[string]interface{}{"role": role, "content": content}
		if len(message.ReasoningContent) > 0 && string(message.ReasoningContent) != "null" {
			record["reasoning_content"] = message.ReasoningContent
		}
		if len(message.TranscriptReasoning) > 0 {
			record["reasoning"] = message.TranscriptReasoning
		}
		if len(message.TranscriptItems) > 0 {
			record["events"] = message.TranscriptItems
		}
		if len(message.ToolCalls) > 0 {
			record["tool_calls"] = message.ToolCalls
		}
		if message.ToolCallID != "" {
			record["tool_call_id"] = message.ToolCallID
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		records = append(records, map[string]interface{}{"role": "user", "content": "Hello"})
	}
	transcript, err := json.Marshal(map[string]interface{}{"messages": records})
	if err != nil {
		return "", nil, err
	}
	suffix, err := bridge.PromptSuffix()
	if err != nil {
		return "", nil, err
	}
	return string(transcript) + suffix, files, nil
}

func buildOpenAIResponse(responseID string, created int64, result generationResult, bridge ToolBridge, includeThoughts bool) (gin.H, error) {
	primary := result.Accumulator.Primary()
	content, calls, err := bridge.Parse(primary.Text)
	if err != nil {
		return nil, err
	}
	calls = assignToolCallIDs(calls, responseID)
	content = renderCandidateMarkdown(content, primary, 0)
	message := gin.H{"role": "assistant", "content": content}
	finishReason := "stop"
	if includeThoughts && primary.Thought != "" {
		message["reasoning_content"] = primary.Thought
	}
	if annotations := openAICitationAnnotations(content, primary.Citations); len(annotations) > 0 {
		message["annotations"] = annotations
	}
	if len(calls) > 0 {
		message["content"] = nil
		message["tool_calls"] = openAIToolCallResponses(calls)
		finishReason = "tool_calls"
	}
	response := gin.H{
		"id": responseID, "object": "chat.completion", "created": created, "model": result.Model,
		"provider_model": result.ProviderModel, "conversation_id": result.ConversationID, "response_id": responseID,
		"choices": []gin.H{{"index": 0, "message": message, "finish_reason": finishReason}},
	}
	if result.Accumulator.Usage != nil {
		response["usage"] = openAIUsage(result.Accumulator.Usage)
	}
	return response, nil
}

func writeProjectedOpenAI(w io.Writer, id string, created int64, model string, accumulator *EventAccumulator, bridge ToolBridge, projection *streamProjection) error {
	primary := accumulator.Primary()
	if projection.includeThought && projection.bufferThought && primary.Thought != "" {
		if err := writeOpenAIDelta(w, id, created, model, gin.H{"reasoning_content": primary.Thought}); err != nil {
			return err
		}
	}
	if projection.bufferText {
		content, calls, err := bridge.Parse(primary.Text)
		if err != nil {
			return err
		}
		calls = assignToolCallIDs(calls, id)
		content = renderCandidateMarkdown(content, primary, projection.textRunes)
		if content != "" {
			if err := writeOpenAIDelta(w, id, created, model, gin.H{"content": content}); err != nil {
				return err
			}
		}
		for index, call := range calls {
			delta := gin.H{"tool_calls": []gin.H{{
				"index": index, "id": call.ID, "type": "function",
				"function": gin.H{"name": call.Name, "arguments": string(call.Arguments)},
			}}}
			if err := writeOpenAIDelta(w, id, created, model, delta); err != nil {
				return err
			}
		}
	}
	fullContent, _, _ := bridge.Parse(primary.Text)
	fullContent = renderCandidateMarkdown(fullContent, primary, 0)
	if annotations := openAICitationAnnotations(fullContent, primary.Citations); len(annotations) > 0 {
		if err := writeOpenAIDelta(w, id, created, model, gin.H{"annotations": annotations}); err != nil {
			return err
		}
	}
	return nil
}

func openAICitationAnnotations(text string, citations []gemini.Citation) []gin.H {
	annotations := make([]gin.H, 0, len(citations))
	for _, citation := range citations {
		start, end := citationRange(text, citation)
		annotations = append(annotations, gin.H{
			"type":         "url_citation",
			"url_citation": gin.H{"start_index": start, "end_index": end, "title": citation.Title, "url": citation.URL},
		})
	}
	return annotations
}

func citationRange(text string, citation gemini.Citation) (int, int) {
	length := len([]rune(text))
	start, end := citation.Start, citation.End
	if start < 0 || start > length {
		start = 0
	}
	if end < start || end > length {
		end = start
	}
	return start, end
}

func writeOpenAIRole(w io.Writer, id string, created int64, model string) error {
	return writeOpenAIDelta(w, id, created, model, gin.H{"role": "assistant", "content": ""})
}

func writeOpenAIDelta(w io.Writer, id string, created int64, model string, delta gin.H) error {
	return writeSSEJSON(w, gin.H{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []gin.H{{"index": 0, "delta": delta, "finish_reason": nil}},
	})
}

func writeOpenAIFinish(w io.Writer, id string, created int64, model string, result generationResult, bridge ToolBridge) error {
	finishReason := "stop"
	primary := result.Accumulator.Primary()
	_, calls, _ := bridge.Parse(primary.Text)
	if len(calls) > 0 {
		finishReason = "tool_calls"
	}
	chunk := gin.H{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"provider_model": result.ProviderModel, "conversation_id": result.ConversationID, "response_id": result.ResponseID,
		"choices": []gin.H{{"index": 0, "delta": gin.H{}, "finish_reason": finishReason}},
	}
	return writeSSEJSON(w, chunk)
}

func writeOpenAIUsageChunk(w io.Writer, id string, created int64, model string, usage *gemini.Usage) error {
	return writeSSEJSON(w, gin.H{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []interface{}{}, "usage": openAIUsage(usage),
	})
}

func writeOpenAIStreamError(w io.Writer, code string, err error) error {
	return writeSSEJSON(w, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error", "code": code}})
}

func writeSSEJSON(w io.Writer, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flushWriter(w)
	return nil
}

func writeSSEDone(w io.Writer) {
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flushWriter(w)
}

func setSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
}

func flushWriter(w io.Writer) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func openAIToolBridge(request ChatRequest) (ToolBridge, error) {
	bridge := ToolBridge{Choice: openAIToolChoice(request.ToolChoice)}
	for _, tool := range request.Tools {
		if tool.Type != "function" || tool.Function.Name == "" {
			return ToolBridge{}, fmt.Errorf("仅支持 function 工具")
		}
		parameters := tool.Function.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		bridge.Definitions = append(bridge.Definitions, ToolDefinition{
			Name: tool.Function.Name, Description: tool.Function.Description, Parameters: parameters,
		})
	}
	if len(request.ResponseFormat) > 0 && string(request.ResponseFormat) != "null" {
		var responseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		if err := json.Unmarshal(request.ResponseFormat, &responseFormat); err != nil {
			return ToolBridge{}, fmt.Errorf("response_format 无效: %w", err)
		}
		switch responseFormat.Type {
		case "json_object":
			bridge.JSONSchema = json.RawMessage(`{"type":"object"}`)
		case "json_schema":
			bridge.JSONSchema = responseFormat.JSONSchema.Schema
			if len(bridge.JSONSchema) == 0 || string(bridge.JSONSchema) == "null" {
				return ToolBridge{}, fmt.Errorf("response_format.json_schema.schema 不能为空")
			}
		case "text", "":
		default:
			return ToolBridge{}, fmt.Errorf("不支持的 response_format.type %q", responseFormat.Type)
		}
	}
	return bridge, nil
}

func openAIToolChoice(raw json.RawMessage) string {
	var choice string
	if json.Unmarshal(raw, &choice) == nil {
		return choice
	}
	var object struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &object) == nil && object.Function.Name != "" {
		return object.Function.Name
	}
	var flat struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &flat) == nil && flat.Name != "" {
		return flat.Name
	}
	if flat.Type != "" && flat.Type != "function" {
		return flat.Type
	}
	return "auto"
}

func continuationMessages(messages []ChatMessage) []ChatMessage {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "assistant" || messages[index].Role == "model" {
			if index+1 < len(messages) {
				return messages[index+1:]
			}
		}
	}
	return messages
}

func unsupportedChatParameters(request ChatRequest) []string {
	var fields []string
	if request.MaxTokens != nil {
		fields = append(fields, "max_tokens")
	}
	if request.MaxCompletionTokens != nil {
		fields = append(fields, "max_completion_tokens")
	}
	if request.Temperature != nil {
		fields = append(fields, "temperature")
	}
	if request.TopP != nil {
		fields = append(fields, "top_p")
	}
	if len(request.Stop) > 0 && string(request.Stop) != "null" {
		fields = append(fields, "stop")
	}
	if request.PresencePenalty != nil {
		fields = append(fields, "presence_penalty")
	}
	if request.FrequencyPenalty != nil {
		fields = append(fields, "frequency_penalty")
	}
	if request.Seed != nil {
		fields = append(fields, "seed")
	}
	return fields
}

func openAIToolCallResponses(calls []ToolCall) []gin.H {
	result := make([]gin.H, 0, len(calls))
	for _, call := range calls {
		result = append(result, gin.H{
			"id": call.ID, "type": "function",
			"function": gin.H{"name": call.Name, "arguments": string(call.Arguments)},
		})
	}
	return result
}

func openAIUsage(usage *gemini.Usage) gin.H {
	return gin.H{
		"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.OutputTokens(), "total_tokens": usage.TotalTokens,
		"prompt_tokens_details": gin.H{"cached_tokens": 0}, "completion_tokens_details": gin.H{"reasoning_tokens": usage.ThoughtTokens},
	}
}
