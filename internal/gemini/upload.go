package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

const (
	pathProcessFile = "/_/BardChatUi/data/assistant.lamda.BardFrontendService/ProcessFile"
	uploadTenantID  = "bard-storage"
)

// uploadStartHeaderOrder 是 Chrome 稳定版发起断点续传上传的请求头顺序
var uploadStartHeaderOrder = []string{
	"content-length", "sec-ch-ua-full-version-list", "sec-ch-ua-platform", "sec-ch-ua",
	"sec-ch-ua-bitness", "sec-ch-ua-model", "sec-ch-ua-mobile", "sec-ch-ua-form-factors",
	"sec-ch-ua-wow64", "x-goog-upload-protocol", "sec-ch-ua-arch", "sec-ch-ua-full-version",
	"content-type", "x-goog-upload-header-content-length", "push-id", "x-client-pctx",
	"x-goog-upload-command", "user-agent", "x-tenant-id", "sec-ch-ua-platform-version", "accept", "origin",
	"x-browser-channel", "x-browser-year", "x-browser-validation", "x-browser-copyright",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer",
	"accept-encoding", "accept-language", "cookie", "priority",
}

// uploadFinalizeHeaderOrder 是 Chrome 稳定版写入文件内容并完成上传的请求头顺序
var uploadFinalizeHeaderOrder = []string{
	"content-length", "sec-ch-ua-full-version-list", "sec-ch-ua-platform", "sec-ch-ua",
	"sec-ch-ua-bitness", "sec-ch-ua-model", "sec-ch-ua-mobile", "sec-ch-ua-form-factors",
	"sec-ch-ua-wow64", "sec-ch-ua-arch", "sec-ch-ua-full-version", "content-type",
	"push-id", "x-client-pctx", "x-goog-upload-command", "x-goog-upload-offset", "x-tenant-id",
	"user-agent", "sec-ch-ua-platform-version", "accept", "origin",
	"x-browser-channel", "x-browser-year", "x-browser-validation", "x-browser-copyright",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer",
	"accept-encoding", "accept-language", "cookie", "priority",
}

// processFileHeaderOrder 是 Chrome 稳定版页面 ProcessFile XHR 的请求头顺序
var processFileHeaderOrder = []string{
	"content-length", "sec-ch-ua-full-version-list", "sec-ch-ua-platform", "x-goog-ext-525001261-jspb",
	"sec-ch-ua", "sec-ch-ua-bitness", "sec-ch-ua-model", "sec-ch-ua-mobile", "x-same-domain",
	"sec-ch-ua-wow64", "sec-ch-ua-form-factors", "sec-ch-ua-arch", "sec-ch-ua-full-version",
	"user-agent", "content-type", "sec-ch-ua-platform-version", "accept", "origin",
	"x-browser-channel", "x-browser-year", "x-browser-validation", "x-browser-copyright",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer",
	"accept-encoding", "accept-language", "cookie", "priority",
}

// acceptUploadTerms 在账号的上传条款确认超过官网有效期时，按官网同意按钮把确认时间写入账号偏好
func (c *Client) acceptUploadTerms(ctx context.Context, bootstrap Bootstrap, mode int, level ThinkingLevel) error {
	acceptedAt, _ := bootstrap.Preferences.Field(preferenceUploadTerms).(float64)
	now := time.Now()
	if bootstrap.UploadTermsWindow == 0 || time.Unix(int64(acceptedAt), 0).Add(bootstrap.UploadTermsWindow).After(now) {
		return nil
	}
	message := make([]any, preferenceUploadTerms)
	message[preferenceUploadTerms-1] = now.Unix()
	request, err := json.Marshal([]any{message, nil, []int{preferenceUploadTerms}})
	if err != nil {
		return err
	}
	if _, err := c.batchExecute(ctx, bootstrap, batchRequest{
		rpcID: "L5adhe", payload: string(request), sourcePath: "/app", language: bootstrap.Language, extension: "[0]", mode: mode, level: level,
	}); err != nil {
		return err
	}
	c.bootstrapMu.Lock()
	if c.bootstrap != nil {
		c.bootstrap.Preferences = c.bootstrap.Preferences.with(preferenceUploadTerms, float64(now.Unix()))
	}
	c.bootstrapMu.Unlock()
	return nil
}

