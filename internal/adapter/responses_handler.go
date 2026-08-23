package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
)

// ResponsesTool 表示 Responses API 函数工具
type ResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
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
		thinkingMode, err := responsesThinkingMode(request)
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
			if err := writer.start(responseID, request.Model, created); err != nil {
				return
			}
			projection := newStreamProjection(bridge)
			result, accountID, err := runGeneration(
				c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", request.Model, responseID, false, thinkingMode,
				func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
					return buildResponsesPrompt(c, client, request, continuation, bridge)
				}, func(event gemini.Event) error {
					return projection.project(event, writer.live, writer.fail)
				},
			)
			c.Set("account_id", accountID)
			if err != nil {
				if !projection.errorSent {
					_ = writer.fail(err)
				}
				return
			}
			response, err := buildResponsesObject(responseID, request.Model, created, request.PreviousResponseID, result, bridge)
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
			c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", request.Model, responseID, false, thinkingMode,
			func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
				return buildResponsesPrompt(c, client, request, continuation, bridge)
			}, nil,
		)
		c.Set("account_id", accountID)
		if err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}

		response, err := buildResponsesObject(responseID, request.Model, created, request.PreviousResponseID, result, bridge)
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
		default:
			return nil, fmt.Errorf("不支持的 input item 类型 %q", envelope.Type)
		}
	}
	return messages, nil
}

func responsesPriorToolCall(id string, name string, arguments string) OpenAIToolCall {
	call := OpenAIToolCall{ID: id, Type: "function"}
	call.Function.Name, call.Function.Arguments = name, arguments
	return call
}

func responsesToolBridge(request ResponsesRequest) (ToolBridge, error) {
	bridge := ToolBridge{Choice: openAIToolChoice(request.ToolChoice)}
	for _, tool := range request.Tools {
		if tool.Type != "function" || tool.Name == "" {
			return ToolBridge{}, fmt.Errorf("仅支持 function 工具")
		}
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		bridge.Definitions = append(bridge.Definitions, ToolDefinition{Name: tool.Name, Description: tool.Description, Parameters: parameters})
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
	return bridge, nil
}

func buildResponsesObject(id string, model string, created int64, previousResponseID string, result generationResult, bridge ToolBridge) (gin.H, error) {
	primary := result.Accumulator.Primary()
	content, calls, err := bridge.Parse(primary.Text)
	if err != nil {
		return nil, err
	}
	calls = assignToolCallIDs(calls, id)
	output := make([]gin.H, 0)
	if primary.Thought != "" {
		output = append(output, gin.H{"id": "rs_" + id, "type": "reasoning", "summary": []gin.H{{"type": "summary_text", "text": primary.Thought}}})
	}
	if content != "" || len(primary.Images) > 0 {
		parts := []gin.H{}
		if content != "" {
			parts = append(parts, gin.H{"type": "output_text", "text": content, "annotations": []interface{}{}})
		}
		for _, image := range primary.Images {
			parts = append(parts, gin.H{"type": "output_text", "text": fmt.Sprintf("![%s](%s)", image.Alt, image.URL), "annotations": []interface{}{}})
		}
		output = append(output, gin.H{"id": "msg_" + id, "type": "message", "status": "completed", "role": "assistant", "content": parts})
	}
	for _, call := range calls {
		output = append(output, gin.H{"id": "fc_" + call.ID, "type": "function_call", "status": "completed", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
	}
	response := responseShell(id, model, created, "completed")
	if previousResponseID != "" {
		response["previous_response_id"] = previousResponseID
	}
	response["completed_at"] = time.Now().Unix()
	response["output"], response["conversation_id"], response["provider_model"] = output, result.ConversationID, result.ProviderModel
	if result.Accumulator.Usage != nil {
		response["usage"] = gin.H{
			"input_tokens": result.Accumulator.Usage.PromptTokens, "output_tokens": result.Accumulator.Usage.OutputTokens(), "total_tokens": result.Accumulator.Usage.TotalTokens,
			"input_tokens_details": gin.H{"cached_tokens": 0}, "output_tokens_details": gin.H{"reasoning_tokens": result.Accumulator.Usage.ThoughtTokens},
		}
	}
	return response, nil
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
	flushWriter(w.writer)
	return nil
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
	response["error"] = gin.H{"code": "upstream_error", "message": err.Error()}
	return w.emit("response.failed", gin.H{"response": response})
}

func (w *responseSequenceWriter) finishProjected(response gin.H, accumulator *EventAccumulator, projection *streamProjection) error {
	primary := accumulator.Primary()
	if projection.bufferThought && primary.Thought != "" {
		if err := w.reasoningDelta(primary.Thought); err != nil {
			return err
		}
	}
	output, _ := response["output"].([]gin.H)
	if projection.bufferText {
		for _, item := range output {
			if item["type"] != "message" {
				continue
			}
			parts, _ := item["content"].([]gin.H)
			if len(parts) > 0 {
				text, _ := parts[0]["text"].(string)
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
	return fields
}
