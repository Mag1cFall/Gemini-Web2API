package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// StatusClientClosed 是客户端在请求完成前断开或请求被取消时记录的状态
const StatusClientClosed = 499

// 请求状态
const (
	StateRunning   = "running"
	StateCompleted = "completed"
	StateToolCalls = "tool_calls"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// 等待诊断阈值
const (
	stallThreshold = 15 * time.Second
	stallRepeat    = time.Minute
)

// requestPhase 是请求当前所处的等待位置
type requestPhase int

const (
	phaseQueued requestPhase = iota
	phasePreparing
	phaseAwaiting
	phaseStreaming
	phaseBackground
	phaseDone
)

// Usage 是一次请求的 token 用量
type Usage struct {
	InputTokens     int `json:"input_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
	ReplyTokens     int `json:"reply_tokens"`
	OutputTokens    int `json:"output_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

// Attempt 是一次未成功的上游尝试
type Attempt struct {
	Account    string `json:"account"`
	Status     int    `json:"status"`
	Error      string `json:"error"`
	DurationMS int64  `json:"duration_ms"`
}

// RequestInfo 是请求的阶段、账号、用量与结果
type RequestInfo struct {
	ID             string    `json:"id"`
	ResponseID     string    `json:"response_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	State          string    `json:"state"`
	Protocol       string    `json:"protocol,omitempty"`
	Method         string    `json:"method"`
	Path           string    `json:"path"`
	ClientIP       string    `json:"client_ip,omitempty"`
	Model          string    `json:"model,omitempty"`
	ProviderModel  string    `json:"provider_model,omitempty"`
	Account        string    `json:"account,omitempty"`
	Stream         bool      `json:"stream"`
	Status         int       `json:"status,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	DurationMS     int64     `json:"duration_ms"`
	QueueMS        int64     `json:"queue_ms,omitempty"`
	FirstEventMS   int64     `json:"first_event_ms,omitempty"`
	Stage          string    `json:"stage,omitempty"`
	Messages       int       `json:"messages,omitempty"`
	Media          int       `json:"media,omitempty"`
	Tools          int       `json:"tools,omitempty"`
	ToolCalls      int       `json:"tool_calls,omitempty"`
	FinishReason   string    `json:"finish_reason,omitempty"`
	Usage          *Usage    `json:"usage,omitempty"`
	Error          string    `json:"error,omitempty"`
	Attempts       []Attempt `json:"attempts,omitempty"`
	UpstreamBytes  int64     `json:"upstream_bytes,omitempty"`
	Heartbeats     int       `json:"heartbeats,omitempty"`
}

// Request 跟踪一次公开 API 请求从进入服务到结束的全部阶段
type Request struct {
	mu             sync.Mutex
	info           RequestInfo
	started        time.Time
	cancel         context.CancelCauseFunc
	begun          bool
	phase          requestPhase
	failed         bool
	canceled       bool
	detached       bool
	detachedCancel context.CancelCauseFunc

	attemptStarted time.Time
	phaseStarted   time.Time
	lastEvent      time.Time
	lastEventKind  string
	lastRead       time.Time
	attemptBytes   int64
	stallLogged    time.Time
	stalled        bool
	finished       bool
}

type requestContextKey struct{}

var active = struct {
	sync.Mutex
	requests map[string]*Request
}{requests: make(map[string]*Request)}

// NewRequest 登记一个进入服务的请求并返回带请求跟踪与取消函数的 context
func NewRequest(parent context.Context, method string, path string, clientIP string) (*Request, context.Context) {
	ctx, cancel := context.WithCancelCause(parent)
	now := time.Now()
	request := &Request{
		info: RequestInfo{
			ID: newRequestID(), State: StateRunning, Method: method, Path: path,
			ClientIP: clientIP, StartedAt: now,
		},
		started: now, cancel: cancel, phaseStarted: now,
	}
	active.Lock()
	active.requests[request.info.ID] = request
	active.Unlock()
	return request, context.WithValue(ctx, requestContextKey{}, request)
}

// FromContext 返回 context 中的请求跟踪，没有时返回 nil，nil 上的方法不执行任何操作
func FromContext(ctx context.Context) *Request {
	if ctx == nil {
		return nil
	}
	request, _ := ctx.Value(requestContextKey{}).(*Request)
	return request
}

// ID 返回请求 ID
func (r *Request) ID() string {
	if r == nil {
		return ""
	}
	return r.info.ID
}

// Begin 记录生成请求的协议、模型与输入概况并输出请求开始日志
func (r *Request) Begin(protocol string, model string, stream bool, messages int, tools int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.begun = true
	r.info.Protocol, r.info.Model, r.info.Stream = protocol, model, stream
	r.info.Messages, r.info.Tools = messages, tools
	r.phase, r.phaseStarted = phaseQueued, time.Now()
	fields := []any{"", protocol, "", model, "流式", stream, "消息", messages}
	if tools > 0 {
		fields = append(fields, "工具", tools)
	}
	r.publishLocked("INFO", SourceRequest, EventRequestStarted, Message("请求开始", fields...))
	r.mu.Unlock()
}

// SetMedia 记录本次请求上传的附件数
func (r *Request) SetMedia(count int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.info.Media = count
	r.mu.Unlock()
}

// SetResponseID 记录协议响应 ID
func (r *Request) SetResponseID(responseID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.info.ResponseID = responseID
	r.mu.Unlock()
}

// AccountReady 记录已取得账号请求槽，开始一次上游尝试
func (r *Request) AccountReady(account string, model string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.info.Account = account
	if r.info.QueueMS == 0 {
		r.info.QueueMS = now.Sub(r.started).Milliseconds()
	}
	r.attemptStarted, r.attemptBytes, r.lastRead = now, 0, time.Time{}
	r.lastEvent, r.lastEventKind = time.Time{}, ""
	r.info.Heartbeats = 0
	r.setPhaseLocked(phasePreparing, now)
	fields := []any{"模型", model}
	if waited := now.Sub(r.started); waited >= time.Second {
		fields = append(fields, "排队", waited)
	}
	if len(r.info.Attempts) > 0 {
		fields = append(fields, "尝试", len(r.info.Attempts)+1)
	}
	r.publishLocked("INFO", account, EventRequestProgress, Message("账号开始处理", fields...))
}

// Stage 记录上游准备阶段，例如上传附件与接受条款
func (r *Request) Stage(stage string, fields ...any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.info.Stage = stage
	r.publishLocked("INFO", r.sourceLocked(), EventRequestProgress, Message(stage, fields...))
}

// SetStage 记录当前阶段名，不输出日志，等待诊断与取消日志使用该名称
func (r *Request) SetStage(stage string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.info.Stage = stage
	r.mu.Unlock()
}

// UpstreamResponse 记录生成请求取得响应头
func (r *Request) UpstreamResponse(status int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.stalled {
		r.publishLocked("INFO", r.sourceLocked(), EventRequestProgress, Message("请求准备结束", "等待", now.Sub(r.phaseStarted), "响应头", status))
		r.stalled = false
	}
	r.info.Stage = "等待上游首事件"
	r.setPhaseLocked(phaseAwaiting, now)
}

// UpstreamRead 记录读取到的上游响应字节
func (r *Request) UpstreamRead(n int) {
	if r == nil || n <= 0 {
		return
	}
	r.mu.Lock()
	r.info.UpstreamBytes += int64(n)
	r.attemptBytes += int64(n)
	r.lastRead = time.Now()
	r.mu.Unlock()
}

// UpstreamHeartbeat 记录上游等待期间的心跳帧
func (r *Request) UpstreamHeartbeat() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.info.Heartbeats++
	r.mu.Unlock()
}

// UpstreamEvent 记录解码出的语义事件，kind 为 thought、text、code、media 等
func (r *Request) UpstreamEvent(kind string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.info.FirstEventMS == 0 {
		r.info.FirstEventMS = max(1, now.Sub(r.started).Milliseconds())
	}
	switch {
	case r.phase == phaseAwaiting || r.phase == phasePreparing:
		r.publishLocked("INFO", r.sourceLocked(), EventRequestProgress, Message("收到首个输出", "", eventName(kind), "等待", now.Sub(r.attemptStarted)))
	case r.stalled:
		r.publishLocked("INFO", r.sourceLocked(), EventRequestProgress, Message("事件流恢复", "停顿", now.Sub(r.lastEvent), "当前事件", eventName(kind)))
	}
	r.stalled = false
	r.lastEvent, r.lastEventKind = now, kind
	r.info.Stage = "接收输出"
	if r.phase != phaseStreaming {
		r.setPhaseLocked(phaseStreaming, now)
	}
}

// Background 记录生成转入后台，服务开始轮询成品
func (r *Request) Background(kind string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.info.Stage = "后台生成"
	r.setPhaseLocked(phaseBackground, time.Now())
	r.publishLocked("INFO", r.sourceLocked(), EventRequestProgress, Message("转入后台生成", "", kind))
}

// AttemptFailed 记录一次失败的上游尝试，retry 表示随后换账号重试
func (r *Request) AttemptFailed(account string, status int, err error, retry bool) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	message := SanitizeError(err.Error())
	duration := time.Duration(0)
	if !r.attemptStarted.IsZero() {
		duration = time.Since(r.attemptStarted)
	}
	r.info.Attempts = append(r.info.Attempts, Attempt{Account: account, Status: status, Error: message, DurationMS: duration.Milliseconds()})
	if retry {
		r.setPhaseLocked(phaseQueued, time.Now())
		r.publishLocked("WARN", account, EventRequestProgress, Message("账号切换", "模型", r.info.Model, "状态", status, "耗时", duration)+"\n原因: "+message)
	}
}

