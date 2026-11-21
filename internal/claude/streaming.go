package claude

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Mag1cFall/Gemini-Web2API/internal/fieldorder"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/streamio"
)

// StreamProcessor 将规范事件编码为 Claude SSE
type StreamProcessor struct {
	writer           io.Writer
	messageID        string
	model            string
	messageStartSent bool
	messageStopSent  bool
	blockIndex       int
	currentBlockType string
	usage            *gemini.Usage
	conversationID   string
	// providerModel 是上游实际回答的模型 ID，写在 message_delta 中
	providerModel string
	// webSearchRequests 与 webFetchRequests 为 message_delta usage.server_tool_use 的搜索与读取网页次数
	webSearchRequests int
	webFetchRequests  int
	// stopSequence 为 message_delta 中命中的停止序列
	stopSequence *string
	// contextManagement 与 iterations 为 message_delta 中生效的上下文编辑与各次采样的用量
	contextManagement map[string]any
	iterations        []map[string]any
}

// NewStreamProcessor 创建 Claude SSE 编码器，messageID 为 message_start 中的消息 ID
func NewStreamProcessor(model string, writer io.Writer, messageID string) *StreamProcessor {
	return &StreamProcessor{writer: writer, messageID: messageID, model: model, usage: &gemini.Usage{}}
}

// SetConversationID 设置最终消息携带的显式会话标识
func (p *StreamProcessor) SetConversationID(conversationID string) {
	p.conversationID = conversationID
}

// SetProviderModel 设置 message_delta 携带的上游实际模型
func (p *StreamProcessor) SetProviderModel(model string) {
	p.providerModel = model
}

// SetModel 设置校验后的实际模型
func (p *StreamProcessor) SetModel(model string) {
	if !p.messageStartSent && model != "" {
		p.model = model
	}
}

// ProcessEvent 消费一个 Gemini 规范事件
func (p *StreamProcessor) ProcessEvent(event gemini.Event) error {
	if event.Usage != nil {
		p.usage = event.Usage
		if event.Kind == gemini.EventMetadata {
			return p.ensureStarted()
		}
	}

	switch event.Kind {
	case gemini.EventText:
		return p.emitContent("text", event)
	case gemini.EventThought:
		return p.emitContent("thinking", event)
	}
	return nil
}

// EmitToolCall 输出完整 Claude 工具调用块
func (p *StreamProcessor) EmitToolCall(id string, name string, arguments json.RawMessage) error {
	if err := p.ensureStarted(); err != nil {
		return err
	}
	if err := p.closeBlock(); err != nil {
		return err
	}
	if err := p.emit("content_block_start", map[string]interface{}{
		"type":  "content_block_start",
		"index": p.blockIndex,
		"content_block": map[string]interface{}{
			"type": "tool_use", "id": id, "name": name, "input": map[string]interface{}{},
		},
	}); err != nil {
		return err
	}
	p.currentBlockType = "tool_use"
	if err := p.emit("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": p.blockIndex,
		"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": string(arguments)},
	}); err != nil {
		return err
	}
	return p.closeBlock()
}

// EmitThinking 输出带代理签名的完整思考块
func (p *StreamProcessor) EmitThinking(thinking string, signature string) error {
	if err := p.emitContent("thinking", gemini.Event{Kind: gemini.EventThought, Operation: gemini.SnapshotAppend, Delta: thinking}); err != nil {
		return err
	}
	return p.FinishThinking(signature)
}

// FinishThinking 完成当前思考块并输出签名
func (p *StreamProcessor) FinishThinking(signature string) error {
	if err := p.emit("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": p.blockIndex,
		"delta": map[string]interface{}{"type": "signature_delta", "signature": signature},
	}); err != nil {
		return err
	}
	return p.closeBlock()
}

// SetContextManagement 设置 message_delta 携带的上下文编辑结果与各次采样的用量
func (p *StreamProcessor) SetContextManagement(management map[string]any, iterations []map[string]any) {
	p.contextManagement, p.iterations = management, iterations
}

// EmitCompaction 输出本次请求压缩上下文得到的 compaction 块，摘要以 compaction_delta 发送
func (p *StreamProcessor) EmitCompaction(summary string) error {
	if err := p.ensureStarted(); err != nil {
		return err
	}
	if err := p.closeBlock(); err != nil {
		return err
	}
	if err := p.emit("content_block_start", map[string]interface{}{
		"type": "content_block_start", "index": p.blockIndex,
		"content_block": map[string]interface{}{"type": "compaction", "content": nil, "encrypted_content": nil},
	}); err != nil {
		return err
	}
	p.currentBlockType = "compaction"
	if err := p.emit("content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": p.blockIndex,
		"delta": map[string]interface{}{"type": "compaction_delta", "content": summary, "encrypted_content": nil},
	}); err != nil {
		return err
	}
	return p.closeBlock()
}

// FinishStopSequence 以命中的停止序列完成 Claude SSE 消息
func (p *StreamProcessor) FinishStopSequence(sequence string) error {
	p.stopSequence = &sequence
	return p.Finish("stop_sequence")
}

