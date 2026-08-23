package adapter

import (
	"encoding/json"
	"testing"
)

func TestToolBridgeParseToolCall(t *testing.T) {
	bridge := ToolBridge{
		Definitions: []ToolDefinition{{
			Name:       "get_weather",
			Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
		Choice: "required",
	}

	content, calls, err := bridge.Parse(`{"tool_calls":[{"name":"get_weather","arguments":{"city":"Taipei"}}]}`)
	if err != nil {
		t.Fatalf("解析工具调用失败: %v", err)
	}
	if content != "" || len(calls) != 1 || calls[0].Name != "get_weather" {
		t.Fatalf("工具调用结果异常: content=%q calls=%+v", content, calls)
	}
}

func TestToolBridgeValidatesArgumentsSchema(t *testing.T) {
	bridge := ToolBridge{
		Definitions: []ToolDefinition{{
			Name:       "get_weather",
			Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
		Choice: "required",
	}
	if _, _, err := bridge.Parse(`{"tool_calls":[{"name":"get_weather","arguments":{"city":42}}]}`); err == nil {
		t.Fatal("不符合参数结构的工具调用应返回错误")
	}
}

func TestToolBridgeRejectsInvalidStructuredOutput(t *testing.T) {
	bridge := ToolBridge{JSONSchema: json.RawMessage(`{"type":"object"}`)}
	if _, _, err := bridge.Parse("plain text"); err == nil {
		t.Fatal("无效结构化输出应返回错误")
	}
}

// TestGeminiToolBridgeReadsPythonSDKSchema 验证 Google Python SDK 的线格式
func TestGeminiToolBridgeReadsPythonSDKSchema(t *testing.T) {
	request := GeminiGenerateContentRequest{
		Tools: json.RawMessage(`[{"functionDeclarations":[{"name":"lookup","parameters_json_schema":{"type":"object","required":["city"]}}]}]`),
	}
	bridge, _, err := geminiToolBridge(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(bridge.Definitions) != 1 || !json.Valid(bridge.Definitions[0].Parameters) || string(bridge.Definitions[0].Parameters) == "null" {
		t.Fatalf("Google SDK 工具结构未进入桥接层: %+v", bridge.Definitions)
	}
}

// TestGeminiToolBridgeFiltersAllowedFunctions 验证 Gemini 多工具白名单
func TestGeminiToolBridgeFiltersAllowedFunctions(t *testing.T) {
	request := GeminiGenerateContentRequest{
		Tools:      json.RawMessage(`[{"functionDeclarations":[{"name":"alpha"},{"name":"beta"},{"name":"gamma"}]}]`),
		ToolConfig: json.RawMessage(`{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["alpha","beta"]}}`),
	}
	bridge, _, err := geminiToolBridge(request)
	if err != nil {
		t.Fatal(err)
	}
	if bridge.Choice != "required" || len(bridge.Definitions) != 2 || bridge.Definitions[0].Name != "alpha" || bridge.Definitions[1].Name != "beta" {
		t.Fatalf("Gemini 工具白名单未生效: %+v", bridge)
	}
}

// TestResponsesConversationID 验证 Responses 正式会话引用类型
func TestResponsesConversationID(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
	}{
		{raw: `"conv_string"`, want: "conv_string"},
		{raw: `{"id":"conv_object"}`, want: "conv_object"},
	} {
		got, err := responsesConversationID(json.RawMessage(test.raw))
		if err != nil || got != test.want {
			t.Fatalf("conversation %s: got=%q err=%v", test.raw, got, err)
		}
	}
}
