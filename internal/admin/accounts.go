package admin

import (
	"net/http"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/gin-gonic/gin"
)

// account 返回单个账号的状态
func (h *handler) account(id string) (balancer.AccountStatus, bool) {
	for _, account := range h.Pool.Status().Accounts {
		if account.AccountID == id {
			return account, true
		}
	}
	return balancer.AccountStatus{}, false
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
			Email: account.Email, Importable: account.Importable, Imported: account.Imported,
		})
	}
	c.JSON(http.StatusOK, gin.H{"profiles": profiles})
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

func (h *handler) accountsByID(ids []string) []balancer.AccountStatus {
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	result := []balancer.AccountStatus{}
	for _, account := range h.Pool.Status().Accounts {
		if wanted[account.AccountID] {
			result = append(result, account)
		}
	}
	return result
}
