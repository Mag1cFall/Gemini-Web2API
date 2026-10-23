package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/claude"
)

// historyKeyPrefix 是按对话历史续接网页会话的会话键前缀
const historyKeyPrefix = "hist_"

// historyTurn 是参与历史会话键的一条消息：角色、去掉首尾空白的正文与工具调用，思考、工具调用 ID 与缓存标记不参与
type historyTurn struct {
	Role  string        `json:"r"`
	Text  string        `json:"t,omitempty"`
	Calls []historyCall `json:"c,omitempty"`
}

// historyCall 是历史中的一次工具调用，参数为规范化后的 JSON
type historyCall struct {
	Name      string          `json:"n"`
	Arguments json.RawMessage `json:"a,omitempty"`
}

// isHistoryKey 判断会话键是否由对话历史得出
func isHistoryKey(key string) bool {
	return strings.HasPrefix(key, historyKeyPrefix)
}

// historyKey 返回系统指令、提示词发送的工具定义与消息序列对应的会话键，tool_choice 为 none 时提示词不含工具定义
func historyKey(system string, bridge ToolBridge, turns []historyTurn) string {
	tools := bridge.Definitions
	if bridge.disallowsHostedTools() {
		tools = nil
	}
	data, _ := json.Marshal(struct {
		System string           `json:"s"`
		Tools  []ToolDefinition `json:"d,omitempty"`
		Schema json.RawMessage  `json:"j,omitempty"`
		Turns  []historyTurn    `json:"m"`
	}{strings.TrimSpace(system), tools, canonicalJSON(bridge.JSONSchema), turns})
	sum := sha256.Sum256(data)
	return historyKeyPrefix + hex.EncodeToString(sum[:16])
}

// continuedHistoryKey 返回请求历史到最后一个 assistant 轮为止的会话键，该键已登记到网页会话时可按增量续接，否则返回空
func continuedHistoryKey(system string, bridge ToolBridge, turns []historyTurn) string {
	for index := len(turns) - 1; index > 0; index-- {
		if turns[index].Role != "assistant" {
			continue
		}
		key := historyKey(system, bridge, turns[:index+1])
		if _, _, _, ok := conversations.get(key); ok {
			return key
		}
		return ""
	}
	return ""
}

// rememberHistory 把请求历史加上本轮回答后的会话键登记到本轮网页会话，供不带会话标识的客户端下一轮续接；回答按客户端可能回传的几种渲染各登记一次，turns 为空表示请求带有会话标识
func rememberHistory(pool *balancer.AccountPool, result generationResult, system string, bridge ToolBridge, turns []historyTurn) {
	if len(turns) == 0 {
		return
	}
	state, accountID, contextTokens, ok := conversations.get(result.ResponseID)
	if !ok {
		return
	}
	primary := result.Accumulator.Primary()
	content, calls, err := bridge.Parse(primary.Text)
	if err != nil {
		return
	}
	reply := historyTurn{Role: "assistant"}
	for _, call := range calls {
		reply.Calls = append(reply.Calls, historyCall{Name: call.Name, Arguments: canonicalJSON(call.Arguments)})
	}
	rendered := renderCandidateMarkdown(content, primary, 0)
	var keys []string
	for _, text := range []string{content, rendered, renderCitationMarkdown(rendered, primary.Citations)} {
		reply.Text = strings.TrimSpace(text)
		key := historyKey(system, bridge, append(turns[:len(turns):len(turns)], reply))
		if len(keys) == 0 || keys[len(keys)-1] != key {
			keys = append(keys, key)
		}
	}
	conversations.put(state, accountID, contextTokens, keys...)
	for _, key := range keys {
		pool.BindSession(key, accountID)
	}
}

// chatHistoryTurns 把 Chat 与 Responses 转换后的消息转为历史会话键的消息序列
func chatHistoryTurns(messages []ChatMessage) []historyTurn {
	turns := make([]historyTurn, 0, len(messages))
	for _, message := range messages {
		turn := historyTurn{Role: transcriptRole(message.Role), Text: historyText(message.Content)}
		for _, call := range message.ToolCalls {
			turn.Calls = append(turn.Calls, historyCall{Name: call.Function.Name, Arguments: canonicalJSON(json.RawMessage(call.Function.Arguments))})
		}
		turns = appendHistoryTurn(turns, turn)
	}
	return turns
}

// responsesHistoryTurns 把 Responses 的 input 转为历史会话键的消息序列，无法转换时返回空
func responsesHistoryTurns(raw json.RawMessage) []historyTurn {
	input, err := expandCompaction(raw)
	if err != nil {
		return nil
	}
	messages, err := responsesMessages(input)
	if err != nil {
		return nil
	}
	return chatHistoryTurns(messages)
}

