package adapter

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
)

// openAIAudioOutput 把生成的音轨转为 Chat message.audio，返回去掉该音轨的候选输出供正文渲染
func openAIAudioOutput(id string, output CandidateOutput, transcript string, options *ChatAudioOptions) (gin.H, CandidateOutput, error) {
	index := -1
	for position, media := range output.Media {
		if media.Type == gemini.MediaGeneratedAudio {
			index = position
			break
		}
	}
	if index < 0 {
		return nil, output, nil
	}
	audio := output.Media[index]
	output.Media = append(append([]gemini.Media(nil), output.Media[:index]...), output.Media[index+1:]...)
	_, data, ok := splitDataURL(audio.URL)
	if !ok {
		return nil, output, fmt.Errorf("生成音频尚未下载")
	}
	if options != nil && options.Format != "" && options.Format != "mp3" {
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, output, err
		}
		converted, _, err := transcodeMP3(raw, options.Format)
		if err != nil {
			return nil, output, err
		}
		data = base64.StdEncoding.EncodeToString(converted)
	}
	return gin.H{"id": "audio_" + id, "data": data, "expires_at": time.Now().Add(time.Hour).Unix(), "transcript": transcript}, output, nil
}

// citationSources 返回按首次出现顺序去重后的来源与每条引用对应的来源序号
func citationSources(citations []gemini.Citation) ([]gemini.Citation, []int) {
	sources := make([]gemini.Citation, 0, len(citations))
	indexes := make([]int, len(citations))
	seen := make(map[string]int, len(citations))
	for position, citation := range citations {
		index, ok := seen[citation.URL]
		if !ok {
			index = len(sources)
			seen[citation.URL] = index
			sources = append(sources, citation)
		}
		indexes[position] = index
	}
	return sources, indexes
}

// runeByteOffset 返回 text 中第 runes 个字符的字节偏移
func runeByteOffset(text string, runes int) int {
	if runes <= 0 {
		return 0
	}
	offset := 0
	for index := 0; index < runes && offset < len(text); index++ {
		_, size := utf8.DecodeRuneInString(text[offset:])
		offset += size
	}
	return offset
}

// geminiGroundingMetadata 按 Gemini API Google Search grounding 的形状返回来源、引用片段与搜索入口
func geminiGroundingMetadata(text string, citations []gemini.Citation) gin.H {
	sources, indexes := citationSources(citations)
	chunks := make([]gin.H, 0, len(sources))
	for _, source := range sources {
		web := gin.H{"uri": source.URL, "title": source.Title}
		if source.Publisher != "" {
			web["domain"] = source.Publisher
		}
		chunks = append(chunks, gin.H{"web": web})
	}
	type segmentKey struct{ start, end int }
	supports := make([]gin.H, 0, len(citations))
	positions := make(map[segmentKey]int)
	length := utf8.RuneCountInString(text)
	for position, citation := range citations {
		start, end := citation.Start, citation.End
		if start < 0 || end <= start || end > length {
			continue
		}
		key := segmentKey{start, end}
		if existing, ok := positions[key]; ok {
			indices := supports[existing]["groundingChunkIndices"].([]int)
			supports[existing]["groundingChunkIndices"] = append(indices, indexes[position])
			continue
		}
		startByte, endByte := runeByteOffset(text, start), runeByteOffset(text, end)
		positions[key] = len(supports)
		supports = append(supports, gin.H{
			"segment":               gin.H{"startIndex": startByte, "endIndex": endByte, "text": text[startByte:endByte]},
			"groundingChunkIndices": []int{indexes[position]},
		})
	}
	return gin.H{"webSearchQueries": []string{}, "groundingChunks": chunks, "groundingSupports": supports}
}

// anthropicSearchID 返回由响应 ID 派生的 server_tool_use 标识
func anthropicSearchID(responseID string) string {
	digest := sha256.Sum256([]byte(responseID + ":web_search"))
	return "srvtoolu_" + base64.RawURLEncoding.EncodeToString(digest[:18])
}

// anthropicSearchBlocks 返回 web_search 的 server_tool_use 与 web_search_tool_result 块
func anthropicSearchBlocks(responseID string, citations []gemini.Citation) []gin.H {
	sources, _ := citationSources(citations)
	results := make([]gin.H, 0, len(sources))
	for _, source := range sources {
		results = append(results, gin.H{
			"type": "web_search_result", "url": source.URL, "title": source.Title,
			"encrypted_content": base64.StdEncoding.EncodeToString([]byte(source.Snippet)), "page_age": nil,
		})
	}
	id := anthropicSearchID(responseID)
	return []gin.H{
		{"type": "server_tool_use", "id": id, "name": "web_search", "input": gin.H{"query": ""}},
		{"type": "web_search_tool_result", "tool_use_id": id, "content": results},
	}
}

// citedTextBlock 是带 web_search_result_location 引用的一段正文
type citedTextBlock struct {
	Text      string
	Citations []map[string]any
}

// anthropicCitedText 按引用范围把正文切成文本块，被引用的片段带 web_search_result_location
func anthropicCitedText(text string, citations []gemini.Citation) []citedTextBlock {
	runes := []rune(text)
	type span struct {
		start, end int
		citations  []map[string]any
	}
	spans := make([]span, 0, len(citations))
	for _, citation := range citations {
		start, end := citation.Start, citation.End
		if start < 0 || end <= start || end > len(runes) {
			continue
		}
		location := map[string]any{
			"type": "web_search_result_location", "url": citation.URL, "title": citation.Title,
			"encrypted_index": base64.StdEncoding.EncodeToString([]byte(citation.URL)), "cited_text": string(runes[start:end]),
		}
		merged := false
		for index := range spans {
			if spans[index].start == start && spans[index].end == end {
				spans[index].citations = append(spans[index].citations, location)
				merged = true
				break
			}
		}
		if !merged {
			spans = append(spans, span{start: start, end: end, citations: []map[string]any{location}})
		}
	}
	sort.SliceStable(spans, func(left, right int) bool { return spans[left].start < spans[right].start })
	blocks := make([]citedTextBlock, 0, len(spans)*2+1)
	cursor := 0
	for _, item := range spans {
		if item.start < cursor {
			continue
		}
		if item.start > cursor {
			blocks = append(blocks, citedTextBlock{Text: string(runes[cursor:item.start])})
		}
		blocks = append(blocks, citedTextBlock{Text: string(runes[item.start:item.end]), Citations: item.citations})
		cursor = item.end
	}
	if cursor < len(runes) {
		blocks = append(blocks, citedTextBlock{Text: string(runes[cursor:])})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, citedTextBlock{Text: strings.TrimSpace(text)})
	}
	return blocks
}
