package gemini

import (
	"errors"
	"fmt"
	"regexp"
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
	"Normally I can help with things like this, but I don't seem to have access to that content. You can try again or ask me for something else.",
	"请恕我爱莫能助，我只是一个语言模型，不能理解或回复你的这个问题。",
	"身为一个语言模型，我没办法提供这方面的帮助。",
	"我只会生成文本，你提出的问题超出了我的程序逻辑范畴。",
	"我只是一个语言模型，理解不了这个问题，因此没法帮上忙。",
	"我是一个语言模型，这超出了我的设计用途。",
	"我只是一个语言模型，不具备这方面的信息或能力，因此没法帮到你。",
	"我是一个文本 AI，这超出了我的能力范围。",
	"我无法提供这方面的帮助，因为我只是一个语言模型。",
	"我只是一个文本 AI，在这方面没法帮到你。",
	"由于程序代码的局限，我没法办到。",
	"我只是一个语言模型，无法提供这方面的帮助。",
	"我只是文字型 AI，這件事無法幫忙。",
	"我只是個語言模型，無法了解此內容，所以無法幫助你。",
	"我是語言模型，這件事恐怕幫不上忙。",
	"我幫不上忙，因為我只是個語言模型。",
	"我只是個語言模型，沒有必要的相關資訊或能力，所以幫不上忙。",
}

// errorCardPrefix 是官网临时故障时主候选正文中错误卡片链接的前缀，页面显示重试按钮
const errorCardPrefix = "http://googleusercontent.com/card_content/"

// errorCardPattern 匹配整段正文为错误卡片链接
var errorCardPattern = regexp.MustCompile(`^http://googleusercontent\.com/card_content/\d+$`)

// failureTextGuard 在主候选正文仍可能是失败回复时暂缓正文事件，结束时上游标为失败回复或最终正文整段为固定失败文字则返回可重试错误
type failureTextGuard struct {
	emit    func(Event) error
	pending []Event
	text    string
	// flagged 表示上游元数据把本次回答标为失败回复
	flagged bool
	// media 表示主候选带有媒体，官网在失败文字旁仍交付视频时按正常回答处理
	media   bool
	decided bool
}

func newFailureTextGuard(emit func(Event) error) *failureTextGuard {
	return &failureTextGuard{emit: emit}
}

// Emit 转发事件，正文可能是失败回复时暂存，结束时上游标为失败回复或整段等于固定失败文字则返回可重试错误
func (g *failureTextGuard) Emit(event Event) error {
	switch {
	case event.Kind == EventText && event.Candidate == 0:
		g.text = event.Snapshot
	case event.Kind == EventMetadata && event.Metadata != nil:
		g.flagged = event.Metadata.FailureReply
	case event.Kind == EventMedia && event.Candidate == 0:
		g.media = true
	}
	if g.decided && event.Kind == EventText && event.Candidate == 0 && event.Operation != SnapshotAppend && (g.flagged || isFailureTextPrefix(g.text)) {
		g.decided = false
	}
	if g.decided {
		if event.Kind == EventDone && g.failed() {
			return failureTextError(g.text)
		}
		return g.emit(event)
	}
	switch {
	case event.Kind == EventText && event.Candidate == 0:
		g.pending = append(g.pending, event)
		if g.flagged || isFailureTextPrefix(g.text) {
			return nil
		}
		return g.release()
	case event.Kind == EventDone:
		if len(g.pending) > 0 && g.failed() {
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

// failed 报告本次回答是否为失败回复
func (g *failureTextGuard) failed() bool {
	return !g.media && (g.flagged || isFailureText(g.text))
}

func isFailureTextPrefix(text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(errorCardPrefix, trimmed) || strings.HasPrefix(trimmed, errorCardPrefix) && strings.Trim(trimmed[len(errorCardPrefix):], "0123456789") == "" {
		return true
	}
	for _, failure := range upstreamFailureTexts {
		if strings.HasPrefix(failure, trimmed) {
			return true
		}
	}
	return false
}

func isFailureText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if errorCardPattern.MatchString(trimmed) {
		return true
	}
	for _, failure := range upstreamFailureTexts {
		if trimmed == failure {
			return true
		}
	}
	return false
}

func failureTextError(text string) *ProtocolError {
	message := fmt.Sprintf("Gemini Web 返回固定失败文字：%s", strings.TrimSpace(text))
	if errorCardPattern.MatchString(strings.TrimSpace(text)) {
		message = "Gemini Web 返回错误卡片，官网对应“出错了，请重试”"
	}
	return &ProtocolError{
		HTTPStatus: 502,
		Code:       502,
		Message:    message,
		Retryable:  true,
		Cause:      ErrUpstreamFailureText,
	}
}
