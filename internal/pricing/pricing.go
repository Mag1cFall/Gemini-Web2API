// Package pricing 按 Gemini API 公布的单价估算 Gemini Web 请求的等价费用
package pricing

import (
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// LongContext 是长上下文分档的输入 token 界限，输入超过该值的请求按长上下文单价计费
const LongContext = 200_000

// Price 是一个模型的美元单价：token 单价按每百万 token 计，Long 前缀为长上下文分档单价，0 表示不分档；Media 为生成图片或视频的模型回复 token 的单价；Song 为每个成功请求生成一首歌的单价
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	LongInput  float64 `json:"long_input,omitempty"`
	LongOutput float64 `json:"long_output,omitempty"`
	Media      float64 `json:"media,omitempty"`
	Song       float64 `json:"song,omitempty"`
	from       time.Time
}

// pacific 是 Gemini API 定价页生效日期所用的太平洋时间
var pacific = time.FixedZone("PST", -8*3600)

// gemini4Raise 是 Gemini API 定价页 Gemini 4 Carbon 与 Argon 推广价结束、标准价生效的时刻（2026-12-01 太平洋时间）
var gemini4Raise = time.Date(2026, 12, 1, 0, 0, 0, 0, pacific)

// flash38Raise 是 Gemini API 定价页 Gemini 3.8 Flash 涨价生效的时刻（2027-01-01 太平洋时间）
var flash38Raise = time.Date(2027, 1, 1, 0, 0, 0, 0, pacific)

// gemini4 是 Gemini 4 Carbon 与 Argon 的单价：推广价到 2026-11-30 为止，之后为标准价
var gemini4 = []Price{{Input: 2, Output: 10}, {from: gemini4Raise, Input: 4, Output: 20}}

// prices 是按 Gemini Web 模型 ID 索引、按生效时刻从早到晚排列的对应 Gemini API 模型单价，输出单价包含思考 token；视频对应 Gemini Omni 1.2 Flash，音乐对应 Lyria 3.5
var prices = map[string][]Price{
	"gemini-3.8-flash":       {{Input: 0.75, Output: 3.75}, {from: flash38Raise, Input: 1.50, Output: 7.50}},
	"gemini-3.6-flash":       {{Input: 0.75, Output: 3.75}},
	"gemini-3.5-flash-lite":  {{Input: 0.30, Output: 2.50}},
	"gemini-3.1-pro":         {{Input: 2, Output: 12, LongInput: 4, LongOutput: 18}},
	"gemini-4-argon":         gemini4,
	"gemini-4-carbon":        gemini4,
	"gemini-4-hydrogen":      {{Input: 1.50, Output: 7.50}},
	"gemini-3.1-flash-image": {{Input: 0.50, Output: 3, Media: 60}},
	"gemini-video":           {{Input: 1.50, Output: 9, Media: 12}},
	"gemini-music":           {{Song: 0.08}},
}

// generatorPrefix 是生成图片的上游生成器名前缀，这类生成器按 gemini-3.1-flash-image 计价
const generatorPrefix = "imagen_"

// Lookup 返回模型在 at 时刻生效的单价与模型是否有单价：先按模型 ID 本身查找，没有单价时按改名后的当前 ID 查找，模型 ID 的 -task 后缀不影响单价
func Lookup(model string, at time.Time) (Price, bool) {
	if strings.HasPrefix(model, generatorPrefix) {
		model = gemini.ImageModelID
	}
	tiers, ok := prices[strings.TrimSuffix(gemini.NormalizeModelID(model), "-task")]
	if !ok {
		tiers, ok = prices[strings.TrimSuffix(gemini.CanonicalModelID(model), "-task")]
	}
	var value Price
	for _, tier := range tiers {
		if !at.Before(tier.from) {
			value = tier
		}
	}
	return value, ok
}

// Cost 返回按单价估算的美元费用：long 表示长上下文分档，token 与 succeeded（成功的请求数）可以是同一分档多个请求之和
func (price Price) Cost(long bool, input, reasoning, reply, succeeded int64) float64 {
	if price.LongInput > 0 && long {
		price.Input, price.Output = price.LongInput, price.LongOutput
	}
	replyPrice := price.Output
	if price.Media > 0 {
		replyPrice = price.Media
	}
	return (float64(input)*price.Input+float64(reasoning)*price.Output+float64(reply)*replyPrice)/1e6 + float64(succeeded)*price.Song
}

// Served 返回一条请求记录的单价：实际回答的模型有单价时用它，否则用请求的模型
func Served(model, provider string, at time.Time) (Price, bool) {
	if price, ok := Lookup(provider, at); ok {
		return price, true
	}
	return Lookup(model, at)
}
