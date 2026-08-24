package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// StreamingState 保存 Claude SSE 的输出状态
type StreamingState struct {
	MessageID        string
	Model            string
	MessageStartSent bool
	MessageStopSent  bool
	BlockIndex       int
	CurrentBlockType string
	Usage            *gemini.Usage
	ConversationID   string
}

// NewStreamingState 创建 Claude SSE 状态
func NewStreamingState(model string) *StreamingState {
	return &StreamingState{
		MessageID: fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Model:     model,
		Usage:     &gemini.Usage{},
	}
}

// StreamProcessor 将规范事件编码为 Claude SSE
type StreamProcessor struct {
	state  *StreamingState
	writer io.Writer
}

// NewStreamProcessor 创建 Claude SSE 编码器
func NewStreamProcessor(model string, writer io.Writer, messageIDs ...string) *StreamProcessor {
	state := NewStreamingState(model)
	if len(messageIDs) > 0 && messageIDs[0] != "" {
		state.MessageID = messageIDs[0]
	}
	return &StreamProcessor{state: state, writer: writer}
}

// SetConversationID 设置最终消息携带的显式会话标识
func (p *StreamProcessor) SetConversationID(conversationID string) {
	p.state.ConversationID = conversationID
}

// SetModel 设置校验后的实际模型
func (p *StreamProcessor) SetModel(model string) {
	if !p.state.MessageStartSent && model != "" {
		p.state.Model = model
	}
}

// MessageID 返回当前 Claude 消息标识
func (p *StreamProcessor) MessageID() string {
	return p.state.MessageID
}

// ProcessEvent 消费一个 Gemini 规范事件
func (p *StreamProcessor) ProcessEvent(event gemini.Event) error {
	if event.Usage != nil {
		p.state.Usage = event.Usage
		if event.Kind == gemini.EventMetadata {
			return p.ensureStarted()
		}
	}

	switch event.Kind {
	case gemini.EventText:
		return p.emitContent("text", event)
	case gemini.EventThought:
		return p.emitContent("thinking", event)
	case gemini.EventMedia:
		if event.Media != nil && event.Media.URL != "" {
			text := fmt.Sprintf("[%s](%s)", event.Media.Title, event.Media.URL)
			if event.Media.Type == gemini.MediaGeneratedImage {
				text = fmt.Sprintf("![%s](%s)", event.Media.Alt, event.Media.URL)
			}
			return p.emitContent("text", gemini.Event{
				Kind:      gemini.EventText,
				Operation: gemini.SnapshotAppend,
				Delta:     text,
			})
		}
	case gemini.EventError:
		if event.Err != nil {
			return event.Err
		}
	case gemini.EventDone:
		return p.Finish(mapFinishReason(event.FinishReason))
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
		"index": p.state.BlockIndex,
		"content_block": map[string]interface{}{
			"type": "tool_use", "id": id, "name": name, "input": map[string]interface{}{},
		},
	}); err != nil {
		return err
	}
	p.state.CurrentBlockType = "tool_use"
	if err := p.emit("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": p.state.BlockIndex,
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
		"index": p.state.BlockIndex,
		"delta": map[string]interface{}{"type": "signature_delta", "signature": signature},
	}); err != nil {
		return err
	}
	return p.closeBlock()
}

// Finish 完成 Claude SSE 消息
func (p *StreamProcessor) Finish(stopReason string) error {
	if p.state.MessageStopSent {
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
		"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
	}
	if p.state.ConversationID != "" {
		messageDelta["conversation_id"] = p.state.ConversationID
	}
	messageDelta["usage"] = map[string]int{"output_tokens": p.state.Usage.OutputTokens()}
	if err := p.emit("message_delta", messageDelta); err != nil {
		return err
	}
	p.state.MessageStopSent = true
	return p.emit("message_stop", map[string]interface{}{"type": "message_stop"})
}

// EmitError 输出 Claude 流式错误
func (p *StreamProcessor) EmitError(err error) error {
	if err == nil {
		return nil
	}
	return p.emit("error", map[string]interface{}{
		"type":  "error",
		"error": map[string]string{"type": "api_error", "message": err.Error()},
	})
}

func (p *StreamProcessor) emitContent(blockType string, event gemini.Event) error {
	if err := p.ensureStarted(); err != nil {
		return err
	}
	if p.state.CurrentBlockType != blockType {
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
			"type": "content_block_start", "index": p.state.BlockIndex, "content_block": block,
		}); err != nil {
			return err
		}
		p.state.CurrentBlockType = blockType
	}

	deltaType := "text_delta"
	field := "text"
	if blockType == "thinking" {
		deltaType = "thinking_delta"
		field = "thinking"
	}
	delta := map[string]interface{}{"type": deltaType, field: event.Delta}
	return p.emit("content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": p.state.BlockIndex, "delta": delta,
	})
}

func (p *StreamProcessor) ensureStarted() error {
	if p.state.MessageStartSent {
		return nil
	}
	p.state.MessageStartSent = true
	message := map[string]interface{}{
		"id": p.state.MessageID, "type": "message", "role": "assistant", "model": p.state.Model,
		"content": []interface{}{}, "stop_reason": nil, "stop_sequence": nil,
	}
	message["usage"] = map[string]int{"input_tokens": p.state.Usage.PromptTokens, "output_tokens": 0}
	return p.emit("message_start", map[string]interface{}{"type": "message_start", "message": message})
}

func (p *StreamProcessor) closeBlock() error {
	if p.state.CurrentBlockType == "" {
		return nil
	}
	err := p.emit("content_block_stop", map[string]interface{}{
		"type": "content_block_stop", "index": p.state.BlockIndex,
	})
	p.state.BlockIndex++
	p.state.CurrentBlockType = ""
	return err
}

func (p *StreamProcessor) emit(eventType string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(p.writer, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	if flusher, ok := p.writer.(interface{ Flush() }); ok {
		flusher.Flush()
	}
	return nil
}

func mapFinishReason(reason gemini.FinishReason) string {
	switch reason {
	case gemini.FinishCancelled, gemini.FinishError:
		return "stop_sequence"
	default:
		return "end_turn"
	}
}
