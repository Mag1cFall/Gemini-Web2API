package gemini

// MediaType 表示 Gemini Web 媒体类型
type MediaType string

const (
	// MediaGeneratedImage 是 Create image 生成的图片
	MediaGeneratedImage MediaType = "generated_image"
	// MediaGeneratedVideo 是 Create video 生成的视频
	MediaGeneratedVideo MediaType = "generated_video"
	// MediaGeneratedAudio 是 Create music 生成的音频
	MediaGeneratedAudio MediaType = "generated_audio"
)

// Media 表示规范事件携带的网页或生成媒体
type Media struct {
	Type        MediaType `json:"type"`
	URL         string    `json:"url"`
	Title       string    `json:"title,omitempty"`
	Alt         string    `json:"alt,omitempty"`
	FileName    string    `json:"file_name,omitempty"`
	MIMEType    string    `json:"mime_type,omitempty"`
	Generator   string    `json:"generator,omitempty"`
	Placeholder string    `json:"placeholder,omitempty"`
	Width       int       `json:"width,omitempty"`
	Height      int       `json:"height,omitempty"`
	SizeBytes   int       `json:"size_bytes,omitempty"`
	Offset      int       `json:"offset"`
	// download 是下载生成图片原图所需的条目与会话标识
	download *imageDownload
}
