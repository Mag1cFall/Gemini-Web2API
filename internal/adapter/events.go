package adapter

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// CodeOutput 保存一段 Google 已执行代码的权威快照
type CodeOutput struct {
	Index    int
	Type     gemini.CodeEventType
	Language string
	Offset   int
	Complete bool
	Content  string
	Order    int
}

// CandidateOutput 保存单个候选项的权威累计输出
type CandidateOutput struct {
	Text      string
	Thought   string
	Codes     []CodeOutput
	Citations []gemini.Citation
	Media     []gemini.Media
}

// EventAccumulator 将规范事件还原为最终响应
type EventAccumulator struct {
	Candidates   map[int]*CandidateOutput
	Session      gemini.ConversationSnapshot
	Metadata     *gemini.EventMetadataData
	Usage        *gemini.Usage
	FinishReason gemini.FinishReason
}

// NewEventAccumulator 创建规范事件累加器
func NewEventAccumulator() *EventAccumulator {
	return &EventAccumulator{Candidates: make(map[int]*CandidateOutput)}
}

// Apply 应用一个规范事件
func (a *EventAccumulator) Apply(event gemini.Event) error {
	switch event.Kind {
	case gemini.EventText:
		candidate := a.candidate(event.Candidate)
		candidate.Text = applySnapshot(candidate.Text, event)
	case gemini.EventThought:
		candidate := a.candidate(event.Candidate)
		candidate.Thought = applySnapshot(candidate.Thought, event)
	case gemini.EventCode:
		if event.Code != nil {
			candidate := a.candidate(event.Candidate)
			candidate.applyCode(event)
		}
	case gemini.EventCitations:
		candidate := a.candidate(event.Candidate)
		candidate.Citations = append(candidate.Citations[:0], event.Citations...)
	case gemini.EventMedia:
		if event.Media != nil {
			candidate := a.candidate(event.Candidate)
			candidate.Media = append(candidate.Media, *event.Media)
		}
	case gemini.EventSession:
		a.Session = event.Session
	case gemini.EventMetadata:
		a.Metadata = event.Metadata
	case gemini.EventError:
		if event.Err != nil {
			return event.Err
		}
	case gemini.EventDone:
		a.FinishReason = event.FinishReason
	}
	if event.Usage != nil {
		a.Usage = event.Usage
	}
	return nil
}

func (c *CandidateOutput) applyCode(event gemini.Event) {
	for index := range c.Codes {
		code := &c.Codes[index]
		if code.Index != event.Code.Index || code.Type != event.Code.Type {
			continue
		}
		code.Language = event.Code.Language
		code.Offset = event.Code.Offset
		code.Complete = event.Code.Complete
		code.Content = applySnapshot(code.Content, event)
		if code.Content == "" {
			c.Codes = append(c.Codes[:index], c.Codes[index+1:]...)
		}
		return
	}
	content := applySnapshot("", event)
	if content == "" {
		return
	}
	c.Codes = append(c.Codes, CodeOutput{
		Index: event.Code.Index, Type: event.Code.Type, Language: event.Code.Language,
		Offset: event.Code.Offset, Complete: event.Code.Complete, Content: content, Order: len(c.Codes),
	})
}

type candidateInsert struct {
	offset int
	order  int
	code   *CodeOutput
	media  *gemini.Media
}

func candidateInserts(output CandidateOutput) []candidateInsert {
	inserts := make([]candidateInsert, 0, len(output.Codes)+len(output.Media))
	for index := range output.Codes {
		code := &output.Codes[index]
		inserts = append(inserts, candidateInsert{offset: code.Offset, order: code.Order, code: code})
	}
	for index := range output.Media {
		media := &output.Media[index]
		inserts = append(inserts, candidateInsert{offset: media.Offset, order: len(output.Codes) + index, media: media})
	}
	sort.SliceStable(inserts, func(left, right int) bool {
		leftOffset := inserts[left].offset
		rightOffset := inserts[right].offset
		if leftOffset < 0 {
			leftOffset = int(^uint(0) >> 1)
		}
		if rightOffset < 0 {
			rightOffset = int(^uint(0) >> 1)
		}
		if leftOffset == rightOffset {
			return inserts[left].order < inserts[right].order
		}
		return leftOffset < rightOffset
	})
	return inserts
}

