package tokencount

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"sync"

	sentencepiece "github.com/eliben/go-sentencepiece"
)

//go:embed gemma3.model.gz
var modelData []byte

// processor 在首次计数或解码时解析内嵌的 Gemini 分词器模型
var processor = sync.OnceValue(loadProcessor)

func loadProcessor() *sentencepiece.Processor {
	model, err := gzip.NewReader(bytes.NewReader(modelData))
	if err != nil {
		panic(err)
	}
	processor, err := sentencepiece.NewProcessor(model)
	if err != nil {
		panic(err)
	}
	return processor
}

// Text 返回 Gemini 可见文本的 token 数
func Text(value string) int {
	return len(processor().Encode(value))
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
	vocabularySize := processor().ModelInfo().VocabularySize
	for _, id := range ids {
		if id < 0 || id >= vocabularySize {
			return "", fmt.Errorf("token %d is not in the Gemini tokenizer vocabulary of %d tokens", id, vocabularySize)
		}
	}
	return processor().Decode(ids), nil
}
