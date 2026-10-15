package requestdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound 表示保存的响应、文件或视频不存在
var ErrNotFound = errors.New("记录不存在")

// StoredResponse 是 Responses 接口保存的响应对象与生成它的输入项
type StoredResponse struct {
	ID         string
	CreatedAt  time.Time
	PreviousID string
	Background bool
	Status     string
	// Response 为完整的响应对象 JSON
	Response json.RawMessage
	// Input 为带 ID 的输入项 JSON 数组
	Input json.RawMessage
}

// SaveResponse 写入或替换一个保存的响应
func (store *Store) SaveResponse(ctx context.Context, response StoredResponse) error {
	if len(response.Input) == 0 {
		response.Input = json.RawMessage("[]")
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO responses(id,created_at,previous_id,background,status,response,input) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET status=excluded.status, response=excluded.response, input=excluded.input`,
		response.ID, response.CreatedAt.UnixMilli(), response.PreviousID, response.Background, response.Status, string(response.Response), string(response.Input))
	return err
}

// Response 返回保存的响应，不存在时返回 ErrNotFound
func (store *Store) Response(ctx context.Context, id string) (StoredResponse, error) {
	response := StoredResponse{ID: id}
	var created int64
	var body, input string
	err := store.db.QueryRowContext(ctx, `SELECT created_at,previous_id,background,status,response,input FROM responses WHERE id=?`, id).
		Scan(&created, &response.PreviousID, &response.Background, &response.Status, &body, &input)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredResponse{}, ErrNotFound
	}
	if err != nil {
		return StoredResponse{}, err
	}
	response.CreatedAt = time.UnixMilli(created)
	response.Response, response.Input = json.RawMessage(body), json.RawMessage(input)
	return response, nil
}

// DeleteResponse 删除保存的响应，返回是否存在
func (store *Store) DeleteResponse(ctx context.Context, id string) (bool, error) {
	result, err := store.db.ExecContext(ctx, `DELETE FROM responses WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// UnfinishedResponses 返回状态为 queued 或 in_progress 的后台响应
func (store *Store) UnfinishedResponses(ctx context.Context) ([]StoredResponse, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,created_at,previous_id,background,status,response,input FROM responses WHERE background=1 AND status IN ('queued','in_progress')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var responses []StoredResponse
	for rows.Next() {
		var response StoredResponse
		var created int64
		var body, input string
		if err := rows.Scan(&response.ID, &created, &response.PreviousID, &response.Background, &response.Status, &body, &input); err != nil {
			return nil, err
		}
		response.CreatedAt = time.UnixMilli(created)
		response.Response, response.Input = json.RawMessage(body), json.RawMessage(input)
		responses = append(responses, response)
	}
	return responses, rows.Err()
}
