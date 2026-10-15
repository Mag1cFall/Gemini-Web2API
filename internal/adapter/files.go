package adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/gin-gonic/gin"
)

// fileUploadLimit 是单个上传文件的最大字节数
const fileUploadLimit = 2 << 30

// geminiFileTTL 是 Gemini Files API 上传文件的保留时长
const geminiFileTTL = 48 * time.Hour

// sharedStore 是文件、Responses 与视频接口共用的账本库，生成请求据此解析文件引用
var sharedStore atomic.Pointer[requestdb.Store]

// useStore 登记文件引用解析使用的账本库
func useStore(store *requestdb.Store) {
	if store != nil {
		sharedStore.Store(store)
	}
}

// randomID 返回 prefix 加 n 个随机字符的标识，lower 为真时只用小写字母与数字
func randomID(prefix string, n int, lower bool) string {
	alphabet := "abcdefghijklmnopqrstuvwxyz0123456789"
	if !lower {
		alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	}
	var builder strings.Builder
	builder.WriteString(prefix)
	limit := big.NewInt(int64(len(alphabet)))
	for range n {
		index, _ := rand.Int(rand.Reader, limit)
		builder.WriteByte(alphabet[index.Int64()])
	}
	return builder.String()
}

// fileReferencePattern 匹配 Gemini 文件名或本服务文件 URI 中的文件 ID
var fileReferencePattern = regexp.MustCompile(`(?:^|/)files/([A-Za-z0-9_-]+)(?::download)?(?:[?#].*)?$`)

// storedFileID 从 file_id、Gemini 文件名 files/<ID> 或文件 URI 中取出存储中的文件 ID
func storedFileID(reference string) (string, bool) {
	reference = strings.TrimSpace(reference)
	if match := fileReferencePattern.FindStringSubmatch(reference); match != nil {
		return match[1], true
	}
	if reference != "" && !strings.ContainsAny(reference, "/:?#") {
		return reference, true
	}
	return "", false
}

// missingFileError 表示生成请求引用的文件不在文件存储中
type missingFileError struct{ id string }

// Error 返回文件不存在的说明
func (e missingFileError) Error() string {
	return fmt.Sprintf("file %q was not found", e.id)
}

// storedFileData 读取文件存储中的文件作为生成附件
func storedFileData(ctx context.Context, id string) (*gemini.FileData, error) {
	store := sharedStore.Load()
	if store == nil {
		return nil, missingFileError{id: id}
	}
	file, data, err := store.ReadFile(ctx, id)
	if errors.Is(err, requestdb.ErrNotFound) {
		return nil, missingFileError{id: id}
	}
	if err != nil {
		return nil, err
	}
	return &gemini.FileData{Data: data, FileName: file.Filename, MimeType: file.MIMEType}, nil
}

// referencedFile 解析内容段中的文件引用：Chat file.file_id、Responses file_id 与 Anthropic source.file_id
func referencedFile(ctx context.Context, part map[string]json.RawMessage) (*gemini.FileData, bool, error) {
	var id string
	_ = json.Unmarshal(part["file_id"], &id)
	if id == "" {
		var source struct {
			Type   string `json:"type"`
			FileID string `json:"file_id"`
		}
		_ = json.Unmarshal(part["source"], &source)
		id = source.FileID
	}
	if id == "" {
		return nil, false, nil
	}
	file, err := storedFileData(ctx, id)
	return file, true, err
}

// youtubePattern 匹配 YouTube 视频链接
var youtubePattern = regexp.MustCompile(`^https?://(?:(?:www\.|m\.|music\.)?youtube\.com/|youtu\.be/)`)

// isYouTubeURL 判断地址是否为 YouTube 视频链接，官网按扩展读取这类链接的视频内容
func isYouTubeURL(value string) bool {
	return youtubePattern.MatchString(strings.TrimSpace(value))
}

