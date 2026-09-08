package adapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/streamio"
	"github.com/gin-gonic/gin"
)

// ResponsesTool 表示 Responses API 函数工具
type ResponsesTool struct {
	Type              string          `json:"type"`
	Name              string          `json:"name"`
	Description       string          `json:"description,omitempty"`
	Parameters        json.RawMessage `json:"parameters"`
	Action            string          `json:"action,omitempty"`
	Background        string          `json:"background,omitempty"`
	InputFidelity     string          `json:"input_fidelity,omitempty"`
	InputImageMask    json.RawMessage `json:"input_image_mask,omitempty"`
	ImageModel        string          `json:"model,omitempty"`
	Moderation        string          `json:"moderation,omitempty"`
	OutputCompression *int            `json:"output_compression,omitempty"`
	OutputFormat      string          `json:"output_format,omitempty"`
	PartialImages     *int            `json:"partial_images,omitempty"`
	Quality           string          `json:"quality,omitempty"`
	Size              string          `json:"size,omitempty"`
}

// ResponsesRequest 表示 OpenAI Responses 请求
type ResponsesRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"`
	Instructions       string          `json:"instructions,omitempty"`
	Stream             bool            `json:"stream"`
	Tools              []ResponsesTool `json:"tools,omitempty"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty"`
	Text               json.RawMessage `json:"text,omitempty"`
	Conversation       json.RawMessage `json:"conversation,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	MaxOutputTokens    *int            `json:"max_output_tokens,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	Reasoning          json.RawMessage `json:"reasoning,omitempty"`
	Truncation         string          `json:"truncation,omitempty"`
	ParallelToolCalls  *bool           `json:"parallel_tool_calls,omitempty"`
}

// ResponsesHandler 处理 OpenAI Responses API
func ResponsesHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request ResponsesRequest
		err := decodeRequest(c, &request)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.Model == "" || len(request.Input) == 0 {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "model and input are required")
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
		thinkingMode, includeThoughts, err := responsesThinkingSettings(request)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}

		bridge, err := responsesToolBridge(request)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		setUnsupportedParameters(c, unsupportedResponsesParameters(request))
		conversationID, err := responsesConversationID(request.Conversation)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		sessionKey := conversationKey(request.PreviousResponseID, conversationID, c.GetHeader("X-Conversation-ID"))
		responseID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
		created := time.Now().Unix()

		if request.Stream {
			setSSEHeaders(c)
			writer := responseSequenceWriter{writer: c.Writer}
			streamModel := config.MapModel(request.Model)
			if err := writer.start(responseID, streamModel, created); err != nil {
				return
			}
			projection := newStreamProjection(bridge, includeThoughts)
			result, accountID, err := runGeneration(
				c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", request.Model, responseID, bridge.RequireImageGeneration, thinkingMode,
				func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
					return buildResponsesPrompt(c, client, request, continuation, bridge)
				}, func(event gemini.Event) error {
					if event.Kind == gemini.EventImageProgress {
						projection.bufferText = true
						if bridge.disallowsHostedTools() {
							projection.bufferThought = true
							return nil
						}
						return writer.imageProgress(event.Phase)
					}
					return projection.project(event, func(event gemini.Event) error {
						return writer.live(event)
					}, func(err error) error {
						return writer.fail(err)
					})
				},
				projection.hasVisibleOutput,
			)
			c.Set("account_id", accountID)
			if err != nil {
				if !projection.errorSent {
					_ = writer.fail(err)
				}
				return
			}
			if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
				_ = writer.fail(err)
				return
			}
			response, err := buildResponsesObject(responseID, created, request.PreviousResponseID, result, bridge, includeThoughts)
			if err != nil {
				_ = writer.fail(err)
				return
			}
			if err := writer.finishProjected(response, result.Accumulator, projection); err != nil {
				_ = writer.fail(err)
			}
			return
		}

		result, accountID, err := runGeneration(
			c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", request.Model, responseID, bridge.RequireImageGeneration, thinkingMode,
			func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
				return buildResponsesPrompt(c, client, request, continuation, bridge)
			}, nil, nil,
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
		response, err := buildResponsesObject(responseID, created, request.PreviousResponseID, result, bridge, includeThoughts)
		if err != nil {
			writeOpenAIError(c, http.StatusBadGateway, "output_validation_error", err.Error())
			return
		}
		c.Header("X-Response-ID", responseID)
		c.Header("X-Conversation-ID", result.ConversationID)
		c.JSON(http.StatusOK, response)
	}
}

