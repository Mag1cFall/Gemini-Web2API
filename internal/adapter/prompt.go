package adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// writeTextSegment 把同一消息的一个文本段写入提示词，与前文相接处两侧都不是空白时以换行分隔
func writeTextSegment(builder *strings.Builder, text string) {
	if text == "" {
		return
	}
	if current := builder.String(); current != "" {
		last, _ := utf8.DecodeLastRuneInString(current)
		first, _ := utf8.DecodeRuneInString(text)
		if !unicode.IsSpace(last) && !unicode.IsSpace(first) {
			builder.WriteByte('\n')
		}
	}
	builder.WriteString(text)
}

func decodeMessageContent(ctx context.Context, raw json.RawMessage, client *gemini.Client) (string, []gemini.FileData, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return decodeMarkdownImages(ctx, text, client)
	}

	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, fmt.Errorf("消息 content 必须是字符串或内容段数组")
	}

	var builder strings.Builder
	var files []gemini.FileData
	for _, part := range parts {
		var partType string
		_ = json.Unmarshal(part["type"], &partType)
		switch partType {
		case "text", "input_text", "output_text":
			var value string
			_ = json.Unmarshal(part["text"], &value)
			writeTextSegment(&builder, value)
		case "image_url", "input_image", "image":
			label, file, err := decodeImagePart(ctx, part, client)
			if err != nil {
				return "", nil, err
			}
			builder.WriteString(label)
			if file != nil {
				files = append(files, *file)
			}
		case "document", "input_file", "input_audio", "audio", "input_video", "video", "file":
			if partType == "document" {
				var source struct {
					Type    string          `json:"type"`
					Data    string          `json:"data"`
					Content json.RawMessage `json:"content"`
				}
				_ = json.Unmarshal(part["source"], &source)
				if source.Type == "text" {
					builder.WriteString(source.Data)
					continue
				}
				if source.Type == "content" {
					text, attachments, err := decodeMessageContent(ctx, source.Content, client)
					if err != nil {
						return "", nil, err
					}
					builder.WriteString(text)
					files = append(files, attachments...)
					continue
				}
			}
			if partType == "file" && len(part["file"]) > 0 {
				var nested map[string]json.RawMessage
				if err := json.Unmarshal(part["file"], &nested); err != nil {
					return "", nil, fmt.Errorf("file 内容段格式无效: %w", err)
				}
				part = nested
			}
			label, file, err := decodeFilePart(ctx, part, client)
			if err != nil {
				return "", nil, err
			}
			builder.WriteString(label)
			if file != nil {
				files = append(files, *file)
			}
		case "server_tool_use", "web_search_tool_result":
			appendTranscriptObject(&builder, part)
		case "tool_use":
			var id, name string
			var input json.RawMessage
			_ = json.Unmarshal(part["id"], &id)
			_ = json.Unmarshal(part["name"], &name)
			input = part["input"]
			appendTranscriptObject(&builder, map[string]interface{}{"type": "tool_use", "id": id, "name": name, "input": input})
		case "tool_result":
			var id string
			_ = json.Unmarshal(part["tool_use_id"], &id)
			content, attachments, err := toolResultContent(ctx, part["content"], client)
			if err != nil {
				return "", nil, err
			}
			files = append(files, attachments...)
			appendTranscriptObject(&builder, map[string]interface{}{"type": "tool_result", "tool_use_id": id, "content": content})
		case "thinking", "redacted_thinking":
		default:
			return "", nil, fmt.Errorf("不支持的消息内容段类型 %q", partType)
		}
	}
	return builder.String(), files, nil
}

// toolResultContent 返回工具结果写入提示词的内容：内容段中有文字以外的段时解码为正文与附件，否则原样保留
func toolResultContent(ctx context.Context, raw json.RawMessage, client *gemini.Client) (any, []gemini.FileData, error) {
	var parts []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return raw, nil, nil
	}
	for _, part := range parts {
		if part.Type != "text" {
			text, files, err := decodeMessageContent(ctx, raw, client)
			return text, files, err
		}
	}
	return raw, nil, nil
}

