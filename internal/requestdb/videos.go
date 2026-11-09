package requestdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// StoredVideo 是视频生成任务的协议对象与成品文件
type StoredVideo struct {
	ID        string
	CreatedAt time.Time
	// Protocol 为 openai（/v1/videos）或 gemini（predictLongRunning）
	Protocol string
	Status   string
	// Object 为协议对象 JSON：OpenAI 视频对象或 Gemini 长时操作
	Object json.RawMessage
	// FileID 为成品视频在文件存储中的 ID，未完成时为空
	FileID string
	// Session 为视频所在网页会话的 JSON，未完成时为空
	Session json.RawMessage
}

// SaveVideo 写入或替换视频任务
func (store *Store) SaveVideo(ctx context.Context, video StoredVideo) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO videos(id,created_at,protocol,status,object,file_id) VALUES(?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET status=excluded.status, object=excluded.object, file_id=excluded.file_id`,
		video.ID, video.CreatedAt.UnixMilli(), video.Protocol, video.Status, string(video.Object), video.FileID)
	return err
}

// SaveVideoSession 写入视频所在的网页会话
func (store *Store) SaveVideoSession(ctx context.Context, id string, session json.RawMessage) error {
	_, err := store.db.ExecContext(ctx, `UPDATE videos SET session=? WHERE id=?`, string(session), id)
	return err
}

// Video 返回视频任务，不存在时返回 ErrNotFound
func (store *Store) Video(ctx context.Context, id string) (StoredVideo, error) {
	rows, err := store.reads.QueryContext(ctx, `SELECT `+videoColumns+` FROM videos WHERE id=?`, id)
	if err != nil {
		return StoredVideo{}, err
	}
	videos, err := scanVideos(rows)
	if err != nil {
		return StoredVideo{}, err
	}
	if len(videos) == 0 {
		return StoredVideo{}, ErrNotFound
	}
	return videos[0], nil
}

// Videos 按创建时间返回指定协议的一页视频任务与是否还有下一页，after 为上一页最后一个 ID
func (store *Store) Videos(ctx context.Context, protocol string, ascending bool, after string, limit int) ([]StoredVideo, bool, error) {
	comparison, order := "<", "DESC"
	if ascending {
		comparison, order = ">", "ASC"
	}
	where, args := "protocol=?", []any{protocol}
	if after != "" {
		var created int64
		err := store.reads.QueryRowContext(ctx, `SELECT created_at FROM videos WHERE id=?`, after).Scan(&created)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		if err != nil {
			return nil, false, err
		}
		where += fmt.Sprintf(" AND (created_at %s ? OR (created_at = ? AND id %s ?))", comparison, comparison)
		args = append(args, created, created, after)
	}
	args = append(args, limit+1)
	rows, err := store.reads.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM videos WHERE %s ORDER BY created_at %s, id %s LIMIT ?`, videoColumns, where, order, order), args...)
	if err != nil {
		return nil, false, err
	}
	videos, err := scanVideos(rows)
	if err != nil {
		return nil, false, err
	}
	if len(videos) > limit {
		return videos[:limit], true, nil
	}
	return videos, false, nil
}

// DeleteVideo 删除视频任务与成品文件，返回是否存在
func (store *Store) DeleteVideo(ctx context.Context, id string) (bool, error) {
	video, err := store.Video(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM videos WHERE id=?`, id); err != nil {
		return false, err
	}
	if video.FileID != "" {
		if _, err := store.DeleteFile(ctx, video.FileID); err != nil {
			return true, err
		}
	}
	return true, nil
}

// videoColumns 是视频表按 scanVideos 顺序读取的列
const videoColumns = `id,created_at,protocol,status,object,file_id,session`

// scanVideos 读取视频行并关闭结果集
func scanVideos(rows *sql.Rows) ([]StoredVideo, error) {
	defer rows.Close()
	var videos []StoredVideo
	for rows.Next() {
		var video StoredVideo
		var created int64
		var object, session string
		if err := rows.Scan(&video.ID, &created, &video.Protocol, &video.Status, &object, &video.FileID, &session); err != nil {
			return nil, err
		}
		video.CreatedAt, video.Object, video.Session = time.UnixMilli(created), json.RawMessage(object), json.RawMessage(session)
		videos = append(videos, video)
	}
	return videos, rows.Err()
}

// UnfinishedVideos 返回状态为 queued 或 in_progress 的视频任务
func (store *Store) UnfinishedVideos(ctx context.Context) ([]StoredVideo, error) {
	rows, err := store.reads.QueryContext(ctx, `SELECT `+videoColumns+` FROM videos WHERE status IN ('queued','in_progress')`)
	if err != nil {
		return nil, err
	}
	return scanVideos(rows)
}
