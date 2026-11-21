package requestdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// Log 是账本保存的一条 WARN 以上日志，Seq 为写入序号
type Log struct {
	Seq int64 `json:"seq"`
	logging.Event
}

// LogQuery 选择已保存日志的时间范围、级别、请求 ID、关键字与分页位置
type LogQuery struct {
	From time.Time
	To   time.Time
	// ErrorsOnly 为真时只返回 ERROR
	ErrorsOnly bool
	RequestID  string
	// Search 匹配日志的单行文字
	Search string
	// Before 为 0 时从最新一条开始，否则返回序号更小的日志
	Before int64
	Limit  int
}

// LogPage 是一页已保存日志，NextBefore 为 0 表示没有更早的日志
type LogPage struct {
	Items      []Log `json:"items"`
	NextBefore int64 `json:"next_before,omitempty"`
}

// RecordLog 排队写入一条 WARN 以上日志，不等待落盘
func (store *Store) RecordLog(event logging.Event) {
	store.enqueue(write{log: &event})
}

// Logs 提交排队的写入后按写入顺序倒序返回一页已保存日志
func (store *Store) Logs(ctx context.Context, query LogQuery) (LogPage, error) {
	store.flush()
	clause := ` WHERE time >= ? AND time < ?`
	args := []any{query.From.UnixMilli(), query.To.UnixMilli()}
	if query.ErrorsOnly {
		clause += ` AND level = 'ERROR'`
	}
	if query.RequestID != "" {
		clause += ` AND request_id = ?`
		args = append(args, query.RequestID)
	}
	if query.Search != "" {
		clause += ` AND text LIKE ? ESCAPE '\'`
		args = append(args, "%"+likeEscaper.Replace(query.Search)+"%")
	}
	if query.Before > 0 {
		clause += ` AND seq < ?`
		args = append(args, query.Before)
	}
	rows, err := store.reads.QueryContext(ctx, `SELECT seq, data FROM logs`+clause+` ORDER BY seq DESC LIMIT ?`, append(args, query.Limit+1)...)
	if err != nil {
		return LogPage{}, err
	}
	page := LogPage{Items: []Log{}}
	for rows.Next() {
		var entry Log
		var data string
		if err := rows.Scan(&entry.Seq, &data); err != nil {
			return LogPage{}, errors.Join(err, rows.Close())
		}
		if err := json.Unmarshal([]byte(data), &entry.Event); err != nil {
			return LogPage{}, errors.Join(err, rows.Close())
		}
		page.Items = append(page.Items, entry)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return LogPage{}, err
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		page.NextBefore = page.Items[len(page.Items)-1].Seq
	}
	return page, nil
}

// ClearLogs 提交排队的写入后删除全部已保存的日志并把空闲页交还文件系统
func (store *Store) ClearLogs(ctx context.Context) error {
	store.flush()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM logs`); err != nil {
		return err
	}
	return reclaim(ctx, store.db)
}

// LogsHandler 处理 GET /api/logs，按时间范围、级别、请求 ID 与关键字倒序返回一页已保存日志
func LogsHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		values := c.Request.URL.Query()
		from, to, err := parseUsageRange(values)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		query := LogQuery{
			From: from, To: to, ErrorsOnly: strings.EqualFold(values.Get("level"), "ERROR"),
			RequestID: values.Get("request_id"), Search: strings.TrimSpace(values.Get("q")), Limit: 100,
		}
		if value := values.Get("before"); value != "" {
			if query.Before, err = strconv.ParseInt(value, 10, 64); err != nil {
				writeError(c, http.StatusBadRequest, "invalid_request", "before 必须是整数")
				return
			}
		}
		page, err := store.Logs(c.Request.Context(), query)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "logs_failed", err.Error())
			return
		}
		c.JSON(http.StatusOK, page)
	}
}
