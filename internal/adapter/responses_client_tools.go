package adapter

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

// customToolParameters 是 custom 工具作为函数声明时唯一的字符串参数
var customToolParameters = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string","description":"The raw input passed to the tool"}},"required":["input"]}`)

// responsesClientTools 是 Responses 客户端执行工具对应的固定函数声明
var responsesClientTools = map[string]ToolDefinition{
	"local_shell": {
		Name: "local_shell", Kind: "local_shell",
		Description: "Runs a command on the user's machine and returns its output.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"},"description":"The program and its arguments"},"working_directory":{"type":"string","description":"The working directory to run the command in"},"timeout_ms":{"type":"integer","description":"The timeout for the command in milliseconds"}},"required":["command"]}`),
	},
	"shell": {
		Name: "shell", Kind: "shell",
		Description: "Runs shell commands in the user's environment and returns their output.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"commands":{"type":"array","items":{"type":"string"},"description":"Shell commands to run in order"},"timeout_ms":{"type":"integer","description":"The timeout for the commands in milliseconds"},"max_output_length":{"type":"integer","description":"The maximum number of output characters to return"}},"required":["commands"]}`),
	},
	"apply_patch": {
		Name: "apply_patch", Kind: "apply_patch",
		Description: "Creates, updates or deletes a file. diff uses the V4A format: for create_file every line of the new file starts with +; for update_file each hunk starts with @@ and every line starts with a space for context, - for a removed line or + for an added line.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"type":{"type":"string","enum":["create_file","update_file","delete_file"],"description":"The file operation"},"path":{"type":"string","description":"The file path"},"diff":{"type":"string","description":"The V4A diff for create_file and update_file"}},"required":["type","path"]}`),
	},
}

// customToolDefinition 把 custom 工具声明为只接收字符串 input 的函数，语法格式写入描述
func customToolDefinition(tool ResponsesTool, namespace string) ToolDefinition {
	var format struct {
		Type       string `json:"type"`
		Syntax     string `json:"syntax"`
		Definition string `json:"definition"`
	}
	_ = json.Unmarshal(tool.Format, &format)
	description := tool.Description
	if format.Type == "grammar" && format.Definition != "" {
		description = strings.TrimSpace(description + "\n\nThe input must match this " + format.Syntax + " grammar:\n" + format.Definition)
	}
	return ToolDefinition{Name: tool.Name, Description: description, Parameters: customToolParameters, Namespace: namespace, Kind: "custom"}
}

// customToolInput 取出函数参数中的 input 字符串，缺失时返回参数原文
func customToolInput(arguments json.RawMessage) string {
	var value struct {
		Input *string `json:"input"`
	}
	if json.Unmarshal(arguments, &value) == nil && value.Input != nil {
		return *value.Input
	}
	return string(arguments)
}

// responsesCallItem 按声明的工具类型把工具调用投影为 Responses 输出项
func responsesCallItem(call ToolCall, bridge ToolBridge) gin.H {
	item := gin.H{"id": "fc_" + call.ID, "status": "completed", "call_id": call.ID}
	definition := bridge.tool(call.Name)
	switch definition.Kind {
	case "custom":
		item["type"], item["name"], item["input"] = "custom_tool_call", call.Name, customToolInput(call.Arguments)
	case "local_shell":
		action := map[string]any{}
		_ = json.Unmarshal(call.Arguments, &action)
		action["type"] = "exec"
		if action["env"] == nil {
			action["env"] = map[string]string{}
		}
		item["type"], item["action"] = "local_shell_call", action
	case "shell":
		action := map[string]any{"timeout_ms": nil, "max_output_length": nil}
		_ = json.Unmarshal(call.Arguments, &action)
		item["type"], item["action"], item["environment"] = "shell_call", action, nil
	case "apply_patch":
		item["type"], item["operation"] = "apply_patch_call", json.RawMessage(call.Arguments)
	default:
		item["type"], item["name"], item["arguments"] = "function_call", call.Name, string(call.Arguments)
	}
	if definition.Namespace != "" {
		item["namespace"] = definition.Namespace
	}
	return item
}

// responsesClientCall 把 Responses 历史中的客户端工具调用项转为函数调用参数
func responsesClientCall(kind string, input string, action json.RawMessage, operation json.RawMessage) (string, string) {
	switch kind {
	case "custom_tool_call":
		encoded, _ := json.Marshal(map[string]string{"input": input})
		return "", string(encoded)
	case "local_shell_call":
		values := map[string]json.RawMessage{}
		_ = json.Unmarshal(action, &values)
		delete(values, "type")
		encoded, _ := json.Marshal(values)
		return "local_shell", string(encoded)
	case "shell_call":
		return "shell", rawObject(action)
	default:
		return "apply_patch", rawObject(operation)
	}
}

// rawObject 返回 JSON 原文，缺失时为空对象
func rawObject(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}"
	}
	return string(raw)
}

// toolOutputContent 把工具输出转为工具消息正文，字符串与内容数组原样保留，其他 JSON 值以原文作为字符串
func toolOutputContent(output json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(output))
	if strings.HasPrefix(trimmed, `"`) {
		return output
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(output, &parts) == nil && len(parts) > 0 && !slices.ContainsFunc(parts, func(part map[string]json.RawMessage) bool { return part["type"] == nil }) {
		return output
	}
	encoded, _ := json.Marshal(trimmed)
	return encoded
}

// restrictAllowedTools 按 tool_choice 的 allowed_tools 只保留引用的函数与内置工具，并以其 mode 作为工具选择
func restrictAllowedTools(bridge *ToolBridge, raw json.RawMessage) error {
	type reference struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		Custom struct {
			Name string `json:"name"`
		} `json:"custom"`
	}
	var choice struct {
		Type         string      `json:"type"`
		Mode         string      `json:"mode"`
		Tools        []reference `json:"tools"`
		AllowedTools *struct {
			Mode  string      `json:"mode"`
			Tools []reference `json:"tools"`
		} `json:"allowed_tools"`
	}
	if json.Unmarshal(raw, &choice) != nil || choice.Type != "allowed_tools" {
		return nil
	}
	if choice.AllowedTools != nil {
		choice.Mode, choice.Tools = choice.AllowedTools.Mode, choice.AllowedTools.Tools
	}
	if choice.Mode != "auto" && choice.Mode != "required" {
		return fmt.Errorf("allowed_tools 的 mode 必须是 auto 或 required")
	}
	names := map[string]bool{}
	types := map[string]bool{}
	for _, tool := range choice.Tools {
		types[tool.Type] = true
		for _, name := range []string{tool.Name, tool.Function.Name, tool.Custom.Name} {
			if name != "" {
				names[name] = true
			}
		}
		if _, ok := responsesClientTools[tool.Type]; ok {
			names[tool.Type] = true
		}
	}
	bridge.Definitions = slices.DeleteFunc(bridge.Definitions, func(definition ToolDefinition) bool {
		name := definition.Name
		if definition.Namespace != "" {
			name = definition.Namespace + "." + definition.Name
		}
		return !names[definition.Name] && !names[name]
	})
	bridge.CodeExecution = bridge.CodeExecution && types["code_interpreter"]
	bridge.WebSearch = bridge.WebSearch && (types["web_search"] || types["web_search_preview"])
	bridge.ImageGeneration = bridge.ImageGeneration && types["image_generation"]
	bridge.Choice = choice.Mode
	return nil
}
