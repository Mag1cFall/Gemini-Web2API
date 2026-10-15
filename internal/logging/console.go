package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/term"
)

// ANSI 颜色
const (
	colorReset   = "\x1b[0m"
	colorDim     = "\x1b[2m"
	colorBold    = "\x1b[1m"
	colorRed     = "\x1b[31m"
	colorGreen   = "\x1b[32m"
	colorYellow  = "\x1b[33m"
	colorBlue    = "\x1b[34m"
	colorMagenta = "\x1b[35m"
	colorCyan    = "\x1b[36m"
)

// maxSourceWidth 是来源列对齐的最大宽度
const maxSourceWidth = 32

var sourceWidth atomic.Int32

// SetSourceWidth 按最长的账号 ID 设置控制台来源列宽度
func SetSourceWidth(sources []string) {
	width := len(SourceService)
	for _, source := range sources {
		width = max(width, utf8.RuneCountInString(source))
	}
	sourceWidth.Store(int32(min(width, maxSourceWidth)))
}

type textConsole struct {
	writer io.Writer
	color  bool
}

// newTextConsole 创建可读文本控制台，输出到终端且未设置 NO_COLOR 时着色
func newTextConsole(writer io.Writer) *textConsole {
	color := false
	if file, ok := writer.(*os.File); ok && os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(file.Fd())) {
		color = enableVirtualTerminal(file)
	}
	return &textConsole{writer: writer, color: color}
}

func (c *textConsole) write(event Event) {
	var line strings.Builder
	c.paint(&line, colorDim, event.Time.Local().Format("01-02 15:04:05.000"))
	line.WriteByte(' ')
	c.paint(&line, levelColor(event.Level), padRight(event.Level, 5))
	line.WriteByte(' ')
	c.paint(&line, sourceColor(event.Source), padRight(event.Source, int(sourceWidth.Load())))
	line.WriteByte(' ')
	if event.Request != nil {
		c.paint(&line, colorDim, "#"+ShortID(event.Request.ID))
		line.WriteByte(' ')
	}
	first, rest, multiline := strings.Cut(event.Message, "\n")
	if event.Event == EventRequestFinished && event.Request != nil {
		stage, tail, found := strings.Cut(first, " | ")
		line.WriteString(stage)
		if found {
			line.WriteString(" | ")
			status, after, _ := strings.Cut(tail, " | ")
			c.paint(&line, statusColor(event.Request.Status), status)
			if after != "" {
				line.WriteString(" | ")
				line.WriteString(after)
			}
		}
	} else {
		line.WriteString(first)
	}
	if multiline {
		indent := strings.Repeat(" ", 25+int(sourceWidth.Load()))
		for _, next := range strings.Split(rest, "\n") {
			line.WriteByte('\n')
			line.WriteString(indent)
			c.paint(&line, colorDim, next)
		}
	}
	line.WriteByte('\n')
	_, _ = io.WriteString(c.writer, line.String())
}

func (c *textConsole) paint(builder *strings.Builder, color string, text string) {
	if !c.color || color == "" {
		builder.WriteString(text)
		return
	}
	builder.WriteString(color)
	builder.WriteString(text)
	builder.WriteString(colorReset)
}

func levelColor(level string) string {
	switch level {
	case "ERROR":
		return colorBold + colorRed
	case "WARN":
		return colorYellow
	case "INFO":
		return colorGreen
	}
	return colorMagenta
}

func sourceColor(source string) string {
	switch source {
	case SourceService:
		return colorBold
	case SourceRequest:
		return colorBlue
	}
	return colorCyan
}

func statusColor(status int) string {
	switch {
	case status >= 500:
		return colorBold + colorRed
	case status == StatusClientClosed:
		return colorDim
	case status >= 400:
		return colorYellow
	case status >= 200:
		return colorGreen
	}
	return ""
}

func padRight(text string, width int) string {
	count := utf8.RuneCountInString(text)
	if count >= width {
		return text
	}
	return text + strings.Repeat(" ", width-count)
}

// newJSONConsole 创建逐行 JSON 控制台
func newJSONConsole(writer io.Writer) func(Event) {
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug})
	return func(event Event) {
		level, _ := ParseLevel(event.Level)
		record := slog.NewRecord(event.Time, level, event.Message, 0)
		record.AddAttrs(slog.String("event", event.Event), slog.String("source", event.Source))
		if event.Request != nil {
			record.AddAttrs(slog.Any("request", event.Request))
		}
		_ = handler.Handle(context.Background(), record)
	}
}

// urlQueryPattern 匹配 URL 的查询串
var urlQueryPattern = regexp.MustCompile(`(https?://[^\s"?]+)\?[^\s"]*`)

// SanitizeError 去掉错误文本中 URL 的查询串，其中含会话参数
func SanitizeError(message string) string {
	return urlQueryPattern.ReplaceAllString(message, "$1")
}

// ShortID 返回请求 ID 在控制台显示的前 6 位
func ShortID(id string) string {
	id = strings.TrimPrefix(id, "req_")
	if len(id) > 6 {
		return id[:6]
	}
	return id
}
