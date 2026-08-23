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
	"github.com/gin-gonic/gin"
)

// ImageGenerationRequest 表示 OpenAI 图片生成请求
type ImageGenerationRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model,omitempty"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Style          string `json:"style,omitempty"`
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
			Prompt:         c.PostForm("prompt"),
			Model:          c.PostForm("model"),
			Size:           c.PostForm("size"),
			ResponseFormat: c.PostForm("response_format"),
			Quality:        c.PostForm("quality"),
			Style:          c.PostForm("style"),
		}
		if rawN := strings.TrimSpace(c.PostForm("n")); rawN != "" {
			n, err := strconv.Atoi(rawN)
			if err != nil {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "n must be an integer")
				return
			}
			request.N = n
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
	if strings.TrimSpace(request.Prompt) == "" {
		writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "prompt is required")
		return
	}
	if request.Model == "" {
		request.Model = defaultImageModel(pool)
	}
	if request.Model == "" {
		writeOpenAIError(c, http.StatusBadRequest, "model_not_found", "No image model is available")
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
	if request.Quality != "" {
		prompt += "\nQuality: " + request.Quality
	}
	if request.Style != "" {
		prompt += "\nStyle: " + request.Style
	}
	if request.Size != "" {
		prompt += "\nCanvas size: " + request.Size
	}
	images := make([]gin.H, 0, request.N)
	for index := 0; index < request.N && len(images) < request.N; index++ {
		responseID := fmt.Sprintf("img_%d_%d", time.Now().UnixNano(), index)
		result, accountID, err := runGeneration(
			c.Request.Context(), pool, "", false, request.Model, responseID, true, gemini.ThinkingStandard,
			func(client *gemini.Client, _ bool) (string, []gemini.FileData, error) {
				files := make([]gemini.FileData, 0, len(inputs))
				for _, input := range inputs {
					fileID, err := client.UploadFile(c.Request.Context(), input.data, input.filename)
					if err != nil {
						return "", nil, err
					}
					files = append(files, gemini.FileData{URL: fileID, FileName: input.filename})
				}
				return prompt, files, nil
			}, nil,
		)
		c.Set("account_id", accountID)
		if err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}
		for _, image := range result.Accumulator.Primary().Images {
			if len(images) == request.N {
				break
			}
			data, err := result.Client.FetchMedia(c.Request.Context(), image.URL)
			if err != nil {
				writeOpenAIError(c, upstreamStatus(err), "image_download_error", err.Error())
				return
			}
			if request.ResponseFormat == "url" {
				mimeType := http.DetectContentType(data)
				images = append(images, gin.H{"url": fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))})
				continue
			}
			images = append(images, gin.H{"b64_json": base64.StdEncoding.EncodeToString(data)})
		}
	}
	if len(images) == 0 {
		writeOpenAIError(c, http.StatusBadGateway, "image_generation_error", "Upstream returned no generated images")
		return
	}
	c.JSON(http.StatusOK, gin.H{"created": time.Now().Unix(), "data": images})
}

func defaultImageModel(pool *balancer.AccountPool) string {
	for _, model := range availableModels(pool) {
		if model.Default {
			return model.ID
		}
	}
	return ""
}

func nonEmptyImageParameters(request ImageGenerationRequest) []string {
	var result []string
	if request.Size != "" {
		result = append(result, "size")
	}
	if request.Quality != "" {
		result = append(result, "quality")
	}
	if request.Style != "" {
		result = append(result, "style")
	}
	return result
}
