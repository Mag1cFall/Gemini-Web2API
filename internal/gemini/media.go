package gemini

import (
	"context"
	"fmt"
	"io"
	"math"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"

	http "github.com/bogdanfinn/fhttp"
)

const maxMediaRedirects = 5

// FetchMedia 使用账号固定身份和 Cookie 下载媒体内容
func (c *Client) FetchMedia(ctx context.Context, mediaURL string) ([]byte, error) {
	currentURL, err := url.Parse(mediaURL)
	if err != nil {
		return nil, err
	}
	generatedMedia := prepareGeneratedMediaURL(currentURL)
	for redirects := 0; redirects <= maxMediaRedirects; redirects++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL.String(), nil)
		if err != nil {
			return nil, err
		}
		if generatedMedia {
			applyChromeHeaders(req, c.language, true)
			req.Header.Set("accept", "*/*")
			req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
			req.Header.Set("origin", "https://gemini.google.com")
			req.Header.Set("referer", "https://gemini.google.com/")
			req.Header.Set("sec-fetch-mode", "cors")
			req.Header.Set("sec-fetch-dest", "empty")
			req.Header.Set("priority", "u=1, i")
			if strings.HasSuffix(currentURL.Hostname(), ".usercontent.google.com") {
				req.Header.Set("sec-fetch-site", "same-site")
			} else {
				req.Header.Set("sec-fetch-site", "cross-site")
				req.Header.Set("sec-fetch-storage-access", "active")
			}
			req.Header[http.HeaderOrderKey] = generatedMediaHeaderOrder
		} else {
			applyChromeHeaders(req, c.language, false)
			req.Header.Set("Referer", "https://gemini.google.com/")
			req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
			req.Header[http.HeaderOrderKey] = xhrHeaderOrder
		}
		req.Header[http.PHeaderOrderKey] = pseudoHeaderOrder

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, transportProtocolError("media fetch", err)
		}
		if err := c.absorbResponseCookies(req.URL, resp); err != nil {
			resp.Body.Close()
			return nil, err
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			resp.Body.Close()
			if location == "" {
				return nil, fmt.Errorf("media redirect has no location")
			}
			nextURL, err := currentURL.Parse(location)
			if err != nil {
				return nil, err
			}
			currentURL = nextURL
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, responseStatusError(resp, "media fetch")
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, retryableProtocolError("read media response", err)
		}
		contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if generatedMedia && contentType == "text/plain" {
			nextURL, err := parseGeneratedMediaRelay(body)
			if err != nil {
				return nil, err
			}
			currentURL = nextURL
			continue
		}
		return body, nil
	}
	return nil, fmt.Errorf("media redirect limit exceeded")
}

// generatedMediaHeaderOrder 是 Chrome 稳定版页面 fetch 生成图下载链各跳的请求头顺序
var generatedMediaHeaderOrder = []string{
	"sec-ch-ua-full-version-list", "sec-ch-ua-platform", "sec-ch-ua", "sec-ch-ua-bitness",
	"sec-ch-ua-model", "sec-ch-ua-mobile", "sec-ch-ua-form-factors", "sec-ch-ua-wow64",
	"sec-ch-ua-arch", "sec-ch-ua-full-version", "user-agent", "sec-ch-ua-platform-version",
	"accept", "origin", "x-browser-channel", "x-browser-year", "x-browser-validation", "x-browser-copyright",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "sec-fetch-storage-access", "referer",
	"accept-encoding", "accept-language", "cookie", "priority",
}

// prepareGeneratedMediaURL 对齐 Gemini 页面生成媒体的 fetch 请求，生成图预览链补 =s1024-rj 尺寸
func prepareGeneratedMediaURL(mediaURL *url.URL) bool {
	if mediaURL.Scheme != "https" {
		return false
	}
	if mediaURL.Hostname() == "contribution.usercontent.google.com" {
		return true
	}
	if mediaURL.Hostname() != "lh3.googleusercontent.com" || !strings.HasPrefix(mediaURL.Path, "/gg-dl/") && !strings.HasPrefix(mediaURL.Path, "/gg/") {
		return false
	}
	if !strings.Contains(mediaURL.Path[strings.LastIndex(mediaURL.Path, "/")+1:], "=") {
		mediaURL.Path += "=s1024-rj"
	}
	query := mediaURL.Query()
	query.Set("alr", "yes")
	mediaURL.RawQuery = query.Encode()
	return true
}