func buildResponsesPrompt(c *gin.Context, client *gemini.Client, request ResponsesRequest, continuation bool, bridge ToolBridge) (string, []gemini.FileData, error) {
	messages, err := responsesMessages(request.Input)
	if err != nil {
		return "", nil, err
	}
	if request.Instructions != "" && !continuation {
		instructions, _ := json.Marshal(request.Instructions)
		messages = append([]ChatMessage{{Role: "system", Content: instructions}}, messages...)
	}
	chatRequest := ChatRequest{Messages: messages, Model: request.Model}
	return buildOpenAIChatPrompt(c, client, chatRequest, continuation, bridge)
}

func responsesMessages(raw json.RawMessage) ([]ChatMessage, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		content, _ := json.Marshal(text)
		return []ChatMessage{{Role: "user", Content: content}}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("input 必须是字符串或输入项数组")
	}
	messages := make([]ChatMessage, 0, len(items))
	for _, item := range items {
		var envelope struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			CallID    string          `json:"call_id"`
			Output    json.RawMessage `json:"output"`
			Name      string          `json:"name"`
			Arguments string          `json:"arguments"`
			Result    string          `json:"result"`
		}
		if err := json.Unmarshal(item, &envelope); err != nil {
			return nil, err
		}
		switch envelope.Type {
		case "", "message":
			messages = append(messages, ChatMessage{Role: envelope.Role, Content: envelope.Content})
		case "function_call_output":
			messages = append(messages, ChatMessage{Role: "tool", Content: envelope.Output, ToolCallID: envelope.CallID})
		case "function_call":
			arguments := envelope.Arguments
			messages = append(messages, ChatMessage{Role: "assistant", ToolCalls: []OpenAIToolCall{responsesPriorToolCall(envelope.CallID, envelope.Name, arguments)}})
		case "reasoning":
			messages = append(messages, ChatMessage{Role: "assistant", TranscriptReasoning: append(json.RawMessage(nil), item...)})
		case "code_interpreter_call", "web_search_call":
			messages = append(messages, ChatMessage{Role: "assistant", TranscriptItems: []json.RawMessage{append(json.RawMessage(nil), item...)}})
		case "image_generation_call":
			content, err := responseImageContent(envelope.Result)
			if err != nil {
				return nil, err
			}
			messages = append(messages, ChatMessage{Role: "assistant", Content: content})
		default:
			return nil, fmt.Errorf("不支持的 input item 类型 %q", envelope.Type)
		}
	}
	return messages, nil
}

func responseImageContent(result string) (json.RawMessage, error) {
	if strings.TrimSpace(result) == "" {
		return nil, fmt.Errorf("image_generation_call 缺少 result")
	}
	data, err := base64.StdEncoding.DecodeString(result)
	if err != nil {
		return nil, fmt.Errorf("image_generation_call.result 不是有效 base64: %w", err)
	}
	mimeType := http.DetectContentType(data)
	return json.Marshal([]gin.H{
		{"type": "input_text", "text": "Previous generated image"},
		{"type": "input_image", "image_url": "data:" + mimeType + ";base64," + result},
	})
}

func responsesPriorToolCall(id string, name string, arguments string) OpenAIToolCall {
	call := OpenAIToolCall{ID: id, Type: "function"}
	call.Function.Name, call.Function.Arguments = name, arguments
	return call
}

