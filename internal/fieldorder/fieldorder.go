// Package fieldorder 按各公开协议官方输出的字段顺序编码动态对象
package fieldorder

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

// fieldOrderJSON 是各公开协议输出对象的官方字段顺序表
//
//go:embed field_order.json
var fieldOrderJSON []byte

// fieldOrder 是一个公开协议输出对象的官方字段顺序：Global 为全协议统一的先后，Kinds 按对象种类给出先后，TypeFirst 为 type 在前、其余按字母序
type fieldOrder struct {
	Global    []string            `json:"global"`
	Kinds     map[string][]string `json:"kinds"`
	TypeFirst bool                `json:"typeFirst"`
	Opaque    []string            `json:"opaque"`
	global    map[string]int
	kinds     map[string]map[string]int
	protocol  string
}

// fieldOrders 是 gemini、chat、responses、anthropic、interactions 与 openai 输出的字段顺序
var fieldOrders = loadFieldOrders()

// topLevel 是各协议顶层资源与 Gemini 候选的固定字段先后，优先于 Global 与 Kinds
var topLevel = map[string]map[string]int{
	"chat.completion":       fieldRanks([]string{"id", "object", "created", "model", "choices", "usage", "service_tier", "provider_model", "system_fingerprint"}),
	"chat.completion.chunk": fieldRanks([]string{"id", "object", "created", "model", "system_fingerprint", "choices", "provider_model", "usage"}),
	"gemini.response":       fieldRanks([]string{"candidates", "promptFeedback", "usageMetadata", "modelVersion", "responseId", "turnToken", "debugInfo"}),
	"gemini.candidate":      fieldRanks([]string{"content", "finishReason", "index", "finishMessage", "safetyRatings", "citationMetadata", "groundingMetadata", "detailedFinishReason", "urlContextMetadata"}),
	"anthropic.message":     fieldRanks([]string{"id", "type", "role", "content", "model", "stop_reason", "stop_sequence", "stop_details", "usage", "container", "context_management", "provider_finish_reason", "provider_model"}),
	"response":              fieldRanks([]string{"id", "object", "created_at", "status", "completed_at", "background", "error", "incomplete_details", "instructions", "max_output_tokens", "max_tool_calls", "model", "output", "parallel_tool_calls", "previous_response_id", "reasoning", "service_tier", "temperature", "text", "tool_choice", "tools", "top_p", "truncation", "usage", "metadata", "output_text", "provider_finish_reason", "provider_model"}),
	"interaction":           fieldRanks([]string{"created", "environment_id", "id", "model", "object", "previous_interaction_id", "status", "steps", "updated", "usage"}),
	"model":                 fieldRanks([]string{"id", "object", "created", "owned_by", "shutdown_date", "access_modes", "capabilities", "capability_options", "channels", "context_length", "default_reasoning_level", "description", "input_modalities", "input_token_limit", "max_completion_tokens", "name", "output_token_limit", "paid", "supported_generation_methods", "supported_reasoning_levels"}),
}

// loadFieldOrders 解析内嵌的字段顺序表
func loadFieldOrders() map[string]*fieldOrder {
	var orders map[string]*fieldOrder
	if err := json.Unmarshal(fieldOrderJSON, &orders); err != nil {
		panic(err)
	}
	for protocol, order := range orders {
		order.protocol = protocol
		order.global = fieldRanks(order.Global)
		order.kinds = make(map[string]map[string]int, len(order.Kinds))
		for kind, keys := range order.Kinds {
			order.kinds[kind] = fieldRanks(keys)
		}
	}
	return orders
}

// fieldRanks 返回字段名到先后位置的映射
func fieldRanks(keys []string) map[string]int {
	if len(keys) == 0 {
		return nil
	}
	ranks := make(map[string]int, len(keys))
	for index, key := range keys {
		ranks[key] = index
	}
	return ranks
}

// Official 返回按协议官方字段顺序编码的值：protocol 为 gemini、chat、responses、anthropic、interactions 或 openai，嵌套的动态对象逐层排列，结构体与原始 JSON 原样编码
func Official(protocol string, value any) any {
	return fieldOrders[protocol].value(value, "")
}

// orderedJSON 是按官方字段顺序编码的动态对象
type orderedJSON struct {
	fields map[string]any
	ranks  map[string]int
	order  *fieldOrder
}

