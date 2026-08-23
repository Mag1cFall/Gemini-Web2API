package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ToolDefinition 描述公开协议中的函数工具
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall 描述模型选择执行的函数工具
type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolBridge 将函数工具和结构化输出转换为严格文本契约
type ToolBridge struct {
	Definitions []ToolDefinition
	Choice      string
	JSONSchema  json.RawMessage
}

// PromptSuffix 返回追加到用户提示词的协议约束
func (b ToolBridge) PromptSuffix() (string, error) {
	var sections []string

	if len(b.Definitions) > 0 {
		definitions, err := json.Marshal(b.Definitions)
		if err != nil {
			return "", fmt.Errorf("编码工具定义失败: %w", err)
		}

		choice := strings.TrimSpace(b.Choice)
		if choice == "" {
			choice = "auto"
		}

		sections = append(sections, fmt.Sprintf(`工具协议：
可用工具：%s
工具选择：%s
需要调用工具时，只输出一个 JSON 对象，格式必须为 {"tool_calls":[{"name":"工具名称","arguments":{}}]}，不要输出 Markdown 代码块或额外文字
无需调用工具时，直接输出正常回答`, definitions, choice))
	}

	if len(b.JSONSchema) > 0 {
		if _, err := compileJSONSchema(b.JSONSchema); err != nil {
			return "", fmt.Errorf("响应 JSON Schema 无效: %w", err)
		}
		sections = append(sections, fmt.Sprintf("结构化输出协议：只输出符合以下 JSON Schema 的 JSON 值，不要输出 Markdown 代码块或额外文字\n%s", b.JSONSchema))
	}

	if len(sections) == 0 {
		return "", nil
	}
	return "\n\n" + strings.Join(sections, "\n\n"), nil
}

// Parse 解析严格工具调用并验证结构化输出
func (b ToolBridge) Parse(text string) (string, []ToolCall, error) {
	trimmed := stripJSONFence(strings.TrimSpace(text))

	if len(b.Definitions) > 0 && json.Valid([]byte(trimmed)) {
		var envelope struct {
			ToolCalls []struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"tool_calls"`
		}
		if err := json.Unmarshal([]byte(trimmed), &envelope); err == nil && len(envelope.ToolCalls) > 0 {
			if strings.EqualFold(strings.TrimSpace(b.Choice), "none") {
				return "", nil, fmt.Errorf("模型在 tool_choice=none 时返回了工具调用")
			}
			calls := make([]ToolCall, 0, len(envelope.ToolCalls))
			for index, call := range envelope.ToolCalls {
				if !b.hasTool(call.Name) {
					return "", nil, fmt.Errorf("模型返回了未声明的工具 %q", call.Name)
				}
				if b.explicitTool() != "" && call.Name != b.explicitTool() {
					return "", nil, fmt.Errorf("模型返回工具 %q，预期工具 %q", call.Name, b.explicitTool())
				}
				if len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
					return "", nil, fmt.Errorf("工具 %q 的 arguments 不是有效 JSON", call.Name)
				}
				definition := b.tool(call.Name)
				if err := validateJSONSchema(definition.Parameters, call.Arguments); err != nil {
					return "", nil, fmt.Errorf("工具 %q 的 arguments 不符合参数结构: %w", call.Name, err)
				}
				calls = append(calls, ToolCall{
					ID:        fmt.Sprintf("call_%d", index+1),
					Name:      call.Name,
					Arguments: call.Arguments,
				})
			}
			return "", calls, nil
		}
	}

	if b.requiresTool() {
		return "", nil, fmt.Errorf("模型没有返回必需的工具调用")
	}
	if len(b.JSONSchema) > 0 {
		if err := validateJSONSchema(b.JSONSchema, []byte(trimmed)); err != nil {
			return "", nil, fmt.Errorf("模型返回内容不符合 JSON Schema: %w", err)
		}
		return trimmed, nil, nil
	}
	return text, nil, nil
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
	choice := strings.TrimSpace(strings.ToLower(b.Choice))
	return choice == "required" || (choice != "" && choice != "auto" && choice != "none")
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

func validateJSONSchema(schemaDocument json.RawMessage, instanceDocument []byte) error {
	if len(schemaDocument) == 0 {
		return nil
	}
	schema, err := compileJSONSchema(schemaDocument)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(instanceDocument))
	decoder.UseNumber()
	var instance interface{}
	if err := decoder.Decode(&instance); err != nil {
		return err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return err
	}
	return schema.Validate(instance)
}

func compileJSONSchema(schemaDocument json.RawMessage) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	schemaDecoder := json.NewDecoder(bytes.NewReader(schemaDocument))
	schemaDecoder.UseNumber()
	var schemaResource interface{}
	if err := schemaDecoder.Decode(&schemaResource); err != nil {
		return nil, err
	}
	if err := compiler.AddResource("schema.json", schemaResource); err != nil {
		return nil, err
	}
	schema, err := compiler.Compile("schema.json")
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
