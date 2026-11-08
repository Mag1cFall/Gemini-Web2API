package adapter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/claude"
)

// anthropicServerTool 判断工具类型是否为 Anthropic 在服务端执行的工具或 MCP 工具集，这类工具不进入工具协议
func anthropicServerTool(kind string) bool {
	for _, prefix := range []string{"web_fetch_", "code_execution_", "tool_search_tool_", "advisor_"} {
		if strings.HasPrefix(kind, prefix) {
			return true
		}
	}
	return strings.Contains(kind, "toolset")
}

// anthropicDefinedTool 返回 Anthropic 定义的客户端工具的说明与固定参数结构，其他工具返回空
func anthropicDefinedTool(tool claude.Tool, kind string) (string, json.RawMessage) {
	switch {
	case strings.HasPrefix(kind, "bash_"):
		return "Run a shell command in a persistent bash session. Set restart to true to restart the session.",
			json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"restart":{"type":"boolean"}}}`)
	case strings.HasPrefix(kind, "text_editor_"):
		commands := `"view","create","str_replace","insert"`
		if kind == "text_editor_20241022" || kind == "text_editor_20250124" {
			commands += `,"undo_edit"`
		}
		return "View, create and edit text files. view reads a file or lists a directory, create writes file_text, str_replace replaces old_str with new_str, insert adds insert_text after insert_line.",
			json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","enum":[` + commands + `]},"path":{"type":"string"},"file_text":{"type":"string"},"old_str":{"type":"string"},"new_str":{"type":"string"},"insert_line":{"type":"integer"},"insert_text":{"type":"string"},"view_range":{"type":"array","items":{"type":"integer"}}},"required":["command","path"]}`)
	case strings.HasPrefix(kind, "memory_"):
		return "Read and write persistent memory files in the /memories directory. view reads a file or lists a directory, create writes file_text, str_replace replaces old_str with new_str, insert adds insert_text after insert_line, delete removes path, rename moves old_path to new_path.",
			json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","enum":["view","create","str_replace","insert","delete","rename"]},"path":{"type":"string"},"file_text":{"type":"string"},"old_str":{"type":"string"},"new_str":{"type":"string"},"insert_line":{"type":"integer"},"insert_text":{"type":"string"},"view_range":{"type":"array","items":{"type":"integer"}},"old_path":{"type":"string"},"new_path":{"type":"string"}},"required":["command"]}`)
	case strings.HasPrefix(kind, "computer_"):
		return anthropicComputerTool(kind, tool.DisplayWidthPx, tool.DisplayHeightPx, tool.EnableZoom)
	}
	return "", nil
}

// anthropicComputerTool 返回 computer 工具的说明与参数结构：20241022 为十个基础动作，之后的版本加点击坐标与修饰键、滚动、按住与等待，enable_zoom 时加 zoom
func anthropicComputerTool(kind string, width int, height int, zoom bool) (string, json.RawMessage) {
	actions := []string{"key", "type", "mouse_move", "left_click", "left_click_drag", "right_click", "middle_click", "double_click", "screenshot", "cursor_position"}
	point := map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 2, "maxItems": 2}
	properties := map[string]any{"coordinate": point, "start_coordinate": point, "text": map[string]any{"type": "string"}}
	description := fmt.Sprintf("Use a mouse and keyboard to interact with a computer and take screenshots. The screen is %dx%d pixels; coordinate and start_coordinate are [x, y] pixel positions from the top-left corner. screenshot captures the screen, cursor_position reports the cursor position, mouse_move moves the cursor to coordinate, left_click_drag drags from start_coordinate to coordinate, type types text, key presses the key or key combination in text such as Return or ctrl+s.", width, height)
	if kind == "computer_20241022" {
		description += " left_click, right_click, middle_click and double_click click at the current cursor position."
	} else {
		actions = append(actions, "triple_click", "left_mouse_down", "left_mouse_up", "scroll", "hold_key", "wait")
		properties["scroll_direction"] = map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}}
		properties["scroll_amount"] = map[string]any{"type": "integer"}
		properties["duration"] = map[string]any{"type": "number"}
		properties["key"] = map[string]any{"type": "string"}
		description += " left_click, right_click, middle_click, double_click and triple_click click at coordinate or at the current cursor position while holding the modifier keys in key, left_mouse_down and left_mouse_up press and release the left button at the cursor, scroll turns the wheel scroll_amount clicks in scroll_direction at coordinate while holding the modifier keys in text, hold_key holds the key in text for duration seconds, wait pauses for duration seconds."
	}
	if zoom {
		actions = append(actions, "zoom")
		properties["region"] = map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 4, "maxItems": 4}
		description += " zoom shows the screen region [x1, y1, x2, y2] at full resolution."
	}
	properties["action"] = map[string]any{"type": "string", "enum": actions}
	schema, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": []string{"action"}})
	return description, schema
}