// uploadFile 按官网断点续传协议上传附件内容，返回带上传路径与 MIME 类型的附件
func (c *Client) uploadFile(ctx context.Context, bootstrap Bootstrap, file FileData) (FileData, error) {
	data, filename := file.Data, file.FileName
	mimeType := file.MimeType
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	mimeType, _, _ = strings.Cut(mimeType, ";")

	startURL := bootstrap.UploadURL
	if c.authUser > 0 {
		startURL += fmt.Sprintf("?authuser=%d", c.authUser)
	}
	start, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, strings.NewReader("File name: "+filename))
	if err != nil {
		return FileData{}, err
	}
	c.applyUploadHeaders(start, bootstrap, uploadStartHeaderOrder)
	start.Header.Set("content-type", "application/x-www-form-urlencoded;charset=UTF-8")
	start.Header.Set("x-goog-upload-protocol", "resumable")
	start.Header.Set("x-goog-upload-command", "start")
	start.Header.Set("x-goog-upload-header-content-length", fmt.Sprintf("%d", len(data)))
	resp, err := c.doUpload(start)
	if err != nil {
		return FileData{}, err
	}
	resp.Body.Close()
	uploadURL := resp.Header.Get("x-goog-upload-url")
	if uploadURL == "" {
		return FileData{}, retryableProtocolError("upload start response has no x-goog-upload-url", nil)
	}

	finalize, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(data))
	if err != nil {
		return FileData{}, err
	}
	c.applyUploadHeaders(finalize, bootstrap, uploadFinalizeHeaderOrder)
	finalize.Header.Set("content-type", "application/x-www-form-urlencoded;charset=utf-8")
	finalize.Header.Set("x-goog-upload-command", "upload, finalize")
	finalize.Header.Set("x-goog-upload-offset", "0")
	resp, err = c.doUpload(finalize)
	if err != nil {
		return FileData{}, err
	}
	defer resp.Body.Close()
	path, err := io.ReadAll(resp.Body)
	if err != nil {
		return FileData{}, retryableProtocolError("read upload response", err)
	}
	return FileData{URL: strings.TrimSpace(string(path)), FileName: filename, MimeType: mimeType}, nil
}

// applyUploadHeaders 写入页面向上传服务发起跨站 XHR 的请求头与上传租户标识
func (c *Client) applyUploadHeaders(req *http.Request, bootstrap Bootstrap, order []string) {
	applyChromeHeaders(req, c.language, true)
	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
	req.Header.Set("origin", "https://gemini.google.com")
	req.Header.Set("referer", "https://gemini.google.com/")
	req.Header.Set("sec-fetch-site", "same-site")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("priority", "u=1, i")
	req.Header.Set("push-id", bootstrap.UploadPushID)
	req.Header.Set("x-client-pctx", bootstrap.UploadClientContext)
	req.Header.Set("x-tenant-id", uploadTenantID)
	req.Header[http.HeaderOrderKey] = order
	req.Header[http.PHeaderOrderKey] = pseudoHeaderOrder
}

// doUpload 发送一步上传请求并吸收 Cookie，非 200 状态返回协议错误
func (c *Client) doUpload(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportProtocolError("upload", err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, responseStatusError(resp, "upload")
	}
	return resp, nil
}

// processFile 按官网 ProcessFile 登记已上传的附件，返回生成请求引用的文件 ID
func (c *Client) processFile(ctx context.Context, bootstrap Bootstrap, model Model, level ThinkingLevel, file FileData) (string, error) {
	started := time.Now()
	inner, err := pageJSON([]any{
		[]any{[]any{file.URL, nil, 1, file.MimeType}, file.FileName, nil, nil, nil, nil, nil, nil, []any{0}},
		nil, 1, []any{bootstrap.Language},
	})
	if err != nil {
		return "", err
	}
	outer, err := pageJSON([]any{nil, inner})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.pageURL(pathProcessFile), strings.NewReader(protocolForm(outer, bootstrap.SNlM0e)))
	if err != nil {
		return "", err
	}
	query := req.URL.Query()
	query.Set("bl", bootstrap.BL)
	query.Set("f.sid", bootstrap.FSID)
	query.Set("hl", bootstrap.Language)
	query.Set("_reqid", fmt.Sprintf("%d", c.nextReqID()))
	query.Set("rt", "c")
	req.URL.RawQuery = query.Encode()
	c.applyXHRHeaders(req)
	req.Header[http.HeaderOrderKey] = processFileHeaderOrder
	header, err := json.Marshal([]any{
		1, nil, nil, nil, model.Hash, nil, nil, nil, c.features(), nil, nil, nil, nil, nil, model.Mode, int(level), c.clientID,
		[]string{bootstrap.BL}, nil, requestTiming(time.Since(started), time.Now()),
	})
	if err != nil {
		return "", err
	}
	req.Header.Set("x-goog-ext-525001261-jspb", string(header))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", transportProtocolError("process file", err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		resp.Body.Close()
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", responseStatusError(resp, "process file")
	}
	fileID := ""
	err = scanProtocolRecords(resp.Body, func(record []any) error {
		if tag, _ := stringAt(record, 0); tag != "wrb.fr" {
			return nil
		}
		encoded, _ := stringAt(record, 2)
		if encoded == "" {
			return rpcStatusError("process file", record)
		}
		var payload []any
		if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
			return fmt.Errorf("decode process file payload: %w", err)
		}
		ids, _ := arrayAt(payload, 3)
		fileID, _ = stringAt(ids, 0)
		return nil
	})
	if err != nil {
		return "", err
	}
	if fileID == "" {
		return "", retryableProtocolError("process file response has no file id", nil)
	}
	return fileID, nil
}
