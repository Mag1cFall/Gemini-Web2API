package gemini

// MediaType 表示 Gemini Web 媒体类型
type MediaType string

const (
	MediaGeneratedImage MediaType = "generated_image"
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
}
