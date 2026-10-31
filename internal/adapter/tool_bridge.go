package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ToolDefinition 描述公开协议中的函数工具
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Namespace   string          `json:"-"`
	// Kind 为 Responses 客户端工具类型：custom、local_shell、shell、apply_patch，普通函数为空
	Kind string `json:"-"`
}

// ToolCall 描述模型选择执行的函数工具
type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolBridge 将函数工具和结构化输出转换为严格文本契约
type ToolBridge struct {
	Definitions            []ToolDefinition
	Choice                 string
	JSONSchema             json.RawMessage
	CodeExecution          bool
	WebSearch              bool
	ImageGeneration        bool
	RequireCodeExecution   bool
	RequireWebSearch       bool
	RequireImageGeneration bool
	// AspectRatio 为生图请求的宽高比
	AspectRatio string
	// Enum 为 text/x.enum 响应可选的取值
	Enum []string
	// LegacyFunctions 表示工具来自 Chat 旧式 functions，调用以 function_call 返回
	LegacyFunctions bool
	// Declared 表示工具定义与 JSON Schema 已在本网页会话的前几轮发送，提示词只重申规则
	Declared bool
	// SchemaUnchecked 表示结构化输出不按 JSON Schema 校验，OpenAI json_schema 的 strict 为 false 或缺省时为 true
	SchemaUnchecked bool
	// Stop 是请求的停止序列，正文在首个命中处截断
	Stop []string
}

// declared 返回标记工具定义是否已在会话中发送的副本
func (b ToolBridge) declared(value bool) ToolBridge {
	b.Declared = value
	return b
}

// PromptSuffix 返回追加到用户提示词的协议约束
func (b ToolBridge) PromptSuffix() (string, error) {
	var sections []string

	if len(b.Definitions) > 0 && !strings.EqualFold(strings.TrimSpace(b.Choice), "none") {
		available := "沿用本会话前文的工具定义"
		if !b.Declared {
			definitions, err := json.Marshal(b.Definitions)
			if err != nil {
				return "", fmt.Errorf("编码工具定义失败: %w", err)
			}
			available = string(definitions)
		}

		choice := strings.TrimSpace(b.Choice)
		if choice == "" {
			choice = "auto"
		}
		rule, otherwise := "需要调用工具时", "。无需调用工具时，直接输出正常回答"
		switch strings.ToLower(choice) {
		case "required", "any":
			rule, otherwise = "必须调用一个可用工具", ""
		case "auto":
		default:
			rule, otherwise = fmt.Sprintf("必须调用工具 %s", choice), ""
		}

		sections = append(sections, fmt.Sprintf("工具协议：\n可用工具：%s\n工具选择：%s\n这些工具由客户端在用户的环境中执行，结果会在下一条消息中返回。%s，输出一个 ```json 代码块，内容为 {\"tool_calls\":[{\"name\":\"工具名称\",\"arguments\":{}}]}，代码块之后不再输出内容；对话要求在调用工具前说明时，把说明写在代码块之前，代码块必须另起一行输出。参数中的字符串按 JSON 转义原样写出%s", available, choice, rule, otherwise))
	}

	if len(b.Enum) > 0 {
		sections = append(sections, "输出协议：只输出以下取值之一，不加引号、代码块或其他文字："+strings.Join(b.Enum, "、"))
	}

	if len(b.JSONSchema) > 0 {
		schema := fmt.Sprintf("以下 JSON Schema 的 JSON 值，不要输出 Markdown 代码块或额外文字\n%s", b.JSONSchema)
		if b.Declared {
			schema = "本会话前文 JSON Schema 的 JSON 值，不要输出 Markdown 代码块或额外文字"
		}
		sections = append(sections, "结构化输出协议：只输出符合"+schema)
	}

	if !strings.EqualFold(strings.TrimSpace(b.Choice), "none") {
		if b.CodeExecution {
			sections = append(sections, "本次请求可使用 Gemini Web 内置代码执行；涉及计算或运行代码时使用内置执行并返回实际结果")
		}
		if b.WebSearch {
			sections = append(sections, "本次请求可使用 Gemini Web 内置 Google 搜索；涉及实时或外部信息时搜索并保留来源")
		}
		if b.ImageGeneration {
			sections = append(sections, "本次请求可使用 Gemini Web 内置图片生成；仅在用户请求生成或编辑图片时使用")
		}
		if b.RequireCodeExecution {
			sections = append(sections, "本次请求必须使用 Gemini Web 内置代码执行")
		}
		if b.RequireWebSearch {
			sections = append(sections, "本次请求必须使用 Gemini Web 内置 Google 搜索并保留来源")
		}
		if b.RequireImageGeneration {
			sections = append(sections, "本次请求必须使用 Gemini Web 内置图片生成")
		}
	} else {
		sections = append(sections, "本次请求根据对话和已有工具结果直接回答；禁止调用函数工具、代码执行、Google 搜索和图片生成")
	}

	if len(sections) == 0 {
		return "", nil
	}
	return "\n\n" + strings.Join(sections, "\n\n"), nil
}