// claudeHistoryTurns 把 Anthropic 消息转为历史会话键的消息序列
func claudeHistoryTurns(messages []claude.Message) []historyTurn {
	turns := make([]historyTurn, 0, len(messages))
	for _, message := range messages {
		turn := historyTurn{Role: transcriptRole(message.Role)}
		turn.Text, turn.Calls = historyContent(message.Content)
		turns = appendHistoryTurn(turns, turn)
	}
	return turns
}

// geminiHistoryTurns 把 Gemini contents 转为历史会话键的消息序列：思考与签名跳过，函数调用取名称与规范化参数，其余非文字部件取 JSON；连续的 model 内容直接拼接为一轮，与 google-genai SDK chats 把流式回答的每个分块记为一个内容一致
func geminiHistoryTurns(contents []GeminiContent) []historyTurn {
	turns := make([]historyTurn, 0, len(contents))
	for _, content := range contents {
		role := transcriptRole(content.Role)
		if role != "assistant" || len(turns) == 0 || turns[len(turns)-1].Role != "assistant" {
			turns = append(turns, historyTurn{Role: role})
		}
		turn := &turns[len(turns)-1]
		for _, part := range content.Parts {
			if part.Thought {
				continue
			}
			turn.Text += part.Text
			if part.FunctionCall != nil {
				arguments, _ := json.Marshal(part.FunctionCall.Args)
				turn.Calls = append(turn.Calls, historyCall{Name: part.FunctionCall.Name, Arguments: canonicalJSON(arguments)})
			}
			rest := part
			rest.Text, rest.ThoughtSignature, rest.FunctionCall = "", "", nil
			if encoded, _ := json.Marshal(rest); string(encoded) != "{}" {
				turn.Text += string(encoded)
			}
		}
	}
	for index := range turns {
		turns[index].Text = strings.TrimSpace(turns[index].Text)
	}
	return turns
}

// geminiSystemText 返回 systemInstruction 的文字
func geminiSystemText(instruction *GeminiContent) string {
	if instruction == nil {
		return ""
	}
	var text strings.Builder
	for _, part := range instruction.Parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

// appendHistoryTurn 追加一条消息，连续的 assistant 消息合并为一轮，与 Responses 把思考、正文与每个函数调用拆成多项的回传一致
func appendHistoryTurn(turns []historyTurn, turn historyTurn) []historyTurn {
	if len(turns) == 0 || turn.Role != "assistant" || turns[len(turns)-1].Role != "assistant" {
		return append(turns, turn)
	}
	last := &turns[len(turns)-1]
	if last.Text != "" && turn.Text != "" {
		last.Text += "\n"
	}
	last.Text += turn.Text
	last.Calls = append(last.Calls, turn.Calls...)
	return turns
}

// historyText 返回消息内容的正文，内容数组中的工具调用一并写入正文
func historyText(raw json.RawMessage) string {
	text, calls := historyContent(raw)
	for _, call := range calls {
		text += call.Name + string(call.Arguments)
	}
	return text
}

// historyContent 返回消息内容的正文与工具调用：字符串取原文；内容数组拼接文本项与工具结果，tool_use 项取名称与规范化参数，思考与服务端工具项跳过，其余项取去掉缓存标记的规范化 JSON
func historyContent(raw json.RawMessage) (string, []historyCall) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text), nil
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return strings.TrimSpace(string(canonicalJSON(raw))), nil
	}
	var builder strings.Builder
	var calls []historyCall
	for _, part := range parts {
		var partType string
		_ = json.Unmarshal(part["type"], &partType)
		switch partType {
		case "thinking", "redacted_thinking", "server_tool_use", "web_search_tool_result":
		case "text", "input_text", "output_text":
			var value string
			_ = json.Unmarshal(part["text"], &value)
			builder.WriteString(value)
		case "tool_use":
			var name string
			_ = json.Unmarshal(part["name"], &name)
			calls = append(calls, historyCall{Name: name, Arguments: canonicalJSON(part["input"])})
		case "tool_result":
			builder.WriteString(historyText(part["content"]))
		default:
			delete(part, "cache_control")
			encoded, _ := json.Marshal(part)
			builder.Write(encoded)
		}
	}
	return strings.TrimSpace(builder.String()), calls
}

// canonicalJSON 返回键按字典序排列的 JSON，无效或空值返回 nil
func canonicalJSON(raw json.RawMessage) json.RawMessage {
	var value interface{}
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	return encoded
}
