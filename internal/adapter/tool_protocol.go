package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// callStartPattern 匹配调用块的开头，上游以 Markdown 转义方括号时前面带反斜杠
var callStartPattern = regexp.MustCompile(`\\?\[\s*Call\s*\\?:\s*([^\]\r\n\\]+?)\s*\\?\]`)

// argStartPattern 匹配位于开头的参数段开头
var argStartPattern = regexp.MustCompile(`^\\?\[\s*Arg\s*\\?:\s*([^\]\r\n\\]+?)\s*\\?\]`)

// argEndPattern 匹配位于开头的参数段结尾
var argEndPattern = regexp.MustCompile(`^\\?\[\s*\\?/\s*Arg\s*\\?\]`)

// callEndPattern 匹配位于开头的调用块结尾
var callEndPattern = regexp.MustCompile(`^\\?\[\s*\\?/\s*Call\s*\\?\]`)

// structureAnywherePattern 在任意位置匹配参数段开头、参数段结尾或调用块结尾
var structureAnywherePattern = regexp.MustCompile(`\\?\[\s*(?:Arg\s*\\?:|\\?/\s*(?:Arg|Call)\s*\\?\])`)

// backtickRunPattern 匹配连续的反引号串
var backtickRunPattern = regexp.MustCompile("`+")

// callBlock 是回答中的一个调用块：工具名称与按出现顺序排列的参数原文
type callBlock struct {
	Name string
	Args []callArg
}

// callArg 是调用块中的一个参数：参数名与原文
type callArg struct {
	Name  string
	Value string
}

// firstCallStart 返回第一个位于代码以外的调用块开头的字节区间，位于所在行的行内代码或围栏代码块内的是示例
func firstCallStart(text string) []int {
	for _, location := range callStartPattern.FindAllStringIndex(text, -1) {
		if inside, _ := codeSpanAt(text, location[0]); !inside && !gemini.InFencedCode(text, location[0]) {
			return location
		}
	}
	return nil
}

// codeSpanAt 按所在行判断字节位置与行内代码的关系，反引号串与其后第一个长度相同的反引号串构成行内代码：inside 表示位置在行内代码中，unclosed 表示位置之前有该行没有闭合的反引号串
func codeSpanAt(text string, position int) (inside, unclosed bool) {
	start := strings.LastIndexByte(text[:position], '\n') + 1
	end := len(text)
	if newline := strings.IndexByte(text[position:], '\n'); newline >= 0 {
		end = position + newline
	}
	runs := backtickRunPattern.FindAllStringIndex(text[start:end], -1)
	for index := 0; index < len(runs) && runs[index][0]+start < position; index++ {
		size := runs[index][1] - runs[index][0]
		closing := slices.IndexFunc(runs[index+1:], func(run []int) bool { return run[1]-run[0] == size })
		if closing < 0 {
			unclosed = true
			continue
		}
		index += closing + 1
		if runs[index][0]+start >= position {
			return true, unclosed
		}
	}
	return false, unclosed
}

// splitCallBlocks 把回答分为调用块之前的文字与连续的调用块，之后的内容丢弃；找到调用块开头时 found 为真，调用块没有写完时返回错误
func splitCallBlocks(text string) (before string, blocks []callBlock, found bool, err error) {
	location := firstCallStart(text)
	if location == nil {
		return "", nil, false, nil
	}
	before = strings.TrimRightFunc(text[:location[0]], unicode.IsSpace)
	rest := text[location[0]:]
	for {
		start := callStartPattern.FindStringSubmatchIndex(rest)
		if start == nil || start[0] != 0 {
			return before, blocks, true, nil
		}
		block := callBlock{Name: strings.TrimSpace(rest[start[2]:start[3]])}
		rest = rest[start[1]:]
		for {
			rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
			if end := callEndPattern.FindStringIndex(rest); end != nil {
				rest = rest[end[1]:]
				break
			}
			if next := callStartPattern.FindStringIndex(rest); next != nil && next[0] == 0 {
				break
			}
			arg := argStartPattern.FindStringSubmatchIndex(rest)
			if arg == nil {
				head := []rune(rest)
				return before, nil, true, fmt.Errorf("工具 %q 的调用块不完整；剩余内容开头：%q", block.Name, string(head[:min(len(head), 200)]))
			}
			value, remaining := argValue(rest[arg[1]:])
			block.Args = append(block.Args, callArg{Name: strings.TrimSpace(rest[arg[2]:arg[3]]), Value: restoreMarkers(value)})
			rest = strings.TrimLeftFunc(remaining, unicode.IsSpace)
			if end := argEndPattern.FindStringIndex(rest); end != nil {
				rest = rest[end[1]:]
			}
		}
		blocks = append(blocks, block)
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	}
}

