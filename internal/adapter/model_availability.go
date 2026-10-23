package adapter

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/gin-gonic/gin"
)

// modelRecoveryWindow 是全部账号冷却时请求排队等待的时限：最早恢复的账号在该时长内恢复时等待，否则返回 429
const modelRecoveryWindow = time.Minute

// errCatalogNotReady 是没有任何账号已就绪或已读取模型目录时的错误
var errCatalogNotReady = &gemini.ProtocolError{HTTPStatus: http.StatusServiceUnavailable, Message: "账号与访客会话的模型目录尚未就绪，请稍后重试"}

// modelKnown 判断模型能否识别：没有任何模型目录时为 503；已读取的目录都没有该模型时，有账号尚未读取目录为 503 并写明其状态，否则为 404
func modelKnown(pool *balancer.AccountPool, modelName string) error {
	availability := pool.ModelAvailability(config.MapModel(modelName))
	switch {
	case availability.Known:
		return nil
	case !availability.Catalog:
		return errCatalogNotReady
	case len(availability.Pending) > 0:
		return &gemini.ProtocolError{HTTPStatus: http.StatusServiceUnavailable, Message: fmt.Sprintf("模型 %q 不在已读取的模型目录中，尚未读取模型目录的账号：%s", modelName, strings.Join(availability.Pending, "；"))}
	}
	return &gemini.ProtocolError{HTTPStatus: http.StatusNotFound, Message: modelNotFoundMessage(pool, modelName)}
}

// awaitModel 判断模型能否调度：识别失败按 modelKnown，支持该模型的账号都未就绪时为 503 并列出原因，全部冷却时最早恢复的账号在 modelRecoveryWindow 内恢复则等待，否则为 429
func awaitModel(c *gin.Context, pool *balancer.AccountPool, modelName string) error {
	modelID := config.MapModel(modelName)
	for {
		changed := pool.Changed()
		availability := pool.ModelAvailability(modelID)
		switch {
		case availability.Ready:
			return nil
		case !availability.Catalog || !availability.Known:
			return modelKnown(pool, modelName)
		}
		recoverAt := pool.RetryAfter(modelID)
		if recoverAt.IsZero() {
			return &gemini.ProtocolError{HTTPStatus: http.StatusServiceUnavailable, Message: fmt.Sprintf("模型 %q 暂无可用账号：%s", modelName, strings.Join(availability.Reasons, "；"))}
		}
		wait := time.Until(recoverAt)
		if wait > modelRecoveryWindow {
			return &gemini.ProtocolError{HTTPStatus: http.StatusTooManyRequests, Message: fmt.Sprintf("模型 %q 的账号都在冷却，%s 后恢复：%s", modelName, wait.Round(time.Second), strings.Join(availability.Reasons, "；"))}
		}
		timer := time.NewTimer(wait)
		select {
		case <-c.Request.Context().Done():
			timer.Stop()
			return &gemini.ProtocolError{HTTPStatus: logging.StatusClientClosed, Message: "客户端在等待账号恢复时断开"}
		case <-changed:
		case <-timer.C:
		}
		timer.Stop()
	}
}
