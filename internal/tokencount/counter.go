package tokencount

import (
	"bytes"
	_ "embed"
	"fmt"

	sentencepiece "github.com/eliben/go-sentencepiece"
)

//go:embed gemma3.model
var modelData []byte

var processor = loadProcessor()

// vocabularySize 是 Gemini 分词器词表的 token 数
var vocabularySize = processor.ModelInfo().VocabularySize

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

// Decode 把 Gemini 分词器的 token ID 还原为文本，ID 不在词表中时返回错误
func Decode(ids []int) (string, error) {
	for _, id := range ids {
		if id < 0 || id >= vocabularySize {
			return "", fmt.Errorf("token %d is not in the Gemini tokenizer vocabulary of %d tokens", id, vocabularySize)
		}
	}
	return processor.Decode(ids), nil
}
