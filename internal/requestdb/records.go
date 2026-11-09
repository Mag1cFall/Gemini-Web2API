package requestdb

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// recordColumns 是请求记录查询读取的列，最后一列表示是否保存了正文
const recordColumns = `r.id, r.response_id, r.time, r.protocol, r.path, r.model, r.provider_model, r.account, r.status, r.state,
 r.duration_ms, r.first_event_ms, r.queue_ms, r.input_tokens, r.reasoning_tokens, r.reply_tokens, r.total_tokens,
 r.tool_calls, r.error, r.attempts, r.thinking, EXISTS(SELECT 1 FROM bodies b WHERE b.id = r.id)`

// searchColumns 是关键字匹配的请求记录列
var searchColumns = []string{"id", "response_id", "model", "provider_model", "account", "error"}

// recordFilter 返回请求记录的范围、维度筛选、状态码与关键字条件
func recordFilter(query UsageRecordQuery) (string, []any) {
	clause, args := filterSQL(query.Filters)
	clause = ` WHERE r.time >= ? AND r.time < ?` + strings.ReplaceAll(clause, " AND ", " AND r.")
	args = append([]any{query.From.UnixMilli(), query.To.UnixMilli()}, args...)
	if query.Status != 0 {
		clause += ` AND r.status = ?`
		args = append(args, query.Status)
	}
	if query.Search != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query.Search) + "%"
		conditions := make([]string, len(searchColumns))
		for index, column := range searchColumns {
			conditions[index] = `r.` + column + ` LIKE ? ESCAPE '\'`
			args = append(args, pattern)
		}
		clause += ` AND (` + strings.Join(conditions, " OR ") + `)`
	}
	return clause, args
}

// scanRecord 读取一条请求记录
func scanRecord(rows *sql.Rows) (UsageRecord, error) {
	var record UsageRecord
	var at int64
	var attempts string
	err := rows.Scan(&record.ID, &record.ResponseID, &at, &record.Protocol, &record.Path, &record.Model, &record.ProviderModel,
		&record.Account, &record.Status, &record.State, &record.DurationMS, &record.FirstEventMS, &record.QueueMS, &record.InputTokens,
		&record.ReasoningTokens, &record.ReplyTokens, &record.TotalTokens, &record.ToolCalls, &record.Error, &attempts, &record.Thinking, &record.HasBody)
	if err != nil {
		return record, err
	}
	record.Time = time.UnixMilli(at).UTC()
	if err := json.Unmarshal([]byte(attempts), &record.Attempts); err != nil {
		return record, err
	}
	return record, nil
}

// exportPageSize 是导出时一次读取的记录数，每页读完释放连接后再写出
const exportPageSize = 1000

// Records 按完成时间倒序返回一页请求记录
func (store *Store) Records(ctx context.Context, query UsageRecordQuery) (UsageRecordPage, error) {
	store.flush()
	return store.recordPage(ctx, query)
}

// pageEnd 返回一页记录的时间上界，游标早于查询终点时收紧到游标所在毫秒，索引从游标位置开始扫描
func pageEnd(query UsageRecordQuery) time.Time {
	if query.Before != nil {
		if bound := query.Before.Time.Add(time.Millisecond); bound.Before(query.To) {
			return bound
		}
	}
	return query.To
}

// recordPage 读取一页请求记录
func (store *Store) recordPage(ctx context.Context, query UsageRecordQuery) (UsageRecordPage, error) {
	before := query.Before
	query.To = pageEnd(query)
	clause, args := recordFilter(query)
	if before != nil {
		clause += ` AND (r.time < ? OR r.id < ?)`
		args = append(args, before.Time.UnixMilli(), before.ID)
	}
	rows, err := store.reads.QueryContext(ctx, `SELECT `+recordColumns+` FROM requests r`+clause+
		` ORDER BY r.time DESC, r.id DESC LIMIT ?`, append(args, query.Limit+1)...)
	if err != nil {
		return UsageRecordPage{}, err
	}
	page := UsageRecordPage{Items: []UsageRecord{}}
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return UsageRecordPage{}, errors.Join(err, rows.Close())
		}
		page.Items = append(page.Items, record)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return UsageRecordPage{}, err
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeUsageCursor(UsageCursor{Time: last.Time, ID: last.ID})
	}
	return page, nil
}

// csvHeader 是导出文件的列名
var csvHeader = []string{
	"time", "id", "response_id", "protocol", "path", "model", "provider_model", "thinking", "account", "status", "state", "duration_ms",
	"first_event_ms", "queue_ms", "input_tokens", "reasoning_tokens", "reply_tokens", "total_tokens", "tool_calls", "error", "attempts",
}

// csvText 为可能被电子表格当作公式的文本加单引号前缀
func csvText(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

// ExportRecords 以带 BOM 的 UTF-8 CSV 按完成时间倒序写出范围内全部符合条件的请求记录
func (store *Store) ExportRecords(ctx context.Context, query UsageRecordQuery, output io.Writer) error {
	store.flush()
	if _, err := io.WriteString(output, "\xef\xbb\xbf"); err != nil {
		return err
	}
	writer := csv.NewWriter(output)
	if err := writer.Write(csvHeader); err != nil {
		return err
	}
	integer := func(value int64) string { return strconv.FormatInt(value, 10) }
	query.Before, query.Limit = nil, exportPageSize
	for {
		page, err := store.recordPage(ctx, query)
		if err != nil {
			return err
		}
		for _, record := range page.Items {
			attempts, err := json.Marshal(record.Attempts)
			if err != nil {
				return err
			}
			if err := writer.Write([]string{
				record.Time.Format(time.RFC3339Nano), record.ID, record.ResponseID, record.Protocol, csvText(record.Path),
				csvText(record.Model), csvText(record.ProviderModel), record.Thinking, csvText(record.Account), strconv.Itoa(record.Status), record.State,
				integer(record.DurationMS), integer(record.FirstEventMS), integer(record.QueueMS), integer(record.InputTokens),
				integer(record.ReasoningTokens), integer(record.ReplyTokens), integer(record.TotalTokens), strconv.Itoa(record.ToolCalls),
				csvText(record.Error), string(attempts),
			}); err != nil {
				return err
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return err
		}
		if page.NextCursor == "" {
			return nil
		}
		last := page.Items[len(page.Items)-1]
		query.Before = &UsageCursor{Time: last.Time, ID: last.ID}
	}
}
