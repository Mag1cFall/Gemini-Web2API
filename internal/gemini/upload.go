package gemini

import (
	"fmt"
	"io"
	"strings"

	http "github.com/bogdanfinn/fhttp"
)

const (
	EndpointUpload = "https://content-push.googleapis.com/upload"
	UploadPushID   = "feeds/mcudyrk2a4khkz"
)

func (c *Client) UploadFile(data []byte, filename string) (string, error) {
	req, err := http.NewRequest(http.MethodPost, EndpointUpload, strings.NewReader(string(data)))
	if err != nil {
		return "", err
	}

	req.Header.Set("Push-ID", UploadPushID)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Origin", "https://gemini.google.com")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload failed: %v", err)
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
