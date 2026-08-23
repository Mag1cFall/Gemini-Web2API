package gemini

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"

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
		req.Header.Set("User-Agent", c.fingerprint.UserAgent)
		req.Header.Set("Accept-Language", c.fingerprint.Language)
		req.Header.Set("Referer", "https://gemini.google.com/")
		if generatedMedia {
			req.Header.Set("Accept", "*/*")
			req.Header.Set("Origin", "https://gemini.google.com")
			req.Header.Set("Sec-Fetch-Dest", "empty")
			req.Header.Set("Sec-Fetch-Mode", "cors")
			if strings.HasSuffix(currentURL.Hostname(), ".usercontent.google.com") {
				req.Header.Set("Sec-Fetch-Site", "same-site")
			} else {
				req.Header.Set("Sec-Fetch-Site", "cross-site")
				req.Header.Set("Sec-Fetch-Storage-Access", "none")
			}
		} else {
			req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
		}
		c.applyClientHints(req)
		c.applyHeaderOrder(req, false)
		if generatedMedia {
			c.applyGeneratedMediaCookies(req)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch media: %w", err)
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
			return nil, httpStatusError(resp.StatusCode, "media fetch")
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read media: %w", err)
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

// applyGeneratedMediaCookies 补齐 Gemini 页面同站中继发送的 Google Cookie
func (c *Client) applyGeneratedMediaCookies(req *http.Request) {
	if req.URL.Hostname() != "work.fife.usercontent.google.com" || !strings.HasPrefix(req.URL.Path, "/rd-gg-dl/") {
		return
	}
	c.cookieMu.Lock()
	defer c.cookieMu.Unlock()
	for _, cookie := range c.cookies {
		if cookie.Domain == ".google.com" && cookie.Path == "/" {
			req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
		}
	}
}

// prepareGeneratedMediaURL 对齐 Gemini 页面生成图预览请求
func prepareGeneratedMediaURL(mediaURL *url.URL) bool {
	if mediaURL.Scheme != "https" || mediaURL.Hostname() != "lh3.googleusercontent.com" || !strings.HasPrefix(mediaURL.Path, "/gg-dl/") {
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
	allowedHost := host == "lh3.googleusercontent.com" || strings.HasSuffix(host, ".usercontent.google.com")
	if nextURL.Scheme != "https" || !allowedHost || !strings.HasPrefix(nextURL.Path, "/rd-gg-dl/") {
		return nil, fmt.Errorf("generated media relay returned an unexpected URL")
	}
	return nextURL, nil
}
