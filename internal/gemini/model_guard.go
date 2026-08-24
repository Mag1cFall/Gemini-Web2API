package gemini

import "fmt"

// modelGuard 在公开任何上游事件前确认实际模型 hash
type modelGuard struct {
	expected  Model
	emit      func(Event) error
	pending   []Event
	validated bool
	emitted   bool
}

func newModelGuard(expected Model, emit func(Event) error) *modelGuard {
	return &modelGuard{expected: expected, emit: emit}
}

func (g *modelGuard) Emit(event Event) error {
	if g.validated {
		g.emitted = true
		return g.emit(event)
	}
	if isModelGuardContent(event.Kind) {
		return retryableProtocolError(fmt.Sprintf("gemini response began %s before identifying the actual model for requested model %q", event.Kind, g.expected.ID), nil)
	}
	if event.Kind != EventMetadata || event.Metadata == nil || event.Metadata.ModelHash == "" {
		g.pending = append(g.pending, event)
		return nil
	}
	if event.Metadata.ModelHash != g.expected.Hash {
		return modelMismatchError(g.expected, *event.Metadata)
	}
	g.pending = append(g.pending, event)
	g.validated = true
	for _, pending := range g.pending {
		g.emitted = true
		if err := g.emit(pending); err != nil {
			return err
		}
	}
	g.pending = nil
	return nil
}

func isModelGuardContent(kind EventKind) bool {
	switch kind {
	case EventText, EventThought, EventCode, EventCitations, EventMedia, EventPhase:
		return true
	default:
		return false
	}
}

func (g *modelGuard) Complete() error {
	if g.validated {
		return nil
	}
	return &ProtocolError{
		HTTPStatus: 502,
		Code:       502,
		Message:    fmt.Sprintf("gemini response did not identify the actual model for requested model %q", g.expected.ID),
		Retryable:  true,
	}
}

func (g *modelGuard) Emitted() bool {
	return g.emitted
}

func modelMismatchError(expected Model, actual EventMetadataData) *ProtocolError {
	actualName := actual.ModelName
	if actualName == "" {
		actualName = actual.ModelHash
	}
	return &ProtocolError{
		HTTPStatus: 502,
		Code:       502,
		Message: fmt.Sprintf(
			"gemini returned model %q (%s) instead of requested model %q (%s)",
			actualName, actual.ModelHash, expected.ID, expected.Hash,
		),
		Retryable: true,
	}
}