func responsesToolBridge(request ResponsesRequest) (ToolBridge, error) {
	bridge := ToolBridge{Choice: openAIToolChoice(request.ToolChoice)}
	imageTool := false
	codeTool := false
	webTool := false
	for _, tool := range request.Tools {
		switch tool.Type {
		case "function":
			if tool.Name == "" {
				return ToolBridge{}, fmt.Errorf("function 工具 name 不能为空")
			}
			parameters := tool.Parameters
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			bridge.Definitions = append(bridge.Definitions, ToolDefinition{Name: tool.Name, Description: tool.Description, Parameters: parameters})
		case "code_interpreter":
			bridge.CodeExecution = true
			codeTool = true
		case "web_search", "web_search_preview":
			bridge.WebSearch = true
			webTool = true
		case "image_generation":
			imageTool = true
			bridge.ImageGeneration = true
		default:
			return ToolBridge{}, fmt.Errorf("不支持的 Responses 工具 %q", tool.Type)
		}
	}
	if len(request.Text) > 0 && string(request.Text) != "null" {
		var textConfig struct {
			Format struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			} `json:"format"`
		}
		if err := json.Unmarshal(request.Text, &textConfig); err != nil {
			return ToolBridge{}, err
		}
		switch textConfig.Format.Type {
		case "json_schema":
			bridge.JSONSchema = textConfig.Format.Schema
			if len(bridge.JSONSchema) == 0 || string(bridge.JSONSchema) == "null" {
				return ToolBridge{}, fmt.Errorf("text.format.schema 不能为空")
			}
		case "json_object":
			bridge.JSONSchema = json.RawMessage(`{"type":"object"}`)
		case "", "text":
		default:
			return ToolBridge{}, fmt.Errorf("不支持的 text.format.type %q", textConfig.Format.Type)
		}
	}
	choice := strings.ToLower(strings.TrimSpace(bridge.Choice))
	hostedChoice := false
	switch choice {
	case "image_generation":
		if !imageTool {
			return ToolBridge{}, fmt.Errorf("tool_choice 选择了未声明的 image_generation")
		}
		bridge.RequireImageGeneration = true
		hostedChoice = true
	case "code_interpreter":
		if !codeTool {
			return ToolBridge{}, fmt.Errorf("tool_choice 选择了未声明的 code_interpreter")
		}
		bridge.RequireCodeExecution = true
		hostedChoice = true
	case "web_search", "web_search_preview":
		if !webTool {
			return ToolBridge{}, fmt.Errorf("tool_choice 选择了未声明的 web_search")
		}
		bridge.RequireWebSearch = true
		hostedChoice = true
	case "required", "any":
		if len(request.Tools) == 1 {
			bridge.RequireImageGeneration = imageTool
			bridge.RequireCodeExecution = codeTool
			bridge.RequireWebSearch = webTool
			hostedChoice = imageTool || codeTool || webTool
		} else if imageTool || codeTool || webTool {
			return ToolBridge{}, fmt.Errorf("内置工具与其他工具组合时不支持 tool_choice=%s", choice)
		}
	}
	if hostedChoice {
		bridge.Choice = "auto"
	}
	return bridge, nil
}