// fileUploadMIME 返回上传文件的 MIME：取声明值，缺失或为通用二进制时按扩展名与内容识别
func fileUploadMIME(declared string, filename string, head []byte) string {
	declared = strings.TrimSpace(strings.Split(declared, ";")[0])
	if declared != "" && declared != "application/octet-stream" {
		return declared
	}
	if byExtension := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); byExtension != "" {
		return strings.Split(byExtension, ";")[0]
	}
	return strings.Split(http.DetectContentType(head), ";")[0]
}

// saveUpload 把上传内容写入文件存储
func saveUpload(ctx context.Context, store *requestdb.Store, file requestdb.StoredFile, content io.Reader) (requestdb.StoredFile, error) {
	head := make([]byte, 512)
	count, err := io.ReadFull(content, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return requestdb.StoredFile{}, err
	}
	head = head[:count]
	file.MIMEType = fileUploadMIME(file.MIMEType, file.Filename, head)
	if file.Source == "" {
		file.Source = "uploaded"
	}
	return store.CreateFile(ctx, file, io.MultiReader(bytes.NewReader(head), content))
}

// anthropicRequest 判断请求是否为 Anthropic 协议
func anthropicRequest(c *gin.Context) bool {
	return c.GetHeader("anthropic-version") != "" || c.GetHeader("anthropic-beta") != ""
}

// openAIFileObject 返回 OpenAI 文件对象
func openAIFileObject(file requestdb.StoredFile) gin.H {
	var expires any
	if !file.ExpiresAt.IsZero() {
		expires = file.ExpiresAt.Unix()
	}
	return gin.H{
		"id": file.ID, "object": "file", "bytes": file.Bytes, "created_at": file.CreatedAt.Unix(), "expires_at": expires,
		"filename": file.Filename, "purpose": file.Purpose, "status": "processed", "status_details": nil,
	}
}

// anthropicFileObject 返回 Anthropic 文件对象
func anthropicFileObject(file requestdb.StoredFile) gin.H {
	return gin.H{
		"id": file.ID, "type": "file", "filename": file.Filename, "mime_type": file.MIMEType, "size_bytes": file.Bytes,
		"created_at": file.CreatedAt.UTC().Format(time.RFC3339Nano), "downloadable": file.Source == "generated",
	}
}

// writeFileNotFound 按请求协议返回文件不存在
func writeFileNotFound(c *gin.Context, id string) {
	if anthropicRequest(c) {
		writeClaudeError(c, http.StatusNotFound, "not_found_error", fmt.Sprintf("File not found: %s", id))
		return
	}
	writeOpenAIError(c, http.StatusNotFound, "file_not_found", fmt.Sprintf("No such File object: %s", id))
}

// writeStoreError 按请求协议返回账本库错误
func writeStoreError(c *gin.Context, err error) {
	if anthropicRequest(c) {
		writeClaudeError(c, http.StatusInternalServerError, "api_error", err.Error())
		return
	}
	writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
}

