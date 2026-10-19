package adapter

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/claude"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
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
		logging.FromContext(c.Request.Context()).Begin("messages", request.Model, request.Stream, len(request.Messages), len(request.Tools))
		bindScope(c, pool, request.Model)
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
		thinkingLevel, includeThoughts, err := claudeThinkingSettings(request)
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
		sessionKey := conversationKey(request.PreviousResponseID, request.ConversationID, extractHeaderSessionKey(c))
		if sessionKey == "" && len(request.Messages) > 1 {
			if lastIdx := findLastClaudeAssistantIndex(request.Messages); lastIdx >= 0 {
				fp := claudeMessagesFingerprint(request.System, request.Messages[:lastIdx+1])
				if conversations.has(fp) {
					sessionKey = fp
				}
			}
		}
		responseID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

		if request.Stream {
			setSSEHeaders(c)
			defer startKeepalive(c, anthropicKeepalive)()
			processor := claude.NewStreamProcessor(config.MapModel(request.Model), c.Writer, responseID)
			projection := newStreamProjection(bridge, includeThoughts)
			thinkingOpen := false
			thinkingBlock := 0
			result, accountID, err := runGeneration(
				c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", request.Model, responseID, generationMedia{}, thinkingLevel, bridge,
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
				projection.hasVisibleOutput,
			)
			c.Set("account_id", accountID)
			if err != nil {
				if !projection.errorSent {
					_ = processor.EmitErrorType(claudeUpstreamType(err), streamFailure(c, err))
				}
				return
			}
			processor.SetModel(result.Model)
			if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
				_ = processor.EmitError(streamFailure(c, err))
				return
			}
			primary := result.Accumulator.Primary()
			if err := validateHostedOutput(primary, bridge); err != nil {
				_ = processor.EmitError(streamFailure(c, err))
				return
			}
			content, calls, err := bridge.Parse(primary.Text)
			if err != nil {
				_ = processor.EmitError(streamFailure(c, err))
				return
			}
			calls = assignToolCallIDs(calls, responseID)
			processor.SetConversationID(result.ConversationID)
			if result.Accumulator.Usage != nil {
				if err := processor.ProcessEvent(gemini.Event{Kind: gemini.EventMetadata, Usage: result.Accumulator.Usage}); err != nil {
					return
				}
			}
			if includeThoughts && thinkingOpen {
				if err := processor.FinishThinking(thinkingSignature(responseID, thinkingBlock)); err != nil {
					return
				}
				thinkingOpen = false
			} else if includeThoughts && projection.bufferThought && primary.Thought != "" {
				if err := processor.EmitThinking(primary.Thought, thinkingSignature(responseID, 0)); err != nil {
					return
				}
			}
			if bridge.WebSearch && len(primary.Citations) > 0 && projection.bufferText && projection.textRunes == 0 {
				content = renderCandidateMarkdown(content, primary, 0)
				blocks := anthropicSearchBlocks(responseID, primary.Citations)
				if err := processor.EmitServerToolUse(blocks[0]["id"].(string), "web_search", blocks[0]["input"]); err != nil {
					return
				}
				if err := processor.EmitBlock(blocks[1]); err != nil {
					return
				}
				for _, block := range anthropicCitedText(content, primary.Citations) {
					if err := processor.EmitCitedText(block.Text, block.Citations); err != nil {
						return
					}
				}
			} else if projection.bufferText {
				content = renderCandidateMarkdown(content, primary, projection.textRunes)
				content = renderCitationMarkdown(content, primary.Citations)
				if content != "" {
					if err := processor.ProcessEvent(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: content}); err != nil {
						return
					}
				}
			} else if len(primary.Citations) > 0 {
				if err := processor.ProcessEvent(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: renderCitationSources(primary.Citations)}); err != nil {
					return
				}
			}
			for _, call := range calls {
				if err := processor.EmitToolCall(call.ID, call.Name, call.Arguments); err != nil {
					return
				}
			}
			stopReason := "end_turn"
			if len(calls) > 0 {
				stopReason = "tool_use"
			}
			bindNextClaudeFingerprint(pool, request.System, request.Messages, result)
			_ = processor.Finish(stopReason)
			return
		}

		result, accountID, err := runGeneration(
			c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", request.Model, responseID, generationMedia{}, thinkingLevel, bridge,
			func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
				return buildClaudePrompt(c, client, request, continuation, bridge)
			}, nil, nil,
		)
		c.Set("account_id", accountID)
		if err != nil {
			writeClaudeError(c, upstreamStatus(err), claudeUpstreamType(err), err.Error())
			return
		}

		if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
			writeClaudeError(c, http.StatusBadGateway, "api_error", err.Error())
			return
		}
		primary := result.Accumulator.Primary()
		if err := validateHostedOutput(primary, bridge); err != nil {
			writeClaudeError(c, http.StatusBadGateway, "api_error", err.Error())
			return
		}
		content, calls, err := bridge.Parse(primary.Text)
		if err != nil {
			writeClaudeError(c, http.StatusBadGateway, "api_error", err.Error())
			return
		}
		calls = assignToolCallIDs(calls, responseID)
		content = renderCandidateMarkdown(content, primary, 0)
		searched := bridge.WebSearch && len(primary.Citations) > 0

		blocks := make([]claude.ContentBlock, 0)
		if includeThoughts && primary.Thought != "" {
			blocks = append(blocks, claude.ContentBlock{Type: "thinking", Thinking: primary.Thought, Signature: thinkingSignature(responseID, 0)})
		}
		if searched {
			search := anthropicSearchBlocks(responseID, primary.Citations)
			input := map[string]interface{}(search[0]["input"].(gin.H))
			results, _ := json.Marshal(search[1]["content"])
			blocks = append(blocks,
				claude.ContentBlock{Type: "server_tool_use", ID: search[0]["id"].(string), Name: "web_search", Input: &input},
				claude.ContentBlock{Type: "web_search_tool_result", ToolUseID: search[0]["id"].(string), Content: results},
			)
			for _, block := range anthropicCitedText(content, primary.Citations) {
				blocks = append(blocks, claude.ContentBlock{Type: "text", Text: block.Text, Citations: block.Citations})
			}
		} else if content = renderCitationMarkdown(content, primary.Citations); content != "" {
			blocks = append(blocks, claude.ContentBlock{Type: "text", Text: content})
		}
		for _, call := range calls {
			var input map[string]interface{}
			_ = json.Unmarshal(call.Arguments, &input)
			blocks = append(blocks, claude.ContentBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: &input})
		}
		if len(blocks) == 0 {
			blocks = append(blocks, claude.ContentBlock{Type: "text"})
		}
		stopReason := "end_turn"
		if len(calls) > 0 {
			stopReason = "tool_use"
		}
		response := claude.ClaudeResponse{
			ID: responseID, Type: "message", Role: "assistant", Model: result.Model, Content: blocks,
			StopReason: stopReason, ConversationID: result.ConversationID, Usage: &claude.Usage{},
		}
		if result.Accumulator.Usage != nil {
			usage := result.Accumulator.Usage
			response.Usage = &claude.Usage{InputTokens: usage.PromptTokens, OutputTokens: usage.OutputTokens()}
		}
		if searched {
			response.Usage.ServerToolUse = &claude.ServerToolUse{WebSearchRequests: 1}
		}
		c.Header("X-Response-ID", responseID)
		c.Header("X-Conversation-ID", result.ConversationID)
		c.Header("X-Provider-Model", result.ProviderModel)
		bindNextClaudeFingerprint(pool, request.System, request.Messages, result)
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
		bindScope(c, pool, request.Model)
		knownModel, _ := modelStatus(pool, request.Model)
		if !knownModel {
			writeClaudeError(c, http.StatusNotFound, "not_found_error", fmt.Sprintf("model %q is unavailable", request.Model))
			return
		}
		bridge, err := claudeToolBridge(request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		prompt, files, err := buildClaudePrompt(c, countClient(pool, request.Model), request, false, bridge)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"input_tokens": tokencount.Content(prompt) + mediaTokens(files)})
	}
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
	transcript, err := renderTranscript(records)
	if err != nil {
		return "", nil, err
	}
	var suffix string
	if continuation {
		suffix = bridge.ContinuationSuffix()
	} else {
		suffix, err = bridge.PromptSuffix()
		if err != nil {
			return "", nil, err
		}
	}
	return transcript + suffix, files, nil
}

