package adapter

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
)

// firstEventSampleLimit 是每个模型保留的近期原请求首事件样本数
const firstEventSampleLimit = 200

// firstEventSampleAge 是首事件样本参与统计的最长时间
const firstEventSampleAge = 24 * time.Hour

// firstEventMinSamples 是自适应阈值需要的最少样本数，不足时不发起备用请求
const firstEventMinSamples = 20

// hedgeLimit 是同时进行的备用请求数上限
const hedgeLimit = 4

// hedgeRecheck 是没有空闲账号时再次尝试发起备用请求的间隔
const hedgeRecheck = 2 * time.Second

// slotYieldAfter 是上游迟迟没有首事件时让出账号请求槽的等待时长，与上游频率限制的恢复时间相同
var slotYieldAfter = time.Minute

// FirstEventHedge 是首事件备用请求设置：Auto 按各模型近 24 小时原请求首事件等待的 p95 发起，否则 After 大于 0 时为固定等待阈值、为 0 时关闭；Models 为发起备用请求的模型 ID
type FirstEventHedge struct {
	Auto   bool
	After  time.Duration
	Models []string
}

// hedgeSettings 是当前的首事件备用请求设置
var hedgeSettings atomic.Pointer[FirstEventHedge]

func init() {
	hedgeSettings.Store(&FirstEventHedge{Auto: true, Models: []string{config.Defaults["FIRST_EVENT_HEDGE_MODELS"]}})
}

// SetFirstEventHedge 替换首事件备用请求设置，下一个请求生效
func SetFirstEventHedge(value FirstEventHedge) {
	value.Models = slices.Clone(value.Models)
	hedgeSettings.Store(&value)
}

// activeHedges 是进行中的备用请求数
var activeHedges atomic.Int32

// firstEventSample 是一次原请求从开始发送到首事件或被备用请求取代的时长
type firstEventSample struct {
	at   time.Time
	wait time.Duration
}

// firstEventStats 按模型保存近期原请求的首事件等待，备用请求不计入
type firstEventStats struct {
	mu      sync.Mutex
	samples map[string][]firstEventSample
	seeded  sync.Once
}

// firstEvents 是全部请求共用的首事件样本
var firstEvents = &firstEventStats{samples: make(map[string][]firstEventSample)}

// observe 记录一个原请求样本，每个模型只保留最近 firstEventSampleLimit 个
func (stats *firstEventStats) observe(model string, at time.Time, wait time.Duration) {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	samples := append(stats.samples[model], firstEventSample{at: at, wait: wait})
	stats.samples[model] = samples[max(0, len(samples)-firstEventSampleLimit):]
}

// p95 返回模型 firstEventSampleAge 内样本按最近秩法的 p95 与样本数
func (stats *firstEventStats) p95(model string, now time.Time) (time.Duration, int) {
	stats.mu.Lock()
	var waits []time.Duration
	for _, sample := range stats.samples[model] {
		if now.Sub(sample.at) <= firstEventSampleAge {
			waits = append(waits, sample.wait)
		}
	}
	stats.mu.Unlock()
	if len(waits) == 0 {
		return 0, 0
	}
	slices.Sort(waits)
	return waits[(len(waits)*95+99)/100-1], len(waits)
}

// seed 从请求账本补入 firstEventSampleAge 内的原请求样本：被备用请求取代的原请求取其已等时长，其余只含落败备用请求或没有尝试记录的取首事件减排队
func (stats *firstEventStats) seed(store *requestdb.Store) {
	stats.seeded.Do(func() {
		now := time.Now()
		page, err := store.Records(context.Background(), requestdb.UsageRecordQuery{From: now.Add(-firstEventSampleAge), To: now, Limit: firstEventSampleLimit * 50})
		if err != nil {
			logging.Warn(logging.SourceService, "读取首事件样本失败", "原因", err)
			return
		}
		slices.Reverse(page.Items)
		for _, record := range page.Items {
			model := config.MapModel(record.Model)
			superseded := slices.IndexFunc(record.Attempts, func(attempt logging.Attempt) bool { return attempt.Superseded && !attempt.Hedge })
			switch {
			case superseded >= 0:
				stats.observe(model, record.Time, time.Duration(record.Attempts[superseded].DurationMS)*time.Millisecond)
			case record.FirstEventMS > 0 && !slices.ContainsFunc(record.Attempts, func(attempt logging.Attempt) bool { return !attempt.Hedge }):
				stats.observe(model, record.Time, time.Duration(record.FirstEventMS-record.QueueMS)*time.Millisecond)
			}
		}
	})
}

// hedgeDelay 返回模型的原请求等待首事件多久后发起备用请求，不发起时第二个返回值为 false
func hedgeDelay(model string) (time.Duration, bool) {
	settings := hedgeSettings.Load()
	if !slices.Contains(settings.Models, model) {
		return 0, false
	}
	if !settings.Auto {
		return settings.After, settings.After > 0
	}
	p95, samples := firstEvents.p95(model, time.Now())
	return p95, samples >= firstEventMinSamples
}

// errSuperseded 表示另一方先返回首事件，本次尝试被取消
var errSuperseded = errors.New("另一方先返回首事件")

