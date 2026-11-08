package adapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
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
	// imageURL 为首帧参考图的 http(s) 地址，由生成该视频的账号下载
	imageURL string
	// session 为继续生成时所在网页会话的登记键，新视频为空
	session string
	// instruction 是继续生成时加在提示词前发给官网的指令，新视频为空
	instruction string
}

// videoSession 是视频所在的网页会话与账号
type videoSession struct {
	Account  string                      `json:"account"`
	Snapshot gemini.ConversationSnapshot `json:"snapshot"`
}

// videoResult 是视频生成的成品文件或错误
type videoResult struct {
	file    requestdb.StoredFile
	width   int
	height  int
	seconds int
	err     error
}

// runVideoJob 在独立的请求跟踪中生成视频并保存成品，update 在开始与结束时写入协议对象
func runVideoJob(c *gin.Context, pool *balancer.AccountPool, store *requestdb.Store, job videoJob, update func(status string, result *videoResult)) {
	trace, ctx := logging.NewRequest(context.WithoutCancel(c.Request.Context()), c.Request.Method, c.Request.URL.Path, c.ClientIP())
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
	result, accountID, err := runGeneration(ctx, pool, job.session, sessionContinue, job.model, job.id, generationMedia{aspectRatio: job.size}, gemini.ThinkingLow, ToolBridge{},
		func(client *gemini.Client, _ bool) (string, []gemini.FileData, error) {
			prompt := job.instruction + job.prompt
			if job.imageURL == "" {
				return prompt, job.files, nil
			}
			data, err := fetchRemoteMedia(ctx, client, job.imageURL)
			if err != nil {
				return "", nil, &gemini.ProtocolError{HTTPStatus: http.StatusBadRequest, Message: "input_reference.image_url: " + err.Error()}
			}
			return prompt, append(job.files, gemini.FileData{Data: data, FileName: "input_reference", MimeType: http.DetectContentType(data)}), nil
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
		created := time.Now()
		file, err := store.CreateFile(context.WithoutCancel(ctx), requestdb.StoredFile{
			ID: randomID("", 12, true), CreatedAt: created, ExpiresAt: created.Add(requestdb.GeneratedRetention),
			Filename: filepath.Base(name), MIMEType: mimeType, Purpose: "video", Source: "generated",
		}, bytes.NewReader(data))
		if err == nil {
			saveVideoThumbnail(ctx, result.Client, store, file.ID, media)
			saveVideoSession(store, job.id, videoSession{Account: accountID, Snapshot: result.Accumulator.Session})
		}
		return videoResult{file: file, width: media.Width, height: media.Height, seconds: mp4Seconds(data), err: err}
	}
	text := strings.TrimSpace(result.Accumulator.Primary().Text)
	if text == "" {
		text = "Gemini Web returned no video"
	}
	return videoResult{err: &gemini.ProtocolError{HTTPStatus: http.StatusBadGateway, Message: text}}
}

// mp4Seconds 返回 MP4 mvhd 盒记录的时长，四舍五入为整秒，找不到 mvhd 时返回 0
func mp4Seconds(data []byte) int {
	at := bytes.Index(data, []byte("mvhd"))
	if at < 0 || len(data) < at+36 {
		return 0
	}
	box := data[at+4:]
	if box[0] == 1 {
		return int(math.Round(float64(binary.BigEndian.Uint64(box[24:])) / float64(binary.BigEndian.Uint32(box[20:]))))
	}
	return int(math.Round(float64(binary.BigEndian.Uint32(box[16:])) / float64(binary.BigEndian.Uint32(box[12:]))))
}

// openAIVideoRequest 是 OpenAI 视频创建请求的字段
type openAIVideoRequest struct {
	Prompt         string               `json:"prompt"`
	Model          string               `json:"model"`
	Seconds        string               `json:"seconds"`
	Size           string               `json:"size"`
	InputReference *videoInputReference `json:"input_reference"`
}

// videoInputReference 是 JSON 请求的首帧参考图，image_url 与 file_id 只给一个
type videoInputReference struct {
	ImageURL string `json:"image_url"`
	FileID   string `json:"file_id"`
}

// resolve 把参考图转为文件：file_id 读取文件存储，data: URL 直接解码，http(s) 地址返回给生成账号下载
func (r videoInputReference) resolve(ctx context.Context, store *requestdb.Store) ([]gemini.FileData, string, error) {
	switch {
	case (r.ImageURL == "") == (r.FileID == ""):
		return nil, "", errors.New("input_reference must contain exactly one of image_url or file_id")
	case r.FileID != "":
		file, data, err := store.ReadFile(ctx, r.FileID)
		if errors.Is(err, requestdb.ErrNotFound) {
			return nil, "", fmt.Errorf("input_reference.file_id: no such file: %s", r.FileID)
		}
		if err != nil {
			return nil, "", err
		}
		return []gemini.FileData{{Data: data, FileName: file.Filename, MimeType: file.MIMEType}}, "", nil
	case strings.HasPrefix(r.ImageURL, "data:"):
		mimeType, encoded, ok := splitDataURL(r.ImageURL)
		if !ok {
			return nil, "", errors.New("input_reference.image_url: invalid data URL")
		}
		data, err := decodeBase64(encoded)
		if err != nil {
			return nil, "", fmt.Errorf("input_reference.image_url: %w", err)
		}
		return []gemini.FileData{{Data: data, FileName: "input_reference" + mimeTypeToExt(mimeType), MimeType: mimeType}}, "", nil
	case strings.HasPrefix(r.ImageURL, "http://"), strings.HasPrefix(r.ImageURL, "https://"):
		return nil, r.ImageURL, nil
	}
	return nil, "", errors.New("input_reference.image_url must be a data: URL or an http(s) URL")
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

// saveVideoSession 写入视频所在的网页会话，供 remix 与 extensions 在同一会话中继续生成
func saveVideoSession(store *requestdb.Store, id string, session videoSession) {
	encoded, _ := json.Marshal(session)
	if err := store.SaveVideoSession(context.Background(), id, encoded); err != nil {
		logging.Warn(logging.SourceRequest, "保存视频会话失败", "视频", id, "错误", err)
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
		var imageURL string
		if request.InputReference != nil {
			var err error
			if files, imageURL, err = request.InputReference.resolve(c.Request.Context(), store); err != nil {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
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
		if err := modelKnown(pool, request.Model); err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}
		if !videoModel(pool, request.Model) {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("model %q does not generate videos; use %s", request.Model, defaultVideoModel(pool)))
			return
		}
		if err := awaitModel(c, pool, request.Model); err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}
		if request.Seconds != "" && request.Seconds != videoDuration {
			setUnsupportedParameters(c, []string{"seconds"})
		}
		startOpenAIVideo(c, pool, store, videoJob{model: request.Model, prompt: request.Prompt, size: request.Size, files: files, imageURL: imageURL}, "")
	}
}

// startOpenAIVideo 保存 queued 视频对象、在后台生成并返回该对象，remixedFrom 写入 remixed_from_video_id
func startOpenAIVideo(c *gin.Context, pool *balancer.AccountPool, store *requestdb.Store, job videoJob, remixedFrom string) {
	job.id, job.protocol = randomID("video_", 32, true), "openai"
	created := time.Now()
	object := openAIVideoObject(job.id, job.model, "queued", created.Unix(), job.size, job.prompt)
	if remixedFrom != "" {
		object["remixed_from_video_id"] = remixedFrom
	}
	saveVideoObject(store, job.id, "openai", created, "queued", object, "")
	response := maps.Clone(object)
	scope := c.Copy()
	runVideoJob(c, pool, store, job, func(status string, result *videoResult) {
		object["status"] = status
		fileID := ""
		switch status {
		case "in_progress":
			object["progress"] = 10
		case "completed":
			object["progress"], object["completed_at"], object["expires_at"], fileID = 100, time.Now().Unix(), result.file.ExpiresAt.Unix(), result.file.ID
			if result.width > 0 && result.height > 0 {
				object["size"] = fmt.Sprintf("%dx%d", result.width, result.height)
			}
			if result.seconds > 0 {
				object["seconds"] = strconv.Itoa(result.seconds)
			}
		case "failed":
			object["completed_at"] = time.Now().Unix()
			object["error"] = gin.H{"code": "video_generation_failed", "message": publicMessage(scope, result.err.Error())}
		}
		saveVideoObject(store, job.id, "openai", created, status, object, fileID)
	})
	c.JSON(http.StatusOK, response)
}

// remixInstruction 与 extendInstruction 是官网同一会话追问时触发重新生成与续写的提示词开头
const (
	remixInstruction  = "Make the same video again with this change: "
	extendInstruction = "Extend this video: "
)

// continueOpenAIVideo 在已完成视频的网页会话中以 instruction 加 prompt 继续生成，remixedFrom 写入 remixed_from_video_id
func continueOpenAIVideo(c *gin.Context, pool *balancer.AccountPool, store *requestdb.Store, source requestdb.StoredVideo, prompt string, instruction string, remixedFrom string) {
	if strings.TrimSpace(prompt) == "" {
		writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "prompt is required")
		return
	}
	if source.Status != "completed" {
		writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Video '%s' is %s; only a completed video can be continued.", source.ID, source.Status))
		return
	}
	var session videoSession
	if json.Unmarshal(source.Session, &session) != nil || session.Snapshot.CID == "" {
		writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Video '%s' has no saved Gemini Web conversation to continue.", source.ID))
		return
	}
	var object struct {
		Model string `json:"model"`
		Size  string `json:"size"`
	}
	if err := json.Unmarshal(source.Object, &object); err != nil {
		writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	bindScope(c, pool, object.Model)
	if err := awaitModel(c, pool, object.Model); err != nil {
		writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
		return
	}
	conversations.put(gemini.NewConversationStateFrom(session.Snapshot), session.Account, 0, source.ID)
	pool.BindSession(source.ID, session.Account)
	startOpenAIVideo(c, pool, store, videoJob{model: object.Model, prompt: prompt, size: object.Size, session: source.ID, instruction: instruction}, remixedFrom)
}

// VideoRemixHandler 处理 OpenAI POST /v1/videos/{id}/remix：在源视频的网页会话中按新提示词生成
func VideoRemixHandler(pool *balancer.AccountPool, store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		source, ok := storedVideoOrNotFound(c, store, c.Param("video"))
		if !ok {
			return
		}
		var request struct {
			Prompt string `json:"prompt"`
		}
		if err := decodeRequest(c, &request); err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		continueOpenAIVideo(c, pool, store, source, request.Prompt, remixInstruction, source.ID)
	}
}

