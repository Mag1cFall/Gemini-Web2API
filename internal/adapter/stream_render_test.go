package adapter

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
)

func TestStreamProjectionEmitsAppendImmediately(t *testing.T) {
	projection := newStreamProjection(ToolBridge{})
	var emitted []gemini.Event
	err := projection.project(
		gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: "live"},
		func(event gemini.Event) error {
			emitted = append(emitted, event)
			return nil
		}, nil,
	)
	if err != nil || len(emitted) != 1 || emitted[0].Delta != "live" {
		t.Fatalf("append 未立即输出: err=%v events=%v", err, emitted)
	}
}

func TestStreamProjectionBuffersRewriteBeforeOutput(t *testing.T) {
	projection := newStreamProjection(ToolBridge{})
	emitCount := 0
	emit := func(gemini.Event) error {
		emitCount++
		return nil
	}
	if err := projection.project(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotReplace, Snapshot: "new"}, emit, nil); err != nil {
		t.Fatalf("首次改写不应失败: %v", err)
	}
	if err := projection.project(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: " tail"}, emit, nil); err != nil {
		t.Fatalf("缓冲 append 不应失败: %v", err)
	}
	if !projection.bufferText || emitCount != 0 {
		t.Fatalf("改写后的文本未保持缓冲: buffered=%v emits=%d", projection.bufferText, emitCount)
	}
}

func TestStreamProjectionRejectsRewriteAfterOutput(t *testing.T) {
	projection := newStreamProjection(ToolBridge{})
	writeErr := errors.New("unexpected write error")
	if err := projection.project(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: "live"}, func(gemini.Event) error { return nil }, nil); err != nil {
		t.Fatalf("输出 append 失败: %v", err)
	}
	errorCount := 0
	err := projection.project(
		gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotTruncate, Snapshot: ""},
		func(gemini.Event) error { return writeErr },
		func(error) error {
			errorCount++
			return nil
		},
	)
	if err == nil || !projection.errorSent || errorCount != 1 {
		t.Fatalf("流式改写未终止: err=%v errorSent=%v errors=%d", err, projection.errorSent, errorCount)
	}
}

func TestStreamProjectionTracksTextAndThoughtSeparately(t *testing.T) {
	projection := newStreamProjection(ToolBridge{})
	var emitted []gemini.EventKind
	emit := func(event gemini.Event) error {
		emitted = append(emitted, event.Kind)
		return nil
	}
	if err := projection.project(gemini.Event{Kind: gemini.EventThought, Operation: gemini.SnapshotAppend, Delta: "think"}, emit, nil); err != nil {
		t.Fatalf("输出思考失败: %v", err)
	}
	if err := projection.project(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: "answer"}, emit, nil); err != nil {
		t.Fatalf("思考后的正文缓冲失败: %v", err)
	}
	if !projection.bufferText || projection.errorSent || len(emitted) != 1 || emitted[0] != gemini.EventThought {
		t.Fatalf("思考与正文顺序错误: buffered=%v errorSent=%v events=%v", projection.bufferText, projection.errorSent, emitted)
	}
}

func TestStreamProjectionBuffersToolTextOnly(t *testing.T) {
	projection := newStreamProjection(ToolBridge{Definitions: []ToolDefinition{{Name: "lookup"}}})
	var emitted []gemini.EventKind
	emit := func(event gemini.Event) error {
		emitted = append(emitted, event.Kind)
		return nil
	}
	if err := projection.project(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: "json"}, emit, nil); err != nil {
		t.Fatalf("缓冲工具文本失败: %v", err)
	}
	if err := projection.project(gemini.Event{Kind: gemini.EventThought, Operation: gemini.SnapshotAppend, Delta: "think"}, emit, nil); err != nil {
		t.Fatalf("输出思考失败: %v", err)
	}
	if len(emitted) != 1 || emitted[0] != gemini.EventThought {
		t.Fatalf("工具请求的流式边界错误: %v", emitted)
	}
}

func TestOpenAIStreamUsesFinalSnapshot(t *testing.T) {
	accumulator := NewEventAccumulator()
	accumulator.Candidates[0] = &CandidateOutput{Text: "final answer", Thought: "final thought"}
	projection := newStreamProjection(ToolBridge{})
	projection.bufferText = true
	projection.bufferThought = true
	var output bytes.Buffer
	if err := writeProjectedOpenAI(&output, "chat_test", 1, "model_test", accumulator, ToolBridge{}, projection); err != nil {
		t.Fatalf("输出 OpenAI 流失败: %v", err)
	}
	stream := output.String()
	if !strings.Contains(stream, "final answer") || !strings.Contains(stream, "final thought") {
		t.Fatalf("流没有使用最终快照: %s", stream)
	}
	if strings.Contains(stream, "x_gemini_web_operation") {
		t.Fatalf("标准流泄露了私有快照扩展: %s", stream)
	}
}

func TestResponsesWriterEmitsLiveDeltasBeforeCompletion(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := responseSequenceWriter{writer: recorder}
	if err := writer.start("resp_test", "model_test", 1); err != nil {
		t.Fatalf("启动 Responses 流失败: %v", err)
	}
	if err := writer.live(gemini.Event{Kind: gemini.EventThought, Operation: gemini.SnapshotAppend, Delta: "reason"}); err != nil {
		t.Fatalf("输出思考增量失败: %v", err)
	}
	if err := writer.live(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: "answer"}); err != nil {
		t.Fatalf("输出文本增量失败: %v", err)
	}
	stream := recorder.Body.String()
	for _, expected := range []string{"response.created", "response.reasoning_summary_text.delta", "reason", "response.output_text.delta", "answer"} {
		if !strings.Contains(stream, expected) {
			t.Fatalf("实时流缺少 %q: %s", expected, stream)
		}
	}
	if strings.Contains(stream, "response.completed") {
		t.Fatalf("上游尚未结束时提前完成 Responses 流: %s", stream)
	}
	response := responseShell("resp_test", "model_test", 1, "completed")
	response["output"] = []gin.H{
		{"id": "rs_resp_test", "type": "reasoning", "summary": []gin.H{{"type": "summary_text", "text": "reason"}}},
		{"id": "msg_resp_test", "type": "message", "status": "completed", "role": "assistant", "content": []gin.H{{"type": "output_text", "text": "answer", "annotations": []interface{}{}}}},
	}
	accumulator := NewEventAccumulator()
	accumulator.Candidates[0] = &CandidateOutput{Text: "answer", Thought: "reason"}
	if err := writer.finishProjected(response, accumulator, newStreamProjection(ToolBridge{})); err != nil {
		t.Fatalf("完成 Responses 流失败: %v", err)
	}
	stream = recorder.Body.String()
	if !strings.Contains(stream, "response.completed") || strings.Count(stream, `"delta":"answer"`) != 1 {
		t.Fatalf("Responses 完成事件错误或重复文本: %s", stream)
	}
}
