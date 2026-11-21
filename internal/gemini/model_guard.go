package gemini

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrModelMismatch 表示上游实际回答的模型与请求的模型不同，常见于该模型额度用尽后的降级
var ErrModelMismatch = errors.New("官网改用了其他模型回答")

// modelGuard 在公开任何上游事件前确认实际回答的模型，视频生成在未收到成品视频前同时暂存事件以识别额度用尽、文字拒答与没有视频的文字回答
type modelGuard struct {
	expected     Model
	emit         func(Event) error
	pending      []Event
	validated    bool
	emitted      bool
	text         string
	limitNotice  string
	failureReply bool
	// blockNotice 表示正文中出现过按输入拦截的过滤提示
	blockNotice  bool
	hasVideo     bool
	videoChecked bool
}

func newModelGuard(expected Model, emit func(Event) error) *modelGuard {
	return &modelGuard{expected: expected, emit: emit}
}

func (g *modelGuard) Emit(event Event) error {
	if g.expected.Feature == FeatureVideo && !g.videoChecked {
		g.observeVideoEvent(event)
	}
	if g.validated && (g.expected.Feature != FeatureVideo || g.videoChecked) {
		g.emitted = true
		return g.emit(event)
	}
	if !g.validated && event.Kind == EventMetadata && event.Metadata != nil && event.Metadata.ModelHash != "" {
		if !g.expected.ServedBy(*event.Metadata) {
			return modelMismatchError(g.expected, *event.Metadata)
		}
		g.validated = true
	}
	if g.expected.Feature != FeatureVideo {
		g.pending = append(g.pending, event)
		if !g.validated {
			return nil
		}
		return g.flush()
	}
	if event.Kind == EventError {
		return g.emit(event)
	}
	g.pending = append(g.pending, event)
	if g.hasVideo && g.validated {
		g.videoChecked = true
		return g.flush()
	}
	if event.Kind == EventDone {
		if err := g.checkVideoReply(); err != nil {
			return err
		}
		if !g.validated {
			return nil
		}
		g.videoChecked = true
		return g.flush()
	}
	return nil
}

// observeVideoEvent 记录视频生成响应的正文、额度提示、失败回复标记与成品视频事件
func (g *modelGuard) observeVideoEvent(event Event) {
	switch {
	case event.Kind == EventText && event.Candidate == 0:
		if event.Snapshot != "" || event.Operation != SnapshotAppend {
			g.text = event.Snapshot
		} else {
			g.text += event.Delta
		}
		g.blockNotice = g.blockNotice || strings.Contains(g.text, inputBlockNotice)
	case event.Kind == EventMetadata && event.Metadata != nil:
		if event.Metadata.LimitNotice != "" {
			g.limitNotice = event.Metadata.LimitNotice
		}
		g.failureReply = event.Metadata.FailureReply
	case event.Kind == EventMedia && event.Candidate == 0 && event.Media != nil && event.Media.Type == MediaGeneratedVideo:
		g.hasVideo = true
	}
}

// checkVideoReply 在视频生成没有产出成品视频时识别额度用尽、限流、文字拒答与其他文字回答，按输入拦截的回答交给 failureTextGuard 返回输入拒绝
func (g *modelGuard) checkVideoReply() error {
	switch {
	case g.hasVideo, g.blockNotice && g.failureReply && !isFailureText(g.text):
		return nil
	case g.limitNotice != "" || isVideoQuotaText(g.text):
		return videoQuotaError(g.text, g.limitNotice, parseVideoResetTime(time.Now(), g.limitNotice, g.text))
	case isVideoRefusalText(g.text):
		return videoRefusalError(g.text)
	}
	return noVideoError(g.text)
}

// flush 把暂存事件按到达顺序转发给下游
func (g *modelGuard) flush() error {
	pending := g.pending
	g.pending = nil
	for _, event := range pending {
		g.emitted = true
		if err := g.emit(event); err != nil {
			return err
		}
	}
	return nil
}

func (g *modelGuard) Complete() error {
	if g.expected.Feature == FeatureVideo && !g.videoChecked && !g.hasVideo {
		if err := g.checkVideoReply(); err != nil {
			return err
		}
	}
	if !g.validated {
		return &ProtocolError{
			HTTPStatus: 502,
			Code:       502,
			Message:    fmt.Sprintf("官网回答没有标明实际回答的模型，请求的模型是 %s", g.expected.ID),
			Retryable:  true,
		}
	}
	if g.expected.Feature == FeatureVideo && !g.videoChecked {
		g.videoChecked = true
		return g.flush()
	}
	return nil
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
		Message:    fmt.Sprintf("官网改用 %s 回答，请求的模型是 %s", actualName, expected.ID),
		Retryable:  true,
		Cause:      ErrModelMismatch,
	}
	if actual.LimitNotice != "" {
		err.HTTPStatus, err.Code = 429, 429
		err.Message += ": " + actual.LimitNotice
	}
	return err
}