// SetUsage 记录 token 用量
func (r *Request) SetUsage(input int, reasoning int, reply int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.info.Usage = &Usage{
		InputTokens: input, ReasoningTokens: reasoning, ReplyTokens: reply,
		OutputTokens: reasoning + reply, TotalTokens: input + reasoning + reply,
	}
	r.mu.Unlock()
}

// SetResult 记录上游实际模型、会话与结束原因
func (r *Request) SetResult(providerModel string, conversationID string, finishReason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.info.ProviderModel, r.info.ConversationID = providerModel, conversationID
	if finishReason != "" {
		r.info.FinishReason = finishReason
	}
	r.mu.Unlock()
}

// SetToolCalls 记录本次输出的函数调用数
func (r *Request) SetToolCalls(count int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.info.ToolCalls = count
	r.mu.Unlock()
}

// Fail 记录请求的最终失败状态与原因
func (r *Request) Fail(status int, message string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.failed = true
	r.info.Status = status
	r.info.Error = SanitizeError(message)
	r.mu.Unlock()
}

// ErrCancelled 是管理端取消请求时请求 context 的取消原因
var ErrCancelled = errors.New("request cancelled by the service administrator")

// Cancel 取消请求
func (r *Request) Cancel() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.canceled = true
	detachedCancel := r.detachedCancel
	r.mu.Unlock()
	r.cancel(ErrCancelled)
	if detachedCancel != nil {
		detachedCancel(ErrCancelled)
	}
}

