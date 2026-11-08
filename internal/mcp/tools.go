package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
)

// arguments 是 tools/call 的参数
type arguments map[string]json.RawMessage

// str 返回字符串参数去掉首尾空白后的值
func (a arguments) str(name string) string {
	var value string
	_ = json.Unmarshal(a[name], &value)
	return strings.TrimSpace(value)
}

// flag 返回布尔参数
func (a arguments) flag(name string) bool {
	var value bool
	_ = json.Unmarshal(a[name], &value)
	return value
}

// list 返回字符串数组参数中的非空项
func (a arguments) list(name string) []string {
	var values []string
	_ = json.Unmarshal(a[name], &values)
	result := values[:0]
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

// tool 是一个 MCP 工具：selectsModel 为真时接受参数 model，可选目录中 feature 功能的模型，properties 与 required 为其余参数
type tool struct {
	name         string
	title        string
	description  string
	selectsModel bool
	feature      gemini.Feature
	properties   gin.H
	required     []string
	readOnly     bool
	call         func(s *server, c *gin.Context, args arguments) gin.H
}

// toolList 是 tools/list 公开的全部工具
var toolList = []tool{
	{
		name: "web_search", title: "Web search", readOnly: true, selectsModel: true,
		description: "Search the web with Google Search through Gemini and return a synthesized answer with its source links. Use it for current events, facts and anything that needs up-to-date information.",
		properties:  gin.H{"query": gin.H{"type": "string", "description": "The question or search query."}},
		required:    []string{"query"},
		call:        webSearch,
	},
	{
		name: "generate_image", title: "Generate image", selectsModel: true, feature: gemini.FeatureImage,
		description: "Generate or edit an image with Gemini. Pass reference images to edit, combine or restyle them. The image is attached to the result; do not describe or repeat it.",
		properties: gin.H{
			"prompt":       gin.H{"type": "string", "description": "What to draw, or how to change the reference images."},
			"image_urls":   gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "Reference images as data: URLs or public http(s) URLs."},
			"aspect_ratio": gin.H{"type": "string", "description": "Aspect ratio such as 1:1, 16:9 or 9:16; the closest ratio offered by Gemini is used."},
		},
		required: []string{"prompt"},
		call:     generateImage,
	},
	{
		name: "generate_music", title: "Generate music", selectsModel: true, feature: gemini.FeatureMusic,
		description: "Generate a short music track with Gemini. The audio is attached to the result.",
		properties:  gin.H{"prompt": gin.H{"type": "string", "description": "Genre, mood, instruments, tempo, lyrics and so on."}},
		required:    []string{"prompt"},
		call:        generateMusic,
	},
	{
		name: "create_video_task", title: "Create video", selectsModel: true, feature: gemini.FeatureVideo,
		description: "Start generating a video with Gemini. Generation takes one to several minutes; poll get_video_status with the returned id.",
		properties: gin.H{
			"prompt":    gin.H{"type": "string", "description": "What happens in the video."},
			"size":      gin.H{"type": "string", "description": "Frame size such as 1280x720 or 720x1280; the closest aspect ratio offered by Gemini is used."},
			"image_url": gin.H{"type": "string", "description": "Optional first frame as a data: URL or public http(s) URL."},
		},
		required: []string{"prompt"},
		call:     createVideo,
	},
	{
		name: "get_video_status", title: "Get video status", readOnly: true,
		description: "Check a video task created by create_video_task. A completed video includes its thumbnail; set include_video to attach the MP4 (several MB).",
		properties: gin.H{
			"id":            gin.H{"type": "string", "description": "The id returned by create_video_task."},
			"include_video": gin.H{"type": "boolean", "description": "Attach the MP4 when the video is completed."},
		},
		required: []string{"id"},
		call:     videoStatus,
	},
	{
		name: "start_deep_research", title: "Start Deep Research", selectsModel: true, feature: gemini.FeatureDeepResearch,
		description: "Start a Gemini Deep Research report. It browses many sources and takes several minutes or longer; poll get_deep_research with the returned id.",
		properties:  gin.H{"query": gin.H{"type": "string", "description": "The research topic or question."}},
		required:    []string{"query"},
		call:        startResearch,
	},
	{
		name: "get_deep_research", title: "Get Deep Research", readOnly: true,
		description: "Check a report started by start_deep_research and return the report when it is completed.",
		properties:  gin.H{"id": gin.H{"type": "string", "description": "The id returned by start_deep_research."}},
		required:    []string{"id"},
		call:        researchStatus,
	},
}

