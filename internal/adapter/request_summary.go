package adapter

import "encoding/json"

// responsesInputCount 返回 Responses input 的条目数，字符串输入计为 1
func responsesInputCount(raw json.RawMessage) int {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		return len(items)
	}
	if len(raw) > 0 {
		return 1
	}
	return 0
}

// geminiToolCount 返回 Gemini tools 中的函数声明数与内置工具数
func geminiToolCount(raw json.RawMessage) int {
	var tools []map[string]json.RawMessage
	if json.Unmarshal(raw, &tools) != nil {
		return 0
	}
	count := 0
	for _, tool := range tools {
		for key, value := range tool {
			var declarations []json.RawMessage
			if key == "functionDeclarations" || key == "function_declarations" {
				if json.Unmarshal(value, &declarations) == nil {
					count += len(declarations)
				}
				continue
			}
			count++
		}
	}
	return count
}