// Detach 标记请求与客户端连接分离，客户端断开后请求按实际结果结束，管理端取消时以 cancel 取消分离后的 context
func (r *Request) Detach(cancel context.CancelCauseFunc) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.detached = true
	r.detachedCancel = cancel
	r.mu.Unlock()
}

// Begun 报告请求是否为生成请求
func (r *Request) Begun() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.begun
}

// Snapshot 返回请求当前信息
func (r *Request) Snapshot() RequestInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(time.Now())
}

// Finish 按 HTTP 状态与记录的结果结束请求并输出结束日志，clientGone 表示客户端已断开
func (r *Request) Finish(httpStatus int, clientGone bool) RequestInfo {
	r.mu.Lock()
	if r.finished {
		info := r.snapshotLocked(time.Now())
		r.mu.Unlock()
		return info
	}
	r.finished = true
	now := time.Now()
	switch {
	case clientGone && !r.detached || r.canceled:
		r.info.State, r.info.Status = StateCancelled, StatusClientClosed
	case r.failed || httpStatus >= 400:
		r.info.State = StateFailed
		if r.info.Status == 0 {
			r.info.Status = httpStatus
		}
	case r.info.ToolCalls > 0:
		r.info.State, r.info.Status = StateToolCalls, httpStatus
	default:
		r.info.State, r.info.Status = StateCompleted, httpStatus
	}
	stage := r.info.Stage
	r.phase = phaseDone
	r.info.Stage = ""
	info := r.snapshotLocked(now)
	if r.begun {
		level, message := finishMessage(info, stage)
		r.publishLocked(level, r.sourceLocked(), EventRequestFinished, message)
	}
	begun := r.begun
	r.mu.Unlock()
	active.Lock()
	delete(active.requests, info.ID)
	active.Unlock()
	if begun {
		recent.Lock()
		recent.requests = append(recent.requests, info)
		if len(recent.requests) > recentLimit {
			recent.requests = recent.requests[len(recent.requests)-recentLimit:]
		}
		recent.Unlock()
	}
	r.cancel(nil)
	return info
}