// Finish 完成 Claude SSE 消息
func (p *StreamProcessor) Finish(stopReason string) error {
	if p.messageStopSent {
		return nil
	}
	if err := p.ensureStarted(); err != nil {
		return err
	}
	if err := p.closeBlock(); err != nil {
		return err
	}

	messageDelta := map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": p.stopSequence},
	}
	if p.conversationID != "" {
		messageDelta["conversation_id"] = p.conversationID
	}
	if p.providerModel != "" {
		messageDelta["provider_model"] = p.providerModel
	}
	usage := map[string]any{"output_tokens": p.usage.OutputTokens()}
	if p.webSearchRequests > 0 || p.webFetchRequests > 0 {
		usage["server_tool_use"] = map[string]int{"web_search_requests": p.webSearchRequests, "web_fetch_requests": p.webFetchRequests}
	}
	if p.iterations != nil {
		usage["iterations"] = p.iterations
	}
	messageDelta["usage"] = usage
	if p.contextManagement != nil {
		messageDelta["context_management"] = p.contextManagement
	}
	if err := p.emit("message_delta", messageDelta); err != nil {
		return err
	}
	p.messageStopSent = true
	return p.emit("message_stop", map[string]interface{}{"type": "message_stop"})
}

// EmitErrorType 以指定错误类型输出 Claude 流式错误
func (p *StreamProcessor) EmitErrorType(errorType string, err error) error {
	if err == nil {
		return nil
	}
	return p.emit("error", map[string]interface{}{
		"type":  "error",
		"error": map[string]string{"type": errorType, "message": err.Error()},
	})
}

// EmitServerToolUse 输出一次服务端工具调用块，输入以 input_json_delta 发送
func (p *StreamProcessor) EmitServerToolUse(id string, name string, input any) error {
	if err := p.ensureStarted(); err != nil {
		return err
	}
	if err := p.closeBlock(); err != nil {
		return err
	}
	if err := p.emit("content_block_start", map[string]interface{}{
		"type": "content_block_start", "index": p.blockIndex,
		"content_block": map[string]interface{}{"type": "server_tool_use", "id": id, "name": name, "input": map[string]interface{}{}},
	}); err != nil {
		return err
	}
	p.currentBlockType = "server_tool_use"
	arguments, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if err := p.emit("content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": p.blockIndex,
		"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": string(arguments)},
	}); err != nil {
		return err
	}
	switch name {
	case "web_search":
		p.webSearchRequests++
	case "web_fetch":
		p.webFetchRequests++
	}
	return p.closeBlock()
}

// EmitBlock 以一对 content_block_start 与 content_block_stop 输出完整内容块，例如 web_search_tool_result
func (p *StreamProcessor) EmitBlock(block map[string]any) error {
	if err := p.ensureStarted(); err != nil {
		return err
	}
	if err := p.closeBlock(); err != nil {
		return err
	}
	if err := p.emit("content_block_start", map[string]interface{}{
		"type": "content_block_start", "index": p.blockIndex, "content_block": block,
	}); err != nil {
		return err
	}
	p.currentBlockType = fmt.Sprint(block["type"])
	return p.closeBlock()
}

// EmitCitedText 输出一个带引用的文本块，引用以 citations_delta 先于正文发送
func (p *StreamProcessor) EmitCitedText(text string, citations []map[string]any) error {
	if err := p.ensureStarted(); err != nil {
		return err
	}
	if err := p.closeBlock(); err != nil {
		return err
	}
	block := map[string]interface{}{"type": "text", "text": ""}
	if len(citations) > 0 {
		block["citations"] = []any{}
	}
	if err := p.emit("content_block_start", map[string]interface{}{
		"type": "content_block_start", "index": p.blockIndex, "content_block": block,
	}); err != nil {
		return err
	}
	p.currentBlockType = "cited_text"
	for _, citation := range citations {
		if err := p.emit("content_block_delta", map[string]interface{}{
			"type": "content_block_delta", "index": p.blockIndex,
			"delta": map[string]interface{}{"type": "citations_delta", "citation": citation},
		}); err != nil {
			return err
		}
	}
	if err := p.emit("content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": p.blockIndex,
		"delta": map[string]interface{}{"type": "text_delta", "text": text},
	}); err != nil {
		return err
	}
	return p.closeBlock()
}

func (p *StreamProcessor) emitContent(blockType string, event gemini.Event) error {
	if err := p.ensureStarted(); err != nil {
		return err
	}
	if p.currentBlockType != blockType {
		if err := p.closeBlock(); err != nil {
			return err
		}
		block := map[string]interface{}{"type": blockType}
		if blockType == "thinking" {
			block["thinking"] = ""
			block["signature"] = ""
		} else {
			block["text"] = ""
		}
		if err := p.emit("content_block_start", map[string]interface{}{
			"type": "content_block_start", "index": p.blockIndex, "content_block": block,
		}); err != nil {
			return err
		}
		p.currentBlockType = blockType
	}

	deltaType := "text_delta"
	field := "text"
	if blockType == "thinking" {
		deltaType = "thinking_delta"
		field = "thinking"
	}
	delta := map[string]interface{}{"type": deltaType, field: event.Delta}
	return p.emit("content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": p.blockIndex, "delta": delta,
	})
}

func (p *StreamProcessor) ensureStarted() error {
	if p.messageStartSent {
		return nil
	}
	p.messageStartSent = true
	message := map[string]interface{}{
		"id": p.messageID, "type": "message", "role": "assistant", "model": p.model,
		"content": []interface{}{}, "stop_reason": nil, "stop_sequence": nil,
	}
	message["usage"] = map[string]int{"input_tokens": p.usage.PromptTokens, "output_tokens": 0}
	return p.emit("message_start", map[string]interface{}{"type": "message_start", "message": message})
}

func (p *StreamProcessor) closeBlock() error {
	if p.currentBlockType == "" {
		return nil
	}
	err := p.emit("content_block_stop", map[string]interface{}{
		"type": "content_block_stop", "index": p.blockIndex,
	})
	p.blockIndex++
	p.currentBlockType = ""
	return err
}

func (p *StreamProcessor) emit(eventType string, payload interface{}) error {
	data, err := json.Marshal(fieldorder.Official("anthropic", payload))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(p.writer, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	return streamio.Flush(p.writer)
}
