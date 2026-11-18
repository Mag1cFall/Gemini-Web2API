package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

// pairingAccount 凭配对令牌或账号的续签凭据导入 setup --remote 上传的认证状态，不经过管理会话；凭配对令牌导入时为账号签发续签凭据
func (h *handler) pairingAccount(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	token, _ := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	token = strings.TrimSpace(token)
	paired := h.pairing.valid(token, time.Now())
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, pairingBodyLimit)
	var input struct {
		StorageState json.RawMessage `json:"storage_state"`
	}
	if !bindJSON(c, &input) {
		return
	}
	if !paired && !h.renewalValid(token, input.StorageState) {
		writeError(c, http.StatusUnauthorized, "pairing_token_invalid", "配对令牌无效或已过期")
		return
	}
	id, err := h.Accounts.ImportState(input.StorageState)
	if err != nil {
		writeError(c, http.StatusBadRequest, "account_import_failed", err.Error())
		return
	}
	account, _ := h.account(id)
	response := gin.H{"account": account}
	if paired {
		renewal := rand.Text()
		digest := sha256.Sum256([]byte(renewal))
		if err := h.Accounts.SetPairingRenewal(id, hex.EncodeToString(digest[:])); err != nil {
			writeError(c, http.StatusInternalServerError, "pairing_renewal_failed", err.Error())
			return
		}
		response["renewal_token"] = renewal
	}
	c.JSON(http.StatusCreated, response)
}

// renewalValid 判断 token 是否为上传的认证状态所属账号的续签凭据
func (h *handler) renewalValid(token string, state json.RawMessage) bool {
	var parsed struct {
		Metadata struct {
			ID string `json:"id"`
		} `json:"geminiWeb2api"`
	}
	if json.Unmarshal(state, &parsed) != nil {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare([]byte(h.Accounts.PairingRenewal(parsed.Metadata.ID)), []byte(hex.EncodeToString(digest[:]))) == 1
}
