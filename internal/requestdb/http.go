package requestdb

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/gin-gonic/gin"
)

// usageMaxSpan 是一次用量查询允许的最长时间范围
const usageMaxSpan = 93 * 24 * time.Hour

// usageMaxBuckets 是一次用量查询允许的最多分桶数
const usageMaxBuckets = 1500

// usageBucketSizes 是允许手动选择的分桶时长
var usageBucketSizes = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 24 * time.Hour}

// writeError 写出管理接口的错误响应
func writeError(c *gin.Context, status int, code string, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// autoUsageBucket 按时间范围长度选择分桶时长
func autoUsageBucket(span time.Duration) time.Duration {
	switch {
	case span <= 2*time.Hour:
		return time.Minute
	case span <= 12*time.Hour:
		return 5 * time.Minute
	case span <= 8*24*time.Hour:
		return time.Hour
	}
	return 24 * time.Hour
}

// parseUsageRange 读取 from 与 to，校验先后顺序与最长范围
func parseUsageRange(values url.Values) (time.Time, time.Time, error) {
	from, err := time.Parse(time.RFC3339, values.Get("from"))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from 必须是 RFC 3339 时间")
	}
	to, err := time.Parse(time.RFC3339, values.Get("to"))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to 必须是 RFC 3339 时间")
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("from 必须早于 to")
	}
	if to.Sub(from) > usageMaxSpan {
		return time.Time{}, time.Time{}, fmt.Errorf("时间范围不能超过 93 天")
	}
	return from.UTC(), to.UTC(), nil
}

// parseUsageFilters 读取各维度的筛选值，逗号分隔多个取值，空值与重复值被忽略
func parseUsageFilters(values url.Values) UsageFilters {
	filters := UsageFilters{}
	for _, dimension := range UsageDimensions {
		for _, value := range values[dimension] {
			for _, item := range strings.Split(value, ",") {
				if item = strings.TrimSpace(item); item != "" && !slices.Contains(filters[dimension], item) {
					filters[dimension] = append(filters[dimension], item)
				}
			}
		}
	}
	return filters
}

// UsageHandler 处理 GET /api/usage，返回时间范围内的用量报告
func UsageHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		values := c.Request.URL.Query()
		from, to, err := parseUsageRange(values)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		query := UsageQuery{From: from, To: to, Location: time.UTC, Filters: parseUsageFilters(values), Stack: "model"}
		if name := values.Get("tz"); name != "" {
			if query.Location, err = time.LoadLocation(name); err != nil {
				writeError(c, http.StatusBadRequest, "invalid_request", "tz 必须是 IANA 时区名")
				return
			}
		}
		query.Bucket = autoUsageBucket(to.Sub(from))
		if value := values.Get("bucket"); value != "" && value != "auto" {
			seconds, err := strconv.Atoi(value)
			query.Bucket = time.Duration(seconds) * time.Second
			if err != nil || !slices.Contains(usageBucketSizes, query.Bucket) {
				writeError(c, http.StatusBadRequest, "invalid_request", "bucket 必须是 auto、60、300、900、3600 或 86400")
				return
			}
		}
		if to.Sub(from)/query.Bucket > usageMaxBuckets {
			writeError(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("分桶数超过 %d，请选择更大的粒度", usageMaxBuckets))
			return
		}
		if value := values.Get("stack"); value != "" {
			if !slices.Contains(UsageDimensions, value) {
				writeError(c, http.StatusBadRequest, "invalid_request", "stack 必须是 model、account、protocol 或 state")
				return
			}
			query.Stack = value
		}
		report, err := store.Usage(c.Request.Context(), query)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "usage_failed", err.Error())
			return
		}
		c.JSON(http.StatusOK, report)
	}
}

// parseUsageRecordQuery 读取请求记录的范围、筛选、状态码、关键字与分页参数
func parseUsageRecordQuery(values url.Values) (UsageRecordQuery, error) {
	from, to, err := parseUsageRange(values)
	if err != nil {
		return UsageRecordQuery{}, err
	}
	query := UsageRecordQuery{
		From: from, To: to, Filters: parseUsageFilters(values),
		Search: strings.TrimSpace(values.Get("q")), Limit: 50,
	}
	if value := values.Get("cursor"); value != "" {
		if query.Before, err = decodeUsageCursor(value); err != nil {
			return UsageRecordQuery{}, err
		}
	}
	if value := values.Get("status"); value != "" {
		if query.Status, err = strconv.Atoi(value); err != nil || query.Status < 100 || query.Status > 599 {
			return UsageRecordQuery{}, fmt.Errorf("status 必须是 HTTP 状态码")
		}
	}
	if value := values.Get("limit"); value != "" {
		if query.Limit, err = strconv.Atoi(value); err != nil || query.Limit < 1 || query.Limit > 200 {
			return UsageRecordQuery{}, fmt.Errorf("limit 必须是 1 到 200")
		}
	}
	return query, nil
}

// RecordsHandler 处理 GET /api/usage/records，按完成时间倒序返回一页请求记录
func RecordsHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, err := parseUsageRecordQuery(c.Request.URL.Query())
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		page, err := store.Records(c.Request.Context(), query)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "usage_records_failed", err.Error())
			return
		}
		c.JSON(http.StatusOK, page)
	}
}

// ExportHandler 处理 GET /api/usage/records.csv，以 CSV 附件导出范围内全部符合条件的请求记录
func ExportHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, err := parseUsageRecordQuery(c.Request.URL.Query())
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		name := fmt.Sprintf("usage-%s-%s.csv", query.From.Format("20060102T150405Z"), query.To.Format("20060102T150405Z"))
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
		c.Header("Cache-Control", "no-store")
		c.Status(http.StatusOK)
		if err := store.ExportRecords(c.Request.Context(), query, c.Writer); err != nil {
			logging.Warn(logging.SourceService, "导出请求记录失败", "错误", err)
		}
	}
}

// BodyHandler 处理 GET /api/requests/:id/body，返回请求保存的截断正文
func BodyHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := store.RequestBody(c.Request.Context(), c.Param("id"))
		if errors.Is(err, ErrRequestBodyNotFound) {
			writeError(c, http.StatusNotFound, "request_body_not_found", err.Error())
			return
		}
		if err != nil {
			writeError(c, http.StatusInternalServerError, "request_body_failed", err.Error())
			return
		}
		c.JSON(http.StatusOK, body)
	}
}
