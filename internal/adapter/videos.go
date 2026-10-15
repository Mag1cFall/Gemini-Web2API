package adapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/gin-gonic/gin"
)

// videoDuration 是官网 Create video 生成的视频时长
const videoDuration = "10"

// videoModel 判断模型是否为官网 Create video 工具
func videoModel(pool *balancer.AccountPool, id string) bool {
	model, ok := findModel(pool, config.MapModel(id))
	return ok && model.Feature == gemini.FeatureVideo
}

// defaultVideoModel 返回目录中的视频模型
func defaultVideoModel(pool *balancer.AccountPool) string {
	for _, model := range availableModels(pool) {
		if model.Feature == gemini.FeatureVideo && model.AliasOf == "" {
			return model.ID
		}
	}
	return ""
}

// videoJob 是一次视频生成的输入
type videoJob struct {
	id       string
	protocol string
	model    string
	prompt   string
	size     string
	files    []gemini.FileData
}

// videoResult 是视频生成的成品文件或错误
type videoResult struct {
	file   requestdb.StoredFile
	width  int
	height int
	err    error
}

// runVideoJob 在独立的请求跟踪中生成视频并保存成品，update 在开始与结束时写入协议对象
func runVideoJob(c *gin.Context, pool *balancer.AccountPool, store *requestdb.Store, job videoJob, update func(status string, result *videoResult)) {
	trace, ctx := logging.NewRequest(context.Background(), c.Request.Method, c.Request.URL.Path, c.ClientIP())
	trace.Begin("videos", job.model, false, 1, 0)
	responseJobs.Lock()
	responseJobs.jobs[job.id] = &responseJob{cancel: trace.Cancel}
	responseJobs.Unlock()
	go func() {
		update("in_progress", nil)
		result := generateVideo(ctx, pool, store, job)
		status := http.StatusOK
		if result.err != nil {
			status = upstreamStatus(result.err)
			trace.Fail(status, result.err.Error())
		}
		if !finishResponseJob(job.id) {
			if result.err != nil {
				update("failed", &result)
			} else {
				update("completed", &result)
			}
		}
		store.Record(trace.Finish(status, false), nil)
	}()
}

// generateVideo 调用 Create video 并把成品写入文件存储
func generateVideo(ctx context.Context, pool *balancer.AccountPool, store *requestdb.Store, job videoJob) videoResult {
	result, _, err := runGeneration(ctx, pool, "", false, job.model, job.id, generationMedia{aspectRatio: job.size}, gemini.ThinkingLow, ToolBridge{},
		func(*gemini.Client, bool) (string, []gemini.FileData, error) {
			return job.prompt, job.files, nil
		}, nil, nil)
	if err == nil {
		err = inlineResultMedia(ctx, &result)
	}
	if err != nil {
		return videoResult{err: err}
	}
	for _, media := range result.Accumulator.Primary().Media {
		if media.Type != gemini.MediaGeneratedVideo {
			continue
		}
		mimeType, encoded, ok := splitDataURL(media.URL)
		if !ok {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return videoResult{err: err}
		}
		name := media.FileName
		if name == "" {
			name = job.id + ".mp4"
		}
		file, err := store.CreateFile(context.WithoutCancel(ctx), requestdb.StoredFile{
			ID: randomID("", 12, true), Filename: filepath.Base(name), MIMEType: mimeType, Purpose: "video", Source: "generated",
		}, bytes.NewReader(data))
		return videoResult{file: file, width: media.Width, height: media.Height, err: err}
	}
	text := strings.TrimSpace(result.Accumulator.Primary().Text)
	if text == "" {
		text = "Gemini Web returned no video"
	}
	return videoResult{err: &gemini.ProtocolError{HTTPStatus: http.StatusBadGateway, Message: text}}
}

// openAIVideoRequest 是 OpenAI 视频创建请求的字段
type openAIVideoRequest struct {
	Prompt  string `json:"prompt"`
	Model   string `json:"model"`
	Seconds string `json:"seconds"`
	Size    string `json:"size"`
}

// openAIVideoObject 返回 OpenAI 视频对象
func openAIVideoObject(id string, model string, status string, created int64, size string, prompt string) gin.H {
	if size == "" {
		size = "1280x720"
	}
	return gin.H{
		"id": id, "object": "video", "model": model, "status": status, "progress": 0, "created_at": created,
		"completed_at": nil, "expires_at": nil, "size": size, "seconds": videoDuration, "prompt": prompt,
		"error": nil, "remixed_from_video_id": nil,
	}
}

