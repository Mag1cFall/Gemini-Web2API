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

// keepaliveWriter 串行化流式写入，静默超过 keepaliveInterval 时写入保活帧
type keepaliveWriter struct {
	gin.ResponseWriter
	mu        sync.Mutex
	lastWrite time.Time
	frame     []byte
	stop      chan struct{}
	done      chan struct{}
}

// startKeepalive 替换 c.Writer 并开始发送保活帧，返回的函数停止发送并等待后台写入结束
func startKeepalive(c *gin.Context, frame []byte) func() {
	writer := &keepaliveWriter{
		ResponseWriter: c.Writer, lastWrite: time.Now(), frame: frame,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	c.Writer = writer
	go writer.run()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(writer.stop)
			<-writer.done
		})
	}
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
			if now.Sub(w.lastWrite) >= keepaliveInterval {
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
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Flush 刷新已写入的输出
func (w *keepaliveWriter) Flush() {
	_ = w.FlushError()
}
