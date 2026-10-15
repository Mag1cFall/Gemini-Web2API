package gemini

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// requestCeilingSeconds 是单个上游 HTTP 请求的总时限上限
const requestCeilingSeconds = 3600

// rpcTimeout 是单个 batchexecute RPC 的时限
const rpcTimeout = 600 * time.Second

// streamIdleTimeout 是生成流距上一个语义事件的最长等待，心跳不算进展
var streamIdleTimeout = 600 * time.Second

// errStreamIdle 表示生成流长时间没有语义事件
var errStreamIdle = errors.New("gemini web stream idle")

// idleKey 是生成流上下文中最近进展时间的键
type idleKey struct{}

// watchStreamIdle 返回在 streamIdleTimeout 内没有进展时取消的上下文
func watchStreamIdle(parent context.Context) (context.Context, func()) {
	last := &atomic.Int64{}
	last.Store(time.Now().UnixNano())
	ctx, cancel := context.WithCancelCause(context.WithValue(parent, idleKey{}, last))
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if now.Sub(time.Unix(0, last.Load())) >= streamIdleTimeout {
					cancel(errStreamIdle)
					return
				}
			}
		}
	}()
	return ctx, func() {
		close(done)
		cancel(nil)
	}
}

// progress 记录生成流的一次进展
func progress(ctx context.Context) {
	if last, ok := ctx.Value(idleKey{}).(*atomic.Int64); ok {
		last.Store(time.Now().UnixNano())
	}
}

// streamIdleError 在生成流因长时间没有进展被取消时返回可换号重试的上游错误
func streamIdleError(ctx context.Context) error {
	if !errors.Is(context.Cause(ctx), errStreamIdle) {
		return nil
	}
	return &ProtocolError{
		HTTPStatus: 504, Code: 504, Retryable: true, Cause: errStreamIdle,
		Message: fmt.Sprintf("Gemini Web 生成流 %s 没有新内容，只有心跳", streamIdleTimeout),
	}
}
