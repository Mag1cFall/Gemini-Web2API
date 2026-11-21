package adapter

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"unicode"
)

// geminiResponseFormat 为 generationConfig.responseFormat 的一项输出格式
type geminiResponseFormat struct {
	Text *struct {
		MIMEType string          `json:"mimeType"`
		Schema   json.RawMessage `json:"schema"`
	} `json:"text"`
	Image *struct {
		AspectRatio string `json:"aspectRatio"`
		ImageSize   string `json:"imageSize"`
	} `json:"image"`
	Video json.RawMessage `json:"video"`
}

// geminiResponseFormats 按 REST 的单个对象或 SDK 的数组读取 responseFormat
func geminiResponseFormats(raw json.RawMessage) ([]geminiResponseFormat, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if trimmed[0] == '{' {
		var format geminiResponseFormat
		err := json.Unmarshal(trimmed, &format)
		return []geminiResponseFormat{format}, err
	}
	var formats []geminiResponseFormat
	err := json.Unmarshal(trimmed, &formats)
	return formats, err
}

// geminiResponseFormatValues 为 responseFormat 枚举名对应的取值
var geminiResponseFormatValues = map[string]string{
	"APPLICATION_JSON":                "application/json",
	"TEXT_PLAIN":                      "text/plain",
	"ASPECT_RATIO_ONE_BY_ONE":         "1:1",
	"ASPECT_RATIO_TWO_BY_THREE":       "2:3",
	"ASPECT_RATIO_THREE_BY_TWO":       "3:2",
	"ASPECT_RATIO_THREE_BY_FOUR":      "3:4",
	"ASPECT_RATIO_FOUR_BY_THREE":      "4:3",
	"ASPECT_RATIO_FOUR_BY_FIVE":       "4:5",
	"ASPECT_RATIO_FIVE_BY_FOUR":       "5:4",
	"ASPECT_RATIO_NINE_BY_SIXTEEN":    "9:16",
	"ASPECT_RATIO_SIXTEEN_BY_NINE":    "16:9",
	"ASPECT_RATIO_TWENTY_ONE_BY_NINE": "21:9",
	"ASPECT_RATIO_ONE_BY_EIGHT":       "1:8",
	"ASPECT_RATIO_EIGHT_BY_ONE":       "8:1",
	"ASPECT_RATIO_ONE_BY_FOUR":        "1:4",
	"ASPECT_RATIO_FOUR_BY_ONE":        "4:1",
}

// geminiResponseFormatValue 把 responseFormat 枚举名换成取值，其他值原样返回
func geminiResponseFormatValue(value string) string {
	if mapped, ok := geminiResponseFormatValues[value]; ok {
		return mapped
	}
	return value
}

// geminiSchemaCounts 为 Gemini Schema 中按 int64 字符串传输的计数字段
var geminiSchemaCounts = map[string]bool{"minItems": true, "maxItems": true, "minLength": true, "maxLength": true, "minProperties": true, "maxProperties": true}

// geminiSchema 把 Gemini Schema（大写类型、nullable、int64 字符串）转换为 JSON Schema
func geminiSchema(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return raw, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema any
	if err := decoder.Decode(&schema); err != nil {
		return nil, err
	}
	return json.Marshal(convertGeminiSchema(schema))
}

// convertGeminiSchema 递归转换一个 Gemini Schema 节点
func convertGeminiSchema(node any) any {
	schema, ok := node.(map[string]any)
	if !ok {
		return node
	}
	converted := make(map[string]any, len(schema))
	for key, value := range schema {
		switch key {
		case "type":
			if name, ok := value.(string); ok {
				if name = strings.ToLower(name); name != "type_unspecified" {
					converted[key] = name
				}
				continue
			}
		case "nullable":
			continue
		case "properties", "$defs", "definitions":
			if properties, ok := value.(map[string]any); ok {
				mapped := make(map[string]any, len(properties))
				for name, property := range properties {
					mapped[name] = convertGeminiSchema(property)
				}
				value = mapped
			}
		case "items", "additionalProperties":
			value = convertGeminiSchema(value)
		case "anyOf", "oneOf", "allOf":
			if items, ok := value.([]any); ok {
				mapped := make([]any, len(items))
				for index, item := range items {
					mapped[index] = convertGeminiSchema(item)
				}
				value = mapped
			}
		default:
			if count, ok := value.(string); ok && geminiSchemaCounts[key] {
				value = json.Number(count)
			}
		}
		converted[key] = value
	}
	if nullable, _ := schema["nullable"].(bool); nullable {
		return map[string]any{"anyOf": []any{converted, map[string]any{"type": "null"}}}
	}
	return converted
}