// saveVideoObject 写入视频任务与协议对象
func saveVideoObject(store *requestdb.Store, id string, protocol string, created time.Time, status string, object any, fileID string) {
	encoded, _ := json.Marshal(object)
	if err := store.SaveVideo(context.Background(), requestdb.StoredVideo{
		ID: id, CreatedAt: created, Protocol: protocol, Status: status, Object: encoded, FileID: fileID,
	}); err != nil {
		logging.Warn(logging.SourceRequest, "保存视频任务失败", "视频", id, "错误", err)
	}
}

// failInterruptedVideos 把上次运行未完成的视频任务保存为失败
func failInterruptedVideos(store *requestdb.Store) {
	if store == nil {
		return
	}
	videos, err := store.UnfinishedVideos(context.Background())
	if err != nil {
		logging.Warn(logging.SourceService, "读取未完成的视频任务失败", "错误", err)
		return
	}
	for _, video := range videos {
		var object map[string]any
		if json.Unmarshal(video.Object, &object) != nil {
			continue
		}
		if video.Protocol == "gemini" {
			object = map[string]any{"name": object["name"], "done": true, "error": geminiOperationError(http.StatusServiceUnavailable, interruptedMessage)}
		} else {
			object["status"], object["completed_at"] = "failed", time.Now().Unix()
			object["error"] = gin.H{"code": "video_generation_failed", "message": interruptedMessage}
		}
		saveVideoObject(store, video.ID, video.Protocol, video.CreatedAt, "failed", object, "")
	}
}

// grpcCodes 是 Google RPC 状态名对应的 google.rpc.Code
var grpcCodes = map[string]int{
	"CANCELLED": 1, "INVALID_ARGUMENT": 3, "DEADLINE_EXCEEDED": 4, "NOT_FOUND": 5, "PERMISSION_DENIED": 7, "RESOURCE_EXHAUSTED": 8,
	"FAILED_PRECONDITION": 9, "ABORTED": 10, "UNIMPLEMENTED": 12, "INTERNAL": 13, "UNAVAILABLE": 14, "UNAUTHENTICATED": 16,
}

// geminiOperationError 返回长时操作 error 字段的 google.rpc.Status
func geminiOperationError(status int, message string) gin.H {
	return gin.H{"code": grpcCodes[geminiStatusName(status)], "message": message}
}

// VideoCreateHandler 处理 OpenAI POST /v1/videos：立即返回 queued 视频对象，由官网 Create video 在后台生成
func VideoCreateHandler(pool *balancer.AccountPool, store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	failInterruptedVideos(store)
	return func(c *gin.Context) {
		var request openAIVideoRequest
		var files []gemini.FileData
		if strings.HasPrefix(c.ContentType(), "multipart/") {
			if err := c.Request.ParseMultipartForm(64 << 20); err != nil {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
			defer c.Request.MultipartForm.RemoveAll()
			request = openAIVideoRequest{Prompt: c.PostForm("prompt"), Model: c.PostForm("model"), Seconds: c.PostForm("seconds"), Size: c.PostForm("size")}
			if header, err := c.FormFile("input_reference"); err == nil {
				file, err := header.Open()
				if err != nil {
					writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
					return
				}
				data, err := io.ReadAll(file)
				file.Close()
				if err != nil {
					writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
					return
				}
				files = append(files, gemini.FileData{Data: data, FileName: header.Filename, MimeType: header.Header.Get("Content-Type")})
			}
		} else if err := decodeRequest(c, &request); err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.Model == "" {
			request.Model = defaultVideoModel(pool)
		}
		bindScope(c, pool, request.Model)
		if strings.TrimSpace(request.Prompt) == "" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "prompt is required")
			return
		}
		if request.Model == "" {
			writeOpenAIError(c, http.StatusNotFound, "model_not_found", unavailableCapability("video generation on the ready accounts"))
			return
		}
		knownModel, readyModel := modelStatus(pool, request.Model)
		if !knownModel {
			writeOpenAIError(c, http.StatusNotFound, "model_not_found", modelNotFoundMessage(pool, request.Model))
			return
		}
		if !videoModel(pool, request.Model) {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("model %q does not generate videos; use %s", request.Model, defaultVideoModel(pool)))
			return
		}
		if !readyModel {
			writeOpenAIError(c, http.StatusServiceUnavailable, "upstream_error", "No account is ready for the requested model")
			return
		}
		if request.Seconds != "" && request.Seconds != videoDuration {
			setUnsupportedParameters(c, []string{"seconds"})
		}
		id := randomID("video_", 32, true)
		created := time.Now()
		object := openAIVideoObject(id, request.Model, "queued", created.Unix(), request.Size, request.Prompt)
		saveVideoObject(store, id, "openai", created, "queued", object, "")
		response := maps.Clone(object)
		scope := c.Copy()
		runVideoJob(c, pool, store, videoJob{id: id, protocol: "openai", model: request.Model, prompt: request.Prompt, size: request.Size, files: files},
			func(status string, result *videoResult) {
				object["status"] = status
				fileID := ""
				switch status {
				case "in_progress":
					object["progress"] = 10
				case "completed":
					object["progress"], object["completed_at"], fileID = 100, time.Now().Unix(), result.file.ID
					if result.width > 0 && result.height > 0 {
						object["size"] = fmt.Sprintf("%dx%d", result.width, result.height)
					}
				case "failed":
					object["completed_at"] = time.Now().Unix()
					object["error"] = gin.H{"code": "video_generation_failed", "message": publicMessage(scope, result.err.Error())}
				}
				saveVideoObject(store, id, "openai", created, status, object, fileID)
			})
		c.JSON(http.StatusOK, response)
	}
}

