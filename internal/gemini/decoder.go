package gemini

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxProtocolFrameSize = 64 * 1024 * 1024

// FrameDecoder 将 Google 累计帧转换为唯一规范事件流
type FrameDecoder struct {
	texts         map[int]string
	emittedText   map[int]string
	thoughts      map[int]string
	codes         map[codeEventKey]codeSnapshot
	citations     map[int][]Citation
	phases        map[int]Phase
	imageProgress Phase
	media         map[string]struct{}
	lastSession   ConversationSnapshot
	metadata      EventMetadataData
	sawComplete   bool
	sawError      bool
	sawEnd        bool
}

type codeEventKey struct {
	Candidate int
	Index     int
	Type      CodeEventType
}

type codeSnapshot struct {
	Index    int
	Type     CodeEventType
	Language string
	Content  string
	Offset   int
	Complete bool
}

// NewFrameDecoder 创建独立的单响应解码器
func NewFrameDecoder() *FrameDecoder {
	decoder := &FrameDecoder{}
	decoder.reset()
	return decoder
}

// Decode 增量解析协议帧并原子更新会话状态
func (d *FrameDecoder) Decode(reader io.Reader, state *ConversationState, emit func(Event) error) error {
	d.reset()
	activeState := state
	if activeState == nil {
		activeState = NewConversationState()
	}
	d.lastSession = activeState.Snapshot()

	var protocolErr *ProtocolError
	err := scanProtocolRecords(reader, func(record []any) error {
		tag, _ := stringAt(record, 0)
		switch tag {
		case "wrb.fr":
			encoded, _ := stringAt(record, 2)
			if encoded == "" {
				return nil
			}
			var payload []any
			if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
				return fmt.Errorf("decode wrb.fr payload: %w", err)
			}
			return d.decodePayload(payload, activeState, emit)
		case "er":
			code, _ := intAt(record, 5)
			protocolErr = &ProtocolError{
				Code:      code,
				Message:   fmt.Sprintf("gemini protocol returned code %d", code),
				Retryable: code == 401 || code == 403 || code == 429 || code >= 500,
			}
			d.sawError = true
			return emit(Event{Kind: EventError, Err: protocolErr, FinishReason: FinishError})
		case "e":
			d.sawEnd = true
			if !d.sawComplete && !d.sawError {
				return retryableProtocolError("gemini protocol ended without a completed candidate", nil)
			}
			for candidateIndex := range d.texts {
				if err := d.flushText(candidateIndex, emit); err != nil {
					return err
				}
			}
			finish := FinishUnknown
			if d.sawComplete {
				finish = FinishStop
			}
			if d.sawError {
				finish = FinishError
			}
			return emit(Event{Kind: EventDone, FinishReason: finish, Session: activeState.Snapshot()})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if protocolErr != nil {
		return protocolErr
	}
	if !d.sawEnd {
		return retryableProtocolError("gemini protocol stream ended without terminal frame", nil)
	}
	return nil
}

func (d *FrameDecoder) reset() {
	d.texts = make(map[int]string)
	d.emittedText = make(map[int]string)
	d.thoughts = make(map[int]string)
	d.codes = make(map[codeEventKey]codeSnapshot)
	d.citations = make(map[int][]Citation)
	d.phases = make(map[int]Phase)
	d.imageProgress = PhaseUnknown
	d.media = make(map[string]struct{})
	d.lastSession = ConversationSnapshot{}
	d.metadata = EventMetadataData{}
	d.sawComplete = false
	d.sawError = false
	d.sawEnd = false
}

func (d *FrameDecoder) decodePayload(payload []any, state *ConversationState, emit func(Event) error) error {
	session := d.sessionFromPayload(payload, state.Snapshot())
	if session != d.lastSession {
		state.Update(session)
		d.lastSession = state.Snapshot()
		if err := emit(Event{Kind: EventSession, Session: d.lastSession}); err != nil {
			return err
		}
	}
	if err := d.decodeMetadata(payload, emit); err != nil {
		return err
	}
	if err := d.decodeImageProgress(payload, emit); err != nil {
		return err
	}

	container, ok := arrayAt(payload, 4)
	if !ok {
		return nil
	}
	candidateIndex := 0
	for _, rawCandidate := range container {
		candidate, ok := rawCandidate.([]any)
		if !ok || !isCandidate(candidate) {
			continue
		}
		rcid, _ := stringAt(candidate, 0)
		if rcid != "" && rcid != state.Snapshot().RCID {
			state.Update(ConversationSnapshot{RCID: rcid})
			d.lastSession = state.Snapshot()
			if err := emit(Event{Kind: EventSession, Candidate: candidateIndex, Session: d.lastSession}); err != nil {
				return err
			}
		}

		thought, _ := stringPath(candidate, 37, 0, 0)
		thought = normalizeSnapshot(thought)
		if event, changed := diffSnapshot(EventThought, candidateIndex, d.thoughts[candidateIndex], thought); changed {
			d.thoughts[candidateIndex] = thought
			if err := emit(event); err != nil {
				return err
			}
		}
		if err := d.decodeContent(candidate, candidateIndex, emit); err != nil {
			return err
		}
		phaseValue, _ := intPath(candidate, 8, 0)
		phase := Phase(phaseValue)
		if phase == PhaseComplete {
			if err := d.flushText(candidateIndex, emit); err != nil {
				return err
			}
			d.sawComplete = d.sawComplete || candidateIndex == 0
		}
		if phase != PhaseUnknown && phase != d.phases[candidateIndex] {
			d.phases[candidateIndex] = phase
			if err := emit(Event{Kind: EventPhase, Candidate: candidateIndex, Phase: phase}); err != nil {
				return err
			}
		}
		if err := d.decodeCitations(candidate, candidateIndex, emit); err != nil {
			return err
		}
		if err := d.decodeMedia(candidate, candidateIndex, emit); err != nil {
			return err
		}
		candidateIndex++
	}
	return nil
}

func (d *FrameDecoder) decodeImageProgress(payload []any, emit func(Event) error) error {
	if len(payload) <= 2 {
		return nil
	}
	root, ok := payload[2].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := root["7"].([]any)
	if !ok {
		return nil
	}
	tool, _ := stringPath(raw, 1, 0)
	category, _ := intPath(raw, 1, 3, 1, 7, 0)
	status, _ := intPath(raw, 1, 2)
	if tool != "data_analysis_tool" || category != 20 {
		return nil
	}
	phase := PhaseUnknown
	switch status {
	case 1:
		phase = PhaseGenerating
	case 4:
		phase = PhaseToolComplete
	}
	if phase == PhaseUnknown || phase == d.imageProgress {
		return nil
	}
	d.imageProgress = phase
	return emit(Event{Kind: EventImageProgress, Phase: phase})
}

func (d *FrameDecoder) sessionFromPayload(payload []any, current ConversationSnapshot) ConversationSnapshot {
	values, ok := arrayAt(payload, 1)
	if !ok {
		return current
	}
	if cid, ok := stringAt(values, 0); ok && cid != "" {
		current.CID = cid
	}
	if rid, ok := stringAt(values, 1); ok && rid != "" {
		current.RID = rid
	}
	return current
}

func (d *FrameDecoder) decodeMetadata(payload []any, emit func(Event) error) error {
	next := d.metadata
	if len(payload) > 2 {
		if values, ok := payload[2].(map[string]any); ok {
			if title, ok := firstString(values["11"]); ok {
				next.Title = title
			}
			if token, ok := firstString(values["21"]); ok {
				next.ControlToken = token
			}
		}
	}
	if modelHash, ok := jspbString(payload, 39); ok {
		next.ModelHash = modelHash
	}
	if modelName, ok := jspbString(payload, 42); ok {
		next.ModelName = modelName
	}
	if next == d.metadata {
		return nil
	}
	d.metadata = next
	copy := next
	return emit(Event{Kind: EventMetadata, Metadata: &copy})
}

func (d *FrameDecoder) decodeContent(candidate []any, candidateIndex int, emit func(Event) error) error {
	raw, _ := stringPath(candidate, 1, 0)
	text, codes := parseStructuredContent(raw)
	seen := make(map[codeEventKey]struct{}, len(codes))
	for _, code := range codes {
		key := codeEventKey{Candidate: candidateIndex, Index: code.Index, Type: code.Type}
		seen[key] = struct{}{}
		previous := d.codes[key]
		event, changed := diffSnapshot(EventCode, candidateIndex, previous.Content, code.Content)
		if !changed && previous.Language == code.Language && previous.Offset == code.Offset && previous.Complete == code.Complete {
			continue
		}
		if !changed {
			event = Event{
				Kind: EventCode, Candidate: candidateIndex, Operation: SnapshotReplace,
				Snapshot: code.Content, PrefixLength: utf8.RuneCountInString(code.Content),
			}
		}
		d.codes[key] = code
		data := CodeExecutionEvent{Index: code.Index, Type: code.Type, Language: code.Language, Offset: code.Offset, Complete: code.Complete}
		event.Code = &data
		if err := emit(event); err != nil {
			return err
		}
	}
	for key, previous := range d.codes {
		if key.Candidate != candidateIndex {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		delete(d.codes, key)
		data := CodeExecutionEvent{Index: key.Index, Type: key.Type, Language: previous.Language, Offset: previous.Offset, Complete: true}
		if err := emit(Event{Kind: EventCode, Candidate: candidateIndex, Operation: SnapshotTruncate, PrefixLength: 0, Code: &data}); err != nil {
			return err
		}
	}
	d.texts[candidateIndex] = text
	if event, changed := diffSnapshot(EventText, candidateIndex, d.emittedText[candidateIndex], text); changed {
		d.emittedText[candidateIndex] = text
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

func (d *FrameDecoder) flushText(candidateIndex int, emit func(Event) error) error {
	event, changed := diffSnapshot(EventText, candidateIndex, d.emittedText[candidateIndex], d.texts[candidateIndex])
	if !changed {
		return nil
	}
	d.emittedText[candidateIndex] = d.texts[candidateIndex]
	return emit(event)
}

func (d *FrameDecoder) decodeCitations(candidate []any, candidateIndex int, emit func(Event) error) error {
	raw, _ := valuePath(candidate, 2, 1)
	next := parseCitations(raw)
	if reflect.DeepEqual(next, d.citations[candidateIndex]) {
		return nil
	}
	d.citations[candidateIndex] = append([]Citation(nil), next...)
	return emit(Event{Kind: EventCitations, Candidate: candidateIndex, Citations: next})
}

func (d *FrameDecoder) decodeMedia(candidate []any, candidateIndex int, emit func(Event) error) error {
	if raw, ok := richContentField(candidate, 7); ok {
		if itemsValue, ok := valuePath(raw, 0); ok {
			if items, ok := itemsValue.([]any); ok {
				for index, item := range items {
					mediaURL, _ := stringPath(item, 0, 3, 3)
					if isGeneratedImagePlaceholder(mediaURL) {
						continue
					}
					fileName, _ := stringPath(item, 0, 3, 2)
					mimeType, _ := stringPath(item, 0, 3, 11)
					generator, _ := stringPath(item, 3, 18)
					placeholder, _ := stringPath(item, 1, 0)
					width, _ := intPath(item, 0, 3, 15, 0)
					height, _ := intPath(item, 0, 3, 15, 1)
					sizeBytes, _ := intPath(item, 0, 3, 15, 2)
					if err := d.emitMedia(candidateIndex, &Media{
						Type: MediaGeneratedImage, URL: mediaURL, Title: fmt.Sprintf("[Generated Image %d]", index+1),
						FileName: fileName, MIMEType: mimeType, Generator: generator, Placeholder: placeholder,
						Width: width, Height: height, SizeBytes: sizeBytes, Offset: mediaPlaceholderOffset(candidate, placeholder),
					}, emit); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (d *FrameDecoder) emitMedia(candidateIndex int, media *Media, emit func(Event) error) error {
	if media == nil || media.URL == "" {
		return nil
	}
	key := string(media.Type) + "\x00" + media.URL
	if _, exists := d.media[key]; exists {
		return nil
	}
	d.media[key] = struct{}{}
	return emit(Event{Kind: EventMedia, Candidate: candidateIndex, Media: media})
}

func isGeneratedImagePlaceholder(value string) bool {
	imageURL, err := url.Parse(value)
	return err == nil && imageURL.Hostname() == "googleusercontent.com" && strings.HasPrefix(imageURL.Path, "/image_generation_content/")
}

var (
	structuredCodePattern = regexp.MustCompile("(?m)^```([^?`\\r\\n]*)\\?(code_reference|code_stdout|code_stderr)&code_event_index=(\\d+)\\r?\\n")
	artifactPattern       = regexp.MustCompile("(?m)^https?://googleusercontent\\.com/image_generation_content/\\S+\\s*")
)

func parseStructuredContent(raw string) (string, []codeSnapshot) {
	var text strings.Builder
	codes := make([]codeSnapshot, 0)
	cursor := 0
	for cursor < len(raw) {
		match := structuredCodePattern.FindStringSubmatchIndex(raw[cursor:])
		if match == nil {
			text.WriteString(raw[cursor:])
			break
		}
		start := cursor + match[0]
		bodyStart := cursor + match[1]
		text.WriteString(raw[cursor:start])
		language := raw[cursor+match[2] : cursor+match[3]]
		typeName := raw[cursor+match[4] : cursor+match[5]]
		indexValue := raw[cursor+match[6] : cursor+match[7]]
		index, _ := strconv.Atoi(indexValue)
		remainder := raw[bodyStart:]
		closing := strings.Index(remainder, "\n```")
		content := remainder
		complete := false
		if closing >= 0 {
			content = strings.TrimSuffix(remainder[:closing], "\r")
			cursor = bodyStart + closing + len("\n```")
			if cursor < len(raw) && raw[cursor] == '\r' {
				cursor++
			}
			if cursor < len(raw) && raw[cursor] == '\n' {
				cursor++
			}
			complete = true
		} else {
			cursor = len(raw)
		}
		visiblePrefix := normalizeSnapshot(artifactPattern.ReplaceAllString(text.String(), ""))
		codes = append(codes, codeSnapshot{
			Index: index, Type: CodeEventType(typeName), Language: language,
			Content: content, Offset: utf8.RuneCountInString(visiblePrefix), Complete: complete,
		})
	}
	return normalizeSnapshot(artifactPattern.ReplaceAllString(text.String(), "")), codes
}

func parseCitations(raw any) []Citation {
	groups, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]Citation, 0)
	for _, group := range groups {
		entriesValue, _ := valuePath(group, 2)
		entries, _ := entriesValue.([]any)
		sourceID, _ := stringPath(group, 3)
		claim, _ := stringPath(group, 0, 0)
		start, _ := intPath(group, 0, 3, 0, 0)
		end, _ := intPath(group, 0, 3, 0, 1)
		for _, entry := range entries {
			citationURL, _ := stringPath(entry, 0)
			if citationURL == "" {
				continue
			}
			title, _ := stringPath(entry, 1)
			favicon, _ := stringPath(entry, 2)
			snippet, _ := stringPath(entry, 3)
			publisher, _ := stringPath(entry, 6)
			result = append(result, Citation{
				ID: len(result) + 1, SourceID: sourceID, Claim: claim, Title: title,
				URL: citationURL, Favicon: favicon, Snippet: snippet, Publisher: publisher, Start: start, End: end,
			})
		}
	}
	return result
}

func richContentField(candidate []any, index int) (any, bool) {
	rich, ok := valuePath(candidate, 12)
	if !ok {
		return nil, false
	}
	return jspbField(rich, index)
}

func jspbField(container any, index int) (any, bool) {
	values, ok := container.([]any)
	if !ok || len(values) == 0 {
		return nil, false
	}
	if index >= 0 && index < len(values) && hasJSPBValue(values[index]) {
		if _, sparse := values[index].(map[string]any); !sparse {
			return values[index], true
		}
	}
	bundle, ok := values[len(values)-1].(map[string]any)
	if !ok {
		return nil, false
	}
	value, ok := bundle[strconv.Itoa(index+1)]
	return value, ok && hasJSPBValue(value)
}

func jspbString(container []any, index int) (string, bool) {
	value, ok := jspbField(container, index)
	if !ok {
		return "", false
	}
	result, ok := value.(string)
	return result, ok
}

func hasJSPBValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func mediaPlaceholderOffset(candidate []any, placeholder string) int {
	if placeholder == "" {
		return -1
	}
	raw, _ := stringPath(candidate, 1, 0)
	index := strings.Index(raw, placeholder)
	if index < 0 {
		return -1
	}
	prefix := artifactPattern.ReplaceAllString(raw[:index], "")
	return utf8.RuneCountInString(prefix)
}

func diffSnapshot(kind EventKind, candidate int, previous, current string) (Event, bool) {
	if previous == current {
		return Event{}, false
	}
	previousRunes := []rune(previous)
	currentRunes := []rune(current)
	prefix := 0
	for prefix < len(previousRunes) && prefix < len(currentRunes) && previousRunes[prefix] == currentRunes[prefix] {
		prefix++
	}
	operation := SnapshotReplace
	if prefix == len(previousRunes) {
		operation = SnapshotAppend
	} else if prefix == len(currentRunes) {
		operation = SnapshotTruncate
	}
	return Event{
		Kind:         kind,
		Candidate:    candidate,
		Operation:    operation,
		Delta:        string(currentRunes[prefix:]),
		Snapshot:     current,
		PrefixLength: prefix,
	}, true
}

func scanProtocolRecords(reader io.Reader, handle func([]any) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxProtocolFrameSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimPrefix(line, ")]}'")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := strconv.ParseUint(line, 10, 64); err == nil {
			continue
		}
		var frame []any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			return fmt.Errorf("decode protocol frame: %w", err)
		}
		for _, rawRecord := range frame {
			record, ok := rawRecord.([]any)
			if ok {
				if err := handle(record); err != nil {
					return err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return transportProtocolError("read protocol stream", err)
	}
	return nil
}

func isCandidate(candidate []any) bool {
	rcid, ok := stringAt(candidate, 0)
	if !ok || !strings.HasPrefix(rcid, "rc_") {
		return false
	}
	_, hasPhase := intPath(candidate, 8, 0)
	return hasPhase
}

func valuePath(value any, indexes ...int) (any, bool) {
	current := value
	for _, index := range indexes {
		values, ok := current.([]any)
		if !ok || index < 0 || index >= len(values) {
			return nil, false
		}
		current = values[index]
	}
	return current, true
}

func stringPath(value any, indexes ...int) (string, bool) {
	current, ok := valuePath(value, indexes...)
	if !ok {
		return "", false
	}
	result, ok := current.(string)
	return result, ok
}

func intPath(value any, indexes ...int) (int, bool) {
	current, ok := valuePath(value, indexes...)
	if !ok {
		return 0, false
	}
	number, ok := current.(float64)
	return int(number), ok
}

func firstString(value any) (string, bool) {
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return "", false
	}
	result, ok := values[0].(string)
	return result, ok
}

var snapshotReplacer = strings.NewReplacer(`\<`, `<`, `\>`, `>`, `\_`, `_`, `\[`, `[`, `\]`, `]`)

func normalizeSnapshot(value string) string {
	return snapshotReplacer.Replace(value)
}