func claudeToolBridge(request claude.ClaudeRequest) (ToolBridge, error) {
	bridge := ToolBridge{Choice: claudeToolChoice(request.ToolChoice)}
	webTool := false
	for _, tool := range request.Tools {
		if tool.IsWebSearch() {
			bridge.WebSearch = true
			webTool = true
			continue
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
	choice := strings.ToLower(strings.TrimSpace(bridge.Choice))
	switch choice {
	case "web_search", "web_search_20250305", "google_search":
		if !webTool {
			return ToolBridge{}, fmt.Errorf("tool_choice 选择了未声明的 web_search")
		}
		bridge.RequireWebSearch = true
		bridge.Choice = "auto"
	case "any", "required":
		if webTool && len(request.Tools) == 1 {
			bridge.RequireWebSearch = true
			bridge.Choice = "auto"
		} else if webTool {
			return ToolBridge{}, fmt.Errorf("web_search 与其他工具组合时不支持 tool_choice=%s", choice)
		}
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

func thinkingSignature(messageID string, blockIndex int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", messageID, blockIndex)))
	return base64.StdEncoding.EncodeToString(digest[:])
}

func findLastClaudeAssistantIndex(messages []claude.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return i
		}
	}
	return -1
}

func claudeMessagesFingerprint(system json.RawMessage, messages []claude.Message) string {
	if len(messages) == 0 {
		return ""
	}
	h := sha256.New()
	if len(system) > 0 {
		h.Write(system)
		h.Write([]byte{0xFF})
	}
	for _, m := range messages {
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write(m.Content)
		h.Write([]byte{0xFF})
	}
	sum := h.Sum(nil)
	return "fp_cld_" + hex.EncodeToString(sum[:16])
}

func bindNextClaudeFingerprint(pool *balancer.AccountPool, system json.RawMessage, messages []claude.Message, result generationResult) {
	if result.ConversationID == "" {
		return
	}
	primary := result.Accumulator.Primary()
	if primary.Text == "" && len(primary.Codes) == 0 {
		return
	}
	assistantContent, err := json.Marshal(primary.Text)
	if err != nil {
		return
	}
	nextMessages := append(messages, claude.Message{Role: "assistant", Content: assistantContent})
	nextFp := claudeMessagesFingerprint(system, nextMessages)
	if state, accountID, ctxTokens, ok := conversations.get(result.ConversationID); ok {
		conversations.put(state, accountID, ctxTokens, nextFp)
		pool.BindSession(nextFp, accountID)
	}
}
