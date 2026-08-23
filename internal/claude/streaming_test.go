package claude

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

func TestStreamProcessorEmitsThoughtAndConversation(t *testing.T) {
	var output bytes.Buffer
	processor := NewStreamProcessor("gemini-test", &output, "msg_test")
	processor.SetConversationID("conversation_test")
	if err := processor.EmitThinking("reason", "signature_test"); err != nil {
		t.Fatalf("输出 thought 失败: %v", err)
	}
	if err := processor.ProcessEvent(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: "answer"}); err != nil {
		t.Fatalf("输出 text 失败: %v", err)
	}
	if err := processor.Finish("end_turn"); err != nil {
		t.Fatalf("完成消息失败: %v", err)
	}
	stream := output.String()
	for _, expected := range []string{"thinking_delta", "signature_delta", "signature_test", "text_delta", "msg_test", "conversation_test", `"usage":{"output_tokens":0}`, "message_stop"} {
		if !strings.Contains(stream, expected) {
			t.Fatalf("流中缺少 %q: %s", expected, stream)
		}
	}
}

func TestStreamProcessorFinishesIncrementalThinking(t *testing.T) {
	var output bytes.Buffer
	processor := NewStreamProcessor("gemini-test", &output, "msg_test")
	for _, delta := range []string{"rea", "son"} {
		if err := processor.ProcessEvent(gemini.Event{Kind: gemini.EventThought, Operation: gemini.SnapshotAppend, Delta: delta}); err != nil {
			t.Fatalf("输出 thought 增量失败: %v", err)
		}
	}
	if err := processor.FinishThinking("signature_test"); err != nil {
		t.Fatalf("完成 thought 失败: %v", err)
	}
	if err := processor.ProcessEvent(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: "answer"}); err != nil {
		t.Fatalf("输出 text 失败: %v", err)
	}
	stream := output.String()
	firstThought := strings.Index(stream, `"thinking":"rea"`)
	secondThought := strings.Index(stream, `"thinking":"son"`)
	signature := strings.Index(stream, `"signature":"signature_test"`)
	text := strings.Index(stream, `"text":"answer"`)
	if firstThought < 0 || secondThought <= firstThought || signature <= secondThought || text <= signature {
		t.Fatalf("Claude 增量思考事件顺序错误: %s", stream)
	}
	if strings.Count(stream, "signature_delta") != 1 {
		t.Fatalf("思考签名输出次数错误: %s", stream)
	}
}