// toolByName 返回同名工具
func toolByName(name string) (tool, bool) {
	for _, item := range toolList {
		if item.name == name {
			return item, true
		}
	}
	return tool{}, false
}

// tools 返回 tools/list 的工具定义，带模型功能的工具在参数 model 中列出目录里该功能的模型
func (s *server) tools() []gin.H {
	catalog := s.catalog()
	definitions := make([]gin.H, 0, len(toolList))
	for _, item := range toolList {
		properties := gin.H{}
		for name, value := range item.properties {
			properties[name] = value
		}
		if item.selectsModel {
			var models []string
			for _, model := range catalog {
				if model.Feature == item.feature && model.AliasOf == "" && model.Gem == "" {
					models = append(models, model.ID)
				}
			}
			model := gin.H{"type": "string", "description": "Model to use; defaults to the catalog's model for this tool."}
			if len(models) > 0 {
				model["enum"] = models
			}
			properties["model"] = model
		}
		definitions = append(definitions, gin.H{
			"name": item.name, "title": item.title, "description": item.description,
			"inputSchema": gin.H{"type": "object", "properties": properties, "required": item.required},
			"annotations": gin.H{"title": item.title, "readOnlyHint": item.readOnly, "openWorldHint": true},
		})
	}
	return definitions
}

// model 返回请求的模型，未指定时返回目录中该功能的模型，功能为 0 时返回目录的默认模型
func (s *server) model(args arguments, feature gemini.Feature) (string, error) {
	if model := args.str("model"); model != "" {
		return model, nil
	}
	for _, model := range s.catalog() {
		if model.Feature == feature && model.AliasOf == "" && (feature != 0 || model.Default) {
			return model.ID, nil
		}
	}
	return "", fmt.Errorf("no ready account offers this tool")
}

// toolError 返回 isError 的工具结果
func toolError(format string, values ...any) gin.H {
	return gin.H{"content": []gin.H{{"type": "text", "text": fmt.Sprintf(format, values...)}}, "isError": true}
}

// textContent 返回文本内容块
func textContent(text string) gin.H {
	return gin.H{"type": "text", "text": text}
}

// mediaContent 按 MIME 类型返回 image、audio 内容块，其余类型返回内嵌资源
func mediaContent(mimeType string, data string, uri string) gin.H {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return gin.H{"type": "image", "data": data, "mimeType": mimeType}
	case strings.HasPrefix(mimeType, "audio/"):
		return gin.H{"type": "audio", "data": data, "mimeType": mimeType}
	}
	return gin.H{"type": "resource", "resource": gin.H{"uri": uri, "mimeType": mimeType, "blob": data}}
}

// geminiPart 是 Gemini generateContent 响应中的内容块
type geminiPart struct {
	Text       string `json:"text"`
	Thought    bool   `json:"thought"`
	InlineData *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
}

// geminiResult 是 Gemini generateContent 响应
type geminiResult struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		GroundingMetadata struct {
			WebSearchQueries []string `json:"webSearchQueries"`
			GroundingChunks  []struct {
				Web struct {
					URI   string `json:"uri"`
					Title string `json:"title"`
				} `json:"web"`
			} `json:"groundingChunks"`
		} `json:"groundingMetadata"`
	} `json:"candidates"`
}

// generate 以 Gemini generateContent 调用模型，parts 为用户轮内容
func (s *server) generate(c *gin.Context, model string, parts []gin.H, extra gin.H) (geminiResult, error) {
	body := gin.H{"contents": []gin.H{{"role": "user", "parts": parts}}}
	for key, value := range extra {
		body[key] = value
	}
	var result geminiResult
	err := s.call(c, http.MethodPost, "/v1beta/models/"+url.PathEscape(model)+":generateContent", body, &result)
	if err == nil && len(result.Candidates) == 0 {
		err = fmt.Errorf("Gemini returned no candidate")
	}
	return result, err
}

