package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

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

// warmup 以选定的账号与模型发一条最小生成请求，登记为可从管理端停止的进行中请求并写入请求账本，成功后读取该账号的用量；停用账号以临时客户端预热，账号保持停用；管理端断开连接时预热照常完成
func (h *handler) warmup(c *gin.Context) {
	var input struct {
		Account string `json:"account"`
		Model   string `json:"model"`
	}
	if !bindJSON(c, &input) {
		return
	}
	view, ok := h.account(input.Account)
	if !ok {
		writeError(c, http.StatusNotFound, "account_not_found", "账号不存在")
		return
	}
	disabled := view.State == balancer.StateDisabled
	client := h.Pool.Client(input.Account)
	if !disabled && (client == nil || view.State != balancer.StateReady) {
		writeError(c, http.StatusConflict, "account_not_ready", "账号未就绪")
		return
	}
	request, ctx := logging.NewRequest(context.WithoutCancel(c.Request.Context()), http.MethodPost, c.Request.URL.Path, c.ClientIP())
	request.Begin("warmup", input.Model, true, 1, 0)
	var err error
	if disabled {
		request.SetStage("初始化停用账号")
		if client, err = h.Accounts.TemporaryClient(ctx, input.Account); err == nil {
			defer client.CloseIdleConnections()
		}
	}
	if err == nil {
		err = h.ping(ctx, request, client, input.Account, input.Model, !disabled)
	}
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

// ping 等到账号有空闲并发名额后经生成路径发送 warmupPrompt 并读完回答，report 为真时按结果更新账号与模型的冷却
func (h *handler) ping(ctx context.Context, request *logging.Request, client *gemini.Client, account string, model string, report bool) error {
	request.SetStage("等待账号")
	release, err := client.AcquireRequest(ctx)
	if err != nil {
		return err
	}
	defer release()
	request.AccountReady(account, model)
	resolved, err := client.ResolveModel(model)
	if err != nil {
		return err
	}
	var createdCIDs []string
	finish := ""
	err = client.Stream(ctx, gemini.GenerateRequest{
		Prompt: warmupPrompt,
		Model:  model,
		OnSession: func(snapshot gemini.ConversationSnapshot) {
			if snapshot.CID != "" {
				createdCIDs = append(createdCIDs, snapshot.CID)
			}
		},
	}, func(event gemini.Event) error {
		if event.Kind == gemini.EventError && event.Err != nil {
			return event.Err
		}
		if event.Kind == gemini.EventDone {
			finish = string(event.FinishReason)
		}
		return nil
	})
	if client.TemporaryChats() && resolved.Feature == gemini.FeatureTask && len(createdCIDs) > 0 {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		level := gemini.ThinkingLevel(0)
		if len(resolved.ThinkingLevels) > 0 {
			level = resolved.ThinkingLevels[0]
		}
		seen := make(map[string]struct{}, len(createdCIDs))
		for _, cid := range createdCIDs {
			if _, ok := seen[cid]; ok {
				continue
			}
			seen[cid] = struct{}{}
			if delErr := client.DeleteConversation(cleanupCtx, cid, resolved.Mode, resolved.Variant, level); delErr != nil {
				logging.Warn(account, "删除预热网页会话失败", "会话", cid, "错误", delErr)
			}
		}
	}
	var protocolErr *gemini.ProtocolError
	switch {
	case err == nil:
		request.SetResult(model, "", finish)
		if report {
			h.Pool.ReportSuccess(account, model)
		}
	case report && ctx.Err() == nil && (!errors.As(err, &protocolErr) || protocolErr.Retryable):
		h.Pool.ReportFailure(account, model, err)
	}
	return err
}
