package gemini

import (
	"errors"
	"fmt"
	"strings"
)

// ErrModelMismatch 表示上游实际回答的模型与请求的模型不同，常见于该模型额度用尽后的降级
var ErrModelMismatch = errors.New("gemini web served a different model")

// modelGuard 在公开任何上游事件前确认实际回答的模型
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
	if event.Kind != EventMetadata || event.Metadata == nil || event.Metadata.ModelHash == "" {
		g.pending = append(g.pending, event)
		return nil
	}
	if !g.expected.ServedBy(*event.Metadata) {
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

// ServedBy 报告上游元数据是否表明回答来自该模型：变体模型的响应带账号默认模型的 hash，按型号名核对，上游在 Low 以外的思考强度下于型号名后加档位名（如 4 Argon High）
func (m Model) ServedBy(metadata EventMetadataData) bool {
	if m.Variant == "" {
		return metadata.ModelHash == m.Hash
	}
	name := m.DisplayName
	if m.Feature == FeatureTask {
		name = strings.TrimSuffix(name, " Task")
	}
	return metadata.ModelName == name || strings.HasPrefix(metadata.ModelName, name+" ")
}

func modelMismatchError(expected Model, actual EventMetadataData) *ProtocolError {
	actualName := actual.ModelName
	if actualName == "" {
		actualName = actual.ModelHash
	}
	err := &ProtocolError{
		HTTPStatus: 502,
		Code:       502,
		Message: fmt.Sprintf(
			"gemini returned model %q (%s) instead of requested model %q (%s)",
			actualName, actual.ModelHash, expected.ID, expected.Hash,
		),
		Retryable: true,
		Cause:     ErrModelMismatch,
	}
	if actual.LimitNotice != "" {
		err.HTTPStatus, err.Code = 429, 429
		err.Message += ": " + actual.LimitNotice
	}
	return err
}
