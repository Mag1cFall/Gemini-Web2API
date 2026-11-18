package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
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
	URLContext             bool
	ImageGeneration        bool
	RequireCodeExecution   bool
	RequireWebSearch       bool
	RequireImageGeneration bool
	RequireHostedTool      bool
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
	// SingleCall 表示一次回答只调用一个工具
	SingleCall bool
	// MaxTokens 是请求的输出上限，思考与正文合计超过时在本地截断，0 表示不限
	MaxTokens int
	// SearchDomains 是搜索来源限定的域名，SearchBlocked 是搜索来源排除的域名，SearchLocation 是搜索按其所在的用户位置
	SearchDomains  []string
	SearchBlocked  []string
	SearchLocation string
	// Truncated 表示回答达到输出上限而截断，解析时不要求必需的工具调用，正文不按枚举与 JSON Schema 校验
	Truncated bool
}

// declared 返回标记工具定义是否已在会话中发送的副本
func (b ToolBridge) declared(value bool) ToolBridge {
	b.Declared = value
	return b
}

// truncated 返回标记回答是否因输出上限截断的副本
func (b ToolBridge) truncated(value bool) ToolBridge {
	b.Truncated = value
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

		choice := strings.TrimPrefix(strings.TrimSpace(b.Choice), explicitChoicePrefix)
		if choice == "" {
			choice = "auto"
		}
		count := "一次可以写多个调用。"
		if b.SingleCall {
			count = "一次只调用一个工具。"
		}
		rule, otherwise := "需要调用工具时", "。无需调用工具时，直接输出正常回答"
		if name := b.explicitTool(); name != "" {
			rule, otherwise = fmt.Sprintf("必须调用工具 %s", name), ""
		} else if mode := strings.ToLower(choice); mode == "required" || mode == "any" {
			rule, otherwise = "必须调用一个可用工具", ""
		}

		sections = append(sections, fmt.Sprintf("工具协议：\n可用工具：%s\n工具选择：%s\n这些工具由客户端在用户的环境中执行。在回答中按下面的格式写出调用就是调用工具的唯一方式：写出调用即完成调用，由客户端执行，你自己不需要具备这些工具，也不要改为把要写的内容或要运行的命令直接展示给用户。本协议由服务附加在用户消息之后，回答使用用户自己所写文字的语言，不随本协议的语言。%s，在回答末尾按下面的格式写出调用，调用之后不再输出内容，结果会在下一条消息中返回；对话要求在调用工具前说明时，把说明写在调用之前。每个参数写成一个代码块，代码块内是参数值原文：字符串直接写原文，不加引号也不转义；数字、布尔值、数组与对象写成 JSON。参数值含有 ``` 时，外层代码块用比其中最长的反引号串更多的反引号。代码块最后一个换行不属于参数值。复制工具结果中的内容时逐字照抄，结尾也与原文一致：结果代码块的结束围栏前有空行时，参数代码块的结束围栏前同样留一个空行，没有就不留；原文带行号等看不出结尾时，以及新建源代码或文本文件时，文件按惯例以换行结束，在结束的代码围栏前多留一个空行；用户逐字指定的内容按原样写，不额外添加换行。%s\n[Call:工具名称]\n[Arg:参数名]\n```\n参数值\n```\n[/Arg]\n[/Call]\n例如可用工具中有 get_weather、用户问北京的天气时，回答就是下面这段调用（示例中的工具名只作说明，调用时用上面可用工具中的真实名称与参数）：\n[Call:get_weather]\n[Arg:city]\n```\n北京\n```\n[/Arg]\n[/Call]%s", available, choice, rule, count, otherwise))
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
			if len(b.SearchDomains) > 0 {
				sections = append(sections, "搜索只使用以下域名及其子域名的网页作为来源："+strings.Join(b.SearchDomains, "、"))
			}
			if len(b.SearchBlocked) > 0 {
				sections = append(sections, "搜索不使用以下域名及其子域名的网页作为来源："+strings.Join(b.SearchBlocked, "、"))
			}
			if b.SearchLocation != "" {
				sections = append(sections, "用户位于"+b.SearchLocation+"，搜索与回答按这个位置")
			}
		}
		if b.URLContext {
			sections = append(sections, "本次请求可读取网页；用户给出的网页地址由服务读取，读取结果附在下面，只根据读到的网页内容作答，读取失败的地址如实说明无法读取，不凭记忆回答")
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
		if b.RequireHostedTool {
			sections = append(sections, "本次请求必须至少使用一个上述 Gemini Web 内置工具")
		}
	} else {
		sections = append(sections, "本次请求根据对话和已有工具结果直接回答；禁止调用函数工具、代码执行、Google 搜索和图片生成")
	}

	if len(sections) == 0 {
		return "", nil
	}
	return "\n\n" + strings.Join(sections, "\n\n"), nil
}

