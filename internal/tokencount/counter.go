package tokencount

import (
	"bytes"
	_ "embed"

	sentencepiece "github.com/eliben/go-sentencepiece"
)

//go:embed gemma3.model
var modelData []byte

var processor = loadProcessor()

func loadProcessor() *sentencepiece.Processor {
	processor, err := sentencepiece.NewProcessor(bytes.NewReader(modelData))
	if err != nil {
		panic(err)
	}
	return processor
}

// Text 返回 Gemini 可见文本的 token 数
func Text(value string) int {
	return len(processor.Encode(value))
}

// Content 返回单个 Gemini Content 的 token 数
func Content(parts ...string) int {
	total := 1
	for _, part := range parts {
		total += Text(part)
	}
	return total
}