// parseGeneratedMediaRelay 解析生成图下载链返回的下一跳
func parseGeneratedMediaRelay(body []byte) (*url.URL, error) {
	nextURL, err := url.Parse(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse generated media relay: %w", err)
	}
	host := nextURL.Hostname()
	allowedHost := host == "lh3.googleusercontent.com" || host == "lh3.google.com" || strings.HasSuffix(host, ".usercontent.google.com")
	if nextURL.Scheme != "https" || !allowedHost || !strings.HasPrefix(nextURL.Path, "/rd-gg-dl/") && !strings.HasPrefix(nextURL.Path, "/rd-gg/") {
		return nil, fmt.Errorf("generated media relay returned an unexpected URL")
	}
	return nextURL, nil
}

// imageDownload 是官网“下载原图”请求 c8o8Fe 所需的生成图条目、会话标识与页面状态
type imageDownload struct {
	entry         []any
	cid           string
	rid           string
	rcid          string
	index         int
	mode          int
	level         ThinkingLevel
	temporaryChat bool
}

// FetchMediaContent 下载媒体内容，生成图片按官网“下载原图”取推荐尺寸原图
func (c *Client) FetchMediaContent(ctx context.Context, media Media) ([]byte, error) {
	if media.download == nil {
		return c.FetchMedia(ctx, media.URL)
	}
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return nil, err
	}
	payload, err := media.download.payload()
	if err != nil {
		return nil, err
	}
	result, err := c.batchExecute(ctx, bootstrap, batchRequest{
		rpcID: "c8o8Fe", payload: payload, sourcePath: "/app", language: bootstrap.Language, extension: "[0]",
		mode: media.download.mode, level: media.download.level, conversation: true, temporaryChat: media.download.temporaryChat,
	})
	if err != nil {
		return nil, err
	}
	downloadURL, ok := stringAt(result, 0)
	if !ok || downloadURL == "" {
		return nil, fmt.Errorf("c8o8Fe returned no download URL")
	}
	return c.FetchMedia(ctx, downloadURL+"=d-I")
}

// payload 按页面逐字段重建图片条目：令牌、占位链接与变体号、类别与名称与标志与生成器与思维签名、artifact ID 与图片 ID，末尾为网页来源、Temporary Chat 状态与推荐尺寸
func (d imageDownload) payload() (string, error) {
	token, _ := stringPath(d.entry, 0, 3, 5)
	placeholder, _ := stringPath(d.entry, 1, 0)
	variant, _ := intPath(d.entry, 1, 1)
	source, _ := arrayAt(d.entry, 3)
	details := make([]any, 23)
	details[0], details[1], details[16] = 0, "", 0
	if category, ok := intAt(source, 0); ok {
		details[0] = category
	}
	if name, ok := stringAt(source, 1); ok {
		details[1] = name
	}
	if flag, _ := valuePath(source, 16); flag == true {
		details[16] = 1
	}
	details[18], _ = valuePath(source, 18)
	signature, _ := valuePath(source, 22)
	details[22] = jspbBooleans(signature)
	for len(details) > 0 && details[len(details)-1] == nil {
		details = details[:len(details)-1]
	}
	artifactID, _ := stringAt(d.entry, 8)
	imageID, _ := stringAt(d.entry, 9)
	if imageID == "" {
		imageID = generatedImageID(d.rcid, variant, d.index)
	}
	image := []any{
		[]any{nil, nil, nil, []any{nil, nil, nil, nil, nil, token}}, []any{placeholder, variant}, nil, details,
		nil, nil, nil, nil, artifactID, imageID,
	}
	temporaryChat := 0
	if d.temporaryChat {
		temporaryChat = 1
	}
	return pageJSON([]any{image, []any{d.rid, d.rcid, d.cid, nil, imageID}, 1, temporaryChat, 1})
}

// jspbBooleans 返回把布尔值序列化为 1、0 的副本，与页面 JSPB 序列化一致
func jspbBooleans(value any) any {
	switch typed := value.(type) {
	case bool:
		if typed {
			return 1
		}
		return 0
	case []any:
		copied := make([]any, len(typed))
		for index, item := range typed {
			copied[index] = jspbBooleans(item)
		}
		return copied
	default:
		return value
	}
}

// generatedImageID 按页面规则由 RCID、变体号与图片序号算出 16 位图片 ID，也用于下载文件名
func generatedImageID(rcid string, variant, index int) string {
	var hash int32
	for _, unit := range utf16.Encode([]rune(fmt.Sprintf("%s_%d_%d", rcid, variant, index))) {
		hash = hash<<5 - hash + int32(unit)
	}
	encoded := strconv.FormatInt(int64(math.Abs(float64(hash))), 36)
	encoded = encoded[max(0, len(encoded)-12):]
	return strings.Repeat(encoded, 8)[:16]
}