// storedVideoOrNotFound 读取 OpenAI 视频任务，不存在时写入 404
func storedVideoOrNotFound(c *gin.Context, store *requestdb.Store) (requestdb.StoredVideo, bool) {
	id := c.Param("video")
	video, err := store.Video(c.Request.Context(), id)
	if errors.Is(err, requestdb.ErrNotFound) || err == nil && video.Protocol != "openai" {
		writeOpenAIError(c, http.StatusNotFound, "video_not_found", fmt.Sprintf("Video with id '%s' not found.", id))
		return requestdb.StoredVideo{}, false
	}
	if err != nil {
		writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
		return requestdb.StoredVideo{}, false
	}
	return video, true
}

// VideoRetrieveHandler 返回 OpenAI 视频对象
func VideoRetrieveHandler(store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if video, ok := storedVideoOrNotFound(c, store); ok {
			c.Data(http.StatusOK, "application/json; charset=utf-8", video.Object)
		}
	}
}

// VideoContentHandler 返回已完成视频的 MP4 内容
func VideoContentHandler(store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		video, ok := storedVideoOrNotFound(c, store)
		if !ok {
			return
		}
		if variant := c.DefaultQuery("variant", "video"); variant != "video" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("variant %q is not available: Gemini Web returns only the video file", variant))
			return
		}
		if video.Status != "completed" || video.FileID == "" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Video '%s' is %s; content is available after it completes.", video.ID, video.Status))
			return
		}
		serveStoredFile(c, store, video.FileID, func() {
			writeOpenAIError(c, http.StatusNotFound, "video_not_found", fmt.Sprintf("Content of video '%s' was not found.", video.ID))
		}, func(err error) { writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error()) })
	}
}

// VideoListHandler 按 after、limit 与 order 分页列出 OpenAI 视频对象
func VideoListHandler(store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := 20
		if raw := c.Query("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 100 {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100")
				return
			}
			limit = value
		}
		order := c.DefaultQuery("order", "desc")
		if order != "asc" && order != "desc" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "order must be asc or desc")
			return
		}
		videos, more, err := store.Videos(c.Request.Context(), "openai", order == "asc", c.Query("after"), limit)
		if errors.Is(err, requestdb.ErrNotFound) {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("after video %q was not found", c.Query("after")))
			return
		}
		if err != nil {
			writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		data := make([]json.RawMessage, 0, len(videos))
		for _, video := range videos {
			data = append(data, video.Object)
		}
		list := gin.H{"object": "list", "data": data, "first_id": nil, "last_id": nil, "has_more": more}
		if len(videos) > 0 {
			list["first_id"], list["last_id"] = videos[0].ID, videos[len(videos)-1].ID
		}
		c.JSON(http.StatusOK, list)
	}
}

