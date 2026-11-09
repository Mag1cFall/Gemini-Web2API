package tokencount

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"hash/maphash"
	"strings"
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

const (
	// chunkBytes 是分段计数的目标字节数
	chunkBytes = 4096
	// chunkLimit 是记忆的分段数量上限，达到时清空
	chunkLimit = 1 << 16
)

// chunks 记忆各分段的 token 数，对话历史每轮重发时相同分段不再编码
var chunks = struct {
	sync.RWMutex
	seed   maphash.Seed
	tokens map[uint64]int32
}{seed: maphash.MakeSeed(), tokens: make(map[uint64]int32)}

// Text 返回 Gemini 可见文本的 token 数，长文本按换行分段计数
func Text(value string) int {
	total := 0
	for value != "" {
		end := chunkEnd(value)
		total += chunkTokens(value[:end])
		value = value[end:]
	}
	return total
}

// chunkEnd 返回第一段的结束位置：目标长度内最后一个换行之后，没有换行时取目标长度之后第一个换行之后或文本结尾
func chunkEnd(value string) int {
	if len(value) <= chunkBytes {
		return len(value)
	}
	if newline := strings.LastIndexByte(value[:chunkBytes], '\n'); newline >= 0 {
		return newline + 1
	}
	if newline := strings.IndexByte(value[chunkBytes:], '\n'); newline >= 0 {
		return chunkBytes + newline + 1
	}
	return len(value)
}

// chunkTokens 返回一段文本的 token 数，相同内容只编码一次
func chunkTokens(chunk string) int {
	key := maphash.String(chunks.seed, chunk)
	chunks.RLock()
	count, ok := chunks.tokens[key]
	chunks.RUnlock()
	if ok {
		return int(count)
	}
	count = int32(len(processor().Encode(chunk)))
	chunks.Lock()
	if len(chunks.tokens) >= chunkLimit {
		clear(chunks.tokens)
	}
	chunks.tokens[key] = count
	chunks.Unlock()
	return int(count)
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