func buildResponsesObject(id string, created int64, previousResponseID string, result generationResult, bridge ToolBridge, includeThoughts bool) (gin.H, error) {
	primary := result.Accumulator.Primary()
	if err := validateHostedOutput(primary, bridge); err != nil {
		return nil, err
	}
	content, calls, err := bridge.Parse(primary.Text)
	if err != nil {
		return nil, err
	}
	calls = assignToolCallIDs(calls, id)
	output := make([]gin.H, 0)
	if includeThoughts && primary.Thought != "" {
		output = append(output, gin.H{"id": "rs_" + id, "type": "reasoning", "summary": []gin.H{{"type": "summary_text", "text": primary.Thought}}})
	}
	if len(primary.Citations) > 0 {
		sources := make([]gin.H, 0, len(primary.Citations))
		seenSources := make(map[string]struct{}, len(primary.Citations))
		for _, citation := range primary.Citations {
			if _, exists := seenSources[citation.URL]; exists {
				continue
			}
			seenSources[citation.URL] = struct{}{}
			sources = append(sources, gin.H{"type": "url", "url": citation.URL})
		}
		output = append(output, gin.H{
			"id": "ws_" + id, "type": "web_search_call", "status": "completed",
			"action": gin.H{"type": "search", "query": "", "sources": sources},
		})
	}
	output = append(output, responseCodeInterpreterItems(primary, id)...)
	output = append(output, responseImageGenerationItems(primary, id)...)
	markdownOutput := responsesMarkdownOutput(primary)
	content = renderCandidateMarkdown(content, markdownOutput, 0)
	if strings.TrimSpace(content) != "" {
		parts := []gin.H{}
		if content != "" {
			parts = append(parts, gin.H{"type": "output_text", "text": content, "annotations": responsesCitationAnnotations(content, primary.Citations)})
		}
		output = append(output, gin.H{"id": "msg_" + id, "type": "message", "status": "completed", "role": "assistant", "content": parts})
	}
	for _, call := range calls {
		output = append(output, gin.H{"id": "fc_" + call.ID, "type": "function_call", "status": "completed", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
	}
	response := responseShell(id, result.Model, created, "completed")
	if previousResponseID != "" {
		response["previous_response_id"] = previousResponseID
	}
	response["completed_at"] = time.Now().Unix()
	response["output"], response["conversation_id"], response["provider_model"] = output, result.ConversationID, result.ProviderModel
	if result.Accumulator.Usage != nil {
		outputTokens := result.Accumulator.Usage.OutputTokens()
		reasoningTokens := result.Accumulator.Usage.ThoughtTokens
		response["usage"] = gin.H{
			"input_tokens": result.Accumulator.Usage.PromptTokens, "output_tokens": outputTokens, "total_tokens": result.Accumulator.Usage.PromptTokens + outputTokens,
			"input_tokens_details": gin.H{"cached_tokens": 0}, "output_tokens_details": gin.H{"reasoning_tokens": reasoningTokens},
		}
	}
	return response, nil
}

func responseImageGenerationItems(output CandidateOutput, responseID string) []gin.H {
	items := make([]gin.H, 0)
	for _, media := range output.Media {
		if media.Type != gemini.MediaGeneratedImage {
			continue
		}
		_, data, ok := splitDataURL(media.URL)
		if !ok {
			continue
		}
		items = append(items, gin.H{
			"id": fmt.Sprintf("ig_%s_%d", responseID, len(items)), "type": "image_generation_call",
			"status": "completed", "result": data,
		})
	}
	return items
}

func responsesMarkdownOutput(output CandidateOutput) CandidateOutput {
	output.Codes = nil
	media := make([]gemini.Media, 0, len(output.Media))
	for _, item := range output.Media {
		if item.Type != gemini.MediaGeneratedImage {
			media = append(media, item)
		}
	}
	output.Media = media
	return output
}

func responseCodeInterpreterItems(output CandidateOutput, responseID string) []gin.H {
	type codeCall struct {
		index  int
		code   string
		logs   []gin.H
		failed bool
	}
	calls := make([]codeCall, 0)
	indexes := make(map[int]int)
	for _, event := range output.Codes {
		position, ok := indexes[event.Index]
		if !ok {
			position = len(calls)
			indexes[event.Index] = position
			calls = append(calls, codeCall{index: event.Index})
		}
		call := &calls[position]
		switch event.Type {
		case gemini.CodeReference:
			call.code = event.Content
		case gemini.CodeStdout:
			call.logs = append(call.logs, gin.H{"type": "logs", "logs": event.Content})
		case gemini.CodeStderr:
			call.logs = append(call.logs, gin.H{"type": "logs", "logs": "stderr:\n" + event.Content})
			call.failed = true
		}
	}
	items := make([]gin.H, 0, len(calls))
	for _, call := range calls {
		status := "completed"
		if call.failed {
			status = "failed"
		}
		items = append(items, gin.H{
			"id": fmt.Sprintf("ci_%s_%d", responseID, call.index), "type": "code_interpreter_call",
			"status": status, "code": call.code, "container_id": "gemini_web", "outputs": call.logs,
		})
	}
	return items
}

func responsesCitationAnnotations(text string, citations []gemini.Citation) []gin.H {
	annotations := make([]gin.H, 0, len(citations))
	for _, citation := range citations {
		start, end := citationRange(text, citation)
		annotations = append(annotations, gin.H{
			"type": "url_citation", "start_index": start, "end_index": end,
			"title": citation.Title, "url": citation.URL,
		})
	}
	return annotations
}

func responsesConversationID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var id string
	if err := json.Unmarshal(raw, &id); err == nil {
		return id, nil
	}
	var reference struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &reference); err != nil || reference.ID == "" {
		return "", fmt.Errorf("conversation 必须是字符串或包含 id 的对象")
	}
	return reference.ID, nil
}

