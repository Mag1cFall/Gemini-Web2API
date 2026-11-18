package adapter

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"
)

// urlContextLimit 是一次请求读取的网页数上限，与 Gemini API urlContext 每个请求最多 20 个地址一致
const urlContextLimit = 20

// urlContextKey 是请求上下文中已读取网页的键，换号重试时沿用同一批结果
const urlContextKey = "url_context_pages"

// pageURLPattern 匹配用户文字中的 http 与 https 地址
var pageURLPattern = regexp.MustCompile(`https?://[A-Za-z0-9\-._~:/?#@!$&*+,;=%]+`)

// fetchedPage 是服务为 Anthropic web_fetch 与 Gemini urlContext 读取的一个网页
type fetchedPage struct {
	URL string
	// Title 是 HTML 页面的标题
	Title string
	// Text 是页面正文的文字，File 非空时为空
	Text string
	// File 是 PDF 等非文字内容，作为附件发给模型
	File *gemini.FileData
	// Err 是读取失败的原因
	Err         error
	RetrievedAt time.Time
}

// pageURLs 按出现顺序返回文字中的不重复地址，去掉末尾的标点
func pageURLs(text string) []string {
	var urls []string
	seen := map[string]bool{}
	for _, match := range pageURLPattern.FindAllString(text, -1) {
		match = strings.TrimRight(match, ".,;:!?。，；：！？")
		if !seen[match] {
			seen[match] = true
			urls = append(urls, match)
		}
	}
	return urls
}

// errURLNotAllowed 表示地址的域名不在 web_fetch 工具的允许名单中或在屏蔽名单中
var errURLNotAllowed = errors.New("web_fetch 工具的域名设置不允许读取这个地址")

// urlContextPages 读取最后一条用户消息中的地址，同一请求只读取一次，最多读取 limit 个；allowed 与 blocked 不允许的地址不读取
func urlContextPages(c *gin.Context, client *gemini.Client, text string, limit int, allowed, blocked []string) []fetchedPage {
	if cached, ok := c.Get(urlContextKey); ok {
		return cached.([]fetchedPage)
	}
	urls := pageURLs(text)
	if len(urls) > limit {
		urls = urls[:limit]
	}
	pages := make([]fetchedPage, len(urls))
	for index, address := range urls {
		if !domainsPermit(address, allowed, blocked) {
			pages[index] = fetchedPage{URL: address, Err: errURLNotAllowed, RetrievedAt: time.Now().UTC()}
			continue
		}
		pages[index] = fetchPage(c.Request.Context(), client, address)
	}
	c.Set(urlContextKey, pages)
	return pages
}

// requestPages 返回本次请求已读取的网页
func requestPages(c *gin.Context) []fetchedPage {
	pages, _ := c.Get(urlContextKey)
	result, _ := pages.([]fetchedPage)
	return result
}

// fetchPage 以账号的浏览器身份与出口读取网页：HTML 取标题与正文文字，其他文字原样保留，PDF、图片等作为附件
func fetchPage(ctx context.Context, client *gemini.Client, address string) fetchedPage {
	page := fetchedPage{URL: address, RetrievedAt: time.Now().UTC()}
	data, err := fetchRemoteMedia(ctx, client, address)
	if err != nil {
		page.Err = err
		return page
	}
	mediaType := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(mediaType, "text/html"):
		page.Title, page.Text = htmlText(string(data))
	case strings.HasPrefix(mediaType, "text/"):
		page.Text = string(data)
	default:
		mediaType, _, _ = strings.Cut(mediaType, ";")
		page.File = &gemini.FileData{Data: data, FileName: fmt.Sprintf("page_%d%s", time.Now().UnixNano(), mimeTypeToExt(mediaType)), MimeType: mediaType}
	}
	return page
}

// htmlText 返回 HTML 的标题与正文文字，去掉脚本、样式与不显示的元素，块级元素之间换行
func htmlText(source string) (string, string) {
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", source
	}
	var title string
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "noscript", "template", "svg", "head":
				if node.Data == "head" {
					for child := node.FirstChild; child != nil; child = child.NextSibling {
						if child.Type == html.ElementNode && child.Data == "title" && child.FirstChild != nil {
							title = strings.TrimSpace(child.FirstChild.Data)
						}
					}
				}
				return
			case "br", "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "pre", "section", "article", "header", "footer", "table", "ul", "ol", "blockquote":
				builder.WriteByte('\n')
			}
		}
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	lines := strings.Split(builder.String(), "\n")
	kept := lines[:0]
	for _, line := range lines {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			kept = append(kept, line)
		}
	}
	return title, strings.Join(kept, "\n")
}