// attemptRunner 以指定账号执行一次生成，emit 接收这次生成的事件
type attemptRunner func(ctx context.Context, client *gemini.Client, accountID string, emit func(gemini.Event) error) (generationResult, error)

// generationAttempt 是一次正在进行的上游生成
type generationAttempt struct {
	accountID string
	hedge     bool
	trace     *logging.Request
	started   time.Time
	cancel    context.CancelFunc
	// claimed 在本次尝试取得客户端输出时关闭
	claimed chan struct{}
	done    chan struct{}
	result  generationResult
	err     error
	// panicked 是尝试中 panic 的值与堆栈，不为 nil 时由调用方 goroutine 重新抛出
	panicked any
}

// rethrow 在调用方 goroutine 重新抛出尝试中的 panic，交给 gin Recovery 返回 500
func (attempt *generationAttempt) rethrow() {
	if attempt.panicked != nil {
		panic(attempt.panicked)
	}
}

// outputGate 让原请求与备用请求中先出首个内容事件的一方独占客户端输出
type outputGate struct {
	mu     sync.Mutex
	racing bool
	winner *generationAttempt
	// usageSent 表示已输出生成开始时的输入用量，另一方的同类事件不再输出
	usageSent bool
}

// contentEvent 判断事件是否为上游开始输出回答的内容事件
func contentEvent(event gemini.Event) bool {
	switch event.Kind {
	case gemini.EventText, gemini.EventThought, gemini.EventCode, gemini.EventMedia, gemini.EventImageProgress:
		return event.Candidate == 0
	}
	return false
}

// emitter 返回尝试的事件输出：首个内容事件到达时争取客户端输出，取得后补发暂存事件；落败时取消本次尝试；没有备用请求时原请求的其余事件直接输出
func (g *outputGate) emitter(attempt *generationAttempt, onEvent func(gemini.Event) error) func(gemini.Event) error {
	var pending []gemini.Event
	forward := func(event gemini.Event) error {
		if usageOnly(event) {
			g.mu.Lock()
			sent := g.usageSent
			g.usageSent = true
			g.mu.Unlock()
			if sent {
				return nil
			}
		}
		if onEvent == nil {
			return nil
		}
		return onEvent(event)
	}
	return func(event gemini.Event) error {
		g.mu.Lock()
		if g.winner == nil && contentEvent(event) {
			g.winner = attempt
			close(attempt.claimed)
		}
		winner, racing := g.winner, g.racing
		g.mu.Unlock()
		switch {
		case winner == attempt:
			for _, held := range pending {
				if err := forward(held); err != nil {
					return err
				}
			}
			pending = nil
			return forward(event)
		case winner != nil:
			attempt.cancel()
			return errSuperseded
		case !racing && !attempt.hedge:
			return forward(event)
		}
		pending = append(pending, event)
		return nil
	}
}

// yieldingRelease 返回请求结束时的释放函数与首个内容事件到达时的通知函数：等待首事件超过 slotYieldAfter 时提前让出请求槽，让其他请求可以使用该账号
func yieldingRelease(client *gemini.Client, release func()) (func(), func()) {
	var mu sync.Mutex
	var endYield func()
	released := false
	timer := time.AfterFunc(slotYieldAfter, func() {
		mu.Lock()
		defer mu.Unlock()
		if released {
			return
		}
		end, ok := client.YieldRequest()
		if !ok {
			return
		}
		endYield, released = end, true
		release()
	})
	finish := func() {
		timer.Stop()
		mu.Lock()
		defer mu.Unlock()
		if endYield != nil {
			endYield()
		}
		if !released {
			released = true
			release()
		}
	}
	return finish, func() { timer.Stop() }
}

// startAttempt 在后台以 client 执行一次生成，结束时释放请求槽并按结果报告账号状态，被另一方取代或 panic 的尝试不报告
func startAttempt(ctx context.Context, pool *balancer.AccountPool, gate *outputGate, client *gemini.Client, accountID string, release func(), hedge bool, run attemptRunner, onEvent func(gemini.Event) error, model string) *generationAttempt {
	attemptCtx, cancel := context.WithCancel(ctx)
	attempt := &generationAttempt{
		accountID: accountID, hedge: hedge, trace: logging.FromContext(ctx), started: time.Now(), cancel: cancel,
		claimed: make(chan struct{}), done: make(chan struct{}),
	}
	emit := gate.emitter(attempt, onEvent)
	finish, firstContent := yieldingRelease(client, release)
	go func() {
		defer close(attempt.done)
		defer cancel()
		defer finish()
		defer func() {
			if recovered := recover(); recovered != nil {
				attempt.panicked = fmt.Sprintf("%v\n%s", recovered, debug.Stack())
			}
		}()
		attempt.result, attempt.err = run(attemptCtx, client, accountID, func(event gemini.Event) error {
			if contentEvent(event) {
				firstContent()
			}
			return emit(event)
		})
		switch {
		case attempt.err == nil:
			pool.ReportSuccess(accountID, model)
		case attemptCtx.Err() == nil && retryableAccountError(attempt.err) && !isOutputMismatchError(attempt.err):
			if usageLimitError(attempt.err) {
				_, _ = client.FetchUsage(attemptCtx)
			}
			pool.ReportFailure(accountID, model, attempt.err)
		}
	}()
	return attempt
}