// value 为动态字段准备嵌套对象，其余值交给 encoding/json
func (order *fieldOrder) value(value any, parent string) any {
	if slices.Contains(order.Opaque, parent) {
		return value
	}
	switch value := value.(type) {
	case nil:
		return json.RawMessage("null")
	case gin.H:
		return order.mapValue(value, parent)
	case map[string]any:
		return order.mapValue(value, parent)
	case map[string]string:
		return orderedMap(order, value, parent)
	case map[string]json.RawMessage:
		return orderedMap(order, value, parent)
	case []gin.H:
		return orderedSlice(order, value, parent)
	case []map[string]any:
		return orderedSlice(order, value, parent)
	case []any:
		return orderedSlice(order, value, parent)
	}
	return value
}

// orderedMap 按字段顺序排列字符串键对象，nil 原样返回以输出 null
func orderedMap[T any](order *fieldOrder, value map[string]T, parent string) any {
	if value == nil {
		return value
	}
	fields := make(map[string]any, len(value))
	for key, item := range value {
		fields[key] = item
	}
	return order.object(fields, parent)
}

// orderedSlice 逐项排列数组中的嵌套值，nil 原样返回以输出 null
func orderedSlice[T any](order *fieldOrder, value []T, parent string) any {
	if value == nil {
		return value
	}
	items := make([]any, len(value))
	for index, item := range value {
		items[index] = order.value(item, parent)
	}
	return items
}

// mapValue 逐个排列动态对象的嵌套值
func (order *fieldOrder) mapValue(value map[string]any, parent string) any {
	if value == nil {
		return value
	}
	fields := make(map[string]any, len(value))
	for key, item := range value {
		fields[key] = order.value(item, key)
	}
	return order.object(fields, parent)
}

// MarshalJSON 只排列当前动态对象的键，字段值由 encoding/json 编码
func (o orderedJSON) MarshalJSON() ([]byte, error) {
	keys := slices.Collect(maps.Keys(o.fields))
	slices.SortFunc(keys, func(a, b string) int { return o.order.compare(o.ranks, a, b) })
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		item, err := json.Marshal(o.fields[key])
		if err != nil {
			return nil, err
		}
		buffer.Write(name)
		buffer.WriteByte(':')
		buffer.Write(item)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// object 返回动态字段对象，顶层资源与 Gemini 候选用 topLevel 的固定先后
func (order *fieldOrder) object(fields map[string]any, parent string) any {
	ranks := order.ranksFor(fields, parent)
	switch object := mapString(fields, "object"); {
	case order.protocol == "chat" && (object == "chat.completion" || object == "chat.completion.chunk"):
		ranks = topLevel[object]
	case order.protocol == "gemini" && (fields["modelVersion"] != nil || fields["candidates"] != nil || fields["promptFeedback"] != nil):
		ranks = topLevel["gemini.response"]
	case order.protocol == "gemini" && parent == "candidates":
		ranks = topLevel["gemini.candidate"]
	case order.protocol == "anthropic" && mapString(fields, "type") == "message" && mapString(fields, "role") == "assistant":
		ranks = topLevel["anthropic.message"]
	case order.protocol == "responses" && object == "response", order.protocol == "interactions" && object == "interaction", order.protocol == "openai" && object == "model":
		ranks = topLevel[object]
	}
	return orderedJSON{fields: fields, ranks: ranks, order: order}
}

// ranksFor 返回对象所属种类的字段先后：Global 优先，否则按 type、object 或所在字段名查 Kinds
func (order *fieldOrder) ranksFor(object map[string]any, parent string) map[string]int {
	if order.global != nil {
		return order.global
	}
	if order.TypeFirst {
		return nil
	}
	for _, field := range []string{"type", "object"} {
		if name := mapString(object, field); name != "" {
			return order.kinds[field+":"+name]
		}
	}
	return order.kinds["key:"+parent]
}

// compare 比较同一对象中的两个字段：表中的字段按表的先后在前，其余按字母序
func (order *fieldOrder) compare(ranks map[string]int, a, b string) int {
	if order.TypeFirst && (a == "type") != (b == "type") {
		if a == "type" {
			return -1
		}
		return 1
	}
	left, leftKnown := ranks[a]
	right, rightKnown := ranks[b]
	switch {
	case leftKnown && rightKnown:
		return left - right
	case leftKnown:
		return -1
	case rightKnown:
		return 1
	}
	return strings.Compare(a, b)
}

// mapString 读取对象中字符串字段的值，原始 JSON 字段按 JSON 字符串解析
func mapString(object map[string]any, field string) string {
	switch value := object[field].(type) {
	case string:
		return value
	case json.RawMessage:
		var name string
		_ = json.Unmarshal(value, &name)
		return name
	}
	return ""
}
