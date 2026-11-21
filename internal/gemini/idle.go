package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

// upstreamIdleTimeout 是上游请求连续没有收发数据的最长时长，等待响应头同样计入，收到响应头重新计时，持续收发数据的请求不限总时长
var upstreamIdleTimeout = 600 * time.Second

// errUpstreamIdle 表示上游请求长时间没有收发数据
var errUpstreamIdle = errors.New("官网连接长时间没有收发数据")

// doIdle 以 do 发出请求，请求连续 idle 没有收发数据时取消
func doIdle(do func(*http.Request) (*http.Response, error), req *http.Request, idle time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	timer := time.AfterFunc(idle, func() { cancel(errUpstreamIdle) })
	req = req.WithContext(ctx)
	if req.Body != nil && req.Body != http.NoBody {
		req.Body = &idleReader{ReadCloser: req.Body, ctx: ctx, timer: timer, idle: idle}
	}
	resp, err := do(req)
	if err != nil {
		timer.Stop()
		cancel(nil)
		return nil, idleError(ctx, idle, err)
	}
	timer.Reset(idle)
	resp.Body = &idleReader{ReadCloser: resp.Body, ctx: ctx, timer: timer, idle: idle, done: func() {
		timer.Stop()
		cancel(nil)
	}}
	return resp, nil
}

// idleReader 是每读到数据就重新计时的请求或响应正文，done 在响应正文关闭时结束计时
type idleReader struct {
	io.ReadCloser
	ctx   context.Context
	timer *time.Timer
	idle  time.Duration
	done  func()
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		r.timer.Reset(r.idle)
	}
	if err != nil && err != io.EOF {
		err = idleError(r.ctx, r.idle, err)
	}
	return n, err
}

func (r *idleReader) Close() error {
	if r.done != nil {
		r.done()
	}
	return r.ReadCloser.Close()
}

// idleError 在请求因长时间没有收发数据被取消时返回写明时长的错误，其余错误原样返回
func idleError(ctx context.Context, idle time.Duration, err error) error {
	if !errors.Is(context.Cause(ctx), errUpstreamIdle) {
		return err
	}
	return fmt.Errorf("%w（%s）", errUpstreamIdle, idle)
}

// rpcTimeout 是单个 batchexecute RPC 的时限
const rpcTimeout = 600 * time.Second

// streamIdleTimeout 是生成流距上一个语义事件的最长等待，心跳不算进展
var streamIdleTimeout = 600 * time.Second

// errStreamIdle 表示生成流长时间没有语义事件
var errStreamIdle = errors.New("官网的回答长时间没有新内容")

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
		Message: fmt.Sprintf("官网的回答 %s 没有新内容", streamIdleTimeout),
	}
}