// hedgeTarget 是原请求的备用请求计划：等待阈值、不能使用的账号（原请求在竞速中失败时加入其账号）与备用请求的执行方式
type hedgeTarget struct {
	delay    time.Duration
	excluded map[string]struct{}
	run      attemptRunner
}

// raceFirstEvent 以 client 执行原请求，target 不为 nil 时原请求等待首事件超过阈值后向空闲账号发起备用请求，先出首事件的一方继续输出，另一方取消并通知上游停止生成；返回胜出方或最后失败一方的结果与账号
func raceFirstEvent(ctx context.Context, pool *balancer.AccountPool, model string, client *gemini.Client, accountID string, release func(), run attemptRunner, onEvent func(gemini.Event) error, target *hedgeTarget) (generationResult, string, error) {
	trace := logging.FromContext(ctx)
	gate := &outputGate{}
	primary := startAttempt(ctx, pool, gate, client, accountID, release, false, run, onEvent, model)
	var hedge *generationAttempt
	var check <-chan time.Time
	if target != nil {
		timer := time.NewTimer(target.delay)
		defer timer.Stop()
		check = timer.C
	}
	defer func() {
		if hedge != nil {
			activeHedges.Add(-1)
		}
	}()
	primaryClaimed, primaryDone := primary.claimed, primary.done
	var hedgeClaimed, hedgeDone chan struct{}
	primaryWins := func() (generationResult, string, error) {
		if hedgeDone != nil {
			hedge.cancel()
			trace.HedgeSuperseded(hedge.accountID, time.Since(hedge.started), "原请求先返回首事件")
			trace.Progress("INFO", primary.accountID, "原请求胜出，取消备用请求", "备用账号", hedge.accountID)
		}
		<-primary.done
		primary.rethrow()
		return primary.result, primary.accountID, primary.err
	}
	hedgeWins := func() (generationResult, string, error) {
		waited := time.Since(primary.started)
		reason := ""
		if primaryDone != nil {
			primary.cancel()
			firstEvents.observe(model, time.Now(), waited)
			reason = "首事件等待 " + waited.Round(time.Second).String() + " 后被备用请求取代"
		}
		trace.AdoptHedge(hedge.trace, reason)
		trace.Progress("INFO", hedge.accountID, "备用请求胜出", "原请求已等待", waited.Round(time.Millisecond))
		<-hedge.done
		hedge.rethrow()
		return hedge.result, hedge.accountID, hedge.err
	}
	for {
		select {
		case <-primaryClaimed:
			firstEvents.observe(model, time.Now(), time.Since(primary.started))
			return primaryWins()
		case <-hedgeClaimed:
			return hedgeWins()
		case <-primaryDone:
			primary.rethrow()
			if ctx.Err() != nil {
				return primary.result, primary.accountID, primary.err
			}
			if primary.err == nil || hedgeDone == nil {
				return primaryWins()
			}
			primaryClaimed, primaryDone, check = nil, nil, nil
			target.excluded[primary.accountID] = struct{}{}
			trace.AttemptFailed(primary.accountID, upstreamStatus(primary.err), primary.err, false)
			trace.Progress("WARN", primary.accountID, "原请求失败，等待备用请求", "备用账号", hedge.accountID, "原因", logging.SanitizeError(primary.err.Error()))
		case <-hedgeDone:
			hedge.rethrow()
			if hedge.err == nil {
				return hedgeWins()
			}
			if primaryDone == nil {
				return hedge.result, hedge.accountID, hedge.err
			}
			hedgeClaimed, hedgeDone = nil, nil
			trace.HedgeSuperseded(hedge.accountID, time.Since(hedge.started), "备用请求失败："+logging.SanitizeError(hedge.err.Error()))
		case <-check:
			check = nil
			if activeHedges.Add(1) > hedgeLimit {
				activeHedges.Add(-1)
				check = time.After(hedgeRecheck)
				continue
			}
			excluded := map[string]struct{}{accountID: {}}
			for id := range target.excluded {
				excluded[id] = struct{}{}
			}
			hedgeClient, hedgeAccountID, hedgeRelease := pool.TryAcquireIdle(ctx, model, excluded, accountID)
			if hedgeClient == nil {
				activeHedges.Add(-1)
				check = time.After(hedgeRecheck)
				continue
			}
			gate.mu.Lock()
			gate.racing = true
			gate.mu.Unlock()
			trace.Progress("WARN", hedgeAccountID, "备用请求发起", "原请求已等待", time.Since(primary.started).Round(time.Millisecond), "阈值", target.delay.Round(time.Millisecond), "原账号", accountID)
			hedge = startAttempt(logging.WithRequest(ctx, trace.Hedge()), pool, gate, hedgeClient, hedgeAccountID, hedgeRelease, true, target.run, onEvent, model)
			hedgeClaimed, hedgeDone = hedge.claimed, hedge.done
		}
	}
}
