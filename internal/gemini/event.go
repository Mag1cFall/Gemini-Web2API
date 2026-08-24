package gemini

// EventKind 表示规范化协议事件类型
type EventKind string

const (
	EventSession       EventKind = "session"
	EventText          EventKind = "text"
	EventThought       EventKind = "thought"
	EventCode          EventKind = "code"
	EventCitations     EventKind = "citations"
	EventMedia         EventKind = "media"
	EventImageProgress EventKind = "image_progress"
	EventPhase         EventKind = "phase"
	EventMetadata      EventKind = "metadata"
	EventError         EventKind = "error"
	EventDone          EventKind = "done"
)

// SnapshotOperation 表示累计快照相对上一版本的变化
type SnapshotOperation string

const (
	SnapshotAppend   SnapshotOperation = "append"
	SnapshotReplace  SnapshotOperation = "replace"
	SnapshotTruncate SnapshotOperation = "truncate"
)

// Phase 表示候选生成阶段
type Phase int

const (
	PhaseUnknown      Phase = 0
	PhaseGenerating   Phase = 1
	PhaseComplete     Phase = 2
	PhaseToolComplete Phase = 4
)

// FinishReason 表示规范化结束原因
type FinishReason string

const (
	FinishUnknown   FinishReason = "unknown"
	FinishStop      FinishReason = "stop"
	FinishCancelled FinishReason = "cancelled"
	FinishError     FinishReason = "error"
)

// Usage 表示上游提供的令牌用量
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	ThoughtTokens    int
	TotalTokens      int
}

// OutputTokens 返回包含可见思考的输出 token 数
func (u Usage) OutputTokens() int {
	return u.CompletionTokens + u.ThoughtTokens
}

// EventMetadataData 表示页面协议返回的附加元数据
type EventMetadataData struct {
	Title        string
	ControlToken string
	ModelHash    string
	ModelName    string
}

// CodeEventType 表示 Gemini Web 内置代码执行事件类型
type CodeEventType string

const (
	CodeReference CodeEventType = "code_reference"
	CodeStdout    CodeEventType = "code_stdout"
	CodeStderr    CodeEventType = "code_stderr"
)

// CodeExecutionEvent 表示 Google 已执行的代码及其结果
type CodeExecutionEvent struct {
	Index    int
	Type     CodeEventType
	Language string
	Offset   int
	Complete bool
}

// Citation 表示正文引用标记对应的网页来源
type Citation struct {
	ID        int    `json:"id"`
	SourceID  string `json:"source_id,omitempty"`
	Claim     string `json:"claim,omitempty"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url"`
	Favicon   string `json:"favicon,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
	Publisher string `json:"publisher,omitempty"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
}

// ProtocolError 表示 HTTP 或帧内协议错误
type ProtocolError struct {
	HTTPStatus int
	Code       int
	Message    string
	Retryable  bool
	Cause      error
}

// Error 返回适合日志和公开错误映射的描述
func (e *ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != 0 {
		return "gemini protocol error"
	}
	return "gemini upstream error"
}

// Unwrap 返回底层错误
func (e *ProtocolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Event 表示所有公开适配器共享的上游事件
type Event struct {
	Kind         EventKind
	Candidate    int
	Operation    SnapshotOperation
	Delta        string
	Snapshot     string
	PrefixLength int
	Phase        Phase
	Session      ConversationSnapshot
	Code         *CodeExecutionEvent
	Citations    []Citation
	Media        *Media
	Metadata     *EventMetadataData
	Usage        *Usage
	FinishReason FinishReason
	Err          *ProtocolError
}