// FileUploadHandler 处理 OpenAI 与 Anthropic 文件上传，带 anthropic-version 头时返回 Anthropic 文件对象
func FileUploadHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		anthropic := anthropicRequest(c)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, fileUploadLimit)
		reader, err := c.Request.MultipartReader()
		if err != nil {
			writeFileRequestError(c, anthropic, "request must be multipart/form-data with a file field")
			return
		}
		fields := map[string]string{}
		var saved *requestdb.StoredFile
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				writeFileRequestError(c, anthropic, err.Error())
				return
			}
			if part.FormName() != "file" || part.FileName() == "" {
				value, _ := io.ReadAll(io.LimitReader(part, 1<<20))
				fields[part.FormName()] = string(value)
				continue
			}
			if saved != nil {
				writeFileRequestError(c, anthropic, "only one file can be uploaded per request")
				return
			}
			record := requestdb.StoredFile{Filename: filepath.Base(part.FileName()), MIMEType: part.Header.Get("Content-Type")}
			if anthropic {
				record.ID, record.Purpose = randomID("file_", 24, false), "anthropic"
			} else {
				record.ID = randomID("file-", 24, false)
			}
			file, err := saveUpload(c.Request.Context(), store, record, part)
			if err != nil {
				writeFileRequestError(c, anthropic, err.Error())
				return
			}
			saved = &file
		}
		if saved == nil {
			writeFileRequestError(c, anthropic, "file is required")
			return
		}
		if anthropic {
			c.JSON(http.StatusOK, anthropicFileObject(*saved))
			return
		}
		purpose := strings.TrimSpace(fields["purpose"])
		if purpose == "" {
			_, _ = store.DeleteFile(c.Request.Context(), saved.ID)
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "Missing required parameter: 'purpose'.")
			return
		}
		saved.Purpose = purpose
		if seconds, err := strconv.Atoi(strings.TrimSpace(fields["expires_after[seconds]"])); err == nil && seconds > 0 {
			saved.ExpiresAt = saved.CreatedAt.Add(time.Duration(seconds) * time.Second)
		}
		if err := store.UpdateFile(c.Request.Context(), *saved); err != nil {
			writeStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, openAIFileObject(*saved))
	}
}

// writeFileRequestError 按协议返回上传请求错误
func writeFileRequestError(c *gin.Context, anthropic bool, message string) {
	if anthropic {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", message)
		return
	}
	writeOpenAIError(c, http.StatusBadRequest, "invalid_request", message)
}

// FileListHandler 列出文件：OpenAI 按 purpose、order、after、limit 分页，Anthropic 按 page、limit 分页
func FileListHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		if anthropicRequest(c) {
			listAnthropicFiles(c, store)
			return
		}
		limit := 10000
		if raw := c.Query("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 10000 {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 10000")
				return
			}
			limit = value
		}
		order := c.DefaultQuery("order", "desc")
		if order != "asc" && order != "desc" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "order must be asc or desc")
			return
		}
		files, more, err := store.Files(c.Request.Context(), requestdb.FileQuery{Purpose: c.Query("purpose"), Ascending: order == "asc", After: c.Query("after"), Limit: limit})
		if errors.Is(err, requestdb.ErrNotFound) {
			writeFileNotFound(c, c.Query("after"))
			return
		}
		if err != nil {
			writeStoreError(c, err)
			return
		}
		data := make([]gin.H, 0, len(files))
		for _, file := range files {
			data = append(data, openAIFileObject(file))
		}
		list := gin.H{"object": "list", "data": data, "first_id": nil, "last_id": nil, "has_more": more}
		if len(files) > 0 {
			list["first_id"], list["last_id"] = files[0].ID, files[len(files)-1].ID
		}
		c.JSON(http.StatusOK, list)
	}
}

// anthropicPagePrefix 是 Anthropic Files 列表 page 游标的前缀，其后为上一页最后一个文件的 ID
const anthropicPagePrefix = "page_"

// listAnthropicFiles 按 Anthropic Files API 的 page 游标分页列出文件，新文件在前，还有下一页时 next_page 为其游标
func listAnthropicFiles(c *gin.Context, store *requestdb.Store) {
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000 {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "limit must be between 1 and 1000")
			return
		}
		limit = value
	}
	query := requestdb.FileQuery{After: strings.TrimPrefix(c.Query("page"), anthropicPagePrefix), Limit: limit}
	files, more, err := store.Files(c.Request.Context(), query)
	if errors.Is(err, requestdb.ErrNotFound) {
		writeFileNotFound(c, query.After)
		return
	}
	if err != nil {
		writeStoreError(c, err)
		return
	}
	data := make([]gin.H, 0, len(files))
	for _, file := range files {
		data = append(data, anthropicFileObject(file))
	}
	list := gin.H{"data": data, "next_page": nil}
	if more {
		list["next_page"] = anthropicPagePrefix + files[len(files)-1].ID
	}
	c.JSON(http.StatusOK, list)
}

