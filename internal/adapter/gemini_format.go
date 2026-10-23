package adapter

import (
	"bytes"
	"encoding/json"
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
		case "properties":
			if properties, ok := value.(map[string]any); ok {
				mapped := make(map[string]any, len(properties))
				for name, property := range properties {
					mapped[name] = convertGeminiSchema(property)
				}
				value = mapped
			}
		case "items":
			value = convertGeminiSchema(value)
		case "anyOf":
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

// geminiCamelKeys 按 proto3 JSON 规则把各层消息字段名转为 lowerCamelCase，同层两种写法并存时保留驼峰字段，自由 JSON 字段的取值原样保留
func geminiCamelKeys(raw []byte) ([]byte, bool, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '{' && value[0] != '[' {
		return raw, false, nil
	}
	changed := false
	if value[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(value, &items); err != nil {
			return nil, false, err
		}
		for index, item := range items {
			converted, itemChanged, err := geminiCamelKeys(item)
			if err != nil {
				return nil, false, err
			}
			items[index], changed = converted, changed || itemChanged
		}
		if !changed {
			return raw, false, nil
		}
		encoded, err := json.Marshal(items)
		return encoded, true, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return nil, false, err
	}
	converted := make(map[string]json.RawMessage, len(fields))
	for key, field := range fields {
		name := geminiJSONName(key)
		if name != key {
			changed = true
			if _, exists := fields[name]; exists {
				continue
			}
		}
		if !geminiOpaqueFields[name] {
			var fieldChanged bool
			var err error
			field, fieldChanged, err = geminiCamelKeys(field)
			if err != nil {
				return nil, false, err
			}
			changed = changed || fieldChanged
		}
		converted[name] = field
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(converted)
	return encoded, true, err
}

// geminiJSONName 返回 proto 字段名对应的 JSON 名
func geminiJSONName(name string) string {
	if !strings.Contains(name, "_") {
		return name
	}
	var builder strings.Builder
	upper := false
	for _, char := range name {
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