func responseShell(id string, model string, created int64, status string) gin.H {
	return gin.H{
		"id": id, "object": "response", "created_at": created, "completed_at": nil, "status": status, "model": model,
		"output": []interface{}{}, "error": nil, "incomplete_details": nil, "instructions": nil, "metadata": gin.H{},
		"parallel_tool_calls": true, "previous_response_id": nil, "reasoning": nil,
		"temperature": 1, "text": gin.H{"format": gin.H{"type": "text"}}, "tool_choice": "auto", "tools": []interface{}{},
		"top_p": 1, "truncation": "disabled", "usage": nil,
	}
}

type responseSequenceWriter struct {
	writer             http.ResponseWriter
	sequence           int
	responseID         string
	model              string
	created            int64
	outputOrder        []string
	outputIndexes      map[string]int
	reasoningStarted   bool
	messagePartStarted bool
	imageStarted       bool
	imageGenerating    bool
	failed             bool
}

func (w *responseSequenceWriter) emit(eventType string, payload gin.H) error {
	payload["type"] = eventType
	payload["sequence_number"] = w.sequence
	w.sequence++
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w.writer, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	return streamio.Flush(w.writer)
}

func (w *responseSequenceWriter) start(responseID string, model string, created int64) error {
	w.responseID = responseID
	w.model = model
	w.created = created
	w.outputIndexes = make(map[string]int)
	if err := w.emit("response.created", gin.H{"response": responseShell(responseID, model, created, "in_progress")}); err != nil {
		return err
	}
	return w.emit("response.in_progress", gin.H{"response": responseShell(responseID, model, created, "in_progress")})
}

func (w *responseSequenceWriter) live(event gemini.Event) error {
	if event.Kind == gemini.EventThought {
		return w.reasoningDelta(event.Delta)
	}
	return w.textDelta(event.Delta)
}

func (w *responseSequenceWriter) imageProgress(phase gemini.Phase) error {
	id := "ig_" + w.responseID + "_0"
	index, err := w.ensureOutputItem(id, gin.H{"id": id, "type": "image_generation_call", "status": "in_progress", "result": nil})
	if err != nil {
		return err
	}
	if !w.imageStarted {
		if err := w.emit("response.image_generation_call.in_progress", gin.H{"item_id": id, "output_index": index}); err != nil {
			return err
		}
		w.imageStarted = true
	}
	if phase == gemini.PhaseToolComplete && !w.imageGenerating {
		if err := w.emit("response.image_generation_call.generating", gin.H{"item_id": id, "output_index": index}); err != nil {
			return err
		}
		w.imageGenerating = true
	}
	return nil
}