var markdownImagePattern = regexp.MustCompile(`!\[[^\r\n]*\]\((data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/_=-]+)\)`)

func decodeMarkdownImages(ctx context.Context, text string, client *gemini.Client) (string, []gemini.FileData, error) {
	matches := markdownImagePattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil, nil
	}

	var builder strings.Builder
	files := make([]gemini.FileData, 0, len(matches))
	cursor := 0
	for _, match := range matches {
		builder.WriteString(text[cursor:match[0]])
		imageURL, _ := json.Marshal(text[match[2]:match[3]])
		label, file, err := decodeImagePart(ctx, map[string]json.RawMessage{"image_url": imageURL}, client)
		if err != nil {
			return "", nil, err
		}
		builder.WriteString(label)
		if file != nil {
			files = append(files, *file)
		}
		cursor = match[1]
	}
	builder.WriteString(text[cursor:])
	return builder.String(), files, nil
}

func decodeImagePart(ctx context.Context, part map[string]json.RawMessage, client *gemini.Client) (string, *gemini.FileData, error) {
	if file, ok, err := referencedFile(ctx, part); ok || err != nil {
		return "[Image]", file, err
	}
	var imageURL string
	if raw := part["image_url"]; len(raw) > 0 {
		if json.Unmarshal(raw, &imageURL) != nil {
			var value struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(raw, &value)
			imageURL = value.URL
		}
	}
	if imageURL == "" {
		_ = json.Unmarshal(part["url"], &imageURL)
	}
	if isYouTubeURL(imageURL) {
		return "\n" + imageURL + "\n", nil, nil
	}
	if imageURL != "" && !strings.HasPrefix(imageURL, "data:") {
		data, err := fetchRemoteMedia(ctx, client, imageURL)
		if err != nil {
			return "", nil, fmt.Errorf("下载远程图片失败: %w", err)
		}
		filename := fmt.Sprintf("image_%d.bin", time.Now().UnixNano())
		return "[Image]", &gemini.FileData{Data: data, FileName: filename}, nil
	}

	var source struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	}
	_ = json.Unmarshal(part["source"], &source)
	if imageURL == "" {
		imageURL = source.URL
	}
	if imageURL != "" && !strings.HasPrefix(imageURL, "data:") {
		data, err := fetchRemoteMedia(ctx, client, imageURL)
		if err != nil {
			return "", nil, fmt.Errorf("下载远程图片失败: %w", err)
		}
		filename := fmt.Sprintf("image_%d%s", time.Now().UnixNano(), mimeTypeToExt(source.MediaType))
		return "[Image]", &gemini.FileData{Data: data, FileName: filename, MimeType: source.MediaType}, nil
	}
	if imageURL != "" {
		mime, data, ok := strings.Cut(strings.TrimPrefix(imageURL, "data:"), ",")
		if !ok {
			return "", nil, fmt.Errorf("图片 data URL 缺少数据")
		}
		source.MediaType = strings.TrimSuffix(mime, ";base64")
		source.Data = data
	}
	if source.Data == "" {
		return "", nil, fmt.Errorf("图片内容为空")
	}

	data, err := decodeBase64(source.Data)
	if err != nil {
		return "", nil, fmt.Errorf("图片 base64 解码失败: %w", err)
	}
	filename := fmt.Sprintf("image_%d%s", time.Now().UnixNano(), mimeTypeToExt(source.MediaType))
	return "[Image]", &gemini.FileData{Data: data, FileName: filename, MimeType: source.MediaType}, nil
}