// argValue 返回参数段的值与其后的内容：值写在代码块中时取代码块原文，结束标记取其后紧跟结构标记或回答结尾的第一行；不在代码块中的值取到下一个结构标记为止
func argValue(text string) (string, string) {
	body := strings.TrimLeft(text, " \t")
	if newline := strings.IndexByte(body, '\n'); newline >= 0 && strings.TrimSpace(body[:newline]) == "" {
		body = strings.TrimLeft(body[newline+1:], " \t")
	}
	fence := len(body) - len(strings.TrimLeft(body, "`"))
	if fence < 3 {
		end := len(body)
		if location := structureAnywherePattern.FindStringIndex(body); location != nil {
			end = location[0]
		}
		return strings.TrimSpace(body[:end]), body[end:]
	}
	newline := strings.IndexByte(body, '\n')
	if newline < 0 {
		return "", ""
	}
	content := body[newline+1:]
	for offset := 0; ; {
		line := strings.IndexByte(content[offset:], '\n')
		current, next := content[offset:], len(content)
		if line >= 0 {
			current, next = content[offset:offset+line], offset+line+1
		}
		trimmed := strings.TrimSpace(current)
		if len(trimmed) >= fence && strings.Trim(trimmed, "`") == "" && structureFollows(content[next:]) {
			return strings.TrimSuffix(content[:max(offset-1, 0)], "\r"), content[next:]
		}
		if line < 0 {
			return strings.TrimRight(content, "\r\n"), ""
		}
		offset = next
	}
}

// structureFollows 判断代码块结束标记之后是否紧跟参数段结尾、下一参数、调用块结尾、下一调用块或回答结尾
func structureFollows(after string) bool {
	rest := strings.TrimLeftFunc(after, unicode.IsSpace)
	if rest == "" || argEndPattern.MatchString(rest) || argStartPattern.MatchString(rest) || callEndPattern.MatchString(rest) {
		return true
	}
	location := callStartPattern.FindStringIndex(rest)
	return location != nil && location[0] == 0
}

// resolveCalls 按声明把调用块转为工具调用：工具名去掉前导下划线后匹配，参数按 Schema 还原类型后校验
func (b ToolBridge) resolveCalls(blocks []callBlock) ([]ToolCall, error) {
	if len(blocks) == 0 {
		return nil, fmt.Errorf("模型返回的工具调用为空")
	}
	calls := make([]ToolCall, 0, len(blocks))
	for index, block := range blocks {
		name := block.Name
		if !b.hasTool(name) && b.hasTool(strings.TrimLeft(name, "_")) {
			name = strings.TrimLeft(name, "_")
		}
		if !b.hasTool(name) {
			return nil, fmt.Errorf("模型返回了未声明的工具 %q", block.Name)
		}
		if b.explicitTool() != "" && name != b.explicitTool() {
			return nil, fmt.Errorf("模型返回工具 %q，预期工具 %q", name, b.explicitTool())
		}
		arguments, err := typedArguments(b.tool(name).Parameters, block.Args)
		if err != nil {
			return nil, fmt.Errorf("工具 %q 的参数无效: %w", name, err)
		}
		if err := validateJSONSchema(b.tool(name).Parameters, arguments); err != nil {
			return nil, fmt.Errorf("工具 %q 的 arguments 不符合参数结构: %w", name, err)
		}
		calls = append(calls, ToolCall{ID: fmt.Sprintf("call_%d", index+1), Name: name, Arguments: arguments})
	}
	return calls, nil
}

// typedArguments 把参数原文按参数 Schema 组成 JSON 对象
func typedArguments(parameters json.RawMessage, args []callArg) (json.RawMessage, error) {
	object := make(map[string]json.RawMessage, len(args))
	for _, arg := range args {
		value, err := typedValue(parameters, propertySchema(parameters, parameters, arg.Name, map[string]bool{}), arg.Value)
		if err != nil {
			return nil, fmt.Errorf("参数 %q %w", arg.Name, err)
		}
		object[arg.Name] = value
	}
	return marshalJSON(object)
}