func finishMessage(info RequestInfo, stage string) (string, string) {
	duration := time.Duration(info.DurationMS) * time.Millisecond
	fields := []any{"", info.Status, "", info.Model, "", duration}
	switch info.State {
	case StateCancelled:
		if stage != "" {
			fields = append(fields, "阶段", stage)
		}
		return "WARN", Message("请求取消", fields...)
	case StateFailed:
		message := Message("请求失败", fields...)
		if info.Error != "" {
			message += "\n原因: " + info.Error
		}
		if info.Status < 500 {
			return "WARN", message
		}
		return "ERROR", message
	}
	if info.Usage != nil {
		usage := fmt.Sprintf("输入 %d · 回复 %d", info.Usage.InputTokens, info.Usage.ReplyTokens)
		if info.Usage.ReasoningTokens > 0 {
			usage = fmt.Sprintf("输入 %d · 思考 %d · 回复 %d", info.Usage.InputTokens, info.Usage.ReasoningTokens, info.Usage.ReplyTokens)
		}
		fields = append(fields, "", usage)
		if seconds := duration.Seconds(); seconds > 0 {
			fields = append(fields, "", fmt.Sprintf("%.1f tokens/s", float64(info.Usage.OutputTokens)/seconds))
		}
	}
	if info.ToolCalls > 0 {
		fields = append(fields, "工具调用", info.ToolCalls)
	}
	if info.ProviderModel != "" && info.ProviderModel != info.Model {
		fields = append(fields, "实际模型", info.ProviderModel)
	}
	return "INFO", Message("请求完成", fields...)
}

func (r *Request) snapshotLocked(now time.Time) RequestInfo {
	info := r.info
	info.DurationMS = now.Sub(r.started).Milliseconds()
	info.Attempts = append([]Attempt(nil), r.info.Attempts...)
	if r.info.Usage != nil {
		usage := *r.info.Usage
		info.Usage = &usage
	}
	return info
}

func (r *Request) sourceLocked() string {
	if r.info.Account != "" {
		return r.info.Account
	}
	return SourceRequest
}

func (r *Request) setPhaseLocked(phase requestPhase, now time.Time) {
	r.phase, r.phaseStarted = phase, now
	r.stallLogged, r.stalled = time.Time{}, false
}

func (r *Request) publishLocked(level string, source string, name string, message string) {
	parsed, err := ParseLevel(level)
	if err != nil || !Enabled(parsed) {
		return
	}
	info := r.snapshotLocked(time.Now())
	Publish(Event{Level: level, Source: source, Message: message, Event: name, Request: &info})
}

