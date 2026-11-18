package adapter

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/claude"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
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
		suffix, err := takeModelSuffix(&request.Model)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		logging.FromContext(c.Request.Context()).Begin("messages", request.Model, request.Stream, len(request.Messages), len(request.Tools))
		bindScope(c, pool, request.Model)
		if request.Model == "" {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
			return
		}
		if len(request.Messages) == 0 && claudeSystemEmpty(request.System) {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "messages 与 system 不能同时为空")
			return
		}
		if err := continuationInputError("previous_response_id", request.PreviousResponseID, "conversation_id", request.ConversationID, "X-Conversation-ID", c.GetHeader("X-Conversation-ID")); err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if err := awaitModel(c, pool, request.Model); err != nil {
			writeClaudeError(c, upstreamStatus(err), claudeUpstreamType(err), err.Error())
			return
		}
		request.Messages = compactedClaudeMessages(request.Messages)
		contextResult, err := applyClaudeContextEdits(c, pool, &request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		thinkingLevel, includeThoughts, err := claudeThinkingSettings(request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		thinkingLevel, includeThoughts = suffix.apply(thinkingLevel, includeThoughts)

		bridge, err := claudeToolBridge(request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		setUnsupportedParameters(c, unsupportedClaudeParameters(request))
		responseID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
		if contextResult.compacting {
			if contextResult.compaction, err = compactClaude(c, pool, &request, responseID, thinkingLevel); err != nil {
				writeClaudeError(c, upstreamStatus(err), claudeUpstreamType(err), err.Error())
				return
			}
			if contextResult.compaction != nil && contextResult.compaction.pause {
				writeClaudeCompactionPause(c, pool, request, responseID, contextResult)
				return
			}
		}
		sessionKey := conversationKey(request.PreviousResponseID, request.ConversationID, c.GetHeader("X-Conversation-ID"))
		var system string
		var turns []historyTurn
		if sessionKey == "" {
			system, _ = claude.ParseSystemPrompt(request.System)
			turns = claudeHistoryTurns(request.Messages)
			sessionKey = continuedHistoryKey(system, bridge, turns)
		}

		if request.Stream {
			setSSEHeaders(c)
			stream, stop := holdStream(c, anthropicKeepalive)
			defer stop()
			processor := claude.NewStreamProcessor(publicModelID(pool, request.Model), c.Writer, responseID)
			projection := newStreamProjection(bridge, includeThoughts)
			thinkingOpen := false
			thinkingBlock := 0
			fetchesSent := false
			compactionSent := false
			// emitFetches 在正文之前输出服务读取网页的内容块
			emitFetches := func() error {
				if fetchesSent {
					return nil
				}
				fetchesSent = true
				for _, block := range anthropicFetchBlocks(responseID, requestPages(c)) {
					if block["type"] == "server_tool_use" {
						if err := processor.EmitServerToolUse(block["id"].(string), "web_fetch", block["input"]); err != nil {
							return err
						}
						continue
					}
					if err := processor.EmitBlock(block); err != nil {
						return err
					}
				}
				return nil
			}
			result, accountID, err := runGeneration(
				c.Request.Context(), pool, sessionKey, previousResponsePolicy(request.PreviousResponseID), request.Model, responseID, generationMedia{}, thinkingLevel, bridge,
				func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
					return buildClaudePrompt(c, client, request, continuation, bridge.declared(continuation && isHistoryKey(sessionKey)))
				}, func(event gemini.Event) error {
					if !usageOnly(event) {
						stream.release()
					}
					if event.Kind == gemini.EventMetadata && event.Usage != nil {
						if err := processor.ProcessEvent(event); err != nil || compactionSent || contextResult.compaction == nil {
							return err
						}
						compactionSent = true
						return processor.EmitCompaction(contextResult.compaction.summary)
					}
					return projection.project(event, func(event gemini.Event) error {
						if event.Kind == gemini.EventText && thinkingOpen {
							if err := processor.FinishThinking(thinkingSignature(responseID, thinkingBlock)); err != nil {
								return err
							}
							thinkingOpen = false
							thinkingBlock++
						}
						if event.Kind == gemini.EventText {
							if err := emitFetches(); err != nil {
								return err
							}
						}
						if event.Kind == gemini.EventThought {
							thinkingOpen = true
						}
						return processor.ProcessEvent(event)
					})
				},
				projection,
			)
			c.Set("account_id", accountID)
			if err != nil {
				if stream.reject() {
					writeClaudeError(c, upstreamStatus(err), claudeUpstreamType(err), err.Error())
				} else {
					_ = processor.EmitErrorType(claudeUpstreamType(err), streamFailure(c, err))
				}
				return
			}
			processor.SetModel(result.Model)
			processor.SetProviderModel(result.ProviderModel)
			if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
				_ = processor.EmitError(streamFailure(c, err))
				return
			}
			primary := result.Accumulator.Primary()
			if err := validateHostedOutput(primary, bridge); err != nil {
				_ = processor.EmitError(streamFailure(c, err))
				return
			}
			rememberHistory(pool, result, system, bridge, turns)
			content, calls, err := bridge.truncated(result.Truncated).Parse(primary.Text)
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
			if err := emitFetches(); err != nil {
				return
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
			processor.SetContextManagement(contextResult.management(), contextResult.iterations(result.Accumulator.Usage))
			if result.StopSequence != "" && len(calls) == 0 {
				_ = processor.FinishStopSequence(result.StopSequence)
				return
			}
			stopReason := "end_turn"
			if len(calls) > 0 {
				stopReason = "tool_use"
			} else if result.Truncated {
				stopReason = "max_tokens"
			}
			_ = processor.Finish(stopReason)
			return
		}

		defer startNonstreamKeepalive(c)()
		result, accountID, err := runGeneration(
			c.Request.Context(), pool, sessionKey, previousResponsePolicy(request.PreviousResponseID), request.Model, responseID, generationMedia{}, thinkingLevel, bridge,
			func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
				return buildClaudePrompt(c, client, request, continuation, bridge.declared(continuation && isHistoryKey(sessionKey)))
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
		rememberHistory(pool, result, system, bridge, turns)
		content, calls, err := bridge.truncated(result.Truncated).Parse(primary.Text)
		if err != nil {
			writeClaudeError(c, http.StatusBadGateway, "api_error", err.Error())
			return
		}
		calls = assignToolCallIDs(calls, responseID)
		content = renderCandidateMarkdown(content, primary, 0)
		searched := bridge.WebSearch && len(primary.Citations) > 0

		blocks := make([]claude.ContentBlock, 0)
		if compaction := contextResult.compaction; compaction != nil {
			blocks = append(blocks, compaction.contentBlock())
		}
		if includeThoughts && primary.Thought != "" {
			blocks = append(blocks, claude.ContentBlock{Type: "thinking", Thinking: primary.Thought, Signature: thinkingSignature(responseID, 0)})
		}
		pages := requestPages(c)
		for _, block := range anthropicFetchBlocks(responseID, pages) {
			if block["type"] == "server_tool_use" {
				input := map[string]interface{}(block["input"].(gin.H))
				blocks = append(blocks, claude.ContentBlock{Type: "server_tool_use", ID: block["id"].(string), Name: "web_fetch", Input: &input})
				continue
			}
			content, _ := json.Marshal(block["content"])
			blocks = append(blocks, claude.ContentBlock{Type: "web_fetch_tool_result", ToolUseID: block["tool_use_id"].(string), Content: content})
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
			input := claudeToolInput(call.Arguments)
			blocks = append(blocks, claude.ContentBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: &input})
		}
		stopReason := "end_turn"
		var stopSequence *string
		if len(calls) > 0 {
			stopReason = "tool_use"
		} else if result.StopSequence != "" {
			stopReason, stopSequence = "stop_sequence", &result.StopSequence
		} else if result.Truncated {
			stopReason = "max_tokens"
		}
		response := claude.ClaudeResponse{
			ID: responseID, Type: "message", Role: "assistant", Model: result.Model, Content: blocks,
			StopReason: stopReason, StopSequence: stopSequence, ConversationID: result.ConversationID, Usage: &claude.Usage{}, ProviderModel: result.ProviderModel,
		}
		if result.Accumulator.Usage != nil {
			usage := result.Accumulator.Usage
			response.Usage = &claude.Usage{InputTokens: usage.PromptTokens, OutputTokens: usage.OutputTokens()}
		}
		if searched || len(pages) > 0 {
			response.Usage.ServerToolUse = &claude.ServerToolUse{WebFetchRequests: len(pages)}
			if searched {
				response.Usage.ServerToolUse.WebSearchRequests = 1
			}
		}
		response.Usage.Iterations = contextResult.iterations(result.Accumulator.Usage)
		response.ContextManagement = contextResult.management()
		c.Header("X-Response-ID", responseID)
		c.Header("X-Conversation-ID", result.ConversationID)
		c.Header("X-Provider-Model", result.ProviderModel)
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
		if _, err := takeModelSuffix(&request.Model); err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if request.Model == "" {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
			return
		}
		bindScope(c, pool, request.Model)
		if err := modelKnown(pool, request.Model); err != nil {
			writeClaudeError(c, upstreamStatus(err), claudeUpstreamType(err), err.Error())
			return
		}
		request.Messages = compactedClaudeMessages(request.Messages)
		original := request
		contextResult, err := applyClaudeContextEdits(c, pool, &request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		inputTokens, err := claudeInputTokens(c, pool, request)
		if err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		response := gin.H{"input_tokens": inputTokens}
		if contextResult.requested {
			tokens, err := claudeInputTokens(c, pool, original)
			if err != nil {
				writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
				return
			}
			response["context_management"] = gin.H{"original_input_tokens": tokens}
		}
		c.JSON(http.StatusOK, response)
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
	turns := make([]transcriptTurn, 0, len(messages)+1)
	var files []gemini.FileData
	if !continuation {
		if system, err := claude.ParseSystemPrompt(request.System); err != nil {
			return "", nil, err
		} else if system != "" {
			turns = append(turns, transcriptTurn{Role: "system", Text: system})
		}
	}
	toolNames := claudeToolNames(request.Messages)
	for _, message := range messages {
		content, attachments, err := decodeMessageContent(c.Request.Context(), message.Content, client, toolNames)
		if err != nil {
			return "", nil, err
		}
		turns = append(turns, transcriptTurn{Role: transcriptRole(message.Role), Text: content})
		files = append(files, attachments...)
	}
	suffix, err := bridge.PromptSuffix()
	if err != nil {
		return "", nil, err
	}
	if bridge.URLContext && len(request.Messages) > 0 {
		last := request.Messages[len(request.Messages)-1]
		if last.Role == "user" {
			fetch := claudeFetchTool(request.Tools)
			section, pageFiles := pagePrompt(urlContextPages(c, client, claudeMessageText(last.Content), cmp.Or(fetch.MaxUses, urlContextLimit), fetch.AllowedDomains, fetch.BlockedDomains))
			if section != "" {
				suffix += "\n\n" + section
				files = append(files, pageFiles...)
			}
		}
	}
	return renderTranscript(turns, suffix), files, nil
}

// claudeMessageText 返回消息内容中的文字
func claudeMessageText(raw json.RawMessage) string {
	blocks, text, err := claude.ParseMessageContent(raw)
	if err != nil {
		return ""
	}
	for _, block := range blocks {
		if block.Type == "text" {
			text += block.Text + "\n"
		}
	}
	return text
}

// claudeFetchTool 返回请求中的 web_fetch 工具
func claudeFetchTool(tools []claude.Tool) claude.Tool {
	for _, tool := range tools {
		if tool.Type != nil && strings.HasPrefix(strings.ToLower(*tool.Type), "web_fetch_") {
			return tool
		}
	}
	return claude.Tool{}
}

// claudeToolNames 返回消息中 tool_use 的调用 ID 到结果标签的映射
func claudeToolNames(messages []claude.Message) map[string]string {
	names := map[string]string{}
	for _, message := range messages {
		var blocks []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(message.Content, &blocks)
		var turn []toolCallRef
		for _, block := range blocks {
			if block.Type == "tool_use" {
				turn = append(turn, toolCallRef{ID: block.ID, Name: block.Name})
			}
		}
		labelToolCalls(names, turn)
	}
	return names
}

// claudeToolInput 解码工具调用参数，数字保持原文
func claudeToolInput(arguments json.RawMessage) map[string]interface{} {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var input map[string]interface{}
	_ = decoder.Decode(&input)
	return input
}

func claudeToolBridge(request claude.ClaudeRequest) (ToolBridge, error) {
	var parallel struct {
		Disable bool `json:"disable_parallel_tool_use"`
	}
	_ = json.Unmarshal(request.ToolChoice, &parallel)
	bridge := ToolBridge{Choice: claudeToolChoice(request.ToolChoice), Stop: request.StopSequences, SingleCall: parallel.Disable}
	if request.MaxTokens != nil {
		bridge.MaxTokens = *request.MaxTokens
	}
	webTool := false
	for _, tool := range request.Tools {
		if tool.IsWebSearch() {
			bridge.WebSearch, bridge.SearchDomains, bridge.SearchBlocked, bridge.SearchLocation = true, tool.AllowedDomains, tool.BlockedDomains, searchLocation(tool.UserLocation)
			webTool = true
			continue
		}
		kind := ""
		if tool.Type != nil {
			kind = strings.ToLower(*tool.Type)
		}
		if anthropicServerTool(kind) {
			bridge.URLContext = bridge.URLContext || strings.HasPrefix(kind, "web_fetch_")
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
		if text, schema := anthropicDefinedTool(tool, kind); schema != nil {
			description, parameters = text, schema
		}
		bridge.Definitions = append(bridge.Definitions, ToolDefinition{Name: *tool.Name, Description: description, Parameters: parameters})
	}
	format := request.OutputFormat
	if request.OutputConfig != nil && request.OutputConfig.Format != nil {
		format = request.OutputConfig.Format
	}
	if format != nil {
		if format.Type != "json_schema" {
			return ToolBridge{}, fmt.Errorf("不支持的输出格式类型 %q", format.Type)
		}
		bridge.JSONSchema = format.Schema
	}
	if bridge.hasTool(bridge.explicitTool()) {
		return bridge, nil
	}
	choice := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(bridge.Choice), explicitChoicePrefix))
	switch choice {
	case "web_search", "web_search_20250305", "google_search":
		if !webTool {
			return ToolBridge{}, fmt.Errorf("tool_choice 选择了未声明的 web_search")
		}
		bridge.RequireWebSearch = true
		bridge.Choice = "auto"
	case "any", "required":
		if webTool && len(bridge.Definitions) == 0 {
			bridge.RequireWebSearch = true
			bridge.Choice = "auto"
		}
	}
	return bridge, bridge.checkExplicitTool()
}

func claudeToolChoice(raw json.RawMessage) string {
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &choice) == nil {
		if choice.Name != "" {
			return explicitChoicePrefix + choice.Name
		}
		if choice.Type != "" {
			return choice.Type
		}
	}
	return "auto"
}

// claudeSystemEmpty 判断 system 是否为空
func claudeSystemEmpty(raw json.RawMessage) bool {
	system, err := claude.ParseSystemPrompt(raw)
	return err == nil && strings.TrimSpace(system) == ""
}

func unsupportedClaudeParameters(request claude.ClaudeRequest) []string {
	var fields []string
	if request.Temperature != nil {
		fields = append(fields, "temperature")
	}
	if request.TopP != nil {
		fields = append(fields, "top_p")
	}
	if request.TopK != nil {
		fields = append(fields, "top_k")
	}
	return fields
}

func thinkingSignature(messageID string, blockIndex int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", messageID, blockIndex)))
	return base64.StdEncoding.EncodeToString(digest[:])
}