func renderCandidateMarkdown(text string, output CandidateOutput, fromRune int) string {
	if hasGeneratedImage(output) && strings.TrimSpace(text) == "" {
		text = ""
	}
	runes := []rune(text)
	if fromRune < 0 {
		fromRune = 0
	}
	if fromRune > len(runes) {
		fromRune = len(runes)
	}
	var builder strings.Builder
	cursor := fromRune
	for _, insert := range candidateInserts(output) {
		offset := insert.offset
		if offset < 0 || offset > len(runes) {
			offset = len(runes)
		}
		if offset < fromRune {
			if insert.code != nil {
				builder.WriteString(renderCodeMarkdown(*insert.code))
			} else if insert.media != nil {
				builder.WriteString(renderMediaMarkdown(*insert.media))
			}
			continue
		}
		if offset > cursor {
			builder.WriteString(string(runes[cursor:offset]))
			cursor = offset
		}
		if insert.code != nil {
			builder.WriteString(renderCodeMarkdown(*insert.code))
		} else if insert.media != nil {
			builder.WriteString(renderMediaMarkdown(*insert.media))
		}
	}
	builder.WriteString(string(runes[cursor:]))
	return builder.String()
}

func hasGeneratedImage(output CandidateOutput) bool {
	for _, media := range output.Media {
		if media.Type == gemini.MediaGeneratedImage {
			return true
		}
	}
	return false
}

func inlineResultMedia(ctx context.Context, result *generationResult) error {
	if result == nil || result.Client == nil || result.Accumulator == nil {
		return nil
	}
	for _, candidate := range result.Accumulator.Candidates {
		for index := range candidate.Media {
			media := &candidate.Media[index]
			if media.URL == "" || strings.HasPrefix(media.URL, "data:") {
				continue
			}
			data, err := result.Client.FetchMedia(ctx, media.URL)
			if err != nil {
				return fmt.Errorf("下载生成媒体失败: %w", err)
			}
			mimeType := http.DetectContentType(data)
			if mimeType == "application/octet-stream" && strings.TrimSpace(media.MIMEType) != "" {
				mimeType = strings.TrimSpace(media.MIMEType)
			}
			media.MIMEType = mimeType
			media.SizeBytes = len(data)
			media.URL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
		}
	}
	return nil
}

func renderCodeMarkdown(code CodeOutput) string {
	language := strings.TrimSpace(code.Language)
	if code.Type != gemini.CodeReference || language == "" {
		language = "text"
	}
	return fmt.Sprintf("\n```%s\n%s\n```\n", language, code.Content)
}

func renderMediaMarkdown(media gemini.Media) string {
	url := media.URL
	if url == "" {
		return ""
	}
	title := media.Title
	if title == "" {
		title = media.Alt
	}
	if title == "" {
		title = string(media.Type)
	}
	if media.Type == gemini.MediaGeneratedImage {
		return fmt.Sprintf("\n![%s](%s)\n", title, url)
	}
	return fmt.Sprintf("\n[%s](%s)\n", title, url)
}

func renderCitationMarkdown(text string, citations []gemini.Citation) string {
	replaced := false
	for _, citation := range citations {
		marker := fmt.Sprintf("[cite:%d]", citation.ID)
		if strings.Contains(text, marker) {
			text = strings.ReplaceAll(text, marker, fmt.Sprintf("[%s](%s)", marker, citation.URL))
			replaced = true
		}
	}
	if !replaced {
		text += renderCitationSources(citations)
	}
	return text
}

func renderCitationSources(citations []gemini.Citation) string {
	if len(citations) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\n\nSources:")
	for _, citation := range citations {
		title := citation.Title
		if title == "" {
			title = citation.URL
		}
		fmt.Fprintf(&builder, "\n- [%s](%s)", title, citation.URL)
	}
	return builder.String()
}

// Primary 返回首个候选项
func (a *EventAccumulator) Primary() CandidateOutput {
	if candidate, ok := a.Candidates[0]; ok {
		return *candidate
	}
	for _, candidate := range a.Candidates {
		return *candidate
	}
	return CandidateOutput{}
}

func (a *EventAccumulator) candidate(index int) *CandidateOutput {
	candidate, ok := a.Candidates[index]
	if !ok {
		candidate = &CandidateOutput{}
		a.Candidates[index] = candidate
	}
	return candidate
}

func applySnapshot(current string, event gemini.Event) string {
	if event.Snapshot != "" || event.Operation != gemini.SnapshotAppend {
		return event.Snapshot
	}
	return current + event.Delta
}
