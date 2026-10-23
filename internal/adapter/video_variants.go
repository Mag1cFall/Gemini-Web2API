package adapter

import (
	"bytes"
	"context"
	"net/http"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
)

// videoThumbnailFileID 返回视频缩略图文件的 ID，由视频文件 ID 派生
func videoThumbnailFileID(fileID string) string {
	return fileID + "-thumbnail"
}

// saveVideoThumbnail 用生成视频的账号下载网页视频条目给出的缩略图并保存，下载失败时记入日志后跳过
func saveVideoThumbnail(ctx context.Context, client *gemini.Client, store *requestdb.Store, fileID string, media gemini.Media) {
	if media.Thumbnail == "" {
		return
	}
	data, err := client.FetchMedia(ctx, media.Thumbnail)
	if err == nil {
		_, err = store.CreateFile(context.WithoutCancel(ctx), requestdb.StoredFile{
			ID: videoThumbnailFileID(fileID), Filename: fileID + "-thumbnail", MIMEType: http.DetectContentType(data), Purpose: "video", Source: "generated",
		}, bytes.NewReader(data))
	}
	if err != nil {
		logging.Warn("video", "视频缩略图保存失败", "错误", err)
	}
}