func decodeFilePart(ctx context.Context, part map[string]json.RawMessage, client *gemini.Client) (string, *gemini.FileData, error) {
	if file, ok, err := referencedFile(ctx, part); ok || err != nil {
		return "[File]", file, err
	}
	var source struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	}
	_ = json.Unmarshal(part["source"], &source)
	var embedded struct {
		Data     string `json:"data"`
		Format   string `json:"format"`
		MimeType string `json:"mime_type"`
	}
	for _, field := range []string{"input_audio", "audio", "input_video", "video"} {
		if len(part[field]) == 0 {
			continue
		}
		if err := json.Unmarshal(part[field], &embedded); err != nil {
			return "", nil, fmt.Errorf("媒体内容格式无效: %w", err)
		}
		if source.Data == "" {
			source.Data = embedded.Data
		}
		if source.MediaType == "" {
			source.MediaType = embedded.MimeType
			if source.MediaType == "" {
				source.MediaType = mediaFormatMIME(embedded.Format)
			}
		}
		break
	}
	var fileURL, filename string
	_ = json.Unmarshal(part["file_url"], &fileURL)
	_ = json.Unmarshal(part["filename"], &filename)
	if fileURL == "" {
		fileURL = source.URL
	}
	if isYouTubeURL(fileURL) {
		return "\n" + fileURL + "\n", nil, nil
	}
	if fileURL != "" && !strings.HasPrefix(fileURL, "data:") {
		data, err := fetchRemoteMedia(ctx, client, fileURL)
		if err != nil {
			return "", nil, fmt.Errorf("下载远程文件失败: %w", err)
		}
		if filename == "" {
			filename = fmt.Sprintf("file_%d%s", time.Now().UnixNano(), mimeTypeToExt(source.MediaType))
		}
		return "[File]", &gemini.FileData{Data: data, FileName: filename, MimeType: source.MediaType}, nil
	}
	if strings.HasPrefix(fileURL, "data:") && source.Data == "" {
		source.Data = fileURL
	}
	if source.Data == "" {
		_ = json.Unmarshal(part["file_data"], &source.Data)
	}
	if source.Data == "" {
		return "", nil, fmt.Errorf("文件内容为空")
	}
	if strings.HasPrefix(source.Data, "data:") {
		metadata, data, ok := strings.Cut(strings.TrimPrefix(source.Data, "data:"), ",")
		if !ok {
			return "", nil, fmt.Errorf("文件 data URL 缺少数据")
		}
		source.MediaType = strings.TrimSuffix(metadata, ";base64")
		source.Data = data
	}
	data, err := decodeBase64(source.Data)
	if err != nil {
		return "", nil, fmt.Errorf("文件 base64 解码失败: %w", err)
	}
	if filename == "" {
		filename = fmt.Sprintf("file_%d%s", time.Now().UnixNano(), mimeTypeToExt(source.MediaType))
	}
	return "[File]", &gemini.FileData{Data: data, FileName: filename, MimeType: source.MediaType}, nil
}

func mediaFormatMIME(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "wav":
		return "audio/wav"
	case "mp3", "mpeg":
		return "audio/mpeg"
	case "ogg":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	case "mp4":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "mov", "quicktime":
		return "video/quicktime"
	default:
		return "application/octet-stream"
	}
}

func decodeBase64(value string) ([]byte, error) {
	encodings := []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}
	for _, encoding := range encodings {
		if data, err := encoding.DecodeString(strings.TrimSpace(value)); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("base64 数据无效")
}

// renderTranscript 把消息记录写成网页提示词，单条普通用户消息与官网一样直接发送原文
func renderTranscript(records []map[string]interface{}) (string, error) {
	if len(records) == 1 && len(records[0]) == 2 && records[0]["role"] == "user" {
		if content, ok := records[0]["content"].(string); ok {
			return content, nil
		}
	}
	transcript, err := json.Marshal(map[string]interface{}{"messages": records})
	if err != nil {
		return "", err
	}
	return string(transcript), nil
}

func appendTranscriptObject(builder *strings.Builder, value interface{}) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteByte('\n')
	}
	builder.Write(data)
	builder.WriteByte('\n')
}

func transcriptRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant", "model":
		return "assistant"
	case "system", "developer":
		return "system"
	case "tool":
		return "tool"
	default:
		return "user"
	}
}