// propertySchema 返回 Schema 中参数 name 的子 Schema：先取 properties，再展开本地 $ref 与 allOf，最后取 additionalProperties，seen 是已展开的引用
func propertySchema(root, schema json.RawMessage, name string, seen map[string]bool) json.RawMessage {
	var node struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		Ref                  string                     `json:"$ref"`
		AllOf                []json.RawMessage          `json:"allOf"`
		AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	}
	if json.Unmarshal(schema, &node) != nil {
		return nil
	}
	if property, ok := node.Properties[name]; ok {
		return property
	}
	branches := node.AllOf
	if node.Ref != "" && !seen[node.Ref] {
		seen[node.Ref] = true
		branches = append(branches, schemaRef(root, node.Ref))
	}
	for _, branch := range branches {
		if property := propertySchema(root, branch, name, seen); property != nil {
			return property
		}
	}
	return node.AdditionalProperties
}

// marshalJSON 按 JSON.stringify 编码，HTML 字符保持原样
func marshalJSON(value any) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// typedValue 按 Schema 允许的类型转换原文：只允许字符串时取原文；原文是允许类型的非字符串 JSON 时取该值；未声明类型时 JSON 取值、其余取原文；允许字符串时取原文
func typedValue(root, schema json.RawMessage, raw string) (json.RawMessage, error) {
	types := schemaTypes(root, schema, map[string]bool{})
	if len(types) == 1 && types["string"] {
		return marshalJSON(raw)
	}
	trimmed := strings.TrimSpace(raw)
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if trimmed != "" && decoder.Decode(&value) == nil && ensureJSONEnd(decoder) == nil {
		kind := jsonKind(value)
		if kind != "string" && (len(types) == 0 || types[kind] || kind == "integer" && types["number"]) {
			return json.RawMessage(trimmed), nil
		}
	}
	if len(types) == 0 || types["string"] {
		return marshalJSON(raw)
	}
	return nil, fmt.Errorf("的原文不是 %s 类型的 JSON", strings.Join(slices.Sorted(maps.Keys(types)), "、"))
}

// schemaTypes 返回 Schema 中 type、anyOf、oneOf、allOf、enum、const 与本地 $ref 声明的 JSON 类型，seen 是已展开的引用
func schemaTypes(root, schema json.RawMessage, seen map[string]bool) map[string]bool {
	var node struct {
		Type  json.RawMessage   `json:"type"`
		Ref   string            `json:"$ref"`
		AnyOf []json.RawMessage `json:"anyOf"`
		OneOf []json.RawMessage `json:"oneOf"`
		AllOf []json.RawMessage `json:"allOf"`
		Enum  []any             `json:"enum"`
		Const json.RawMessage   `json:"const"`
	}
	types := map[string]bool{}
	if json.Unmarshal(schema, &node) != nil {
		return types
	}
	var single string
	var multiple []string
	if json.Unmarshal(node.Type, &single) == nil && single != "" {
		types[strings.ToLower(single)] = true
	} else if json.Unmarshal(node.Type, &multiple) == nil {
		for _, name := range multiple {
			types[strings.ToLower(name)] = true
		}
	}
	branches := slices.Concat(node.AnyOf, node.OneOf, node.AllOf)
	if node.Ref != "" && !seen[node.Ref] {
		seen[node.Ref] = true
		branches = append(branches, schemaRef(root, node.Ref))
	}
	for _, branch := range branches {
		for name := range schemaTypes(root, branch, seen) {
			types[name] = true
		}
	}
	var fixed any
	if json.Unmarshal(node.Const, &fixed) == nil {
		node.Enum = append(node.Enum, fixed)
	}
	for _, value := range node.Enum {
		types[jsonKind(value)] = true
	}
	return types
}