// FileRetrieveHandler 返回 OpenAI 或 Anthropic 文件元数据
func FileRetrieveHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		id := c.Param("file")
		file, err := store.File(c.Request.Context(), id)
		if errors.Is(err, requestdb.ErrNotFound) {
			writeFileNotFound(c, id)
			return
		}
		if err != nil {
			writeStoreError(c, err)
			return
		}
		if anthropicRequest(c) {
			c.JSON(http.StatusOK, anthropicFileObject(file))
			return
		}
		c.JSON(http.StatusOK, openAIFileObject(file))
	}
}

// FileContentHandler 返回文件内容
func FileContentHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		id := c.Param("file")
		serveStoredFile(c, store, id, func() { writeFileNotFound(c, id) }, func(err error) { writeStoreError(c, err) })
	}
}

// serveStoredFile 以文件 MIME 与文件名输出文件内容
func serveStoredFile(c *gin.Context, store *requestdb.Store, id string, notFound func(), failed func(error)) {
	file, content, err := store.OpenFile(c.Request.Context(), id)
	if errors.Is(err, requestdb.ErrNotFound) {
		notFound()
		return
	}
	if err != nil {
		failed(err)
		return
	}
	defer content.Close()
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Filename}))
	c.DataFromReader(http.StatusOK, file.Bytes, file.MIMEType, content, nil)
}

// FileDeleteHandler 删除文件，Anthropic 返回 file_deleted 对象
func FileDeleteHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		id := c.Param("file")
		deleted, err := store.DeleteFile(c.Request.Context(), id)
		if err != nil {
			writeStoreError(c, err)
			return
		}
		if !deleted {
			writeFileNotFound(c, id)
			return
		}
		if anthropicRequest(c) {
			c.JSON(http.StatusOK, gin.H{"id": id, "type": "file_deleted"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id, "object": "file", "deleted": true})
	}
}

// geminiFileObject 返回 Gemini Files API 的 File 资源
func geminiFileObject(c *gin.Context, file requestdb.StoredFile) gin.H {
	digest, _ := hex.DecodeString(file.SHA256)
	object := gin.H{
		"name": "files/" + file.ID, "displayName": file.Filename, "mimeType": file.MIMEType,
		"sizeBytes": strconv.FormatInt(file.Bytes, 10), "createTime": file.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updateTime": file.CreatedAt.UTC().Format(time.RFC3339Nano), "sha256Hash": base64.StdEncoding.EncodeToString(digest),
		"uri": requestBaseURL(c) + "/v1beta/files/" + file.ID, "state": "ACTIVE", "source": "UPLOADED",
	}
	if !file.ExpiresAt.IsZero() {
		object["expirationTime"] = file.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if file.Source == "generated" {
		object["source"] = "GENERATED"
		object["downloadUri"] = requestBaseURL(c) + "/v1beta/files/" + file.ID + ":download?alt=media"
	}
	return object
}

// requestBaseURL 返回客户端访问本服务所用的协议与主机
func requestBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0]); forwarded != "" {
		scheme = forwarded
	}
	host := c.Request.Host
	if forwarded := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Host"), ",")[0]); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}

// writeGeminiFileNotFound 与官方一样以 403 PERMISSION_DENIED 返回不存在的文件
func writeGeminiFileNotFound(c *gin.Context, id string) {
	writeGeminiError(c, http.StatusForbidden, "PERMISSION_DENIED", geminiFileNotFound(id).Message)
}

// geminiFileNotFound 返回与官方相同的文件不存在错误，Gemini 生成与计数引用不存在的文件时使用
func geminiFileNotFound(id string) *gemini.ProtocolError {
	return &gemini.ProtocolError{HTTPStatus: http.StatusForbidden, Message: fmt.Sprintf("You do not have permission to access the File %s or it may not exist.", id)}
}