// VideoDeleteHandler 删除视频任务与成品，进行中的生成同时停止
func VideoDeleteHandler(store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		video, ok := storedVideoOrNotFound(c, store)
		if !ok {
			return
		}
		cancelResponseJob(video.ID)
		if _, err := store.DeleteVideo(c.Request.Context(), video.ID); err != nil {
			writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": video.ID, "object": "video.deleted", "deleted": true})
	}
}

// geminiVideoRequest 是 predictLongRunning 的请求
type geminiVideoRequest struct {
	Instances []struct {
		Prompt string `json:"prompt"`
		Image  *struct {
			BytesBase64Encoded string `json:"bytesBase64Encoded"`
			MimeType           string `json:"mimeType"`
		} `json:"image"`
	} `json:"instances"`
	Parameters map[string]json.RawMessage `json:"parameters"`
}

// geminiVideoOperation 处理 models/{model}:predictLongRunning，返回长时操作名并在后台生成
func geminiVideoOperation(c *gin.Context, pool *balancer.AccountPool, store *requestdb.Store, model string) {
	var request geminiVideoRequest
	if err := decodeRequest(c, &request); err != nil {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if len(request.Instances) != 1 || strings.TrimSpace(request.Instances[0].Prompt) == "" {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "instances must contain one instance with a prompt")
		return
	}
	if _, readyModel := modelStatus(pool, model); !readyModel {
		writeGeminiError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "No account is ready for the requested model")
		return
	}
	instance := request.Instances[0]
	var files []gemini.FileData
	if instance.Image != nil && instance.Image.BytesBase64Encoded != "" {
		data, err := decodeBase64(instance.Image.BytesBase64Encoded)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "image.bytesBase64Encoded: "+err.Error())
			return
		}
		files = append(files, gemini.FileData{Data: data, FileName: "image" + mimeTypeToExt(instance.Image.MimeType), MimeType: instance.Image.MimeType})
	}
	var aspectRatio string
	_ = json.Unmarshal(request.Parameters["aspectRatio"], &aspectRatio)
	var unsupported []string
	for field := range request.Parameters {
		if field != "aspectRatio" {
			unsupported = append(unsupported, "parameters."+field)
		}
	}
	setUnsupportedParameters(c, unsupported)
	operationID := randomID("", 12, true)
	name := "models/" + model + "/operations/" + operationID
	created := time.Now()
	operation := gin.H{"name": name}
	saveVideoObject(store, operationID, "gemini", created, "queued", operation, "")
	base := requestBaseURL(c)
	scope := c.Copy()
	runVideoJob(c, pool, store, videoJob{id: operationID, protocol: "gemini", model: model, prompt: instance.Prompt, size: aspectRatio, files: files},
		func(status string, result *videoResult) {
			done := gin.H{"name": name, "done": true}
			switch status {
			case "in_progress":
				saveVideoObject(store, operationID, "gemini", created, status, operation, "")
			case "completed":
				done["response"] = gin.H{
					"@type": "type.googleapis.com/google.ai.generativelanguage.v1beta.PredictLongRunningResponse",
					"generateVideoResponse": gin.H{"generatedSamples": []gin.H{{"video": gin.H{
						"uri": base + "/v1beta/files/" + result.file.ID + ":download?alt=media",
					}}}},
				}
				saveVideoObject(store, operationID, "gemini", created, status, done, result.file.ID)
			case "failed":
				done["error"] = geminiOperationError(upstreamStatus(result.err), publicMessage(scope, result.err.Error()))
				saveVideoObject(store, operationID, "gemini", created, status, done, "")
			}
		})
	c.JSON(http.StatusOK, operation)
}

// GeminiOperationHandler 返回 predictLongRunning 的长时操作，路径为 models/{model}/operations/{id} 或 operations/{id}
func GeminiOperationHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		id := c.Param("operation")
		video, err := store.Video(c.Request.Context(), id)
		if errors.Is(err, requestdb.ErrNotFound) || err == nil && video.Protocol != "gemini" {
			writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("Operation %s was not found.", id))
			return
		}
		if err != nil {
			writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", video.Object)
	}
}