func (w *responseSequenceWriter) reasoningDelta(delta string) error {
	id := "rs_" + w.responseID
	index, err := w.ensureOutputItem(id, gin.H{"id": id, "type": "reasoning", "status": "in_progress", "summary": []interface{}{}})
	if err != nil {
		return err
	}
	if !w.reasoningStarted {
		if err := w.emit("response.reasoning_summary_part.added", gin.H{"item_id": id, "output_index": index, "summary_index": 0, "part": gin.H{"type": "summary_text", "text": ""}}); err != nil {
			return err
		}
		w.reasoningStarted = true
	}
	return w.emit("response.reasoning_summary_text.delta", gin.H{"item_id": id, "output_index": index, "summary_index": 0, "delta": delta})
}

func (w *responseSequenceWriter) textDelta(delta string) error {
	id := "msg_" + w.responseID
	index, err := w.ensureOutputItem(id, gin.H{"id": id, "type": "message", "status": "in_progress", "role": "assistant", "content": []interface{}{}})
	if err != nil {
		return err
	}
	if !w.messagePartStarted {
		if err := w.emit("response.content_part.added", gin.H{"item_id": id, "output_index": index, "content_index": 0, "part": gin.H{"type": "output_text", "text": "", "annotations": []interface{}{}}}); err != nil {
			return err
		}
		w.messagePartStarted = true
	}
	return w.emit("response.output_text.delta", gin.H{"item_id": id, "output_index": index, "content_index": 0, "delta": delta, "logprobs": []interface{}{}})
}

func (w *responseSequenceWriter) ensureOutputItem(id string, item gin.H) (int, error) {
	if index, ok := w.outputIndexes[id]; ok {
		return index, nil
	}
	index := len(w.outputOrder)
	w.outputOrder = append(w.outputOrder, id)
	w.outputIndexes[id] = index
	if err := w.emit("response.output_item.added", gin.H{"output_index": index, "item": item}); err != nil {
		return 0, err
	}
	return index, nil
}

func (w *responseSequenceWriter) fail(err error) error {
	if err == nil || w.failed {
		return nil
	}
	w.failed = true
	response := responseShell(w.responseID, w.model, w.created, "failed")
	response["error"] = gin.H{"code": "server_error", "message": err.Error()}
	return w.emit("response.failed", gin.H{"response": response})
}