// pagePrompt 把读取的网页写成提示词段，返回提示词段与作为附件发送的内容
func pagePrompt(pages []fetchedPage) (string, []gemini.FileData) {
	if len(pages) == 0 {
		return "", nil
	}
	var builder strings.Builder
	var files []gemini.FileData
	builder.WriteString("服务已读取用户给出的网页地址，读取结果如下：")
	for _, page := range pages {
		switch {
		case page.Err != nil:
			fmt.Fprintf(&builder, "\n[Page:%s]\n读取失败：%s\n[/Page]", page.URL, page.Err)
		case page.File != nil:
			fmt.Fprintf(&builder, "\n[Page:%s]\n内容为附件 %s\n[/Page]", page.URL, page.File.FileName)
			files = append(files, *page.File)
		default:
			fence := strings.Repeat("`", max(3, longestBacktickRun(page.Text)+1))
			fmt.Fprintf(&builder, "\n[Page:%s]\n", page.URL)
			if page.Title != "" {
				fmt.Fprintf(&builder, "标题：%s\n", page.Title)
			}
			fmt.Fprintf(&builder, "%s\n%s\n%s\n[/Page]", fence, page.Text, fence)
		}
	}
	return builder.String(), files
}

// longestBacktickRun 返回文字中最长的连续反引号数
func longestBacktickRun(text string) int {
	longest, current := 0, 0
	for _, char := range text {
		if char == '`' {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return longest
}

// anthropicFetchBlocks 返回读取网页对应的 server_tool_use 与 web_fetch_tool_result 内容块
func anthropicFetchBlocks(responseID string, pages []fetchedPage) []gin.H {
	blocks := make([]gin.H, 0, 2*len(pages))
	for index, page := range pages {
		id := fmt.Sprintf("srvtoolu_%s_fetch_%d", strings.TrimPrefix(responseID, "msg_"), index)
		var content gin.H
		switch {
		case errors.Is(page.Err, errURLNotAllowed):
			content = gin.H{"type": "web_fetch_tool_result_error", "error_code": "url_not_allowed"}
		case page.Err != nil:
			content = gin.H{"type": "web_fetch_tool_result_error", "error_code": "url_not_accessible"}
		case page.File != nil:
			source := gin.H{"type": "base64", "media_type": page.File.MimeType, "data": base64.StdEncoding.EncodeToString(page.File.Data)}
			content = gin.H{"type": "web_fetch_result", "url": page.URL, "content": gin.H{"type": "document", "source": source, "title": page.Title}, "retrieved_at": page.RetrievedAt.Format(time.RFC3339)}
		default:
			source := gin.H{"type": "text", "media_type": "text/plain", "data": page.Text}
			content = gin.H{"type": "web_fetch_result", "url": page.URL, "content": gin.H{"type": "document", "source": source, "title": page.Title}, "retrieved_at": page.RetrievedAt.Format(time.RFC3339)}
		}
		blocks = append(blocks,
			gin.H{"type": "server_tool_use", "id": id, "name": "web_fetch", "input": gin.H{"url": page.URL}},
			gin.H{"type": "web_fetch_tool_result", "tool_use_id": id, "content": content},
		)
	}
	return blocks
}

// geminiURLContextMetadata 返回读取网页对应的 urlContextMetadata
func geminiURLContextMetadata(pages []fetchedPage) gin.H {
	metadata := make([]gin.H, 0, len(pages))
	for _, page := range pages {
		status := "URL_RETRIEVAL_STATUS_SUCCESS"
		if page.Err != nil {
			status = "URL_RETRIEVAL_STATUS_ERROR"
		}
		metadata = append(metadata, gin.H{"retrievedUrl": page.URL, "urlRetrievalStatus": status})
	}
	return gin.H{"urlMetadata": metadata}
}
