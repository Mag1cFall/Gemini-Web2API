// Package logging 提供运行日志事件、控制台输出、请求跟踪与事件订阅
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
	"gopkg.in/natefinch/lumberjack.v2"
)

// historyLimit 是保留供管理端回放的最近事件数
const historyLimit = 2000

// subscriberBuffer 是单个订阅者的待发事件上限，写满时断开该订阅者
const subscriberBuffer = 1024

// 事件来源
const (
	SourceService = "service"
	SourceRequest = "request"
)

// 稳定事件名
const (
	EventMessage         = "runtime.message"
	EventRequestStarted  = "request.started"
	EventRequestProgress = "request.progress"
	EventRequestFinished = "request.finished"
)

// Event 是一条运行日志事件
type Event struct {
	Time    time.Time    `json:"time"`
	Level   string       `json:"level"`
	Source  string       `json:"source"`
	Message string       `json:"message"`
	Event   string       `json:"event"`
	Request *RequestInfo `json:"request,omitempty"`
}

// Options 是日志输出配置
type Options struct {
	// Level 是最低输出级别，取值 debug、info、warn、error
	Level string
	// Format 是控制台格式，取值 text 或 json
	Format string
	// Writer 是控制台输出位置
	Writer io.Writer
}

// Hub 保存最近事件，把新事件写到控制台并分发给订阅者
type Hub struct {
	mu          sync.Mutex
	level       slog.Level
	console     func(Event)
	history     []Event
	subscribers map[chan Event]struct{}
	// format 是控制台格式，日志文件使用同一格式
	format string
	// file 是开启时的轮转日志文件，fileWrite 把事件写入其中
	file      *lumberjack.Logger
	fileWrite func(Event)
}

var std = newHub()

func newHub() *Hub {
	return &Hub{level: slog.LevelInfo, console: func(Event) {}, subscribers: make(map[chan Event]struct{})}
}

// Setup 按配置设置默认日志输出
func Setup(options Options) error {
	level, err := ParseLevel(options.Level)
	if err != nil {
		return err
	}
	writer := options.Writer
	if writer == nil {
		writer = os.Stderr
	}
	if file, ok := writer.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		disableQuickEdit()
	}
	var console func(Event)
	format := strings.ToLower(strings.TrimSpace(options.Format))
	switch format {
	case "", "text":
		console = newTextConsole(writer).write
	case "json":
		console = newJSONConsole(writer)
	default:
		return fmt.Errorf("LOG_FORMAT 必须是 text 或 json")
	}
	std.mu.Lock()
	std.level, std.console, std.format = level, console, format
	std.mu.Unlock()
	return nil
}

// ParseLevel 解析日志级别名称
func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("LOG_LEVEL 必须是 debug、info、warn 或 error")
}

// SetLevel 修改最低输出级别
func SetLevel(level slog.Level) {
	std.mu.Lock()
	std.level = level
	std.mu.Unlock()
}

// Enabled 报告该级别的事件是否会输出
func Enabled(level slog.Level) bool {
	std.mu.Lock()
	defer std.mu.Unlock()
	return level >= std.level
}

// Publish 记录事件，低于最低级别的事件丢弃
func Publish(event Event) {
	level, err := ParseLevel(event.Level)
	if err != nil {
		level = slog.LevelInfo
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	if event.Event == "" {
		event.Event = EventMessage
	}
	std.mu.Lock()
	defer std.mu.Unlock()
	if level < std.level {
		return
	}
	std.console(event)
	if std.fileWrite != nil {
		std.fileWrite(event)
	}
	std.history = append(std.history, event)
	if len(std.history) > historyLimit {
		std.history = append(std.history[:0:0], std.history[len(std.history)-historyLimit:]...)
	}
	for subscriber := range std.subscribers {
		select {
		case subscriber <- event:
		default:
			delete(std.subscribers, subscriber)
			close(subscriber)
		}
	}
}

// Subscribe 返回最近 limit 条事件与后续事件通道，通道在订阅者跟不上或取消时关闭
func Subscribe(limit int) ([]Event, <-chan Event, func()) {
	std.mu.Lock()
	defer std.mu.Unlock()
	start := max(0, len(std.history)-limit)
	history := append([]Event(nil), std.history[start:]...)
	channel := make(chan Event, subscriberBuffer)
	std.subscribers[channel] = struct{}{}
	cancel := func() {
		std.mu.Lock()
		defer std.mu.Unlock()
		if _, ok := std.subscribers[channel]; ok {
			delete(std.subscribers, channel)
			close(channel)
		}
	}
	return history, channel, cancel
}

// Clear 清空保留的事件
func Clear() {
	std.mu.Lock()
	std.history = nil
	std.mu.Unlock()
}

// Debug 记录调试事件
func Debug(source string, stage string, fields ...any) {
	log(slog.LevelDebug, source, stage, fields)
}

// Info 记录状态推进与完成结果
func Info(source string, stage string, fields ...any) {
	log(slog.LevelInfo, source, stage, fields)
}

// Warn 记录等待、切换与可恢复的失败
func Warn(source string, stage string, fields ...any) {
	log(slog.LevelWarn, source, stage, fields)
}

// Error 记录失败结果
func Error(source string, stage string, fields ...any) {
	log(slog.LevelError, source, stage, fields)
}

func log(level slog.Level, source string, stage string, fields []any) {
	if !Enabled(level) {
		return
	}
	Publish(Event{Level: levelName(level), Source: source, Message: Message(stage, fields...)})
}

// Message 把阶段名与键值对拼成“阶段 | 键=值”，键为空字符串时只写值
func Message(stage string, fields ...any) string {
	var builder strings.Builder
	builder.WriteString(stage)
	for index := 0; index+1 < len(fields); index += 2 {
		key := fmt.Sprint(fields[index])
		builder.WriteString(" | ")
		if key != "" {
			builder.WriteString(key)
			builder.WriteByte('=')
		}
		builder.WriteString(FormatValue(fields[index+1]))
	}
	return builder.String()
}

// FormatValue 把日志字段值格式化为短文本
func FormatValue(value any) string {
	switch typed := value.(type) {
	case time.Duration:
		return FormatDuration(typed)
	case bool:
		if typed {
			return "是"
		}
		return "否"
	case error:
		return SanitizeError(typed.Error())
	case time.Time:
		return typed.Local().Format("15:04:05")
	}
	return fmt.Sprint(value)
}

// FormatDuration 把时长四舍五入到毫秒，超过一分钟时到 100 毫秒
func FormatDuration(duration time.Duration) string {
	if duration >= time.Minute {
		return duration.Round(100 * time.Millisecond).String()
	}
	return duration.Round(time.Millisecond).String()
}

func levelName(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "ERROR"
	case level >= slog.LevelWarn:
		return "WARN"
	case level >= slog.LevelInfo:
		return "INFO"
	}
	return "DEBUG"
}

// History 返回保留的最近事件
func History() []Event {
	std.mu.Lock()
	defer std.mu.Unlock()
	return append([]Event(nil), std.history...)
}