func (w *responseSequenceWriter) finishProjected(response gin.H, accumulator *EventAccumulator, projection *streamProjection) error {
	primary := accumulator.Primary()
	if projection.includeThought && projection.bufferThought && primary.Thought != "" {
		if err := w.reasoningDelta(primary.Thought); err != nil {
			return err
		}
	}
	output, _ := response["output"].([]gin.H)
	if projection.bufferText && projection.emittedText {
		for _, item := range output {
			if item["type"] != "message" {
				continue
			}
			parts, _ := item["content"].([]gin.H)
			if len(parts) > 0 {
				markdownOutput := responsesMarkdownOutput(primary)
				text := renderCandidateMarkdown(primary.Text, markdownOutput, projection.textRunes)
				if text != "" {
					if err := w.textDelta(text); err != nil {
						return err
					}
				}
			}
			break
		}
	}
	output = w.orderOutput(output)
	response["output"] = output
	for _, item := range output {
		id, _ := item["id"].(string)
		index, err := w.ensureOutputItem(id, addedResponseItem(item))
		if err != nil {
			return err
		}
		switch item["type"] {
		case "reasoning":
			if err := w.finishReasoning(index, item); err != nil {
				return err
			}
		case "message":
			if err := w.finishMessage(index, item); err != nil {
				return err
			}
		case "function_call":
			arguments, _ := item["arguments"].(string)
			if err := w.emit("response.function_call_arguments.delta", gin.H{"item_id": id, "output_index": index, "delta": arguments}); err != nil {
				return err
			}
			if err := w.emit("response.function_call_arguments.done", gin.H{"item_id": id, "output_index": index, "arguments": arguments, "name": item["name"]}); err != nil {
				return err
			}
		case "code_interpreter_call":
			code, _ := item["code"].(string)
			if err := w.emit("response.code_interpreter_call.in_progress", gin.H{"item_id": id, "output_index": index}); err != nil {
				return err
			}
			if code != "" {
				if err := w.emit("response.code_interpreter_call_code.delta", gin.H{"item_id": id, "output_index": index, "delta": code}); err != nil {
					return err
				}
				if err := w.emit("response.code_interpreter_call_code.done", gin.H{"item_id": id, "output_index": index, "code": code}); err != nil {
					return err
				}
			}
			if err := w.emit("response.code_interpreter_call.interpreting", gin.H{"item_id": id, "output_index": index}); err != nil {
				return err
			}
			if err := w.emit("response.code_interpreter_call.completed", gin.H{"item_id": id, "output_index": index}); err != nil {
				return err
			}
		case "web_search_call":
			if err := w.emit("response.web_search_call.in_progress", gin.H{"item_id": id, "output_index": index}); err != nil {
				return err
			}
			if err := w.emit("response.web_search_call.searching", gin.H{"item_id": id, "output_index": index}); err != nil {
				return err
			}
			if err := w.emit("response.web_search_call.completed", gin.H{"item_id": id, "output_index": index}); err != nil {
				return err
			}
		case "image_generation_call":
			if !w.imageStarted {
				if err := w.emit("response.image_generation_call.in_progress", gin.H{"item_id": id, "output_index": index}); err != nil {
					return err
				}
				w.imageStarted = true
			}
			if !w.imageGenerating {
				if err := w.emit("response.image_generation_call.generating", gin.H{"item_id": id, "output_index": index}); err != nil {
					return err
				}
				w.imageGenerating = true
			}
			if err := w.emit("response.image_generation_call.completed", gin.H{"item_id": id, "output_index": index}); err != nil {
				return err
			}
		}
		if err := w.emit("response.output_item.done", gin.H{"output_index": index, "item": item}); err != nil {
			return err
		}
	}
	return w.emit("response.completed", gin.H{"response": response})
}

func (w *responseSequenceWriter) finishReasoning(index int, item gin.H) error {
	id, _ := item["id"].(string)
	summaries, _ := item["summary"].([]gin.H)
	for summaryIndex, summary := range summaries {
		text, _ := summary["text"].(string)
		if !w.reasoningStarted {
			if err := w.emit("response.reasoning_summary_part.added", gin.H{"item_id": id, "output_index": index, "summary_index": summaryIndex, "part": gin.H{"type": "summary_text", "text": ""}}); err != nil {
				return err
			}
			if err := w.emit("response.reasoning_summary_text.delta", gin.H{"item_id": id, "output_index": index, "summary_index": summaryIndex, "delta": text}); err != nil {
				return err
			}
			w.reasoningStarted = true
		}
		if err := w.emit("response.reasoning_summary_text.done", gin.H{"item_id": id, "output_index": index, "summary_index": summaryIndex, "text": text}); err != nil {
			return err
		}
		if err := w.emit("response.reasoning_summary_part.done", gin.H{"item_id": id, "output_index": index, "summary_index": summaryIndex, "part": summary}); err != nil {
			return err
		}
	}
	return nil
}

