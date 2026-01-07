package gemini

type ImageType string

const (
	ImageTypeWeb       ImageType = "web"
	ImageTypeGenerated ImageType = "generated"
)

type Image struct {
	Type  ImageType `json:"type"`
	URL   string    `json:"url"`
	Title string    `json:"title,omitempty"`
	Alt   string    `json:"alt,omitempty"`
}
