package admin

import (
	"context"
	"net/http"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/gin-gonic/gin"
)

// accountView 是管理端返回的账号状态与首页读取的登录邮箱
type accountView struct {
	balancer.AccountStatus
	// Email 是首页 oPEP7c 给出的登录邮箱，账号尚未初始化时为空
	Email string `json:"email,omitempty"`
}

// accountViews 返回全部账号的管理端状态
func (h *handler) accountViews() []accountView {
	accounts := h.Pool.Status().Accounts
	views := make([]accountView, len(accounts))
	for index, account := range accounts {
		views[index].AccountStatus = account
		if client := h.Pool.Client(account.AccountID); client != nil {
			views[index].Email = client.Email()
		}
	}
	return views
}

// account 返回单个账号的状态
func (h *handler) account(id string) (accountView, bool) {
	for _, account := range h.accountViews() {
		if account.AccountID == id {
			return account, true
		}
	}
	return accountView{}, false
}

// respondAccount 返回账号最新状态，操作失败时返回 400 与原因
func (h *handler) respondAccount(c *gin.Context, id string, err error) {
	if err != nil {
		writeError(c, http.StatusBadRequest, "account_operation_failed", err.Error())
		return
	}
	account, ok := h.account(id)
	if !ok {
		writeError(c, http.StatusNotFound, "account_not_found", "账号不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"account": account})
}

func (h *handler) updateAccount(c *gin.Context) {
	id := c.Param("id")
	if _, ok := h.account(id); !ok {
		writeError(c, http.StatusNotFound, "account_not_found", "账号不存在")
		return
	}
	var input struct {
		Enabled *bool   `json:"enabled"`
		Proxy   *string `json:"proxy"`
	}
	if !bindJSON(c, &input) {
		return
	}
	var err error
	if input.Proxy != nil {
		err = h.Accounts.SetProxy(id, trimmed(*input.Proxy))
	}
	if err == nil && input.Enabled != nil {
		err = h.Accounts.SetEnabled(id, *input.Enabled)
	}
	h.respondAccount(c, id, err)
}

func (h *handler) deleteAccount(c *gin.Context) {
	id := c.Param("id")
	if _, ok := h.account(id); !ok {
		writeError(c, http.StatusNotFound, "account_not_found", "账号不存在")
		return
	}
	if err := h.Accounts.Remove(id); err != nil {
		writeError(c, http.StatusInternalServerError, "account_operation_failed", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) verifyAccount(c *gin.Context) {
	id := c.Param("id")
	if _, ok := h.account(id); !ok {
		writeError(c, http.StatusNotFound, "account_not_found", "账号不存在")
		return
	}
	h.respondAccount(c, id, h.Accounts.Verify(id))
}

// chromeProfile 是本机 Chrome 中可导入的账号
type chromeProfile struct {
	ID          string `json:"id"`
	Profile     string `json:"profile"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Importable  bool   `json:"importable"`
	Imported    bool   `json:"imported"`
	Reason      string `json:"reason,omitempty"`
}

func (h *handler) chromeProfiles(c *gin.Context) {
	discovered, err := h.Accounts.ChromeAccounts()
	if err != nil {
		writeError(c, http.StatusInternalServerError, "chrome_unavailable", err.Error())
		return
	}
	profiles := make([]chromeProfile, 0, len(discovered))
	for _, account := range discovered {
		profiles = append(profiles, chromeProfile{
			ID: chromeauth.AccountID(account.Email), Profile: account.Profile, DisplayName: account.DisplayName,
			Email: account.Email, Importable: account.Importable, Imported: account.Imported, Reason: account.Reason,
		})
	}
	c.JSON(http.StatusOK, gin.H{"profiles": profiles})
}

// importChrome 在后台任务中逐个导入选中的本机 Chrome 账号，返回 202 与任务
func (h *handler) importChrome(c *gin.Context) {
	var input struct {
		Emails []string `json:"emails"`
		Proxy  string   `json:"proxy"`
	}
	if !bindJSON(c, &input) {
		return
	}
	if len(input.Emails) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "emails 不能为空")
		return
	}
	proxy := trimmed(input.Proxy)
	task := h.tasks.start(c.Request.Context(), "chrome", input.Emails, func(ctx context.Context, index int) error {
		_, err := h.Accounts.ImportChrome(ctx, input.Emails[index:index+1], proxy)
		return err
	})
	c.JSON(http.StatusAccepted, gin.H{"task": task})
}

// discoverCookie 识别 Cookie 中各会话登录的账号
func (h *handler) discoverCookie(c *gin.Context) {
	var input struct {
		Cookies string `json:"cookies"`
		Proxy   string `json:"proxy"`
	}
	if !bindJSON(c, &input) {
		return
	}
	found, err := h.Accounts.DiscoverCookie(c.Request.Context(), input.Cookies, trimmed(input.Proxy))
	if err != nil {
		writeError(c, http.StatusBadRequest, "cookie_discover_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"accounts": found})
}

// importCookie 在后台任务中逐个导入 Cookie 中选中会话的账号，账号 ID 取自邮箱，返回 202 与任务
func (h *handler) importCookie(c *gin.Context) {
	var input struct {
		Cookies  string `json:"cookies"`
		Proxy    string `json:"proxy"`
		Accounts []struct {
			Email    string `json:"email"`
			AuthUser int    `json:"auth_user"`
		} `json:"accounts"`
	}
	if !bindJSON(c, &input) {
		return
	}
	if len(input.Accounts) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "accounts 不能为空")
		return
	}
	labels := make([]string, len(input.Accounts))
	for index, account := range input.Accounts {
		labels[index] = account.Email
	}
	proxy := trimmed(input.Proxy)
	task := h.tasks.start(c.Request.Context(), "cookie", labels, func(ctx context.Context, index int) error {
		account := input.Accounts[index]
		_, err := h.Accounts.ImportCookie(ctx, input.Cookies, account.Email, proxy, account.AuthUser)
		return err
	})
	c.JSON(http.StatusAccepted, gin.H{"task": task})
}