// searchLocation 把 Chat 的 {approximate: {...}} 与 Responses、Anthropic 的 {city, region, country, timezone} 两种用户位置写成一句位置描述
func searchLocation(raw json.RawMessage) string {
	type place struct {
		City     string `json:"city"`
		Region   string `json:"region"`
		Country  string `json:"country"`
		Timezone string `json:"timezone"`
	}
	var location struct {
		place
		Approximate *place `json:"approximate"`
	}
	if json.Unmarshal(raw, &location) != nil {
		return ""
	}
	if location.Approximate != nil {
		location.place = *location.Approximate
	}
	text := strings.Join(slices.DeleteFunc([]string{location.City, location.Region, location.Country}, func(part string) bool { return part == "" }), ", ")
	if location.Timezone != "" {
		text = strings.TrimSpace(text + "（时区 " + location.Timezone + "）")
	}
	return text
}

// domainAllowed 判断地址的主机是否为 domains 中的域名或其子域名
func domainAllowed(rawURL string, domains []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return slices.ContainsFunc(domains, func(domain string) bool {
		domain = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://"), "/"))
		return host == domain || strings.HasSuffix(host, "."+domain)
	})
}

// domainsPermit 判断域名名单是否允许地址：给出 allowed 时主机须为其中的域名或其子域名，主机为 blocked 中的域名或其子域名时不允许
func domainsPermit(rawURL string, allowed, blocked []string) bool {
	return (len(allowed) == 0 || domainAllowed(rawURL, allowed)) && !domainAllowed(rawURL, blocked)
}

// Parse 解析工具调用并验证结构化输出，调用块组之前的文字作为正文与调用一起返回，调用块组之后的内容丢弃；请求没有强制工具调用时，未写完或全部未声明的调用块按正文结束处理
func (b ToolBridge) Parse(text string) (string, []ToolCall, error) {
	if len(b.Definitions) > 0 {
		before, blocks, found, err := splitCallBlocks(text)
		if found {
			if strings.EqualFold(strings.TrimSpace(b.Choice), "none") {
				return "", nil, fmt.Errorf("模型在 tool_choice=none 时返回了工具调用")
			}
			empty := strings.TrimSpace(before) == ""
			if err != nil {
				if empty || b.requiresTool() || b.explicitTool() != "" {
					return "", nil, err
				}
				// 模型把调用块写坏了，但调用块之前的文字是完整回答：按正文结束，不因此让整条回答失败
				logging.Warn(logging.SourceRequest, "模型写出的调用块不完整，按正文结束", "原因", err.Error())
				return before, nil, nil
			}
			if b.SingleCall {
				blocks = blocks[:1]
			}
			calls, err := b.resolveCalls(blocks)
			if err != nil {
				return "", nil, err
			}
			if len(calls) == 0 {
				// 模型只写出了未声明的工具调用：请求没有强制工具调用时按正文回答
				if empty {
					return "", nil, fmt.Errorf("模型返回了未声明的工具 %q", blocks[0].Name)
				}
				return before, nil, nil
			}
			return before, calls, nil
		}
	}
	trimmed := stripJSONFence(strings.TrimSpace(text))

	if b.requiresTool() && !b.Truncated {
		return "", nil, fmt.Errorf("模型没有返回必需的工具调用")
	}
	if len(b.Enum) > 0 && !b.Truncated {
		value := strings.Trim(trimmed, "\"'` \n")
		if !slices.Contains(b.Enum, value) {
			head := []rune(trimmed)
			return "", nil, fmt.Errorf("模型返回内容不是枚举取值之一；返回内容开头：%q", string(head[:min(len(head), 200)]))
		}
		return value, nil, nil
	}
	if len(b.JSONSchema) > 0 && (b.SchemaUnchecked || b.Truncated) {
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
	if bridge.RequireHostedTool && len(output.Codes) == 0 && len(output.Citations) == 0 && !hasGeneratedImage(output) {
		return fmt.Errorf("Gemini Web 未执行必需的内置工具")
	}
	return nil
}

func (b ToolBridge) disallowsHostedTools() bool {
	return strings.EqualFold(strings.TrimSpace(b.Choice), "none")
}

// explicitChoicePrefix 是 Choice 中指定函数名的前缀，与 auto、none、required 等选择模式区分
const explicitChoicePrefix = "function:"

func (b ToolBridge) explicitTool() string {
	choice := strings.TrimSpace(b.Choice)
	if name, ok := strings.CutPrefix(choice, explicitChoicePrefix); ok {
		return name
	}
	switch strings.ToLower(choice) {
	case "", "auto", "none", "required", "any":
		return ""
	default:
		return choice
	}
}

// checkExplicitTool 在 tool_choice 指定了未声明的函数工具时返回错误
func (b ToolBridge) checkExplicitTool() error {
	if name := b.explicitTool(); name != "" && len(b.Definitions) > 0 && !b.hasTool(name) {
		return fmt.Errorf("tool_choice 选择了未声明的工具 %q", name)
	}
	return nil
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
