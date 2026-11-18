package adapter

import (
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
)

// outputLimit 按请求的输出上限截断候选 0 的思考与正文：累计 token 按增量的本地计数估算，超过上限时按快照精确计数找出截断位置
type outputLimit struct {
	max  int
	used int
	// hit 表示输出已达到上限
	hit bool
}

// newOutputLimit 返回输出上限为 max 的 outputLimit，max 不大于 0 时为 nil；接续生成时已发出的内容计入用量
func newOutputLimit(max int, resume partialOutput) *outputLimit {
	if max <= 0 {
		return nil
	}
	return &outputLimit{max: max, used: tokencount.Text(resume.text) + tokencount.Text(resume.thought)}
}

// apply 在候选 0 的思考或正文事件写入 accumulator 之后核对上限：超过时把该段截到上限内并改写事件，返回改写后的事件与本次是否达到上限
func (l *outputLimit) apply(accumulator *EventAccumulator, event gemini.Event) (gemini.Event, bool) {
	if event.Candidate != 0 || event.Kind != gemini.EventText && event.Kind != gemini.EventThought {
		return event, false
	}
	l.used += tokencount.Text(event.Delta)
	if event.Operation != gemini.SnapshotAppend || l.used > l.max {
		primary := accumulator.Primary()
		l.used = tokencount.Text(primary.Text) + tokencount.Text(primary.Thought)
	}
	if l.used <= l.max {
		return event, false
	}
	l.hit = true
	output := accumulator.candidate(0)
	field, other := &output.Text, output.Thought
	if event.Kind == gemini.EventThought {
		field, other = &output.Thought, output.Text
	}
	before := len(*field) - len(event.Delta)
	*field = tokenPrefix(*field, l.max-tokencount.Text(other))
	if event.Operation == gemini.SnapshotAppend {
		event.Delta = (*field)[min(max(before, 0), len(*field)):]
	} else {
		event.Delta, event.Snapshot = "", *field
	}
	return event, true
}

// tokenPrefix 返回 text 中本地计数不超过 limit 个 token 的最长前缀，在 rune 边界上截断
func tokenPrefix(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		middle := (low + high + 1) / 2
		if tokencount.Text(string(runes[:middle])) <= limit {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return string(runes[:low])
}
