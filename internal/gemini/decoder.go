package gemini

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

const maxProtocolFrameSize = 64 * 1024 * 1024

// FrameDecoder 将 Google 累计帧转换为唯一规范事件流
type FrameDecoder struct {
	texts       map[int]string
	thoughts    map[int]string
	phases      map[int]Phase
	images      map[string]struct{}
	lastSession ConversationSnapshot
	metadata    EventMetadataData
	sawComplete bool
	sawError    bool
	sawEnd      bool
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
				Retryable: code == 400 || code == 401 || code == 403,
			}
			d.sawError = true
			return emit(Event{Kind: EventError, Err: protocolErr, FinishReason: FinishError})
		case "e":
			d.sawEnd = true
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
		return fmt.Errorf("gemini protocol stream ended without terminal frame")
	}
	return nil
}

func (d *FrameDecoder) reset() {
	d.texts = make(map[int]string)
	d.thoughts = make(map[int]string)
	d.phases = make(map[int]Phase)
	d.images = make(map[string]struct{})
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

		text, _ := stringPath(candidate, 1, 0)
		text = normalizeSnapshot(text)
		if event, changed := diffSnapshot(EventText, candidateIndex, d.texts[candidateIndex], text); changed {
			d.texts[candidateIndex] = text
			if err := emit(event); err != nil {
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
		phaseValue, _ := intPath(candidate, 8, 0)
		phase := Phase(phaseValue)
		if phase != PhaseUnknown && phase != d.phases[candidateIndex] {
			d.phases[candidateIndex] = phase
			d.sawComplete = d.sawComplete || phase == PhaseComplete
			if err := emit(Event{Kind: EventPhase, Candidate: candidateIndex, Phase: phase}); err != nil {
				return err
			}
		}
		if err := d.decodeImages(candidate, candidateIndex, emit); err != nil {
			return err
		}
		candidateIndex++
	}
	return nil
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
	if modelHash, ok := stringAt(payload, 39); ok {
		next.ModelHash = modelHash
	}
	if modelName, ok := stringAt(payload, 42); ok {
		next.ModelName = modelName
	}
	if next == d.metadata {
		return nil
	}
	d.metadata = next
	copy := next
	return emit(Event{Kind: EventMetadata, Metadata: &copy})
}

func (d *FrameDecoder) decodeImages(candidate []any, candidateIndex int, emit func(Event) error) error {
	rawImages, ok := valuePath(candidate, 12, 7, 0)
	if !ok {
		return nil
	}
	images, ok := rawImages.([]any)
	if !ok {
		return nil
	}
	for _, rawImage := range images {
		urlValue, ok := stringPath(rawImage, 0, 3, 3)
		if !ok || urlValue == "" || isGeneratedImagePlaceholder(urlValue) {
			continue
		}
		if _, exists := d.images[urlValue]; exists {
			continue
		}
		d.images[urlValue] = struct{}{}
		image := &Image{Type: ImageTypeGenerated, URL: urlValue}
		if err := emit(Event{Kind: EventImage, Candidate: candidateIndex, Image: image}); err != nil {
			return err
		}
	}
	return nil
}

func isGeneratedImagePlaceholder(value string) bool {
	imageURL, err := url.Parse(value)
	return err == nil && imageURL.Hostname() == "googleusercontent.com" && strings.HasPrefix(imageURL.Path, "/image_generation_content/")
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
		return fmt.Errorf("read protocol stream: %w", err)
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
