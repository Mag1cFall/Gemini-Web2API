package gemini

// FileData 表示生成请求携带的附件
type FileData struct {
	// Data 为待上传的文件内容，生成前上传
	Data []byte
	// URL 为上传服务返回的文件路径
	URL      string
	FileName string
	MimeType string
	// FileID 为 ProcessFile 返回的文件 ID，生成前登记
	FileID string
}

// ThinkingLevel 表示网页模型菜单中的思考强度，取值为官网协议编号
type ThinkingLevel int

const (
	// ThinkingLow 是低思考强度
	ThinkingLow ThinkingLevel = 1
	// ThinkingMedium 是中等思考强度
	ThinkingMedium ThinkingLevel = 4
	// ThinkingHigh 是高思考强度
	ThinkingHigh ThinkingLevel = 2
)

// String 返回思考强度的小写名称
func (l ThinkingLevel) String() string {
	switch l {
	case ThinkingLow:
		return "low"
	case ThinkingMedium:
		return "medium"
	case ThinkingHigh:
		return "high"
	default:
		return ""
	}
}

// Feature 表示网页输入框工具菜单选中的功能，取值为官网 featureMode 编号
type Feature int

const (
	// FeatureDeepResearch 是 Deep Research 研究报告，只能在普通会话中使用
	FeatureDeepResearch Feature = 1
	// FeatureCanvas 是 Canvas 文档工具
	FeatureCanvas Feature = 2
	// FeatureVideo 是 Create video 视频生成，成品在生成流结束后由会话读取
	FeatureVideo Feature = 11
	// FeatureImage 是 Create image 生图工具
	FeatureImage Feature = 14
	// FeatureMusic 是 Create music 音乐生成
	FeatureMusic Feature = 21
	// FeatureLearning 是 Guided Learning 引导式学习
	FeatureLearning Feature = 24
	// FeatureTask 是 Task 模式，只能在普通会话中使用
	FeatureTask Feature = 40
)

// savedChatOnly 判断该工具是否需要普通会话：Task 与 Deep Research 只在普通会话中提供，视频成品由会话读取
func (f Feature) savedChatOnly() bool {
	return f == FeatureTask || f == FeatureDeepResearch || f == FeatureVideo
}

// GenerateRequest 表示一次网页协议生成请求
type GenerateRequest struct {
	Prompt        string
	Model         string
	ModelMode     int
	ThinkingLevel ThinkingLevel
	Feature       Feature
	// Gem 为在 Gem 中对话时的 Gem ID
	Gem string
	// AspectRatio 为生成媒体请求的宽高比，例如 16:9 或 1536x1024，空值使用官网默认
	AspectRatio string
	// tools 为工具选中的选项，工具没有选项组时为 nil
	tools *toolChoice
	// researchConfirm 表示本轮确认 Deep Research 计划
	researchConfirm bool
	// researchPlan 接收 Deep Research 计划卡片上确认按钮的文字
	researchPlan *string
	// personalIntelligence 表示账号目录提供 Personal Intelligence 开关
	personalIntelligence bool
	Files                []FileData
	Conversation         *ConversationState
	// Branch 表示从会话中较早的回答继续，官网对应编辑该回答之后的提问
	Branch bool
}
