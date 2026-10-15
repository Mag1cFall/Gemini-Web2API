package adapter

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/gin-gonic/gin"
)

// ImageGenerationRequest 表示 OpenAI 图片生成请求
type ImageGenerationRequest struct {
	Prompt            string `json:"prompt"`
	Model             string `json:"model,omitempty"`
	N                 int    `json:"n,omitempty"`
	Size              string `json:"size,omitempty"`
	ResponseFormat    string `json:"response_format,omitempty"`
	Quality           string `json:"quality,omitempty"`
	Style             string `json:"style,omitempty"`
	Stream            bool   `json:"stream,omitempty"`
	PartialImages     *int   `json:"partial_images,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
	Background        string `json:"background,omitempty"`
	User              string `json:"user,omitempty"`
	InputFidelity     string `json:"input_fidelity,omitempty"`
	Moderation        string `json:"moderation,omitempty"`
}

type imageInput struct {
	data     []byte
	filename string
}

// ImageGenerationHandler 处理 OpenAI 图片生成
func ImageGenerationHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request ImageGenerationRequest
		err := decodeRequest(c, &request)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		handleImageRequest(c, pool, request, nil)
	}
}

// ImageEditHandler 处理 OpenAI 图片编辑
func ImageEditHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if c.Request.MultipartForm != nil {
			defer c.Request.MultipartForm.RemoveAll()
		}
		if len(c.Request.MultipartForm.File["mask"]) != 0 {
			writeOpenAIError(c, http.StatusBadRequest, "unsupported_parameter", "mask is not supported by Gemini Web image editing")
			return
		}

		request := ImageGenerationRequest{
			Prompt: c.PostForm("prompt"), Model: c.PostForm("model"), Size: c.PostForm("size"),
			ResponseFormat: c.PostForm("response_format"), Quality: c.PostForm("quality"), Style: c.PostForm("style"),
			OutputFormat: c.PostForm("output_format"), Background: c.PostForm("background"), User: c.PostForm("user"),
			InputFidelity: c.PostForm("input_fidelity"), Moderation: c.PostForm("moderation"),
		}
		if rawN := strings.TrimSpace(c.PostForm("n")); rawN != "" {
			n, err := strconv.Atoi(rawN)
			if err != nil {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "n must be an integer")
				return
			}
			request.N = n
		}
		if raw := strings.TrimSpace(c.PostForm("stream")); raw != "" {
			stream, err := strconv.ParseBool(raw)
			if err != nil {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "stream must be a boolean")
				return
			}
			request.Stream = stream
		}
		for name, target := range map[string]**int{
			"partial_images": &request.PartialImages, "output_compression": &request.OutputCompression,
		} {
			raw := strings.TrimSpace(c.PostForm(name))
			if raw == "" {
				continue
			}
			value, err := strconv.Atoi(raw)
			if err != nil {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", name+" must be an integer")
				return
			}
			*target = &value
		}

		headers := c.Request.MultipartForm.File["image"]
		if len(headers) == 0 {
			headers = c.Request.MultipartForm.File["image[]"]
		}
		inputs := make([]imageInput, 0, len(headers))
		for index, header := range headers {
			file, err := header.Open()
			if err != nil {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
			data, readErr := io.ReadAll(file)
			file.Close()
			if readErr != nil {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", readErr.Error())
				return
			}
			filename := strings.TrimSpace(header.Filename)
			if filename == "" {
				filename = fmt.Sprintf("image_%d.bin", index)
			}
			inputs = append(inputs, imageInput{data: data, filename: filename})
		}
		if len(inputs) == 0 {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "image is required")
			return
		}
		handleImageRequest(c, pool, request, inputs)
	}
}

func handleImageRequest(c *gin.Context, pool *balancer.AccountPool, request ImageGenerationRequest, inputs []imageInput) {
	logging.FromContext(c.Request.Context()).Begin("images", request.Model, request.Stream, 1, 0)
	if strings.TrimSpace(request.Prompt) == "" {
		writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "prompt is required")
		return
	}
	if request.Model == "" {
		request.Model = defaultImageModel(pool)
	}
	bindScope(c, pool, request.Model)
	if request.Model == "" {
		writeOpenAIError(c, http.StatusServiceUnavailable, "upstream_error", "No image model is available")
		return
	}
	knownModel, readyModel := modelStatus(pool, request.Model)
	if !knownModel {
		writeOpenAIError(c, http.StatusNotFound, "model_not_found", fmt.Sprintf("model %q is unavailable", request.Model))
		return
	}
	if !readyModel {
		writeOpenAIError(c, http.StatusServiceUnavailable, "upstream_error", "No account is ready for the requested model")
		return
	}
	if request.N == 0 {
		request.N = 1
	}
	if request.N < 1 || request.N > 4 {
		writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "n must be between 1 and 4")
		return
	}
	if request.ResponseFormat == "" {
		request.ResponseFormat = "b64_json"
	}
	if request.ResponseFormat != "b64_json" && request.ResponseFormat != "url" {
		writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "response_format must be b64_json or url")
		return
	}
	setUnsupportedParameters(c, nonEmptyImageParameters(request))

	prompt := request.Prompt
	images := make([][]byte, 0, request.N)
	for index := 0; index < request.N && len(images) < request.N; index++ {
		responseID := fmt.Sprintf("img_%d_%d", time.Now().UnixNano(), index)
		result, accountID, err := runGeneration(
			c.Request.Context(), pool, "", false, request.Model, responseID, generationMedia{requireImage: true, aspectRatio: request.Size}, gemini.ThinkingLow, ToolBridge{},
			func(*gemini.Client, bool) (string, []gemini.FileData, error) {
				files := make([]gemini.FileData, 0, len(inputs))
				for _, input := range inputs {
					files = append(files, gemini.FileData{Data: input.data, FileName: input.filename})
				}
				return prompt, files, nil
			}, nil, nil,
		)
		c.Set("account_id", accountID)
		if err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}
		for _, image := range result.Accumulator.Primary().Media {
			if len(images) == request.N {
				break
			}
			if image.Type != gemini.MediaGeneratedImage {
				continue
			}
			data, err := result.Client.FetchMediaContent(c.Request.Context(), image)
			if err != nil {
				writeOpenAIError(c, upstreamStatus(err), "image_download_error", err.Error())
				return
			}
			images = append(images, data)
		}
	}
	if len(images) == 0 {
		writeOpenAIError(c, http.StatusBadGateway, "image_generation_error", "Upstream returned no generated images")
		return
	}
	created := time.Now().Unix()
	if request.Stream {
		event := "image_generation.completed"
		if inputs != nil {
			event = "image_edit.completed"
		}
		setSSEHeaders(c)
		for _, data := range images {
			payload := imageFields(data)
			payload["type"], payload["b64_json"], payload["created_at"] = event, base64.StdEncoding.EncodeToString(data), created
			if err := writeSSEJSON(c.Writer, payload); err != nil {
				return
			}
		}
		return
	}
	data := make([]gin.H, 0, len(images))
	for _, image := range images {
		if request.ResponseFormat == "url" {
			data = append(data, gin.H{"url": fmt.Sprintf("data:%s;base64,%s", http.DetectContentType(image), base64.StdEncoding.EncodeToString(image))})
			continue
		}
		data = append(data, gin.H{"b64_json": base64.StdEncoding.EncodeToString(image)})
	}
	response := imageFields(images[0])
	response["created"], response["data"] = created, data
	c.JSON(http.StatusOK, response)
}

// imageFields 返回生成图片的 output_format 与 size
func imageFields(data []byte) gin.H {
	fields := gin.H{"output_format": strings.TrimPrefix(http.DetectContentType(data), "image/")}
	if width, height := imageSize(data); width > 0 && height > 0 {
		fields["size"] = fmt.Sprintf("%dx%d", width, height)
	}
	return fields
}

func defaultImageModel(pool *balancer.AccountPool) string {
	for _, model := range availableModels(pool) {
		if model.ID == gemini.ImageModelID {
			return gemini.ImageModelID
		}
	}
	return ""
}

func nonEmptyImageParameters(request ImageGenerationRequest) []string {
	var result []string
	if request.Quality != "" {
		result = append(result, "quality")
	}
	if request.Style != "" {
		result = append(result, "style")
	}
	if request.PartialImages != nil {
		result = append(result, "partial_images")
	}
	if request.OutputFormat != "" {
		result = append(result, "output_format")
	}
	if request.OutputCompression != nil {
		result = append(result, "output_compression")
	}
	if request.Background != "" {
		result = append(result, "background")
	}
	if request.User != "" {
		result = append(result, "user")
	}
	if request.InputFidelity != "" {
		result = append(result, "input_fidelity")
	}
	if request.Moderation != "" {
		result = append(result, "moderation")
	}
	return result
}
