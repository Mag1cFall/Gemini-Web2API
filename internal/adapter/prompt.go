package adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

func decodeMessageContent(ctx context.Context, raw json.RawMessage, client *gemini.Client) (string, []gemini.FileData, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil, nil
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
			builder.WriteString(value)
		case "image_url", "input_image", "image":
			label, file, err := decodeImagePart(ctx, part, client)
			if err != nil {
				return "", nil, err
			}
			builder.WriteString(label)
			if file != nil {
				files = append(files, *file)
			}
		case "document", "input_file":
			label, file, err := decodeFilePart(ctx, part, client)
			if err != nil {
				return "", nil, err
			}
			builder.WriteString(label)
			if file != nil {
				files = append(files, *file)
			}
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
			appendTranscriptObject(&builder, map[string]interface{}{"type": "tool_result", "tool_use_id": id, "content": part["content"]})
		case "thinking":
			var thinking, signature string
			_ = json.Unmarshal(part["thinking"], &thinking)
			_ = json.Unmarshal(part["signature"], &signature)
			appendTranscriptObject(&builder, map[string]interface{}{"type": "thinking", "thinking": thinking, "signature": signature})
		default:
			return "", nil, fmt.Errorf("不支持的消息内容段类型 %q", partType)
		}
	}
	return builder.String(), files, nil
}

func decodeImagePart(ctx context.Context, part map[string]json.RawMessage, client *gemini.Client) (string, *gemini.FileData, error) {
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
	if imageURL != "" && !strings.HasPrefix(imageURL, "data:") {
		data, err := client.FetchMedia(ctx, imageURL)
		if err != nil {
			return "", nil, fmt.Errorf("下载远程图片失败: %w", err)
		}
		filename := fmt.Sprintf("image_%d.bin", time.Now().UnixNano())
		fileID, err := client.UploadFile(ctx, data, filename)
		if err != nil {
			return "", nil, fmt.Errorf("上传远程图片失败: %w", err)
		}
		return "[Image]", &gemini.FileData{URL: fileID, FileName: filename}, nil
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
		data, err := client.FetchMedia(ctx, imageURL)
		if err != nil {
			return "", nil, fmt.Errorf("下载远程图片失败: %w", err)
		}
		filename := fmt.Sprintf("image_%d%s", time.Now().UnixNano(), mimeTypeToExt(source.MediaType))
		fileID, err := client.UploadFile(ctx, data, filename)
		if err != nil {
			return "", nil, fmt.Errorf("上传远程图片失败: %w", err)
		}
		return "[Image]", &gemini.FileData{URL: fileID, FileName: filename}, nil
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
	fileID, err := client.UploadFile(ctx, data, filename)
	if err != nil {
		return "", nil, fmt.Errorf("上传图片失败: %w", err)
	}
	return "[Image]", &gemini.FileData{URL: fileID, FileName: filename}, nil
}

func decodeFilePart(ctx context.Context, part map[string]json.RawMessage, client *gemini.Client) (string, *gemini.FileData, error) {
	var source struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	}
	_ = json.Unmarshal(part["source"], &source)
	var fileURL, filename string
	_ = json.Unmarshal(part["file_url"], &fileURL)
	_ = json.Unmarshal(part["filename"], &filename)
	if fileURL == "" {
		fileURL = source.URL
	}
	if fileURL != "" {
		data, err := client.FetchMedia(ctx, fileURL)
		if err != nil {
			return "", nil, fmt.Errorf("下载远程文件失败: %w", err)
		}
		if filename == "" {
			filename = fmt.Sprintf("file_%d%s", time.Now().UnixNano(), mimeTypeToExt(source.MediaType))
		}
		fileID, err := client.UploadFile(ctx, data, filename)
		if err != nil {
			return "", nil, fmt.Errorf("上传远程文件失败: %w", err)
		}
		return "[File]", &gemini.FileData{URL: fileID, FileName: filename}, nil
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
	fileID, err := client.UploadFile(ctx, data, filename)
	if err != nil {
		return "", nil, fmt.Errorf("上传文件失败: %w", err)
	}
	return "[File]", &gemini.FileData{URL: fileID, FileName: filename}, nil
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

func appendTranscriptObject(builder *strings.Builder, value interface{}) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	if builder.Len() > 0 {
		builder.WriteByte('\n')
	}
	builder.Write(data)
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
