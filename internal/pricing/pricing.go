// Package pricing 按 Gemini API 公布的文本单价估算 Gemini Web 请求的等价费用
package pricing

import "strings"

// longContext 是长上下文分档的输入 token 界限，输入超过该值的请求按长上下文单价计费
const longContext = 200_000

// price 是一个模型每百万 token 的美元单价，long 前缀为长上下文分档单价，0 表示不分档
type price struct {
	input, output         float64
	longInput, longOutput float64
}

// prices 是按 Gemini Web 模型 ID 索引的对应 Gemini API 模型文本单价，输出单价包含思考 token
var prices = map[string]price{
	"gemini-3.8-flash":      {input: 0.75, output: 3.75},
	"gemini-3.5-flash-lite": {input: 0.30, output: 2.50},
	"gemini-3.1-pro":        {input: 2, output: 12, longInput: 4, longOutput: 18},
	"gemini-4-argon":        {input: 2, output: 10},
}

// Cost 返回一个请求按模型单价估算的美元费用与模型是否有单价，模型 ID 的 -task 后缀不影响单价，output 为思考与回复 token 之和
func Cost(model string, input, output int64) (float64, bool) {
	value, ok := prices[strings.TrimSuffix(model, "-task")]
	if value.longInput > 0 && input > longContext {
		value.input, value.output = value.longInput, value.longOutput
	}
	return (float64(input)*value.input + float64(output)*value.output) / 1e6, ok
}
