package adapter

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/claude"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
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

// historyKeys 返回系统指令、提示词发送的工具定义与消息序列 turns 分别接上 replies 中各条消息后对应的会话键，tool_choice 为 none 时提示词不含工具定义；turns 只编码一次
func historyKeys(system string, bridge ToolBridge, turns []historyTurn, replies ...historyTurn) []string {
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
	prefix := data[:len(data)-len("]}")]
	keys := make([]string, 0, len(replies))
	for _, reply := range replies {
		encoded, _ := json.Marshal(reply)
		hash := sha256.New()
		hash.Write(prefix)
		if len(turns) > 0 {
			hash.Write([]byte(","))
		}
		hash.Write(encoded)
		hash.Write([]byte("]}"))
		keys = append(keys, historyKeyPrefix+hex.EncodeToString(hash.Sum(nil)[:16]))
	}
	return keys
}

// continuedHistoryKey 返回请求历史到最后一个 assistant 轮为止的会话键，该键已登记到网页会话时可按增量续接，否则或续接官网会话开关关闭时返回空
func continuedHistoryKey(system string, bridge ToolBridge, turns []historyTurn) string {
	if !continueSession.Load() {
		return ""
	}
	for index := len(turns) - 1; index > 0; index-- {
		if turns[index].Role != "assistant" {
			continue
		}
		key := historyKeys(system, bridge, turns[:index], turns[index])[0]
		if _, _, _, ok := conversations.get(key); ok {
			return key
		}
		return ""
	}
	return ""
}

// rememberHistory 把请求历史加上本轮回答后的会话键登记到本轮网页会话，供不带会话标识的客户端下一轮续接；回答按客户端可能回传的几种渲染各登记一次，projection 为流式请求的输出投影，非流式为 nil，turns 为空表示请求带有会话标识，续接官网会话开关关闭时不登记
func rememberHistory(pool *balancer.AccountPool, result generationResult, system string, bridge ToolBridge, turns []historyTurn, projection *streamProjection) {
	if len(turns) == 0 || !continueSession.Load() {
		return
	}
	state, accountID, contextTokens, ok := conversations.get(result.ResponseID)
	if !ok {
		return
	}
	primary := result.Accumulator.Primary()
	content, calls, err := bridge.truncated(result.Truncated).Parse(primary.Text)
	if err != nil {
		return
	}
	reply := historyTurn{Role: "assistant"}
	for _, call := range calls {
		reply.Calls = append(reply.Calls, historyCall{Name: call.Name, Arguments: canonicalJSON(call.Arguments)})
	}
	rendered := renderCandidateMarkdown(content, primary, 0)
	seen := make(map[string]struct{}, 6)
	var replies []historyTurn
	addReply := func(text string) {
		trimmed := strings.TrimSpace(text)
		if _, exists := seen[trimmed]; exists {
			return
		}
		seen[trimmed] = struct{}{}
		item := reply
		item.Text = trimmed
		replies = append(replies, item)
	}
	for _, text := range []string{content, rendered, renderCitationMarkdown(rendered, primary.Citations)} {
		addReply(text)
	}
	for _, text := range structuredHistoryTexts(content, primary, projection) {
		addReply(text)
	}
	keys := historyKeys(system, bridge, turns, replies...)
	conversations.put(state, accountID, contextTokens, keys...)
	for _, key := range keys {
		pool.BindSession(key, accountID)
	}
}

// structuredHistoryTexts 返回带媒体或代码的回答的回传形态对应的历史文本：Gemini 结构化内容段（inlineData、代码执行）、Responses image_generation_call，以及流式在媒体或代码到达前已发出正文时客户端收到的 Markdown 正文与 Gemini 内容段（已发出的正文接上尾帧）
func structuredHistoryTexts(content string, primary CandidateOutput, projection *streamProjection) []string {
	if len(primary.Media) == 0 && len(primary.Codes) == 0 {
		return nil
	}
	parts := geminiStructuredParts(content, primary)
	texts := []string{geminiHistoryTurns([]GeminiContent{{Role: "model", Parts: parts}})[0].Text}
	if projection != nil && projection.textRunes > 0 {
		sent := projection.sentText.String()
		texts = append(texts, sent+renderCandidateMarkdown(content, primary, projection.textRunes))
		output := make([]gin.H, 0, len(parts))
		for _, part := range parts {
			output = append(output, geminiOutputPart(part))
		}
		tail := projectGeminiStreamTail(gin.H{"candidates": []gin.H{{"content": gin.H{"parts": output}}}}, projection)
		encoded, _ := json.Marshal(append([]gin.H{{"text": sent}}, geminiResponseParts(tail)...))
		var received []GeminiPart
		_ = json.Unmarshal(encoded, &received)
		texts = append(texts, geminiHistoryTurns([]GeminiContent{{Role: "model", Parts: received}})[0].Text)
	}
	if hasGeneratedImage(primary) {
		var messages []ChatMessage
		for _, media := range primary.Media {
			if media.Type != gemini.MediaGeneratedImage {
				continue
			}
			_, data, ok := splitDataURL(media.URL)
			if !ok {
				continue
			}
			raw, err := responseImageContent(data)
			if err != nil {
				continue
			}
			messages = append(messages, ChatMessage{Role: "assistant", Content: raw})
		}
		if text := renderCandidateMarkdown(content, responsesMarkdownOutput(primary), 0); strings.TrimSpace(text) != "" {
			encoded, _ := json.Marshal(text)
			messages = append(messages, ChatMessage{Role: "assistant", Content: encoded})
		}
		if turns := chatHistoryTurns(messages); len(turns) > 0 {
			texts = append(texts, turns[0].Text)
		}
	}
	return texts
}

// chatHistoryTurns 把 Chat 与 Responses 转换后的消息转为历史会话键的消息序列
func chatHistoryTurns(messages []ChatMessage) []historyTurn {
	turns := make([]historyTurn, 0, len(messages))
	for _, message := range messages {
		turn := historyTurn{Role: transcriptRole(message.Role), Text: historyText(message.Content)}
		if turn.Role != "tool" {
			turn.Text = speakerText(message.Name, turn.Text)
		}
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
	messages, err := responsesMessages(input, nil)
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
			if rest.InlineData != nil {
				mimeType, data := normalizeInlineData(rest.InlineData.MimeType, rest.InlineData.Data)
				rest.InlineData = &GeminiInlineData{MimeType: mimeType, Data: data}
			}
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

// normalizeInlineData 把内联数据的 MIME 规范为小写、data URL 与 URL-safe 或无填充 Base64 规范为标准 Base64
func normalizeInlineData(mimeType, data string) (string, string) {
	if dataMime, payload, ok := splitDataURL(data); ok {
		if strings.TrimSpace(mimeType) == "" {
			mimeType = dataMime
		}
		data = payload
	}
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	return mimeType, canonicalBase64(data)
}

// canonicalBase64 把 URL-safe、无填充或带空白的 Base64 转为标准 Base64，已是标准 Base64 时原样返回
func canonicalBase64(value string) string {
	if value == "" {
		return ""
	}
	if len(value)%4 == 0 && !strings.ContainsAny(value, "-_ \t\r\n") {
		return value
	}
	decoded, err := decodeBase64(value)
	if err != nil {
		return strings.TrimSpace(value)
	}
	return base64.StdEncoding.EncodeToString(decoded)
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
