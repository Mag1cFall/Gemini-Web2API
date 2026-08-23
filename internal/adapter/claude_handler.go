package adapter

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/claude"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
	"github.com/gin-gonic/gin"
)

// ClaudeMessagesHandler 处理 Anthropic Messages
func ClaudeMessagesHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request claude.ClaudeRequest
		err := decodeRequest(c, &request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if request.Model == "" || len(request.Messages) == 0 || request.MaxTokens == nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "model, messages and max_tokens are required")
			return
		}
		knownModel, readyModel := modelStatus(pool, request.Model)
		if !knownModel {
			writeClaudeError(c, http.StatusNotFound, "not_found_error", fmt.Sprintf("model %q is unavailable", request.Model))
			return
		}
		if !readyModel {
			writeClaudeError(c, http.StatusServiceUnavailable, "overloaded_error", "No account is ready for the requested model")
			return
		}
		thinkingMode, err := claudeThinkingMode(request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}

		bridge, err := claudeToolBridge(request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		setUnsupportedParameters(c, unsupportedClaudeParameters(request))
		sessionKey := conversationKey(request.PreviousResponseID, request.ConversationID, c.GetHeader("X-Conversation-ID"))
		responseID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

		if request.Stream {
			setSSEHeaders(c)
			processor := claude.NewStreamProcessor(request.Model, c.Writer, responseID)
			projection := newStreamProjection(bridge)
			thinkingOpen := false
			thinkingBlock := 0
			result, accountID, err := runGeneration(
				c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", request.Model, responseID, false, thinkingMode,
				func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
					return buildClaudePrompt(c, client, request, continuation, bridge)
				}, func(event gemini.Event) error {
					if event.Kind == gemini.EventMetadata && event.Usage != nil {
						return processor.ProcessEvent(event)
					}
					return projection.project(event, func(event gemini.Event) error {
						if event.Kind == gemini.EventText && thinkingOpen {
							if err := processor.FinishThinking(thinkingSignature(responseID, thinkingBlock)); err != nil {
								return err
							}
							thinkingOpen = false
							thinkingBlock++
						}
						if event.Kind == gemini.EventThought {
							thinkingOpen = true
						}
						return processor.ProcessEvent(event)
					}, processor.EmitError)
				},
			)
			c.Set("account_id", accountID)
			if err != nil {
				if !projection.errorSent {
					_ = processor.EmitError(err)
				}
				return
			}
			primary := result.Accumulator.Primary()
			content, calls, err := bridge.Parse(primary.Text)
			if err != nil {
				_ = processor.EmitError(err)
				return
			}
			calls = assignToolCallIDs(calls, responseID)
			processor.SetConversationID(result.ConversationID)
			if result.Accumulator.Usage != nil {
				_ = processor.ProcessEvent(gemini.Event{Kind: gemini.EventMetadata, Usage: result.Accumulator.Usage})
			}
			if thinkingOpen {
				_ = processor.FinishThinking(thinkingSignature(responseID, thinkingBlock))
				thinkingOpen = false
			} else if projection.bufferThought && primary.Thought != "" {
				_ = processor.EmitThinking(primary.Thought, thinkingSignature(responseID, 0))
			}
			if projection.bufferText && content != "" {
				_ = processor.ProcessEvent(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: content})
			}
			for _, call := range calls {
				_ = processor.EmitToolCall(call.ID, call.Name, call.Arguments)
			}
			for _, image := range primary.Images {
				image := image
				_ = processor.ProcessEvent(gemini.Event{Kind: gemini.EventImage, Image: &image})
			}
			stopReason := "end_turn"
			if len(calls) > 0 {
				stopReason = "tool_use"
			}
			_ = processor.Finish(stopReason)
			return
		}

		result, accountID, err := runGeneration(
			c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", request.Model, responseID, false, thinkingMode,
			func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
				return buildClaudePrompt(c, client, request, continuation, bridge)
			}, nil,
		)
		c.Set("account_id", accountID)
		if err != nil {
			writeClaudeError(c, upstreamStatus(err), claudeUpstreamType(err), err.Error())
			return
		}

		primary := result.Accumulator.Primary()
		content, calls, err := bridge.Parse(primary.Text)
		if err != nil {
			writeClaudeError(c, http.StatusBadGateway, "api_error", err.Error())
			return
		}
		calls = assignToolCallIDs(calls, responseID)

		blocks := make([]claude.ContentBlock, 0)
		if primary.Thought != "" {
			blocks = append(blocks, claude.ContentBlock{Type: "thinking", Thinking: primary.Thought, Signature: thinkingSignature(responseID, 0)})
		}
		if content != "" {
			blocks = append(blocks, claude.ContentBlock{Type: "text", Text: content})
		}
		for _, call := range calls {
			var input map[string]interface{}
			_ = json.Unmarshal(call.Arguments, &input)
			blocks = append(blocks, claude.ContentBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: input})
		}
		for _, image := range primary.Images {
			blocks = append(blocks, claude.ContentBlock{Type: "text", Text: fmt.Sprintf("![%s](%s)", image.Alt, image.URL)})
		}
		if len(blocks) == 0 {
			blocks = append(blocks, claude.ContentBlock{Type: "text"})
		}
		stopReason := "end_turn"
		if len(calls) > 0 {
			stopReason = "tool_use"
		}
		response := claude.ClaudeResponse{
			ID: responseID, Type: "message", Role: "assistant", Model: request.Model, Content: blocks,
			StopReason: stopReason, ConversationID: result.ConversationID, Usage: &claude.Usage{},
		}
		if result.Accumulator.Usage != nil {
			response.Usage = &claude.Usage{InputTokens: result.Accumulator.Usage.PromptTokens, OutputTokens: result.Accumulator.Usage.OutputTokens()}
		}
		c.Header("X-Response-ID", responseID)
		c.Header("X-Conversation-ID", result.ConversationID)
		c.JSON(http.StatusOK, response)
	}
}