// geminiFileID 校验 Gemini 文件名中的 ID：小写字母、数字与连字符，最长 40 个字符
var geminiFileID = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// pendingUpload 是 Gemini 断点续传上传会话
type pendingUpload struct {
	file      requestdb.StoredFile
	temporary string
	size      int64
	expected  int64
	created   time.Time
}

// geminiUploads 保存进行中的断点续传会话
var geminiUploads = struct {
	sync.Mutex
	sessions map[string]*pendingUpload
}{sessions: make(map[string]*pendingUpload)}

// geminiFileMetadata 是上传请求中的 File 元数据
type geminiFileMetadata struct {
	File struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		MimeType    string `json:"mimeType"`
	} `json:"file"`
}

// GeminiFileUploadHandler 处理 /upload/v1beta/files 的断点续传、multipart 与单次媒体上传，创建时删除上次运行遗留的续传临时文件
func GeminiFileUploadHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	removeStaleUploads(store)
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, fileUploadLimit)
		if uploadID := c.Query("upload_id"); uploadID != "" {
			continueGeminiUpload(c, store, uploadID)
			return
		}
		protocol := strings.ToLower(c.GetHeader("X-Goog-Upload-Protocol"))
		if protocol == "" {
			protocol = c.Query("uploadType")
		}
		switch protocol {
		case "resumable":
			startGeminiUpload(c, store)
		case "multipart":
			multipartGeminiUpload(c, store)
		default:
			record, err := newGeminiFile(store, c, geminiFileMetadata{})
			if err != nil {
				writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
				return
			}
			record.MIMEType = c.GetHeader("Content-Type")
			finishGeminiUpload(c, store, record, c.Request.Body)
		}
	}
}

// newGeminiFile 按上传元数据生成文件记录，name 已存在时返回错误
func newGeminiFile(store *requestdb.Store, c *gin.Context, metadata geminiFileMetadata) (requestdb.StoredFile, error) {
	id := randomID("", 12, true)
	if name := strings.TrimPrefix(strings.TrimSpace(metadata.File.Name), "files/"); name != "" {
		if !geminiFileID.MatchString(name) {
			return requestdb.StoredFile{}, fmt.Errorf("file name %q must contain only lowercase letters, digits and dashes, up to 40 characters", metadata.File.Name)
		}
		if _, err := store.File(c.Request.Context(), name); err == nil {
			return requestdb.StoredFile{}, errFileExists
		}
		id = name
	}
	created := time.Now()
	return requestdb.StoredFile{
		ID: id, CreatedAt: created, ExpiresAt: created.Add(geminiFileTTL), Filename: metadata.File.DisplayName,
		MIMEType: metadata.File.MimeType, Purpose: "gemini", Source: "uploaded",
	}, nil
}

// errFileExists 表示上传请求指定的文件名已存在
var errFileExists = errors.New("file already exists")

// removeStaleUploads 删除文件目录中上次运行遗留的断点续传临时文件
func removeStaleUploads(store *requestdb.Store) {
	if store == nil {
		return
	}
	dir, err := store.FilesDir()
	if err != nil {
		return
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "upload-*.part"))
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			logging.Warn(logging.SourceService, "删除遗留的上传临时文件失败", "文件", path, "错误", err)
		}
	}
}

// writeGeminiUploadError 返回上传元数据错误，文件名已存在时为 409 ALREADY_EXISTS
func writeGeminiUploadError(c *gin.Context, err error) {
	if errors.Is(err, errFileExists) {
		writeGeminiError(c, http.StatusConflict, "ALREADY_EXISTS", "File already exists.")
		return
	}
	writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
}