// Parse 解析工具调用并验证结构化输出，工具调用信封前后的文字作为正文与调用一起返回
func (b ToolBridge) Parse(text string) (string, []ToolCall, error) {
	trimmed := stripJSONFence(strings.TrimSpace(text))

	if len(b.Definitions) > 0 {
		if before, envelope, after, found := splitToolEnvelope(trimmed); found {
			if strings.EqualFold(strings.TrimSpace(b.Choice), "none") {
				return "", nil, fmt.Errorf("模型在 tool_choice=none 时返回了工具调用")
			}
			calls, err := b.parseToolEnvelope(envelope)
			if err != nil {
				return "", nil, err
			}
			return strings.Join(slices.DeleteFunc([]string{before, after}, func(part string) bool { return part == "" }), "\n\n"), calls, nil
		}
	}

	if b.requiresTool() {
		return "", nil, fmt.Errorf("模型没有返回必需的工具调用")
	}
	if len(b.Enum) > 0 {
		value := strings.Trim(trimmed, "\"'` \n")
		if !slices.Contains(b.Enum, value) {
			head := []rune(trimmed)
			return "", nil, fmt.Errorf("模型返回内容不是枚举取值之一；返回内容开头：%q", string(head[:min(len(head), 200)]))
		}
		return value, nil, nil
	}
	if len(b.JSONSchema) > 0 && b.SchemaUnchecked {
		return trimmed, nil, nil
	}
	if len(b.JSONSchema) > 0 {
		if err := validateJSONSchema(b.JSONSchema, []byte(trimmed)); err != nil {
			repaired := repairModelJSON(trimmed)
			if validateJSONSchema(b.JSONSchema, []byte(repaired)) != nil {
				head := []rune(trimmed)
				return "", nil, fmt.Errorf("模型返回内容不符合 JSON Schema: %w；返回内容开头：%q", err, string(head[:min(len(head), 200)]))
			}
			trimmed = repaired
		}
		return trimmed, nil, nil
	}
	return text, nil, nil
}

// toolEnvelopeStart 匹配工具调用信封的开头
var toolEnvelopeStart = regexp.MustCompile(`\{\s*"tool_calls"\s*:`)

// openingFenceRe 匹配信封前的代码块开头（支持紧贴文字、空格或换行）
var openingFenceRe = regexp.MustCompile("```+[ \t]*[a-zA-Z0-9_-]*\\s*$")

// trailingFenceRe 匹配末尾残留的多余代码块标记
var trailingFenceRe = regexp.MustCompile("\\s*`{3,}[a-zA-Z0-9_-]*\\s*$")

// leadingFenceRe 匹配开头残留的多余代码块标记
var leadingFenceRe = regexp.MustCompile("^\\s*`{3,}[a-zA-Z0-9_-]*\\s*")

