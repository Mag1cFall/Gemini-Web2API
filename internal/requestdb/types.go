package requestdb

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// ErrRequestBodyNotFound 表示请求没有保存正文
var ErrRequestBodyNotFound = errors.New("请求正文不存在")

// UsageDimensions 是用量可筛选与分组的维度，维度名同时是请求与汇总表的列名
var UsageDimensions = []string{"model", "account", "protocol", "state"}

// UsageFilters 按维度限定参与统计的请求，同一维度内多个取值为或关系
type UsageFilters map[string][]string

// UsageQuery 选择用量统计的时间范围、分桶、时区、筛选与堆叠维度
type UsageQuery struct {
	From     time.Time
	To       time.Time
	Bucket   time.Duration
	Location *time.Location
	Filters  UsageFilters
	Stack    string
}

// UsageLatency 汇总一组请求的平均值与分位耗时
type UsageLatency struct {
	AvgMS float64 `json:"avg_ms"`
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

// UsageStats 汇总一组请求的结果、token 与耗时；成功包含工具调用
type UsageStats struct {
	Requests        int64        `json:"requests"`
	Succeeded       int64        `json:"succeeded"`
	Failed          int64        `json:"failed"`
	Canceled        int64        `json:"canceled"`
	RateLimited     int64        `json:"rate_limited"`
	InputTokens     int64        `json:"input_tokens"`
	ReasoningTokens int64        `json:"reasoning_tokens"`
	ReplyTokens     int64        `json:"reply_tokens"`
	TotalTokens     int64        `json:"total_tokens"`
	Duration        UsageLatency `json:"duration"`
	FirstEvent      UsageLatency `json:"first_event"`
	QueueAvgMS      float64      `json:"queue_avg_ms"`
	LastAt          *time.Time   `json:"last_at,omitempty"`
}

// UsageBucket 保存一个时间分桶的汇总、等价 Gemini API 美元费用与有单价的请求数
type UsageBucket struct {
	At             time.Time `json:"at"`
	Cost           float64   `json:"cost"`
	PricedRequests int64     `json:"priced_requests"`
	UsageStats
}

// UsageGroup 保存一个维度取值的汇总
type UsageGroup struct {
	Key string `json:"key"`
	UsageStats
}

// UsagePair 保存账户与模型组合的汇总
type UsagePair struct {
	Account string `json:"account"`
	Model   string `json:"model"`
	UsageStats
}

// UsageSeries 是堆叠趋势中一个维度取值逐桶的请求数与 token，Other 合并排名之外的取值
type UsageSeries struct {
	Key      string  `json:"key"`
	Other    bool    `json:"other,omitempty"`
	Requests []int64 `json:"requests"`
	Tokens   []int64 `json:"tokens"`
}

// UsageRecent 汇总最近几分钟的请求与 token，用于计算当前吞吐
type UsageRecent struct {
	Minutes  int   `json:"minutes"`
	Requests int64 `json:"requests"`
	Tokens   int64 `json:"tokens"`
}

// UsageReport 返回用量看板的汇总、上一周期、当前吞吐、等价费用、逐桶趋势、堆叠序列、分项与筛选候选
type UsageReport struct {
	From                   time.Time               `json:"from"`
	To                     time.Time               `json:"to"`
	BucketSeconds          int64                   `json:"bucket_seconds"`
	GeneratedAt            time.Time               `json:"generated_at"`
	LatestAt               *time.Time              `json:"latest_at,omitempty"`
	Totals                 UsageStats              `json:"totals"`
	Previous               UsageStats              `json:"previous"`
	Cost                   float64                 `json:"cost"`
	PreviousCost           float64                 `json:"previous_cost"`
	PricedRequests         int64                   `json:"priced_requests"`
	PreviousPricedRequests int64                   `json:"previous_priced_requests"`
	Recent                 UsageRecent             `json:"recent"`
	Buckets                []UsageBucket           `json:"buckets"`
	StackBy                string                  `json:"stack_by"`
	Series                 []UsageSeries           `json:"series"`
	Groups                 map[string][]UsageGroup `json:"groups"`
	Pairs                  []UsagePair             `json:"pairs"`
	Options                map[string][]string     `json:"options"`
}

// UsageRecordQuery 选择请求记录的时间范围、筛选、状态码、关键字与分页位置
type UsageRecordQuery struct {
	From    time.Time
	To      time.Time
	Filters UsageFilters
	Status  int
	Search  string
	Before  *UsageCursor
	Limit   int
}

// UsageCursor 是请求记录分页位置，下一页从该记录之后的更早记录开始
type UsageCursor struct {
	Time time.Time
	ID   string
}

// encodeUsageCursor 将分页位置编码为 URL 安全的字符串
func encodeUsageCursor(cursor UsageCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(cursor.Time.UnixMilli(), 10) + "|" + cursor.ID))
}

// decodeUsageCursor 解析 encodeUsageCursor 生成的分页位置
func decodeUsageCursor(value string) (*UsageCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("cursor 无效")
	}
	at, id, found := strings.Cut(string(raw), "|")
	milliseconds, err := strconv.ParseInt(at, 10, 64)
	if !found || err != nil || id == "" {
		return nil, fmt.Errorf("cursor 无效")
	}
	return &UsageCursor{Time: time.UnixMilli(milliseconds).UTC(), ID: id}, nil
}

// UsageRecord 是一条已结束请求的账本记录，Model 是请求的公开模型，ProviderModel 是实际处理的模型
type UsageRecord struct {
	ID              string            `json:"id"`
	ResponseID      string            `json:"response_id"`
	Time            time.Time         `json:"time"`
	Protocol        string            `json:"protocol"`
	Path            string            `json:"path"`
	Model           string            `json:"model"`
	ProviderModel   string            `json:"provider_model"`
	Account         string            `json:"account"`
	Status          int               `json:"status"`
	State           string            `json:"state"`
	DurationMS      int64             `json:"duration_ms"`
	FirstEventMS    int64             `json:"first_event_ms"`
	QueueMS         int64             `json:"queue_ms"`
	InputTokens     int64             `json:"input_tokens"`
	ReasoningTokens int64             `json:"reasoning_tokens"`
	ReplyTokens     int64             `json:"reply_tokens"`
	TotalTokens     int64             `json:"total_tokens"`
	ToolCalls       int               `json:"tool_calls"`
	Error           string            `json:"error"`
	Attempts        []logging.Attempt `json:"attempts"`
	HasBody         bool              `json:"has_body"`
}

// UsageRecordPage 是按完成时间倒序的一页请求记录，NextCursor 为空表示没有更早的记录
type UsageRecordPage struct {
	Items      []UsageRecord `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// RequestBody 保存一次请求截断后的请求与响应正文及原始字节数
type RequestBody struct {
	ID           string    `json:"id"`
	Time         time.Time `json:"time"`
	Request      string    `json:"request"`
	RequestSize  int64     `json:"request_size"`
	Response     string    `json:"response"`
	ResponseSize int64     `json:"response_size"`
}
