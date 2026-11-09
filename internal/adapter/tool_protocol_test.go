package adapter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestToolBridgeParsesCallBlocks 验证调用块的参数取代码块原文：引号、反斜杠与换行不转义，外层代码块更长或过短、上游以反斜杠转义方括号、工具名带空格或下划线前缀、缺少参数结尾与参数不在代码块中时都还原为声明的工具调用
func TestToolBridgeParsesCallBlocks(t *testing.T) {
	bridge := ToolBridge{Choice: "auto", Definitions: []ToolDefinition{{Name: "bash", Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)}}}
	raw := "printf 'name=\"torrents\"'\ntype C:\\new\\table.txt \\d+"
	nested := "cat <<EOF\n```python\nprint(\"x\")\n```\nEOF"
	cases := []struct {
		name    string
		reply   string
		command string
	}{
		{"raw text", "[Call:bash]\n[Arg:command]\n```\n" + raw + "\n```\n[/Arg]\n[/Call]", raw},
		{"trailing newline", "[Call:bash]\n[Arg:command]\n```\nls\n\n```\n[/Arg]\n[/Call]", "ls\n"},
		{"longer fence", "[Call:bash]\n[Arg:command]\n````\n" + nested + "\n````\n[/Arg]\n[/Call]", nested},
		{"short fence", "[Call:bash]\n[Arg:command]\n```\n" + nested + "\n```\n[/Arg]\n[/Call]", nested},
		{"escaped brackets", "\\[Call:bash\\]\n\\[Arg:command\\]\n```\nls\n```\n\\[/Arg\\]\n\\[/Call\\]", "ls"},
		{"name spacing", "[Call: _bash ]\n[Arg: command ]\n```\nls\n```\n[/Arg]\n[/Call]", "ls"},
		{"missing arg end", "[Call:bash]\n[Arg:command]\n```\nls\n```\n[/Call]", "ls"},
		{"unfenced", "[Call:bash]\n[Arg:command]\nls -la\n[/Arg]\n[/Call]", "ls -la"},
	}
	for _, tc := range cases {
		content, calls, err := bridge.Parse(tc.reply)
		if err != nil || content != "" || len(calls) != 1 || calls[0].Name != "bash" {
			t.Fatalf("%s: content %q calls %+v err %v", tc.name, content, calls, err)
		}
		var arguments struct{ Command string }
		if err := json.Unmarshal(calls[0].Arguments, &arguments); err != nil || arguments.Command != tc.command {
			t.Fatalf("%s: arguments %s err %v", tc.name, calls[0].Arguments, err)
		}
	}
}

// TestToolBridgeTypedArguments 验证参数原文按 Schema 类型还原：只允许字符串的参数保留原文，数字、布尔值、数组与对象取 JSON，未声明类型的参数是 JSON 时取值，原文不是声明类型时报错，HTML 字符按原样写入
func TestToolBridgeTypedArguments(t *testing.T) {
	bridge := ToolBridge{Choice: "auto", Definitions: []ToolDefinition{{Name: "search", Parameters: json.RawMessage(`{"type":"object","properties":{
		"path":{"type":"string"},"limit":{"type":"integer"},"recursive":{"type":"boolean"},"globs":{"type":"array","items":{"type":"string"}},
		"options":{"type":"object"},"ratio":{"type":["number","null"]},"extra":{}}}`)}}}
	call := func(pairs ...string) string {
		var builder strings.Builder
		builder.WriteString("[Call:search]\n")
		for index := 0; index+1 < len(pairs); index += 2 {
			builder.WriteString("[Arg:" + pairs[index] + "]\n```\n" + pairs[index+1] + "\n```\n[/Arg]\n")
		}
		builder.WriteString("[/Call]")
		return builder.String()
	}
	_, calls, err := bridge.Parse(call("path", "42", "limit", "10", "recursive", "true", "globs", `["*.go","a\\b"]`, "options", `{"depth":2}`, "ratio", "0.5", "extra", `{"x":1}`))
	if err != nil || len(calls) != 1 {
		t.Fatalf("calls %+v err %v", calls, err)
	}
	var got, want map[string]any
	_ = json.Unmarshal(calls[0].Arguments, &got)
	_ = json.Unmarshal([]byte(`{"path":"42","limit":10,"recursive":true,"globs":["*.go","a\\b"],"options":{"depth":2},"ratio":0.5,"extra":{"x":1}}`), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments %s", calls[0].Arguments)
	}
	if _, _, err := bridge.Parse(call("limit", "ten")); err == nil {
		t.Fatal("原文不是整数的 integer 参数应返回错误")
	}
	_, calls, err = bridge.Parse(call("limit", "1.0"))
	if err != nil || string(calls[0].Arguments) != `{"limit":1.0}` {
		t.Fatalf("1.0 是 JSON Schema 的整数：%s err %v", argumentsOf(calls), err)
	}
	_, calls, err = bridge.Parse(call("limit", "123456789012345678901234567890"))
	if err != nil || string(calls[0].Arguments) != `{"limit":123456789012345678901234567890}` {
		t.Fatalf("超出 int64 的整数：%s err %v", argumentsOf(calls), err)
	}
	referenced := ToolBridge{Choice: "auto", Definitions: []ToolDefinition{{Name: "search", Parameters: json.RawMessage(`{"type":"object","$defs":{"text":{"type":"string"}},"properties":{
		"id":{"$ref":"#/$defs/text"},"all":{"allOf":[{"type":"string"}]},"fixed":{"const":"7"}}}`)}}}
	_, calls, err = referenced.Parse(call("id", "123", "all", "true", "fixed", "7"))
	if err != nil || string(calls[0].Arguments) != `{"all":"true","fixed":"7","id":"123"}` {
		t.Fatalf("$ref、allOf 与 const 声明的字符串应保留原文：%s err %v", argumentsOf(calls), err)
	}
	circular := ToolBridge{Choice: "auto", Definitions: []ToolDefinition{{Name: "search", Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"$ref":"#/properties/a"}}}`)}}}
	if _, _, err := circular.Parse(call("a", "1")); err == nil {
		t.Fatal("自引用的 Schema 应由校验报错")
	}
	_, calls, err = bridge.Parse(call("path", "a < b && c > d"))
	if err != nil || string(calls[0].Arguments) != `{"path":"a < b && c > d"}` {
		t.Fatalf("HTML 字符应按原样写入 arguments：%s err %v", calls[0].Arguments, err)
	}
}

// argumentsOf 返回第一个调用的参数，没有调用时为空
func argumentsOf(calls []ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	return string(calls[0].Arguments)
}

// TestToolCallHistoryRoundTrip 验证历史工具调用写成调用块后按同一协议解析回原参数，代码块按参数中最长的反引号串加长，null 写为 null
func TestToolCallHistoryRoundTrip(t *testing.T) {
	bridge := ToolBridge{Choice: "auto", Definitions: []ToolDefinition{{Name: "write_file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"mode":{"type":"integer"},"tags":{"type":"array"}}}`)}}}
	arguments := `{"content":"# 标题\n` + "````" + `go\nfmt.Println(\"a\\\\b\")\n` + "````" + `\n[/Arg]\n[/Call]\n<TemplateWidget a=\"b\">","mode":420,"note":null,"path":"C:\\new\\x.md","tags":["a","b"]}`
	var builder strings.Builder
	writeCallBlock(&builder, "write_file", json.RawMessage(arguments))
	_, calls, err := bridge.Parse(builder.String())
	if err != nil || len(calls) != 1 {
		t.Fatalf("history %q calls %+v err %v", builder.String(), calls, err)
	}
	var got, want map[string]any
	_ = json.Unmarshal(calls[0].Arguments, &got)
	_ = json.Unmarshal([]byte(arguments), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip %s\nwant %s", calls[0].Arguments, arguments)
	}
}

