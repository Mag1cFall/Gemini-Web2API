package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/gin-gonic/gin"
)

// warmupPrompt 是预热请求发送的提示词
const warmupPrompt = "Hi"

// warmupResult 是一次预热的结果：是否生成成功、耗时、失败原因、请求记录 ID 与成功后读取的账号用量
type warmupResult struct {
	Account    string            `json:"account"`
	Model      string            `json:"model"`
	OK         bool              `json:"ok"`
	DurationMS int64             `json:"duration_ms"`
	Error      string            `json:"error,omitempty"`
	Request    string            `json:"request"`
	Usage      *gemini.UsageInfo `json:"usage,omitempty"`
}

// warmup 以选定的账号与模型发一条最小生成请求，登记为可从管理端停止的进行中请求并写入请求账本，成功后读取该账号的用量；管理端断开连接时预热照常完成
func (h *handler) warmup(c *gin.Context) {
	var input struct {
		Account string `json:"account"`
		Model   string `json:"model"`
	}
	if !bindJSON(c, &input) {
		return
	}
	client := h.Pool.Client(input.Account)
	if client == nil {
		writeError(c, http.StatusNotFound, "account_not_found", "账号不存在")
		return
	}
	if status, ok := h.account(input.Account); !ok || status.State != balancer.StateReady {
		writeError(c, http.StatusConflict, "account_not_ready", "账号未就绪")
		return
	}
	request, ctx := logging.NewRequest(context.WithoutCancel(c.Request.Context()), http.MethodPost, c.Request.URL.Path, c.ClientIP())
	request.Begin("warmup", input.Model, true, 1, 0)
	err := h.ping(ctx, request, client, input.Account, input.Model)
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadGateway
		var protocolErr *gemini.ProtocolError
		if errors.As(err, &protocolErr) && protocolErr.HTTPStatus >= 400 {
			status = protocolErr.HTTPStatus
		}
		request.Fail(status, err.Error())
	}
	info := request.Finish(status, false)
	h.Store.Record(info, nil)
	result := warmupResult{Account: input.Account, Model: input.Model, OK: err == nil, DurationMS: info.DurationMS, Request: info.ID}
	if err != nil {
		result.Error = info.Error
	} else if usage, err := client.FetchUsage(context.WithoutCancel(c.Request.Context())); err == nil {
		result.Usage = &usage
	}
	c.JSON(http.StatusOK, result)
}

// ping 等到账号有空闲并发名额后经生成路径发送 warmupPrompt 并读完回答，按结果更新账号与模型的冷却
func (h *handler) ping(ctx context.Context, request *logging.Request, client *gemini.Client, account string, model string) error {
	request.SetStage("等待账号")
	release, err := client.AcquireRequest(ctx)
	if err != nil {
		return err
	}
	defer release()
	request.AccountReady(account, model)
	if _, err := client.ResolveModel(model); err != nil {
		return err
	}
	finish := ""
	err = client.Stream(ctx, gemini.GenerateRequest{Prompt: warmupPrompt, Model: model}, func(event gemini.Event) error {
		if event.Kind == gemini.EventError && event.Err != nil {
			return event.Err
		}
		if event.Kind == gemini.EventDone {
			finish = string(event.FinishReason)
		}
		return nil
	})
	var protocolErr *gemini.ProtocolError
	switch {
	case err == nil:
		h.Pool.ReportSuccess(account, model)
		request.SetResult(model, "", finish)
	case ctx.Err() == nil && (!errors.As(err, &protocolErr) || protocolErr.Retryable):
		h.Pool.ReportFailure(account, model, err)
	}
	return err
}
