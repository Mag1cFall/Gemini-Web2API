package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"
	"time"
)

type StreamingState struct {
	MessageID        string
	Model            string
	MessageStartSent bool
	MessageStopSent  bool
	BlockIndex       int
	CurrentBlockType string
	InputTokens      int
	OutputTokens     int
	Buffer           bytes.Buffer
}

func NewStreamingState(model string) *StreamingState {
	return &StreamingState{
		MessageID:  fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Model:      model,
		BlockIndex: 0,
	}
}

func (s *StreamingState) EmitMessageStart() string {
	if s.MessageStartSent {
		return ""
	}
	s.MessageStartSent = true

	event := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            s.MessageID,
			"type":          "message",
			"role":          "assistant",
			"model":         s.Model,
			"content":       []interface{}{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	}

	data, _ := json.Marshal(event)
	return fmt.Sprintf("event: message_start\ndata: %s\n\n", data)
}

func (s *StreamingState) EmitContentBlockStart(blockType string, extra map[string]interface{}) string {
	block := map[string]interface{}{
		"type":  blockType,
		"index": s.BlockIndex,
	}

	switch blockType {
	case "text":
		block["text"] = ""
	case "thinking":
		block["thinking"] = ""
	case "tool_use":
		if extra != nil {
			block["id"] = extra["id"]
			block["name"] = extra["name"]
			block["input"] = map[string]interface{}{}
		}
	}

	event := map[string]interface{}{
		"type":          "content_block_start",
		"index":         s.BlockIndex,
		"content_block": block,
	}

	s.CurrentBlockType = blockType
	data, _ := json.Marshal(event)
	return fmt.Sprintf("event: content_block_start\ndata: %s\n\n", data)
}

func (s *StreamingState) EmitContentBlockDelta(blockType string, content string) string {
	var delta map[string]interface{}

	switch blockType {
	case "text":
		delta = map[string]interface{}{
			"type": "text_delta",
			"text": content,
		}
	case "thinking":
		delta = map[string]interface{}{
			"type":     "thinking_delta",
			"thinking": content,
		}
	default:
		delta = map[string]interface{}{
			"type": "text_delta",
			"text": content,
		}
	}

	event := map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.BlockIndex,
		"delta": delta,
	}

	data, _ := json.Marshal(event)
	return fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", data)
}

func (s *StreamingState) EmitContentBlockStop() string {
	event := map[string]interface{}{
		"type":  "content_block_stop",
		"index": s.BlockIndex,
	}

	s.BlockIndex++
	s.CurrentBlockType = ""
	data, _ := json.Marshal(event)
	return fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", data)
}

func (s *StreamingState) EmitMessageDelta(stopReason string, outputTokens int) string {
	event := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"output_tokens": outputTokens,
		},
	}

	data, _ := json.Marshal(event)
	return fmt.Sprintf("event: message_delta\ndata: %s\n\n", data)
}

func (s *StreamingState) EmitMessageStop() string {
	if s.MessageStopSent {
		return ""
	}
	s.MessageStopSent = true

	event := map[string]interface{}{
		"type": "message_stop",
	}

	data, _ := json.Marshal(event)
	return fmt.Sprintf("event: message_stop\ndata: %s\n\n", data)
}

type StreamProcessor struct {
	state          *StreamingState
	writer         io.Writer
	inThinkingMode bool
	inTextMode     bool
	inToolUse      bool
	toolUseBuffer  bytes.Buffer
}

func NewStreamProcessor(model string, writer io.Writer) *StreamProcessor {
	return &StreamProcessor{
		state:  NewStreamingState(model),
		writer: writer,
	}
}

func (p *StreamProcessor) ProcessGeminiStream(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimPrefix(line, ")]}'")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if err := p.processLine(line); err != nil {
			continue
		}
	}

	p.finalize()
	return scanner.Err()
}

func (p *StreamProcessor) processLine(line string) error {
	var outer []json.RawMessage
	if err := json.Unmarshal([]byte(line), &outer); err != nil {
		return err
	}

	for _, item := range outer {
		var arr []json.RawMessage
		if err := json.Unmarshal(item, &arr); err != nil {
			continue
		}

		if len(arr) < 3 {
			continue
		}

		var dataStr string
		if err := json.Unmarshal(arr[2], &dataStr); err != nil {
			continue
		}

		if dataStr == "" {
			continue
		}

		var inner map[string]interface{}
		if err := json.Unmarshal([]byte(dataStr), &inner); err != nil {
			continue
		}

		p.processGeminiData(inner)
	}

	return nil
}

func (p *StreamProcessor) processGeminiData(data map[string]interface{}) {
	if !p.state.MessageStartSent {
		p.emit(p.state.EmitMessageStart())
	}

	candidates, ok := data["4"].([]interface{})
	if !ok {
		return
	}

	for _, candidate := range candidates {
		candidateMap, ok := candidate.(map[string]interface{})
		if !ok {
			continue
		}

		content, ok := candidateMap["1"].(map[string]interface{})
		if !ok {
			continue
		}

		parts, ok := content["1"].([]interface{})
		if !ok {
			continue
		}

		for _, part := range parts {
			partArr, ok := part.([]interface{})
			if !ok {
				continue
			}

			p.processPart(partArr)
		}
	}
}

func (p *StreamProcessor) processPart(part []interface{}) {
	if len(part) == 0 {
		return
	}

	text, _ := part[0].(string)
	if text == "" {
		return
	}

	text = strings.ReplaceAll(text, `\<`, `<`)
	text = strings.ReplaceAll(text, `\>`, `>`)
	text = strings.ReplaceAll(text, `\_`, `_`)
	text = strings.ReplaceAll(text, `\[`, `[`)
	text = strings.ReplaceAll(text, `\]`, `]`)

	// Decode HTML entities
	text = html.UnescapeString(text)

	isThought := false
	if len(part) > 2 {
		if thought, ok := part[2].(bool); ok && thought {
			isThought = true
		}
	}

	if isThought {
		if p.inTextMode {
			p.emit(p.state.EmitContentBlockStop())
			p.inTextMode = false
		}
		if !p.inThinkingMode {
			p.emit(p.state.EmitContentBlockStart("thinking", nil))
			p.inThinkingMode = true
		}
		p.emit(p.state.EmitContentBlockDelta("thinking", text))
	} else {
		if p.inThinkingMode {
			p.emit(p.state.EmitContentBlockStop())
			p.inThinkingMode = false
		}
		if !p.inTextMode {
			p.emit(p.state.EmitContentBlockStart("text", nil))
			p.inTextMode = true
		}
		p.emit(p.state.EmitContentBlockDelta("text", text))
	}
}

func (p *StreamProcessor) finalize() {
	if p.inThinkingMode {
		p.emit(p.state.EmitContentBlockStop())
	}
	if p.inTextMode {
		p.emit(p.state.EmitContentBlockStop())
	}

	p.emit(p.state.EmitMessageDelta("end_turn", p.state.OutputTokens))
	p.emit(p.state.EmitMessageStop())
}

func (p *StreamProcessor) emit(data string) {
	if data != "" {
		p.writer.Write([]byte(data))
		if flusher, ok := p.writer.(interface{ Flush() }); ok {
			flusher.Flush()
		}
	}
}
