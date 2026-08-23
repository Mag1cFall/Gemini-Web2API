package gemini

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"

	http "github.com/bogdanfinn/fhttp"
)

const (
	endpointUpload = "https://content-push.googleapis.com/upload"
	uploadPushID   = "feeds/mcudyrk2a4khkz"
)

// UploadFile 上传多模态附件并返回网页协议文件标识
func (c *Client) UploadFile(ctx context.Context, data []byte, filename string) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %v", err)
	}

	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("failed to write file data: %v", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointUpload, &buf)
	if err != nil {
		return "", err
	}

	req.Header.Set("Push-ID", uploadPushID)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", c.fingerprint.UserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", c.fingerprint.Language)
	req.Header.Set("Origin", "https://gemini.google.com")
	c.applyClientHints(req)
	c.applyHeaderOrder(req, false)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload failed: %v", err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		resp.Body.Close()
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("upload failed with status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}
