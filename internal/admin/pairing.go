package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// pairingLifetime 是远程配对令牌的有效期
const pairingLifetime = 10 * time.Minute

// pairingBodyLimit 是配对上传的认证状态大小上限
const pairingBodyLimit = 4 << 20

// pairingToken 是远程配对令牌与到期时间
type pairingToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// pairingTokens 保存当前唯一有效配对令牌的摘要
type pairingTokens struct {
	mu      sync.Mutex
	digest  [sha256.Size]byte
	expires time.Time
}

// issue 生成新令牌并使旧令牌失效
func (tokens *pairingTokens) issue(now time.Time) pairingToken {
	token := rand.Text()
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	tokens.digest = sha256.Sum256([]byte(token))
	tokens.expires = now.Add(pairingLifetime)
	return pairingToken{Token: token, ExpiresAt: tokens.expires}
}

// valid 判断令牌是否为当前未过期的配对令牌
func (tokens *pairingTokens) valid(token string, now time.Time) bool {
	digest := sha256.Sum256([]byte(token))
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	return now.Before(tokens.expires) && subtle.ConstantTimeCompare(digest[:], tokens.digest[:]) == 1
}

func (h *handler) createPairing(c *gin.Context) {
	c.JSON(http.StatusCreated, h.pairing.issue(time.Now()))
}

// pairingAccount 凭配对令牌导入 setup --remote 上传的认证状态，不经过管理会话
func (h *handler) pairingAccount(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	token, _ := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if !h.pairing.valid(strings.TrimSpace(token), time.Now()) {
		writeError(c, http.StatusUnauthorized, "pairing_token_invalid", "配对令牌无效或已过期")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, pairingBodyLimit)
	var input struct {
		StorageState json.RawMessage `json:"storage_state"`
	}
	if !bindJSON(c, &input) {
		return
	}
	id, err := h.Accounts.ImportState(input.StorageState)
	if err != nil {
		writeError(c, http.StatusBadRequest, "account_import_failed", err.Error())
		return
	}
	account, _ := h.account(id)
	c.JSON(http.StatusCreated, gin.H{"account": account})
}
