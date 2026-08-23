package gemini

// ImageType 表示媒体来源类型
type ImageType string

const (
	// ImageTypeWeb 表示网页搜索图片
	ImageTypeWeb ImageType = "web"
	// ImageTypeGenerated 表示模型生成图片
	ImageTypeGenerated ImageType = "generated"
)

// Image 表示规范事件携带的图片
type Image struct {
	Type  ImageType `json:"type"`
	URL   string    `json:"url"`
	Title string    `json:"title,omitempty"`
	Alt   string    `json:"alt,omitempty"`
}
