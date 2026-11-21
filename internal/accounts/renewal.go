package accounts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// chromeRenewal 以设备绑定密钥刷新 DBSC 会话，会话失效时用保存的续签材料签发 Cookie，材料被拒时从本机 Chrome 读取同一 Gaia ID 账号的新材料并写回认证状态
type chromeRenewal struct {
	mu       sync.Mutex
	id       string
	material auth.OAuthMaterial
	// bound 是 OAuthMultilogin 登记的 DBSC 会话，没有登记或 Google 拒绝刷新时为 nil
	bound   *auth.BoundSession
	profile string
	proxy   string
	// chrome 与 language 是账号绑定的 Chrome 主版本与 Accept-Language
	chrome   int
	language string
	file     *auth.File
	// tried 是 Chrome 中已确认被拒的 refresh token 密文
	tried []byte
}

// newChromeRenewal 返回账号的续签器
func newChromeRenewal(account auth.LoadedAccount) *chromeRenewal {
	return &chromeRenewal{
		id: account.ID, material: *account.OAuth, bound: account.BoundSession,
		profile: account.Profile, proxy: account.ProxyURL,
		chrome: account.Source.Chrome, language: account.Source.Language, file: account.File,
	}
}

// rotateBound 在绑定 Cookie 将到期时以设备绑定密钥刷新 DBSC 会话，返回距下次刷新的等待时间；Google 拒绝刷新或本机无法使用设备绑定密钥后不再刷新，直到续签登记新的会话
func (r *chromeRenewal) rotateBound(ctx context.Context, cookies func() []gemini.Cookie, send func(*http.Request) (int, http.Header, error)) (time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bound == nil {
		return 0, nil
	}
	wait, err := chromeauth.RotateBoundCookies(ctx, *r.bound, r.material.WrappedBindingKey, r.chrome, r.language, cookies, send)
	if err != nil && !errors.Is(err, gemini.ErrTransport) {
		r.bound = nil
	}
	return wait, err
}

// refresh 签发 Cookie 并登记响应中的 DBSC 会话，保存的材料被拒且 Chrome 中的材料已更新时改用 Chrome 中的材料
func (r *chromeRenewal) refresh(ctx context.Context) ([]gemini.Cookie, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cookies, bound, err := chromeauth.Refresh(ctx, r.material, r.chrome, r.language, r.proxy)
	if err == nil {
		r.bound = bound
		return cookies, r.file.SetBoundSession(bound)
	}
	if !errors.Is(err, chromeauth.ErrCredentialsRejected) {
		return nil, err
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
		cookies, bound, importErr = chromeauth.Refresh(ctx, material, r.chrome, r.language, r.proxy)
	}
	if errors.Is(importErr, chromeauth.ErrCredentialsRejected) {
		r.tried = current.Encrypted
	}
	if importErr != nil {
		return nil, fmt.Errorf("%w；从 Chrome 重新导入失败: %w", err, importErr)
	}
	if err := r.file.SetOAuthAndBound(material, bound, current.Profile); err != nil {
		return nil, err
	}
	r.material, r.bound, r.profile = material, bound, current.Profile
	logging.Info(r.id, "已从 Chrome 重新导入续签材料", "Profile", current.Profile)
	return cookies, nil
}
