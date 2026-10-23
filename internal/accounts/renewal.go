package accounts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// chromeRenewal 用保存的续签材料签发 Cookie，材料被拒时从本机 Chrome 读取同一 Gaia ID 账号的新材料并写回认证状态
type chromeRenewal struct {
	mu       sync.Mutex
	id       string
	material auth.OAuthMaterial
	profile  string
	proxy    string
	file     *auth.File
	// tried 是 Chrome 中已确认被拒的 refresh token 密文
	tried []byte
}

// newChromeRenewal 返回账号的续签器
func newChromeRenewal(account auth.LoadedAccount) *chromeRenewal {
	return &chromeRenewal{id: account.ID, material: *account.OAuth, profile: account.Profile, proxy: account.ProxyURL, file: account.File}
}

// refresh 签发 Cookie，保存的材料被拒且 Chrome 中的材料已更新时改用 Chrome 中的材料
func (r *chromeRenewal) refresh(ctx context.Context) ([]gemini.Cookie, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cookies, err := chromeauth.Refresh(ctx, r.material, r.proxy)
	if !errors.Is(err, chromeauth.ErrCredentialsRejected) || r.file == nil {
		return cookies, err
	}
	current, findErr := chromeauth.FindChromeMaterial(r.material.GaiaID, r.profile)
	if findErr != nil {
		return nil, fmt.Errorf("%w；从 Chrome 重新导入失败: %v", err, findErr)
	}
	if bytes.Equal(current.Encrypted, r.tried) {
		return nil, fmt.Errorf("%w；Chrome 中的材料未更新", err)
	}
	logging.Info(r.id, "续签材料被拒，从 Chrome 重新导入", "Profile", current.Profile)
	material, importErr := current.Decrypt()
	if importErr == nil && material.RefreshToken == r.material.RefreshToken {
		importErr = fmt.Errorf("%w：Chrome 中的材料与被拒材料相同", chromeauth.ErrCredentialsRejected)
	}
	if importErr == nil {
		cookies, importErr = chromeauth.Refresh(ctx, material, r.proxy)
	}
	if errors.Is(importErr, chromeauth.ErrCredentialsRejected) {
		r.tried = current.Encrypted
	}
	if importErr != nil {
		return nil, fmt.Errorf("%w；从 Chrome 重新导入失败: %w", err, importErr)
	}
	if err := r.file.SetOAuth(material, current.Profile); err != nil {
		return nil, err
	}
	r.material, r.profile = material, current.Profile
	logging.Info(r.id, "已从 Chrome 重新导入续签材料", "Profile", current.Profile)
	return cookies, nil
}
