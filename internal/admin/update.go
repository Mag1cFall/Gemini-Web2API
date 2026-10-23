package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// UpdateService 检查并安装 GitHub 上的新发行版本
type UpdateService interface {
	UpdateStatus(ctx context.Context, refresh bool) (UpdateStatus, error)
	ApplyUpdate(ctx context.Context) (UpdateStatus, error)
}

// UpdateStatus 是当前版本与 GitHub 最新发行版本，Mode 为 binary（可原地更新）、container（随镜像更新）或 development（源码构建）
type UpdateStatus struct {
	Current     string    `json:"current"`
	Latest      string    `json:"latest,omitempty"`
	Available   bool      `json:"available"`
	Mode        string    `json:"mode"`
	Notes       string    `json:"notes,omitempty"`
	URL         string    `json:"url,omitempty"`
	PublishedAt time.Time `json:"published_at,omitzero"`
	CheckedAt   time.Time `json:"checked_at,omitzero"`
	Error       string    `json:"error,omitempty"`
	Updating    bool      `json:"updating"`
}

// UpdateConflict 表示当前状态不能安装更新
type UpdateConflict string

func (err UpdateConflict) Error() string { return string(err) }

func (h *handler) updateStatus(c *gin.Context) {
	status, err := h.Updates.UpdateStatus(c.Request.Context(), c.Query("refresh") == "1")
	if err != nil {
		writeError(c, http.StatusBadGateway, "update_check_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *handler) applyUpdate(c *gin.Context) {
	status, err := h.Updates.ApplyUpdate(c.Request.Context())
	var conflict UpdateConflict
	switch {
	case errors.As(err, &conflict):
		writeError(c, http.StatusConflict, "update_conflict", err.Error())
	case err != nil:
		writeError(c, http.StatusBadGateway, "update_failed", err.Error())
	default:
		c.JSON(http.StatusAccepted, status)
	}
}