// TestSplitCallGateHoldsCallMarkers 验证调用块开头在任意两个切分点分片到达时都不进入正文，末尾的反斜杠与普通方括号在下一分片后照常输出
func TestSplitCallGateHoldsCallMarkers(t *testing.T) {
	for _, marker := range []string{"[Call:bash]", `\[Call:bash\]`, `\[Call\:bash\]`, "[ Call : bash ]"} {
		text := "intro " + marker + "\n"
		for first := 1; first < len(text); first++ {
			for second := first; second < len(text); second++ {
				output, held, found := "", "", false
				for _, chunk := range []string{text[:first], text[first:second], text[second:]} {
					if found {
						break
					}
					var safe string
					safe, held, found = splitCallGate("", held+chunk)
					output += safe
				}
				if !found || output != "intro" {
					t.Fatalf("%q 切分于 %d、%d：输出 %q，暂扣 %q，found %v", marker, first, second, output, held, found)
				}
			}
		}
	}
	safe, held, _ := splitCallGate("", `path C:\`)
	if safe != "path C:" || held != `\` {
		t.Fatalf("末尾反斜杠应暂扣：%q %q", safe, held)
	}
	safe, held, _ = splitCallGate("", held+`new [x] done`)
	if safe != `\new [x] done` || held != "" {
		t.Fatalf("反斜杠不属于调用块时应输出：%q %q", safe, held)
	}
}

// TestCallAfterUnclosedBacktick 验证没有闭合的反引号是普通字符，其后的调用块照常解析，同一行闭合的行内代码中的调用块是示例
func TestCallAfterUnclosedBacktick(t *testing.T) {
	bridge := ToolBridge{Choice: "auto", Definitions: []ToolDefinition{{Name: "echo", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	text, calls, err := bridge.Parse("好的 ( ´ ▽ ` )ﾉ[Call:echo]\n[/Call]\n[Call:echo]\n[/Call]")
	if err != nil || len(calls) != 2 || text != "好的 ( ´ ▽ ` )ﾉ" {
		t.Fatalf("正文 %q 调用 %d 错误 %v", text, len(calls), err)
	}
	for _, sample := range []string{"写法 ` x [Call:echo] y ` 完毕", "`` a ` [Call:echo] ``"} {
		text, calls, err := bridge.Parse(sample)
		if err != nil || len(calls) != 0 || text != sample {
			t.Fatalf("样本 %q: 正文 %q 调用 %d 错误 %v", sample, text, len(calls), err)
		}
	}
}

// TestSplitCallGateWaitsForLineAfterUnclosedBacktick 验证未闭合反引号之后的调用块开头暂扣到所在行有结果：行结束时是调用，反引号闭合时是示例，分片位置不影响输出
func TestSplitCallGateWaitsForLineAfterUnclosedBacktick(t *testing.T) {
	cases := []struct {
		text   string
		output string
		found  bool
	}{
		{"好 ( ` )ﾉ[Call:echo]\n", "好 ( ` )ﾉ", true},
		{"写法 ` x [Call:echo] y ` 完毕\n", "写法 ` x [Call:echo] y ` 完毕", false},
	}
	for _, item := range cases {
		for first := 1; first < len(item.text); first++ {
			output, held, found := "", "", false
			for _, chunk := range []string{item.text[:first], item.text[first:]} {
				var safe string
				safe, held, found = splitCallGate(output, held+chunk)
				output += safe
				if found {
					break
				}
			}
			if found != item.found || output != item.output {
				t.Fatalf("%q 切分于 %d：输出 %q，暂扣 %q，found %v", item.text, first, output, held, found)
			}
		}
	}
}
