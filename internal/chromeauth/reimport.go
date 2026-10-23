package chromeauth

import (
	"fmt"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
)

// ChromeMaterial 是本机 Chrome 中一个 Google 账号当前的续签材料
type ChromeMaterial struct {
	Profile string
	// Encrypted 是 token_service 中的 refresh token 密文
	Encrypted  []byte
	chromeRoot string
	gaiaID     string
	wrappedKey []byte
}

// FindChromeMaterial 在本机 Chrome 中查找 Gaia ID 对应的可导入账号，同一账号登录多个 Profile 时优先 profile
func FindChromeMaterial(gaiaID string, profile string) (ChromeMaterial, error) {
	root, err := DefaultChromeRoot()
	if err != nil {
		return ChromeMaterial{}, err
	}
	accounts, err := Discover(root)
	if err != nil {
		return ChromeMaterial{}, err
	}
	selected := ""
	for _, account := range accounts {
		if account.Importable && account.GaiaID == gaiaID && (selected == "" || account.Profile == profile) {
			selected = account.Profile
		}
	}
	if selected == "" {
		return ChromeMaterial{}, fmt.Errorf("Chrome 中没有可导入的同一账号")
	}
	_, encrypted, wrappedKey, err := readTokenService(root, selected, gaiaID)
	if err != nil {
		return ChromeMaterial{}, err
	}
	return ChromeMaterial{Profile: selected, Encrypted: encrypted, chromeRoot: root, gaiaID: gaiaID, wrappedKey: wrappedKey}, nil
}

// Decrypt 以 Chrome App-Bound 主密钥解密 refresh token，返回可保存的续签材料
func (m ChromeMaterial) Decrypt() (auth.OAuthMaterial, error) {
	masterKey, err := retrieveV20Key(m.chromeRoot)
	if err != nil {
		return auth.OAuthMaterial{}, err
	}
	if len(masterKey) != 32 {
		return auth.OAuthMaterial{}, fmt.Errorf("Chrome v20 主密钥长度异常")
	}
	token, err := decryptV20Token(masterKey, m.Encrypted)
	if err != nil {
		return auth.OAuthMaterial{}, err
	}
	return auth.OAuthMaterial{GaiaID: m.gaiaID, RefreshToken: token, WrappedBindingKey: m.wrappedKey}, nil
}
