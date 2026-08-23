package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestContinuationMessagesKeepsParallelToolOutputs 验证并行工具结果完整进入续接提示
func TestContinuationMessagesKeepsParallelToolOutputs(t *testing.T) {
	messages, err := responsesMessages(json.RawMessage(`[
		{"type":"function_call_output","call_id":"call_alpha","output":"alpha"},
		{"type":"function_call_output","call_id":"call_beta","output":"beta"}
	]`))
	if err != nil {
		t.Fatal(err)
	}

	continued := continuationMessages(messages)
	if len(continued) != 2 || continued[0].ToolCallID != "call_alpha" || continued[1].ToolCallID != "call_beta" {
		t.Fatalf("并行工具结果被截断: %+v", continued)
	}
}

// TestGeminiFunctionResponseKeepsCallID 验证 Gemini 工具结果保留调用标识
func TestGeminiFunctionResponseKeepsCallID(t *testing.T) {
	var part GeminiPart
	if err := json.Unmarshal([]byte(`{"functionResponse":{"id":"call_lookup","name":"lookup","response":{"value":42}}}`), &part); err != nil {
		t.Fatal(err)
	}

	transcript, _, err := decodeGeminiParts(nil, nil, []GeminiPart{part})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transcript, `"id":"call_lookup"`) {
		t.Fatalf("Gemini 工具结果缺少调用标识: %s", transcript)
	}
}