func (w *responseSequenceWriter) finishMessage(index int, item gin.H) error {
	id, _ := item["id"].(string)
	parts, _ := item["content"].([]gin.H)
	for contentIndex, part := range parts {
		text, _ := part["text"].(string)
		if contentIndex != 0 || !w.messagePartStarted {
			emptyPart := gin.H{"type": "output_text", "text": "", "annotations": []interface{}{}}
			if err := w.emit("response.content_part.added", gin.H{"item_id": id, "output_index": index, "content_index": contentIndex, "part": emptyPart}); err != nil {
				return err
			}
			if err := w.emit("response.output_text.delta", gin.H{"item_id": id, "output_index": index, "content_index": contentIndex, "delta": text, "logprobs": []interface{}{}}); err != nil {
				return err
			}
		}
		annotations, _ := part["annotations"].([]gin.H)
		for annotationIndex, annotation := range annotations {
			if err := w.emit("response.output_text.annotation.added", gin.H{
				"item_id": id, "output_index": index, "content_index": contentIndex,
				"annotation_index": annotationIndex, "annotation": annotation,
			}); err != nil {
				return err
			}
		}
		if err := w.emit("response.output_text.done", gin.H{"item_id": id, "output_index": index, "content_index": contentIndex, "text": text, "logprobs": []interface{}{}}); err != nil {
			return err
		}
		if err := w.emit("response.content_part.done", gin.H{"item_id": id, "output_index": index, "content_index": contentIndex, "part": part}); err != nil {
			return err
		}
	}
	return nil
}

func (w *responseSequenceWriter) orderOutput(output []gin.H) []gin.H {
	byID := make(map[string]gin.H, len(output))
	for _, item := range output {
		id, _ := item["id"].(string)
		byID[id] = item
	}
	ordered := make([]gin.H, 0, len(output))
	for _, id := range w.outputOrder {
		if item, ok := byID[id]; ok {
			ordered = append(ordered, item)
			delete(byID, id)
		}
	}
	for _, item := range output {
		id, _ := item["id"].(string)
		if _, ok := byID[id]; ok {
			ordered = append(ordered, item)
			delete(byID, id)
		}
	}
	return ordered
}

func addedResponseItem(item gin.H) gin.H {
	added := gin.H{}
	for key, value := range item {
		added[key] = value
	}
	added["status"] = "in_progress"
	switch added["type"] {
	case "message":
		added["content"] = []interface{}{}
	case "reasoning":
		added["summary"] = []interface{}{}
	case "function_call":
		added["arguments"] = ""
	case "code_interpreter_call":
		added["code"] = ""
		added["outputs"] = nil
	case "image_generation_call":
		added["result"] = nil
	}
	return added
}

func unsupportedResponsesParameters(request ResponsesRequest) []string {
	var fields []string
	if request.MaxOutputTokens != nil {
		fields = append(fields, "max_output_tokens")
	}
	if request.Temperature != nil {
		fields = append(fields, "temperature")
	}
	if request.TopP != nil {
		fields = append(fields, "top_p")
	}
	if request.Truncation != "" {
		fields = append(fields, "truncation")
	}
	if request.ParallelToolCalls != nil {
		fields = append(fields, "parallel_tool_calls")
	}
	for index, tool := range request.Tools {
		if tool.Type != "image_generation" {
			continue
		}
		prefix := fmt.Sprintf("tools[%d].", index)
		if tool.Action != "" {
			fields = append(fields, prefix+"action")
		}
		if tool.Background != "" {
			fields = append(fields, prefix+"background")
		}
		if tool.InputFidelity != "" {
			fields = append(fields, prefix+"input_fidelity")
		}
		if len(tool.InputImageMask) > 0 && string(tool.InputImageMask) != "null" {
			fields = append(fields, prefix+"input_image_mask")
		}
		if tool.ImageModel != "" {
			fields = append(fields, prefix+"model")
		}
		if tool.Moderation != "" {
			fields = append(fields, prefix+"moderation")
		}
		if tool.OutputCompression != nil {
			fields = append(fields, prefix+"output_compression")
		}
		if tool.OutputFormat != "" {
			fields = append(fields, prefix+"output_format")
		}
		if tool.PartialImages != nil {
			fields = append(fields, prefix+"partial_images")
		}
		if tool.Quality != "" {
			fields = append(fields, prefix+"quality")
		}
		if tool.Size != "" {
			fields = append(fields, prefix+"size")
		}
	}
	return fields
}