// VideoExtensionHandler 处理 OpenAI POST /v1/videos/extensions：在源视频的网页会话中续写，成品为拼接后的完整视频
func VideoExtensionHandler(pool *balancer.AccountPool, store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			Prompt  string `json:"prompt"`
			Seconds string `json:"seconds"`
			Video   struct {
				ID string `json:"id"`
			} `json:"video"`
		}
		if err := decodeRequest(c, &request); err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		source, ok := storedVideoOrNotFound(c, store, request.Video.ID)
		if !ok {
			return
		}
		if request.Seconds != "" && request.Seconds != videoDuration {
			setUnsupportedParameters(c, []string{"seconds"})
		}
		continueOpenAIVideo(c, pool, store, source, request.Prompt, extendInstruction, "")
	}
}

// storedVideoOrNotFound 读取 OpenAI 视频任务，不存在时写入 404
func storedVideoOrNotFound(c *gin.Context, store *requestdb.Store, id string) (requestdb.StoredVideo, bool) {
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
		if video, ok := storedVideoOrNotFound(c, store, c.Param("video")); ok {
			c.Data(http.StatusOK, "application/json; charset=utf-8", video.Object)
		}
	}
}

// VideoContentHandler 按 variant 返回已完成视频的 MP4 或缩略图
func VideoContentHandler(store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		video, ok := storedVideoOrNotFound(c, store, c.Param("video"))
		if !ok {
			return
		}
		variant := c.DefaultQuery("variant", "video")
		switch variant {
		case "video", "thumbnail":
		case "spritesheet":
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "variant \"spritesheet\" is not available: Gemini Web returns a video file and a thumbnail but no spritesheet")
			return
		default:
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Invalid variant %q: expected one of video, thumbnail, spritesheet.", variant))
			return
		}
		if video.Status != "completed" || video.FileID == "" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Video '%s' is %s; content is available after it completes.", video.ID, video.Status))
			return
		}
		fileID := video.FileID
		if variant == "thumbnail" {
			fileID = videoThumbnailFileID(video.FileID)
		}
		serveStoredFile(c, store, fileID, func() {
			writeOpenAIError(c, http.StatusNotFound, "video_not_found", fmt.Sprintf("Content of video '%s' was not found.", video.ID))
		}, func(err error) { writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error()) })
	}
}

// VideoListHandler 按 after、limit 与 order 分页列出 OpenAI 视频对象，未知的 after 返回空页
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
			videos, more, err = nil, false, nil
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
		video, ok := storedVideoOrNotFound(c, store, c.Param("video"))
		if !ok {
			return
		}
		cancelResponseJob(video.ID)
		if _, err := store.DeleteVideo(c.Request.Context(), video.ID); err != nil {
			writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		if _, err := store.DeleteFile(c.Request.Context(), videoThumbnailFileID(video.FileID)); err != nil {
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
	if len(request.Instances) != 1 || strings.TrimSpace(request.Instances[0].Prompt) == "" && (request.Instances[0].Image == nil || request.Instances[0].Image.BytesBase64Encoded == "") {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "instances must contain one instance with a prompt or an image")
		return
	}
	if err := awaitModel(c, pool, model); err != nil {
		writeGeminiError(c, upstreamStatus(err), geminiUpstreamStatus(err), err.Error())
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