// schemaRef 返回 root 中本地引用 ref 指向的子 Schema，找不到时为空
func schemaRef(root json.RawMessage, ref string) json.RawMessage {
	pointer, local := strings.CutPrefix(ref, "#")
	if !local {
		return nil
	}
	current := root
	for _, token := range strings.Split(pointer, "/")[1:] {
		var object map[string]json.RawMessage
		if json.Unmarshal(current, &object) != nil {
			return nil
		}
		current = object[strings.NewReplacer("~1", "/", "~0", "~").Replace(token)]
	}
	return current
}

// jsonKind 返回 JSON 值的 Schema 类型名，整数记为 integer
func jsonKind(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		if rational, ok := new(big.Rat).SetString(typed.String()); ok && rational.IsInt() {
			return "integer"
		}
		return "number"
	case float64:
		if typed == float64(int64(typed)) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	default:
		return "object"
	}
}

// writeCallBlock 按工具协议把一次工具调用写入提示词：每个参数一个代码块，字符串写原文，其余类型写 JSON
func writeCallBlock(builder *strings.Builder, name string, arguments json.RawMessage) {
	startBlock(builder)
	fmt.Fprintf(builder, "[Call:%s]\n", name)
	var object map[string]json.RawMessage
	if json.Unmarshal(arguments, &object) == nil {
		for _, key := range slices.Sorted(maps.Keys(object)) {
			text := string(object[key])
			if strings.HasPrefix(text, `"`) {
				_ = json.Unmarshal(object[key], &text)
			}
			fmt.Fprintf(builder, "[Arg:%s]\n%s[/Arg]\n", key, fenced(text))
		}
	}
	builder.WriteString("[/Call]\n")
}

// toolCallRef 是历史中一次工具调用的 ID 与名称
type toolCallRef struct {
	ID   string
	Name string
}

// labelToolCalls 把同一轮调用的结果标签按调用 ID 写入 labels：同名工具调用一次时为名称，多次时为“名称 #序号”
func labelToolCalls(labels map[string]string, calls []toolCallRef) {
	total := map[string]int{}
	for _, call := range calls {
		total[call.Name]++
	}
	current := map[string]int{}
	for _, call := range calls {
		current[call.Name]++
		labels[call.ID] = call.Name
		if total[call.Name] > 1 {
			labels[call.ID] = fmt.Sprintf("%s #%d", call.Name, current[call.Name])
		}
	}
}

// writeResultBlock 把一次工具结果写入提示词，结果原文放在代码块中，空结果写为没有输出
func writeResultBlock(builder *strings.Builder, name string, content string) {
	if strings.TrimSpace(content) == "" {
		content = "（执行完成，没有输出）"
	}
	startBlock(builder)
	fmt.Fprintf(builder, "[Result:%s]\n%s[/Result]\n", name, fenced(content))
}

// startBlock 让结构块从新的一行开始
func startBlock(builder *strings.Builder) {
	if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteByte('\n')
	}
}

var (
	// transcriptMarker 匹配转录轮次标记
	transcriptMarker = regexp.MustCompile(`<\|im_(start|end)\|>`)
	// markerEntity 匹配写成实体的轮次标记，第一组是原文字面实体多转义的 amp; 层
	markerEntity = regexp.MustCompile(`&((?:amp;)*)lt;\|im_(start|end)\|&gt;`)
)

// escapeMarkers 把原文中的字面实体多转义一层，再把轮次标记写成实体
func escapeMarkers(text string) string {
	text = markerEntity.ReplaceAllString(text, "&amp;${1}lt;|im_${2}|&gt;")
	return transcriptMarker.ReplaceAllString(text, "&lt;|im_${1}|&gt;")
}

// restoreMarkers 把模型写在工具参数中的实体还原为轮次标记，多转义的字面实体去掉一层
func restoreMarkers(text string) string {
	return markerEntity.ReplaceAllStringFunc(text, func(entity string) string {
		if rest, layered := strings.CutPrefix(entity, "&amp;"); layered {
			return "&" + rest
		}
		return "<" + entity[len("&lt;"):len(entity)-len("&gt;")] + ">"
	})
}

// fenced 返回包住原文的代码块，原文中的轮次标记写成实体，标记比原文中最长的反引号串多一个且不少于三个
func fenced(text string) string {
	if strings.Contains(text, "|im_") {
		text = escapeMarkers(text)
	}
	longest, run := 0, 0
	for _, char := range text {
		if char == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + "\n" + text + "\n" + fence + "\n"
}