// splitToolEnvelope 把回答分为信封前的文字、信封与信封后的文字，代码块标记不计入文字
func splitToolEnvelope(text string) (string, string, string, bool) {
	location := toolEnvelopeStart.FindStringIndex(text)
	if location == nil {
		return "", "", "", false
	}
	rawBefore, rest := text[:location[0]], text[location[0]:]
	before := strings.TrimSpace(rawBefore)

	if match := openingFenceRe.FindStringIndex(rawBefore); match != nil {
		before = strings.TrimSpace(rawBefore[:match[0]])
	} else if line := strings.LastIndex(before, "\n") + 1; strings.HasPrefix(strings.TrimSpace(before[line:]), "```") {
		before = strings.TrimSpace(before[:line])
	}
	before = trailingFenceRe.ReplaceAllString(before, "")
	before = strings.TrimSpace(before)

	decoder := json.NewDecoder(strings.NewReader(rest))
	var value json.RawMessage
	if decoder.Decode(&value) == nil {
		offset := int(decoder.InputOffset())
		envelope := strings.TrimSpace(rest[:offset])
		after := rest[offset:]
		if fenceLoc := strings.Index(after, "```"); fenceLoc >= 0 {
			fenceEnd := fenceLoc + 3
			for fenceEnd < len(after) && after[fenceEnd] == '`' {
				fenceEnd++
			}
			after = strings.TrimSpace(after[fenceEnd:])
		} else {
			after = strings.TrimSpace(after)
		}
		after = leadingFenceRe.ReplaceAllString(after, "")
		after = trailingFenceRe.ReplaceAllString(after, "")
		return before, envelope, strings.TrimSpace(after), true
	}

	if end := closingFence(rest); end >= 0 {
		fenceEnd := end + 3
		for fenceEnd < len(rest) && rest[fenceEnd] == '`' {
			fenceEnd++
		}
		after := strings.TrimSpace(rest[fenceEnd:])
		after = leadingFenceRe.ReplaceAllString(after, "")
		after = trailingFenceRe.ReplaceAllString(after, "")
		return before, strings.TrimSpace(rest[:end]), strings.TrimSpace(after), true
	}

	return before, strings.TrimSpace(rest), "", true
}

// closingFence 返回代码块中信封之后的结束标记位置：信封是有效 JSON 时取 JSON 之后的第一个标记，否则取第一个位于行首的标记
func closingFence(rest string) int {
	decoder := json.NewDecoder(strings.NewReader(rest))
	var value json.RawMessage
	if decoder.Decode(&value) == nil {
		offset := int(decoder.InputOffset())
		if end := strings.Index(rest[offset:], "```"); end >= 0 {
			return offset + end
		}
		return -1
	}
	if end := strings.Index(rest, "\n```"); end >= 0 {
		return end + 1
	}
	return strings.Index(rest, "```")
}

// parseToolEnvelope 解析信封中的工具调用并按声明校验，无法解析时修复非法转义、控制字符与缺失的闭合括号后再解析
func (b ToolBridge) parseToolEnvelope(envelope string) ([]ToolCall, error) {
	var parsed struct {
		ToolCalls []struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(envelope), &parsed); err != nil {
		if repairErr := json.Unmarshal([]byte(repairModelJSON(envelope)), &parsed); repairErr != nil {
			head := []rune(envelope)
			return nil, fmt.Errorf("模型返回的工具调用不是有效 JSON: %w；返回内容开头：%q", err, string(head[:min(len(head), 200)]))
		}
	}
	if len(parsed.ToolCalls) == 0 {
		return nil, fmt.Errorf("模型返回的工具调用为空")
	}
	calls := make([]ToolCall, 0, len(parsed.ToolCalls))
	for index, call := range parsed.ToolCalls {
		name := call.Name
		if !b.hasTool(name) && b.hasTool(strings.TrimLeft(name, "_")) {
			name = strings.TrimLeft(name, "_")
		}
		if !b.hasTool(name) {
			return nil, fmt.Errorf("模型返回了未声明的工具 %q", call.Name)
		}
		if b.explicitTool() != "" && name != b.explicitTool() {
			return nil, fmt.Errorf("模型返回工具 %q，预期工具 %q", name, b.explicitTool())
		}
		if len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
			return nil, fmt.Errorf("工具 %q 的 arguments 不是有效 JSON", name)
		}
		if err := validateJSONSchema(b.tool(name).Parameters, call.Arguments); err != nil {
			return nil, fmt.Errorf("工具 %q 的 arguments 不符合参数结构: %w", name, err)
		}
		calls = append(calls, ToolCall{ID: fmt.Sprintf("call_%d", index+1), Name: name, Arguments: call.Arguments})
	}
	return calls, nil
}