// startGeminiUpload 开始断点续传会话，响应头 X-Goog-Upload-URL 给出后续上传地址
func startGeminiUpload(c *gin.Context, store *requestdb.Store) {
	var metadata geminiFileMetadata
	if body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20)); len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &metadata); err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid file metadata: "+err.Error())
			return
		}
	}
	record, err := newGeminiFile(store, c, metadata)
	if err != nil {
		writeGeminiUploadError(c, err)
		return
	}
	if record.MIMEType == "" {
		record.MIMEType = c.GetHeader("X-Goog-Upload-Header-Content-Type")
	}
	dir, err := store.FilesDir()
	if err == nil {
		err = os.MkdirAll(dir, 0o700)
	}
	var temporary *os.File
	if err == nil {
		temporary, err = os.CreateTemp(dir, "upload-*.part")
	}
	if err != nil {
		writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	temporary.Close()
	expected, _ := strconv.ParseInt(c.GetHeader("X-Goog-Upload-Header-Content-Length"), 10, 64)
	uploadID := randomID("", 32, false)
	geminiUploads.Lock()
	for id, session := range geminiUploads.sessions {
		if time.Since(session.created) > 7*24*time.Hour {
			_ = os.Remove(session.temporary)
			delete(geminiUploads.sessions, id)
		}
	}
	geminiUploads.sessions[uploadID] = &pendingUpload{file: record, temporary: temporary.Name(), expected: expected, created: time.Now()}
	geminiUploads.Unlock()
	c.Header("X-Goog-Upload-URL", requestBaseURL(c)+"/upload/v1beta/files?upload_id="+url.QueryEscape(uploadID)+"&upload_protocol=resumable")
	c.Header("X-Goog-Upload-Status", "active")
	c.Header("X-Goog-Upload-Chunk-Granularity", "8388608")
	c.Status(http.StatusOK)
}

// geminiUploadExists 报告 id 是否为进行中的断点续传会话，续传请求凭它授权
func geminiUploadExists(id string) bool {
	if id == "" {
		return false
	}
	geminiUploads.Lock()
	defer geminiUploads.Unlock()
	return geminiUploads.sessions[id] != nil
}

// continueGeminiUpload 按 X-Goog-Upload-Command 追加数据、查询进度或完成上传
func continueGeminiUpload(c *gin.Context, store *requestdb.Store, uploadID string) {
	geminiUploads.Lock()
	session := geminiUploads.sessions[uploadID]
	geminiUploads.Unlock()
	if session == nil {
		writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", "upload session was not found")
		return
	}
	commands := map[string]bool{}
	for _, command := range strings.Split(c.GetHeader("X-Goog-Upload-Command"), ",") {
		commands[strings.ToLower(strings.TrimSpace(command))] = true
	}
	if commands["query"] {
		c.Header("X-Goog-Upload-Status", "active")
		c.Header("X-Goog-Upload-Size-Received", strconv.FormatInt(session.size, 10))
		c.Status(http.StatusOK)
		return
	}
	if commands["cancel"] {
		geminiUploads.Lock()
		delete(geminiUploads.sessions, uploadID)
		geminiUploads.Unlock()
		_ = os.Remove(session.temporary)
		c.Header("X-Goog-Upload-Status", "cancelled")
		c.Status(http.StatusOK)
		return
	}
	if commands["upload"] {
		if offset := c.GetHeader("X-Goog-Upload-Offset"); offset != "" && offset != strconv.FormatInt(session.size, 10) {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("upload offset %s does not match received size %d", offset, session.size))
			return
		}
		output, err := os.OpenFile(session.temporary, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		written, copyErr := io.Copy(output, c.Request.Body)
		output.Close()
		session.size += written
		if copyErr != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", copyErr.Error())
			return
		}
	}
	if !commands["finalize"] {
		c.Header("X-Goog-Upload-Status", "active")
		c.Header("X-Goog-Upload-Size-Received", strconv.FormatInt(session.size, 10))
		c.Status(http.StatusOK)
		return
	}
	geminiUploads.Lock()
	delete(geminiUploads.sessions, uploadID)
	geminiUploads.Unlock()
	defer os.Remove(session.temporary)
	if session.expected > 0 && session.size != session.expected {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("received %d bytes, expected %d", session.size, session.expected))
		return
	}
	content, err := os.Open(session.temporary)
	if err != nil {
		writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	defer content.Close()
	finishGeminiUpload(c, store, session.file, content)
}

