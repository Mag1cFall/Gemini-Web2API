package adapter

import (
	"encoding/json"
	"testing"
)

func TestSplitToolEnvelopeNoNewline(t *testing.T) {
	// 用户截图中的实际场景：句号后无换行直接紧贴 ```json
	input := `正在讀取目前 C:/Users/fi/source/metasoftware-valorant/tts/features/aimassist/aimassist.cpp 的目標偵測與移動控制區段，準備在不影響原版 Makcu 硬體貝茲移動手感（ApplySmoothing、ExecuteMovement、mouseMoveBezier）的前提下，將先前參考五大開源專案的優秀偵測技術完整整合進去。` + "```json\n" +
		`{"tool_calls":[{"name":"codemode","arguments":{"action":"view"}}]} ` + "\n```"

	before, envelope, after, found := splitToolEnvelope(input)
	if !found {
		t.Fatalf("expected tool envelope to be found")
	}

	expectedBefore := `正在讀取目前 C:/Users/fi/source/metasoftware-valorant/tts/features/aimassist/aimassist.cpp 的目標偵測與移動控制區段，準備在不影響原版 Makcu 硬體貝茲移動手感（ApplySmoothing、ExecuteMovement、mouseMoveBezier）的前提下，將先前參考五大開源專案的優秀偵測技術完整整合進去。`

	if before != expectedBefore {
		t.Errorf("expected before:\n%s\ngot:\n%s", expectedBefore, before)
	}

	if after != "" {
		t.Errorf("expected empty after, got: %q", after)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(envelope), &parsed); err != nil {
		t.Errorf("failed to parse envelope: %v", err)
	}
}

func TestSplitToolEnvelopeWithSpaceAndInlineCode(t *testing.T) {
	// 含有行内单反引号 `code`，且在句号后带空格接 ```json
	input := "正在讀取 `aimassist.cpp` 與 `makcu.cpp` 的內容。 ```json\n" +
		`{"tool_calls":[{"name":"read_file","arguments":{"path":"aimassist.cpp"}}]} ` + "\n```"

	before, envelope, after, found := splitToolEnvelope(input)
	if !found {
		t.Fatalf("expected tool envelope to be found")
	}

	expectedBefore := "正在讀取 `aimassist.cpp` 與 `makcu.cpp` 的內容。"
	if before != expectedBefore {
		t.Errorf("expected before:\n%s\ngot:\n%s", expectedBefore, before)
	}

	if after != "" {
		t.Errorf("expected empty after, got: %q", after)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(envelope), &parsed); err != nil {
		t.Errorf("failed to parse envelope: %v", err)
	}
}

func TestSplitToolEnvelopeStandardNewline(t *testing.T) {
	// 标准换行情况
	input := "正在檢查檔案。\n```json\n" +
		`{"tool_calls":[{"name":"search","arguments":{"q":"test"}}]} ` + "\n```\n後續備註文字"

	before, envelope, after, found := splitToolEnvelope(input)
	if !found {
		t.Fatalf("expected tool envelope to be found")
	}

	if before != "正在檢查檔案。" {
		t.Errorf("expected before '正在檢查檔案。', got %q", before)
	}

	if after != "後續備註文字" {
		t.Errorf("expected after '後續備註文字', got %q", after)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(envelope), &parsed); err != nil {
		t.Errorf("failed to parse envelope: %v", err)
	}
}

func TestToolBridgeParseCleanContent(t *testing.T) {
	bridge := ToolBridge{
		Definitions: []ToolDefinition{
			{
				Name:        "codemode",
				Description: "execute code",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			},
		},
	}

	rawText := `正在分析目標檔案。` + "```json\n" +
		`{"tool_calls":[{"name":"codemode","arguments":{}}]} ` + "\n```"

	content, calls, err := bridge.Parse(rawText)
	if err != nil {
		t.Fatalf("bridge.Parse failed: %v", err)
	}

	if content != "正在分析目標檔案。" {
		t.Errorf("expected content '正在分析目標檔案。', got %q", content)
	}

	if len(calls) != 1 || calls[0].Name != "codemode" {
		t.Errorf("unexpected calls: %v", calls)
	}
}