// ClaudeCountTokensHandler 处理 Anthropic 输入 token 计数
func ClaudeCountTokensHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request claude.ClaudeRequest
		if err := decodeRequest(c, &request); err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if request.Model == "" || len(request.Messages) == 0 {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "model and messages are required")
			return
		}
		knownModel, _ := modelStatus(pool, request.Model)
		if !knownModel {
			writeClaudeError(c, http.StatusNotFound, "not_found_error", fmt.Sprintf("model %q is unavailable", request.Model))
			return
		}
		for _, message := range request.Messages {
			var parts []map[string]json.RawMessage
			if json.Unmarshal(message.Content, &parts) == nil {
				for _, part := range parts {
					var partType string
					_ = json.Unmarshal(part["type"], &partType)
					if partType == "image" || partType == "document" || partType == "input_file" {
						writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "本地 count_tokens 当前只支持文本与工具内容")
						return
					}
				}
			}
		}
		bridge, err := claudeToolBridge(request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		prompt, _, err := buildClaudePrompt(c, nil, request, false, bridge)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"input_tokens": tokencount.Content(prompt)})
	}
}

// ClaudeListModelsHandler 返回 Anthropic 模型目录
func ClaudeListModelsHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		writeClaudeModelList(c, availableModels(pool))
	}
}

func writeClaudeModelList(c *gin.Context, models []gemini.Model) {
	data := make([]gin.H, 0, len(models))
	for _, model := range models {
		data = append(data, gin.H{"id": model.ID, "type": "model", "display_name": model.DisplayName, "created_at": "1970-01-01T00:00:00Z", "default": model.Default})
	}
	c.JSON(http.StatusOK, gin.H{"data": data, "has_more": false, "first_id": firstModelID(models), "last_id": lastModelID(models)})
}

func buildClaudePrompt(c *gin.Context, client *gemini.Client, request claude.ClaudeRequest, continuation bool, bridge ToolBridge) (string, []gemini.FileData, error) {
	messages := request.Messages
	if continuation {
		for index := len(messages) - 1; index >= 0; index-- {
			if messages[index].Role == "assistant" {
				messages = messages[index+1:]
				break
			}
		}
	}
	records := make([]map[string]interface{}, 0, len(messages)+1)
	var files []gemini.FileData
	if !continuation {
		if system, err := claude.ParseSystemPrompt(request.System); err != nil {
			return "", nil, err
		} else if system != "" {
			records = append(records, map[string]interface{}{"role": "system", "content": system})
		}
	}
	for _, message := range messages {
		content, attachments, err := decodeMessageContent(c.Request.Context(), message.Content, client)
		if err != nil {
			return "", nil, err
		}
		records = append(records, map[string]interface{}{"role": transcriptRole(message.Role), "content": content})
		files = append(files, attachments...)
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

func claudeToolBridge(request claude.ClaudeRequest) (ToolBridge, error) {
	bridge := ToolBridge{Choice: claudeToolChoice(request.ToolChoice)}
	for _, tool := range request.Tools {
		if tool.IsWebSearch() {
			return ToolBridge{}, fmt.Errorf("web_search 工具尚未映射到 Gemini Web")
		}
		if tool.Name == nil || *tool.Name == "" {
			return ToolBridge{}, fmt.Errorf("工具 name 不能为空")
		}
		parameters := tool.InputSchema
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		description := ""
		if tool.Description != nil {
			description = *tool.Description
		}
		bridge.Definitions = append(bridge.Definitions, ToolDefinition{Name: *tool.Name, Description: description, Parameters: parameters})
	}
	return bridge, nil
}

func claudeToolChoice(raw json.RawMessage) string {
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &choice) == nil {
		if choice.Name != "" {
			return choice.Name
		}
		if choice.Type != "" {
			return choice.Type
		}
	}
	return "auto"
}

func unsupportedClaudeParameters(request claude.ClaudeRequest) []string {
	fields := []string{"max_tokens"}
	if request.Temperature != nil {
		fields = append(fields, "temperature")
	}
	if request.TopP != nil {
		fields = append(fields, "top_p")
	}
	if request.TopK != nil {
		fields = append(fields, "top_k")
	}
	if len(request.StopSequences) > 0 {
		fields = append(fields, "stop_sequences")
	}
	return fields
}

func firstModelID(models []gemini.Model) string {
	if len(models) == 0 {
		return ""
	}
	return models[0].ID
}
func lastModelID(models []gemini.Model) string {
	if len(models) == 0 {
		return ""
	}
	return models[len(models)-1].ID
}

func thinkingSignature(messageID string, blockIndex int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", messageID, blockIndex)))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