// watch 在请求停留超过阈值时输出等待诊断
func (r *Request) watch(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.begun || r.finished {
		return
	}
	since := r.phaseStarted
	if r.phase == phaseStreaming {
		since = r.lastEvent
	}
	waited := now.Sub(since)
	if waited < stallThreshold || r.phase == phaseBackground {
		return
	}
	if !r.stallLogged.IsZero() && now.Sub(r.stallLogged) < stallRepeat {
		return
	}
	r.stallLogged, r.stalled = now, true
	waited = waited.Truncate(time.Second)
	network := []any{"心跳", r.info.Heartbeats, "网络字节", r.attemptBytes}
	if !r.lastRead.IsZero() {
		network = append(network, "最近网络", now.Sub(r.lastRead).Truncate(100*time.Millisecond))
	}
	var message string
	switch r.phase {
	case phaseQueued:
		message = Message("等待可用账号", "已等待", waited, "当前", r.stageOr("选择账号"), "模型", r.info.Model)
	case phasePreparing:
		message = Message("请求准备等待", "已等待", waited, "当前", r.stageOr("发送生成请求"), "模型", r.info.Model)
	case phaseAwaiting:
		message = Message("上游首事件等待", append([]any{"已等待", waited, "模型", r.info.Model}, network...)...)
	case phaseStreaming:
		message = Message("事件流停顿", append([]any{"已等待", waited, "最近事件", eventName(r.lastEventKind)}, network...)...)
	default:
		return
	}
	r.publishLocked("WARN", r.sourceLocked(), EventRequestProgress, message)
}

func (r *Request) stageOr(fallback string) string {
	if r.info.Stage != "" {
		return r.info.Stage
	}
	return fallback
}

// ActiveRequests 返回按开始时间排序的活动生成请求
func ActiveRequests() []RequestInfo {
	active.Lock()
	requests := make([]*Request, 0, len(active.requests))
	for _, request := range active.requests {
		requests = append(requests, request)
	}
	active.Unlock()
	result := make([]RequestInfo, 0, len(requests))
	for _, request := range requests {
		if request.Begun() {
			result = append(result, request.Snapshot())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt.Before(result[j].StartedAt) })
	return result
}

// recentLimit 是保留的最近结束生成请求数
const recentLimit = 20

// recent 按结束顺序保存最近结束的生成请求
var recent struct {
	sync.Mutex
	requests []RequestInfo
}

// RecentRequests 返回最近结束的生成请求，最新结束的在前
func RecentRequests() []RequestInfo {
	recent.Lock()
	defer recent.Unlock()
	result := make([]RequestInfo, len(recent.requests))
	for index, info := range recent.requests {
		result[len(result)-1-index] = info
	}
	return result
}

// ActiveCount 返回活动生成请求数
func ActiveCount() int {
	return len(ActiveRequests())
}

// CancelRequest 取消指定的活动请求
func CancelRequest(id string) bool {
	active.Lock()
	request := active.requests[id]
	active.Unlock()
	if request == nil {
		return false
	}
	request.Cancel()
	return true
}

// Watch 每秒检查一次活动请求的等待状态，直到 ctx 结束
func Watch(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			active.Lock()
			requests := make([]*Request, 0, len(active.requests))
			for _, request := range active.requests {
				requests = append(requests, request)
			}
			active.Unlock()
			for _, request := range requests {
				request.watch(now)
			}
		}
	}
}

func eventName(kind string) string {
	switch kind {
	case "thought":
		return "思考"
	case "text":
		return "正文"
	case "code":
		return "代码"
	case "media":
		return "媒体"
	case "image_progress":
		return "生图进度"
	case "":
		return "无"
	}
	return kind
}

func newRequestID() string {
	var value [6]byte
	_, _ = rand.Read(value[:])
	return "req_" + hex.EncodeToString(value[:])
}

// Truncate 把文本截断到 limit 个字符
func Truncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