// repairModelJSON 修复模型输出的 JSON：字符串中非法转义的反斜杠与控制字符改为转义，缺失的闭合括号按嵌套补齐
func repairModelJSON(text string) string {
	var builder strings.Builder
	var closers []byte
	inString := false
	for index := 0; index < len(text); index++ {
		char := text[index]
		if inString {
			switch {
			case char == '\\' && index+1 < len(text) && validEscape(text[index+1:]):
				builder.WriteByte(char)
				index++
				char = text[index]
			case char == '\\':
				builder.WriteString(`\\`)
				continue
			case char == '"':
				inString = false
			case char < 0x20:
				fmt.Fprintf(&builder, `\u%04x`, char)
				continue
			}
			builder.WriteByte(char)
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{':
			closers = append(closers, '}')
		case '[':
			closers = append(closers, ']')
		case '}', ']':
			for len(closers) > 0 && closers[len(closers)-1] != char {
				builder.WriteByte(closers[len(closers)-1])
				closers = closers[:len(closers)-1]
			}
			if len(closers) > 0 {
				closers = closers[:len(closers)-1]
			}
		}
		builder.WriteByte(char)
	}
	for index := len(closers) - 1; index >= 0; index-- {
		builder.WriteByte(closers[index])
	}
	return builder.String()
}

// validEscape 判断反斜杠后的内容是否为 JSON 字符串的合法转义
func validEscape(rest string) bool {
	switch rest[0] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	case 'u':
		if len(rest) < 5 {
			return false
		}
		_, err := strconv.ParseUint(rest[1:5], 16, 16)
		return err == nil
	}
	return false
}

func (b ToolBridge) hasTool(name string) bool {
	return b.tool(name).Name != ""
}

func (b ToolBridge) tool(name string) ToolDefinition {
	for _, definition := range b.Definitions {
		if definition.Name == name {
			return definition
		}
	}
	return ToolDefinition{}
}

func (b ToolBridge) requiresTool() bool {
	if len(b.Definitions) == 0 {
		return false
	}
	choice := strings.TrimSpace(strings.ToLower(b.Choice))
	return choice == "required" || (choice != "" && choice != "auto" && choice != "none")
}

func validateHostedOutput(output CandidateOutput, bridge ToolBridge) error {
	if bridge.disallowsHostedTools() {
		if len(output.Codes) > 0 || len(output.Citations) > 0 || hasGeneratedImage(output) {
			return fmt.Errorf("模型在 tool_choice=none 时使用了内置工具")
		}
		return nil
	}
	if bridge.RequireCodeExecution && len(output.Codes) == 0 {
		return fmt.Errorf("Gemini Web 未执行必需的代码工具")
	}
	if bridge.RequireWebSearch && len(output.Citations) == 0 {
		return fmt.Errorf("Gemini Web 未执行必需的搜索工具")
	}
	if bridge.RequireImageGeneration && !hasGeneratedImage(output) {
		return fmt.Errorf("Gemini Web 未执行必需的图片生成工具")
	}
	return nil
}

func (b ToolBridge) disallowsHostedTools() bool {
	return strings.EqualFold(strings.TrimSpace(b.Choice), "none")
}

func (b ToolBridge) explicitTool() string {
	choice := strings.TrimSpace(b.Choice)
	switch strings.ToLower(choice) {
	case "", "auto", "none", "required", "any":
		return ""
	default:
		return choice
	}
}

func stripJSONFence(text string) string {
	if !strings.HasPrefix(text, "```") || !strings.HasSuffix(text, "```") {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return text
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

// validateJSONSchema 按 Schema 校验 JSON 值，Schema 编译不了时只校验 JSON 语法
func validateJSONSchema(schemaDocument json.RawMessage, instanceDocument []byte) error {
	if len(schemaDocument) == 0 {
		return nil
	}
	schema, compileErr := compileJSONSchema(schemaDocument)
	decoder := json.NewDecoder(bytes.NewReader(instanceDocument))
	decoder.UseNumber()
	var instance interface{}
	if err := decoder.Decode(&instance); err != nil {
		return err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return err
	}
	if compileErr != nil {
		return nil
	}
	return schema.Validate(instance)
}

// schemaResourceURL 是编译请求中 JSON Schema 时的资源地址，校验错误以它指代该 Schema
const schemaResourceURL = "mem:///schema.json"

func compileJSONSchema(schemaDocument json.RawMessage) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	schemaDecoder := json.NewDecoder(bytes.NewReader(schemaDocument))
	schemaDecoder.UseNumber()
	var schemaResource interface{}
	if err := schemaDecoder.Decode(&schemaResource); err != nil {
		return nil, err
	}
	if err := compiler.AddResource(schemaResourceURL, schemaResource); err != nil {
		return nil, err
	}
	schema, err := compiler.Compile(schemaResourceURL)
	if err != nil {
		return nil, err
	}
	return schema, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra interface{}
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("JSON 包含多个顶层值")
	}
	return err
}

func assignToolCallIDs(calls []ToolCall, responseID string) []ToolCall {
	result := append([]ToolCall(nil), calls...)
	for index := range result {
		result[index].ID = fmt.Sprintf("call_%s_%d", responseID, index+1)
	}
	return result
}
