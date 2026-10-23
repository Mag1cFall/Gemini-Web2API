package adapter

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// keepaliveInterval 是流式响应发送保活帧前允许的最长静默
const keepaliveInterval = 10 * time.Second

// 各协议的 SSE 保活帧
var (
	openAIKeepalive    = []byte(": ping\n\n")
	anthropicKeepalive = []byte("event: ping\ndata: {\"type\": \"ping\"}\n\n")
	geminiKeepalive    = []byte("\n")
)

// keepaliveWriter 串行化流式写入，静默超过 keepaliveInterval 时写入保活帧；held 期间的写入暂存在 buffer
type keepaliveWriter struct {
	gin.ResponseWriter
	mu        sync.Mutex
	lastWrite time.Time
	started   time.Time
	frame     []byte
	held      bool
	buffer    []byte
	stop      chan struct{}
	done      chan struct{}
}

// startKeepalive 替换 c.Writer 并开始发送保活帧，返回的函数停止发送并等待后台写入结束
func startKeepalive(c *gin.Context, frame []byte) func() {
	_, stop := startStreamWriter(c, frame, false)
	return stop
}

// holdStream 与 startKeepalive 相同，但 release 或开始 keepaliveInterval 之后才写出响应头与暂存的输出
func holdStream(c *gin.Context, frame []byte) (*keepaliveWriter, func()) {
	return startStreamWriter(c, frame, true)
}

// startStreamWriter 替换 c.Writer 并开始发送保活帧，返回写入器与停止函数，停止时写出仍暂存的输出
func startStreamWriter(c *gin.Context, frame []byte, held bool) (*keepaliveWriter, func()) {
	now := time.Now()
	writer := &keepaliveWriter{
		ResponseWriter: c.Writer, lastWrite: now, started: now, frame: frame, held: held,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	c.Writer = writer
	go writer.run()
	var once sync.Once
	return writer, func() {
		once.Do(func() {
			close(writer.stop)
			<-writer.done
			writer.release()
		})
	}
}

// release 写出暂存的输出，此后的写入直接发出
func (w *keepaliveWriter) release() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.releaseLocked()
}

func (w *keepaliveWriter) releaseLocked() {
	if !w.held {
		return
	}
	w.held = false
	if _, err := w.ResponseWriter.Write(w.buffer); err == nil {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
	}
	w.buffer = nil
}

// reject 在输出仍暂存时丢弃暂存内容、流式响应头与保活帧并返回 true，调用方随后按非流式写出错误
func (w *keepaliveWriter) reject() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.held {
		return false
	}
	w.held, w.buffer, w.frame = false, nil, nil
	for _, name := range []string{"Content-Type", "Cache-Control", "Connection"} {
		w.Header().Del(name)
	}
	return true
}

func (w *keepaliveWriter) run() {
	defer close(w.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case now := <-ticker.C:
			w.mu.Lock()
			if w.held && now.Sub(w.started) >= keepaliveInterval {
				w.releaseLocked()
			}
			if !w.held && w.frame != nil && now.Sub(w.lastWrite) >= keepaliveInterval {
				w.lastWrite = now
				if _, err := w.ResponseWriter.Write(w.frame); err == nil {
					_ = http.NewResponseController(w.ResponseWriter).Flush()
				}
			}
			w.mu.Unlock()
		}
	}
}

// Write 写入一段流式输出
func (w *keepaliveWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastWrite = time.Now()
	if w.held {
		w.buffer = append(w.buffer, data...)
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}

// WriteString 写入一段流式输出
func (w *keepaliveWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

// FlushError 刷新已写入的输出
func (w *keepaliveWriter) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.held {
		return nil
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Flush 刷新已写入的输出
func (w *keepaliveWriter) Flush() {
	_ = w.FlushError()
}