// multipartGeminiUpload 处理 multipart/related 上传：第一段为 JSON 元数据，第二段为文件内容
func multipartGeminiUpload(c *gin.Context, store *requestdb.Store) {
	_, parameters, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || parameters["boundary"] == "" {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart upload requires a boundary")
		return
	}
	reader := multipart.NewReader(c.Request.Body, parameters["boundary"])
	first, err := reader.NextPart()
	if err != nil {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	var metadata geminiFileMetadata
	if err := json.NewDecoder(io.LimitReader(first, 1<<20)).Decode(&metadata); err != nil {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid file metadata: "+err.Error())
		return
	}
	content, err := reader.NextPart()
	if err != nil {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart upload is missing the file content")
		return
	}
	record, err := newGeminiFile(store, c, metadata)
	if err != nil {
		writeGeminiUploadError(c, err)
		return
	}
	if record.MIMEType == "" {
		record.MIMEType = content.Header.Get("Content-Type")
	}
	finishGeminiUpload(c, store, record, content)
}

// finishGeminiUpload 写入文件并返回 {"file": File}
func finishGeminiUpload(c *gin.Context, store *requestdb.Store, record requestdb.StoredFile, content io.Reader) {
	file, err := saveUpload(c.Request.Context(), store, record, content)
	if err != nil {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	c.Header("X-Goog-Upload-Status", "final")
	c.JSON(http.StatusOK, gin.H{"file": geminiFileObject(c, file)})
}

// GeminiFileListHandler 按 pageSize 与 pageToken 列出 Gemini 文件
func GeminiFileListHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		limit := 10
		if raw := c.Query("pageSize"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 {
				writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "pageSize must be a non-negative integer")
				return
			}
			limit = min(max(value, 1), 100)
			if value == 0 {
				limit = 10
			}
		}
		files, more, err := store.Files(c.Request.Context(), requestdb.FileQuery{After: c.Query("pageToken"), Limit: limit})
		if errors.Is(err, requestdb.ErrNotFound) {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pageToken")
			return
		}
		if err != nil {
			writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		data := make([]gin.H, 0, len(files))
		for _, file := range files {
			data = append(data, geminiFileObject(c, file))
		}
		response := gin.H{}
		if len(data) > 0 {
			response["files"] = data
		}
		if more {
			response["nextPageToken"] = files[len(files)-1].ID
		}
		c.JSON(http.StatusOK, response)
	}
}

// geminiFileName 返回路径参数中的文件 ID 与是否请求下载内容
func geminiFileName(c *gin.Context) (string, bool) {
	name := c.Param("name")
	if id, ok := strings.CutSuffix(name, ":download"); ok {
		return id, true
	}
	return name, false
}

// GeminiFileGetHandler 返回 Gemini 文件资源，files/{name}:download 返回内容
func GeminiFileGetHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		id, download := geminiFileName(c)
		if download {
			serveStoredFile(c, store, id, func() { writeGeminiFileNotFound(c, id) }, func(err error) {
				writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			})
			return
		}
		file, err := store.File(c.Request.Context(), id)
		if errors.Is(err, requestdb.ErrNotFound) {
			writeGeminiFileNotFound(c, id)
			return
		}
		if err != nil {
			writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		c.JSON(http.StatusOK, geminiFileObject(c, file))
	}
}

// GeminiFileDeleteHandler 删除 Gemini 文件并返回空对象
func GeminiFileDeleteHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		id, _ := geminiFileName(c)
		deleted, err := store.DeleteFile(c.Request.Context(), id)
		if err != nil {
			writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if !deleted {
			writeGeminiFileNotFound(c, id)
			return
		}
		logging.Debug(logging.SourceRequest, "删除文件", "", id)
		c.JSON(http.StatusOK, gin.H{})
	}
}
