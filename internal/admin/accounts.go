package admin

import (
	"net/http"
	"strings"

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

// browserProfile 是本机浏览器中可导入的账号
type browserProfile struct {
	ID          string `json:"id"`
	Browser     string `json:"browser,omitempty"`
	Profile     string `json:"profile"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	AuthUser    int    `json:"auth_user,omitempty"`
	Importable  bool   `json:"importable"`
	Imported    bool   `json:"imported"`
	Reason      string `json:"reason,omitempty"`
}

type chromeProfile = browserProfile

func (h *handler) browserProfiles(c *gin.Context) {
	browser := strings.ToLower(strings.TrimSpace(c.DefaultQuery("browser", c.DefaultQuery("type", "chrome"))))
	if browser == "" {
		browser = "chrome"
	}
	discovered, err := h.Accounts.BrowserAccounts(browser)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "browser_unavailable", err.Error())
		return
	}
	profiles := make([]browserProfile, 0, len(discovered))
	for _, account := range discovered {
		id := chromeauth.AccountID(account.Email)
		if id == "" {
			id = chromeauth.AccountID(account.Profile)
		}
		profiles = append(profiles, browserProfile{
			ID: id, Browser: browser, Profile: account.Profile, DisplayName: account.DisplayName,
			Email: account.Email, AuthUser: account.AuthUser, Importable: account.Importable, Imported: account.Imported, Reason: account.Reason,
		})
	}
	c.JSON(http.StatusOK, gin.H{"browser": browser, "profiles": profiles})
}

func (h *handler) chromeProfiles(c *gin.Context) {
	discovered, err := h.Accounts.ChromeAccounts()
	if err != nil {
		writeError(c, http.StatusInternalServerError, "chrome_unavailable", err.Error())
		return
	}
	profiles := make([]browserProfile, 0, len(discovered))
	for _, account := range discovered {
		id := chromeauth.AccountID(account.Email)
		if id == "" {
			id = chromeauth.AccountID(account.Profile)
		}
		profiles = append(profiles, browserProfile{
			ID: id, Browser: "chrome", Profile: account.Profile, DisplayName: account.DisplayName,
			Email: account.Email, AuthUser: account.AuthUser, Importable: account.Importable, Imported: account.Imported, Reason: account.Reason,
		})
	}
	c.JSON(http.StatusOK, gin.H{"profiles": profiles})
}

func (h *handler) importBrowser(c *gin.Context) {
	var input struct {
		Browser string   `json:"browser"`
		Emails  []string `json:"emails"`
		Targets []string `json:"targets"`
		Proxy   string   `json:"proxy"`
	}
	if !bindJSON(c, &input) {
		return
	}
	targets := input.Targets
	if len(targets) == 0 {
		targets = input.Emails
	}
	if len(targets) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "targets 不能为空")
		return
	}
	browser := strings.ToLower(strings.TrimSpace(input.Browser))
	if browser == "" {
		browser = "chrome"
	}
	ids, failures, err := h.Accounts.ImportBrowser(c.Request.Context(), browser, targets, trimmed(input.Proxy))
	if err != nil && len(ids) == 0 {
		writeError(c, http.StatusBadRequest, "account_import_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"accounts": h.accountsByID(ids), "failures": failures})
}

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
	ids, err := h.Accounts.ImportChrome(c.Request.Context(), input.Emails, trimmed(input.Proxy))
	if err != nil {
		writeError(c, http.StatusBadRequest, "account_import_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"accounts": h.accountsByID(ids)})
}

func (h *handler) importCookie(c *gin.Context) {
	var input struct {
		ID       string `json:"id"`
		Cookies  string `json:"cookies"`
		Proxy    string `json:"proxy"`
		AuthUser int    `json:"authuser"`
	}
	if !bindJSON(c, &input) {
		return
	}
	id, err := h.Accounts.ImportCookie(c.Request.Context(), input.Cookies, trimmed(input.ID), trimmed(input.Proxy), input.AuthUser)
	if err != nil {
		writeError(c, http.StatusBadRequest, "account_import_failed", err.Error())
		return
	}
	h.respondAccount(c, id, nil)
}

func (h *handler) accountsByID(ids []string) []accountView {
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	result := []accountView{}
	for _, account := range h.accountViews() {
		if wanted[account.AccountID] {
			result = append(result, account)
		}
	}
	return result
}