// geminiOpaqueFields 为取值是自由 JSON 的字段，其中的键名属于用户数据
var geminiOpaqueFields = map[string]bool{
	"args": true, "response": true, "parameters": true, "parametersJsonSchema": true,
	"responseSchema": true, "responseJsonSchema": true, "partMetadata": true, "schema": true,
}

// geminiCamelKeys 按 proto3 JSON 规则把各层消息字段名转为 lowerCamelCase，同层两种写法并存时保留驼峰字段，自由 JSON 字段的取值原样保留；只扫描一遍定位要改名的键，其余字节原样复制
func geminiCamelKeys(raw []byte) ([]byte, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '{' && value[0] != '[' {
		return raw, nil
	}
	if !json.Valid(value) {
		var invalid json.RawMessage
		return nil, json.Unmarshal(value, &invalid)
	}
	scanner := camelScanner{data: value}
	scanner.value(0, false)
	if len(scanner.renames) == 0 {
		return raw, nil
	}
	slices.SortFunc(scanner.renames, func(a, b keyRename) int { return a.start - b.start })
	output := make([]byte, 0, len(value))
	last := 0
	for _, rename := range scanner.renames {
		output = append(output, value[last:rename.start]...)
		output = append(output, '"')
		output = append(output, rename.name...)
		output = append(output, '"')
		last = rename.end
	}
	return append(output, value[last:]...), nil
}

// keyRename 是一个要改名的键：data[start:end] 是带引号的原键，name 是新键名
type keyRename struct {
	start, end int
	name       string
}

// camelScanner 在合法 JSON 中定位需要改为驼峰写法的消息字段名
type camelScanner struct {
	data    []byte
	renames []keyRename
}

// value 扫描从 i 开始的 JSON 值并返回其后的位置，opaque 为真时只跳过不记录
func (s *camelScanner) value(i int, opaque bool) int {
	i = s.space(i)
	switch s.data[i] {
	case '{':
		return s.object(i, opaque)
	case '[':
		i = s.space(i + 1)
		if s.data[i] == ']' {
			return i + 1
		}
		for {
			i = s.space(s.value(i, opaque))
			if s.data[i] == ']' {
				return i + 1
			}
			i++
		}
	case '"':
		return s.text(i)
	}
	for i < len(s.data) && !strings.ContainsRune(",}] \t\r\n", rune(s.data[i])) {
		i++
	}
	return i
}

// object 扫描从 i 开始的对象：成员名有下划线且同层没有驼峰写法时记为改名，自由 JSON 字段的取值不进入
func (s *camelScanner) object(i int, opaque bool) int {
	type member struct {
		start, end int
		name       string
	}
	var members []member
	i = s.space(i + 1)
	for s.data[i] != '}' {
		start := i
		i = s.text(i)
		var name string
		_ = json.Unmarshal(s.data[start:i], &name)
		members = append(members, member{start, i, name})
		i = s.space(i)
		i = s.space(s.value(i+1, opaque || geminiOpaqueFields[geminiJSONName(name)]))
		if s.data[i] == ',' {
			i = s.space(i + 1)
		}
	}
	if opaque {
		return i + 1
	}
	for _, item := range members {
		camel := geminiJSONName(item.name)
		if camel == item.name || slices.ContainsFunc(members, func(other member) bool { return other.name == camel }) {
			continue
		}
		s.renames = append(s.renames, keyRename{item.start, item.end, camel})
	}
	return i + 1
}

// text 返回从 i 的引号开始的字符串之后的位置
func (s *camelScanner) text(i int) int {
	j := i + 1
	for {
		quote := j + bytes.IndexByte(s.data[j:], '"')
		escapes := 0
		for k := quote - 1; s.data[k] == '\\'; k-- {
			escapes++
		}
		if escapes%2 == 0 {
			return quote + 1
		}
		j = quote + 1
	}
}

// space 返回 i 起第一个非空白字符的位置
func (s *camelScanner) space(i int) int {
	for i < len(s.data) && (s.data[i] == ' ' || s.data[i] == '\t' || s.data[i] == '\r' || s.data[i] == '\n') {
		i++
	}
	return i
}

// geminiJSONName 返回 proto 字段名对应的 JSON 名
func geminiJSONName(name string) string {
	if !strings.Contains(name, "_") {
		return name
	}
	var builder strings.Builder
	upper := false
	for _, char := range strings.TrimLeft(name, "_") {
		if char == '_' {
			upper = true
			continue
		}
		if upper {
			char, upper = unicode.ToUpper(char), false
		}
		builder.WriteRune(char)
	}
	return builder.String()
}