func webSearch(s *server, c *gin.Context, args arguments) gin.H {
	query := args.str("query")
	if query == "" {
		return toolError("query is required")
	}
	model, err := s.model(args, 0)
	if err != nil {
		return toolError("%v", err)
	}
	result, err := s.generate(c, model, []gin.H{{"text": query}}, gin.H{"tools": []gin.H{{"googleSearch": gin.H{}}}})
	if err != nil {
		return toolError("web search failed: %v", err)
	}
	candidate := result.Candidates[0]
	var answer strings.Builder
	for _, part := range candidate.Content.Parts {
		if !part.Thought {
			answer.WriteString(part.Text)
		}
	}
	text := strings.TrimSpace(answer.String())
	sources := []gin.H{}
	if chunks := candidate.GroundingMetadata.GroundingChunks; len(chunks) > 0 {
		text += "\n\nSources:"
		for index, chunk := range chunks {
			link, _, _ := strings.Cut(chunk.Web.URI, "#:~:text=")
			title := chunk.Web.Title
			if title == "" {
				title = link
			}
			text += fmt.Sprintf("\n%d. %s — %s", index+1, title, link)
			sources = append(sources, gin.H{"title": chunk.Web.Title, "url": link})
		}
	}
	queries := candidate.GroundingMetadata.WebSearchQueries
	if queries == nil {
		queries = []string{}
	}
	return gin.H{
		"content":           []gin.H{textContent(text)},
		"structuredContent": gin.H{"answer": strings.TrimSpace(answer.String()), "sources": sources, "queries": queries},
	}
}

// imagePart 把 data: URL 转为 inlineData，http(s) 地址转为 fileData
func imagePart(value string) (gin.H, error) {
	if rest, ok := strings.CutPrefix(value, "data:"); ok {
		header, data, found := strings.Cut(rest, ",")
		mimeType, isBase64 := strings.CutSuffix(header, ";base64")
		if !found || !isBase64 {
			return nil, fmt.Errorf("image URL must be a base64 data: URL or an http(s) URL")
		}
		return gin.H{"inlineData": gin.H{"mimeType": mimeType, "data": data}}, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("image URL must be a base64 data: URL or an http(s) URL: %s", value)
	}
	return gin.H{"fileData": gin.H{"fileUri": value}}, nil
}

// mediaResult 返回生成结果中的文本与媒体内容块
func mediaResult(result geminiResult, uri string) gin.H {
	var content []gin.H
	media := 0
	for _, part := range result.Candidates[0].Content.Parts {
		switch {
		case part.InlineData != nil:
			content = append(content, mediaContent(part.InlineData.MimeType, part.InlineData.Data, fmt.Sprintf("%s/%d", uri, media)))
			media++
		case !part.Thought && strings.TrimSpace(part.Text) != "":
			content = append(content, textContent(part.Text))
		}
	}
	if media == 0 {
		return gin.H{"content": append(content, textContent("Gemini returned no media for this request.")), "isError": true}
	}
	return gin.H{"content": content}
}

func generateImage(s *server, c *gin.Context, args arguments) gin.H {
	prompt := args.str("prompt")
	if prompt == "" {
		return toolError("prompt is required")
	}
	model, err := s.model(args, gemini.FeatureImage)
	if err != nil {
		return toolError("%v", err)
	}
	parts := []gin.H{{"text": prompt}}
	for _, value := range args.list("image_urls") {
		part, err := imagePart(value)
		if err != nil {
			return toolError("%v", err)
		}
		parts = append(parts, part)
	}
	config := gin.H{"responseModalities": []string{"TEXT", "IMAGE"}}
	if ratio := args.str("aspect_ratio"); ratio != "" {
		config["imageConfig"] = gin.H{"aspectRatio": ratio}
	}
	result, err := s.generate(c, model, parts, gin.H{"generationConfig": config})
	if err != nil {
		return toolError("image generation failed: %v", err)
	}
	return mediaResult(result, "gemini-web2api://image")
}

func generateMusic(s *server, c *gin.Context, args arguments) gin.H {
	prompt := args.str("prompt")
	if prompt == "" {
		return toolError("prompt is required")
	}
	model, err := s.model(args, gemini.FeatureMusic)
	if err != nil {
		return toolError("%v", err)
	}
	result, err := s.generate(c, model, []gin.H{{"text": prompt}}, nil)
	if err != nil {
		return toolError("music generation failed: %v", err)
	}
	return mediaResult(result, "gemini-web2api://music")
}

