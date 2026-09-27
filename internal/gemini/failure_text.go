package gemini

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUpstreamFailureText 表示 Gemini Web 用固定失败文字代替回答
var ErrUpstreamFailureText = errors.New("gemini web returned a fixed failure text")

// upstreamFailureTexts 为 Gemini Web 以正文形式返回的已知固定失败文字
var upstreamFailureTexts = []string{
	"Sorry, something went wrong. Please try your request again.",
	"I encountered an error doing what you asked. Could you try again?",
	"I seem to be encountering an error. Can I try something else for you?",
	"I'm having a hard time fulfilling your request. Can I help you with something else instead?",
}

// failureTextGuard 在主候选正文仍可能是固定失败文字时暂缓正文事件
type failureTextGuard struct {
	emit    func(Event) error
	pending []Event
	text    string
	decided bool
}

func newFailureTextGuard(emit func(Event) error) *failureTextGuard {
	return &failureTextGuard{emit: emit}
}

// Emit 转发事件，正文可能是固定失败文字时暂存，结束时整段相等则返回可重试错误
func (g *failureTextGuard) Emit(event Event) error {
	if g.decided {
		return g.emit(event)
	}
	switch {
	case event.Kind == EventText && event.Candidate == 0:
		g.text = event.Snapshot
		g.pending = append(g.pending, event)
		if isFailureTextPrefix(g.text) {
			return nil
		}
		return g.release()
	case event.Kind == EventDone:
		if len(g.pending) > 0 && isFailureText(g.text) {
			return failureTextError(g.text)
		}
		if err := g.release(); err != nil {
			return err
		}
		return g.emit(event)
	case len(g.pending) > 0 && event.Kind != EventError:
		g.pending = append(g.pending, event)
		return nil
	default:
		return g.emit(event)
	}
}

func (g *failureTextGuard) release() error {
	g.decided = true
	pending := g.pending
	g.pending = nil
	for _, event := range pending {
		if err := g.emit(event); err != nil {
			return err
		}
	}
	return nil
}

func isFailureTextPrefix(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, failure := range upstreamFailureTexts {
		if strings.HasPrefix(failure, trimmed) {
			return true
		}
	}
	return false
}

func isFailureText(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, failure := range upstreamFailureTexts {
		if trimmed == failure {
			return true
		}
	}
	return false
}

func failureTextError(text string) *ProtocolError {
	return &ProtocolError{
		HTTPStatus: 502,
		Code:       502,
		Message:    fmt.Sprintf("Gemini Web 返回固定失败文字：%s", strings.TrimSpace(text)),
		Retryable:  true,
		Cause:      ErrUpstreamFailureText,
	}
}
