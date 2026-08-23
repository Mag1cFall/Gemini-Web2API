package gemini

// FileData 表示已上传并可放入提示词的附件
type FileData struct {
	URL      string
	FileName string
}

// ThinkingMode 表示 Gemini Web 思考策略
type ThinkingMode int

const (
	// ThinkingStandard 使用标准思考策略
	ThinkingStandard ThinkingMode = iota
	// ThinkingExtended 使用扩展思考策略
	ThinkingExtended
)

// GenerateRequest 表示一次网页协议生成请求
type GenerateRequest struct {
	Prompt          string
	Model           string
	ModelMode       int
	ThinkingMode    ThinkingMode
	Files           []FileData
	Conversation    *ConversationState
	ImageGeneration bool
}