func createVideo(s *server, c *gin.Context, args arguments) gin.H {
	prompt := args.str("prompt")
	if prompt == "" {
		return toolError("prompt is required")
	}
	model, err := s.model(args, gemini.FeatureVideo)
	if err != nil {
		return toolError("%v", err)
	}
	body := gin.H{"model": model, "prompt": prompt}
	if size := args.str("size"); size != "" {
		body["size"] = size
	}
	if image := args.str("image_url"); image != "" {
		body["input_reference"] = gin.H{"image_url": image}
	}
	var video map[string]any
	if err := s.call(c, http.MethodPost, "/v1/videos", body, &video); err != nil {
		return toolError("video task failed: %v", err)
	}
	return objectResult(video)
}

func videoStatus(s *server, c *gin.Context, args arguments) gin.H {
	id := args.str("id")
	if id == "" {
		return toolError("id is required")
	}
	var video map[string]any
	if err := s.call(c, http.MethodGet, "/v1/videos/"+url.PathEscape(id), nil, &video); err != nil {
		return toolError("video status failed: %v", err)
	}
	result := objectResult(video)
	if video["status"] != "completed" {
		return result
	}
	content := result["content"].([]gin.H)
	base := "/v1/videos/" + url.PathEscape(id) + "/content"
	if data, mimeType, err := s.fetch(c, base+"?variant=thumbnail"); err == nil {
		content = append(content, mediaContent(mimeType, data, ""))
	}
	if args.flag("include_video") {
		data, mimeType, err := s.fetch(c, base)
		if err != nil {
			return toolError("video download failed: %v", err)
		}
		content = append(content, mediaContent(mimeType, data, publicURL(c, base)))
	}
	result["content"] = content
	return result
}

func startResearch(s *server, c *gin.Context, args arguments) gin.H {
	query := args.str("query")
	if query == "" {
		return toolError("query is required")
	}
	model, err := s.model(args, gemini.FeatureDeepResearch)
	if err != nil {
		return toolError("%v", err)
	}
	var research map[string]any
	if err := s.call(c, http.MethodPost, "/v1/responses", gin.H{"model": model, "input": query, "background": true, "store": true}, &research); err != nil {
		return toolError("Deep Research failed to start: %v", err)
	}
	return researchResult(research)
}

func researchStatus(s *server, c *gin.Context, args arguments) gin.H {
	id := args.str("id")
	if id == "" {
		return toolError("id is required")
	}
	var research map[string]any
	if err := s.call(c, http.MethodGet, "/v1/responses/"+url.PathEscape(id), nil, &research); err != nil {
		return toolError("Deep Research status failed: %v", err)
	}
	return researchResult(research)
}

// researchResult 返回 Responses 对象的标识、状态与已完成报告的正文
func researchResult(research map[string]any) gin.H {
	summary := gin.H{"id": research["id"], "status": research["status"], "error": research["error"]}
	content := []gin.H{}
	if research["status"] == "completed" {
		output, _ := research["output"].([]any)
		for _, item := range output {
			message, _ := item.(map[string]any)
			parts, _ := message["content"].([]any)
			for _, part := range parts {
				if text, _ := part.(map[string]any)["text"].(string); text != "" {
					content = append(content, textContent(text))
				}
			}
		}
	}
	encoded, _ := json.Marshal(summary)
	return gin.H{"content": append([]gin.H{textContent(string(encoded))}, content...), "structuredContent": summary}
}

// objectResult 返回以 JSON 文本与结构化内容表示的协议对象
func objectResult(object map[string]any) gin.H {
	encoded, _ := json.Marshal(object)
	return gin.H{"content": []gin.H{textContent(string(encoded))}, "structuredContent": object}
}

// publicURL 返回本服务对外地址下的路径
func publicURL(c *gin.Context, path string) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host + path
}

// fetch 读取公开 API 返回的二进制内容，返回 base64 内容与 MIME 类型
func (s *server) fetch(c *gin.Context, path string) (string, string, error) {
	status, header, body := s.do(c, http.MethodGet, path, nil)
	if status != http.StatusOK {
		return "", "", apiError(status, body)
	}
	mimeType, _, _ := strings.Cut(header.Get("Content-Type"), ";")
	return base64.StdEncoding.EncodeToString(body), mimeType, nil
}
