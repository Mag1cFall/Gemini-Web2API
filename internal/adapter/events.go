package adapter

import "github.com/Mag1cFall/Gemini-Web2API/internal/gemini"

// CandidateOutput 保存单个候选项的权威累计输出
type CandidateOutput struct {
	Text    string
	Thought string
	Images  []gemini.Image
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
	case gemini.EventImage:
		if event.Image != nil {
			candidate := a.candidate(event.Candidate)
			candidate.Images = append(candidate.Images, *event.Image)
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
