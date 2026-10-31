package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// StateCookie 表示 Playwright storage-state 中的 Cookie
type StateCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"sameSite,omitempty"`
}

// ImportSource 记录认证状态的浏览器来源
type ImportSource struct {
	Browser string `json:"browser,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// OAuthMaterial 保存无需再次读取浏览器的续签材料
type OAuthMaterial struct {
	GaiaID            string `json:"gaiaId"`
	RefreshToken      string `json:"refreshToken"`
	WrappedBindingKey []byte `json:"wrappedBindingKey,omitempty"`
}

// Metadata 保存账号固定身份
type Metadata struct {
	ID       string         `json:"id"`
	Proxy    string         `json:"proxy,omitempty"`
	Source   ImportSource   `json:"source,omitempty"`
	Language string         `json:"language,omitempty"`
	AuthUser int            `json:"authUser,omitempty"`
	OAuth    *OAuthMaterial `json:"oauth,omitempty"`
	Disabled bool           `json:"disabled,omitempty"`
}

// StorageState 是兼容 Playwright 的认证状态文件
type StorageState struct {
	Cookies  []StateCookie     `json:"cookies"`
	Origins  []json.RawMessage `json:"origins"`
	Metadata Metadata          `json:"geminiWeb2api"`
}

// LoadedAccount 是服务启动使用的账号配置
type LoadedAccount struct {
	ID       string
	ProxyURL string
	Source   gemini.AccountSource
	OAuth    *OAuthMaterial
	// Browser 为导入该账号的浏览器名称，如 chrome、brave、edge、firefox，Cookie Header 导入时为空
	Browser string
	// Profile 为浏览器导入的 Profile 目录名，Cookie Header 导入时为空
	Profile  string
	Disabled bool
	File     *File
}

// File 管理一个可原子写回的认证状态文件
type File struct {
	path  string
	state StorageState
	mu    sync.Mutex
}

// LoadFiles 加载全部认证状态文件
func LoadFiles(paths []string) ([]LoadedAccount, error) {
	expanded, err := expandPaths(paths)
	if err != nil {
		return nil, err
	}
	accounts := make([]LoadedAccount, 0, len(expanded))
	seen := make(map[string]struct{}, len(expanded))
	for _, path := range expanded {
		file, err := Load(path)
		if err != nil {
			return nil, err
		}
		account := file.Account()
		if _, exists := seen[account.ID]; exists {
			return nil, fmt.Errorf("账号标识 %q 重复", account.ID)
		}
		seen[account.ID] = struct{}{}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func expandPaths(paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("读取认证状态路径 %s: %w", path, err)
		}
		if !info.IsDir() {
			result = append(result, path)
			continue
		}
		statePath := filepath.Join(path, "storage-state.json")
		if stateInfo, stateErr := os.Stat(statePath); stateErr == nil && !stateInfo.IsDir() {
			result = append(result, statePath)
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("读取认证状态目录 %s: %w", path, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			statePath = filepath.Join(path, entry.Name(), "storage-state.json")
			if stateInfo, stateErr := os.Stat(statePath); stateErr == nil && !stateInfo.IsDir() {
				result = append(result, statePath)
			}
		}
	}
	return result, nil
}

// Load 读取一个认证状态文件
func Load(path string) (*File, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("解析认证状态路径 %q: %w", path, err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("读取认证状态 %s: %w", absPath, err)
	}

	var state StorageState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析认证状态 %s: %w", absPath, err)
	}
	if len(state.Cookies) == 0 {
		return nil, fmt.Errorf("认证状态 %s 没有 Cookie", absPath)
	}
	if state.Origins == nil {
		state.Origins = []json.RawMessage{}
	}
	if strings.TrimSpace(state.Metadata.ID) == "" {
		state.Metadata.ID = accountIDFromPath(absPath)
	}
	if err := validateOAuthMaterial(state.Metadata.OAuth); err != nil {
		return nil, fmt.Errorf("认证状态 %s: %w", absPath, err)
	}
	return &File{path: absPath, state: state}, nil
}

func accountIDFromPath(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.EqualFold(name, "storage-state") || strings.EqualFold(name, "state") {
		return filepath.Base(filepath.Dir(path))
	}
	return name
}

// New 创建一个待写入的认证状态文件
func New(path string, state StorageState) (*File, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("解析认证状态路径 %q: %w", path, err)
	}
	if len(state.Cookies) == 0 {
		return nil, fmt.Errorf("认证状态没有 Cookie")
	}
	if strings.TrimSpace(state.Metadata.ID) == "" {
		return nil, fmt.Errorf("认证状态缺少账号标识")
	}
	if state.Origins == nil {
		state.Origins = []json.RawMessage{}
	}
	if err := validateOAuthMaterial(state.Metadata.OAuth); err != nil {
		return nil, err
	}
	return &File{path: absPath, state: state}, nil
}

// Write 将当前状态原子写入磁盘
func (f *File) Write() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writeLocked()
}

// Account 返回协议客户端使用的账号来源
func (f *File) Account() LoadedAccount {
	f.mu.Lock()
	defer f.mu.Unlock()

	proxyURL := strings.TrimSpace(f.state.Metadata.Proxy)
	source := gemini.AccountSource{
		ID:       f.state.Metadata.ID,
		Cookies:  toGeminiCookies(f.state.Cookies),
		Language: f.state.Metadata.Language,
		AuthUser: f.state.Metadata.AuthUser,
	}
	source.Save = f.SaveCookies
	return LoadedAccount{
		ID: source.ID, ProxyURL: proxyURL, Source: source,
		OAuth: f.state.Metadata.OAuth, Browser: f.state.Metadata.Source.Browser, Profile: f.state.Metadata.Source.Profile,
		Disabled: f.state.Metadata.Disabled, File: f,
	}
}

// Path 返回认证状态文件的绝对路径
func (f *File) Path() string {
	return f.path
}

// SetDisabled 修改账号停用标记并写回
func (f *File) SetDisabled(disabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Metadata.Disabled = disabled
	return f.writeLocked()
}

// SetProxy 修改账号固定代理并写回，空值表示从代理池分配，direct 表示直连
func (f *File) SetProxy(proxy string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Metadata.Proxy = strings.TrimSpace(proxy)
	return f.writeLocked()
}

// Proxy 返回账号固定代理
func (f *File) Proxy() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state.Metadata.Proxy
}

// SetOAuth 更新续签材料与来源 Profile 并写回
func (f *File) SetOAuth(material OAuthMaterial, profile string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Metadata.OAuth = &material
	f.state.Metadata.Source.Profile = profile
	return f.writeLocked()
}

// Cookies 返回当前保存的 Cookie 副本
func (f *File) Cookies() []gemini.Cookie {
	f.mu.Lock()
	defer f.mu.Unlock()
	return toGeminiCookies(f.state.Cookies)
}

// SaveCookies 更新 Cookie 并原子写回认证状态
func (f *File) SaveCookies(cookies []gemini.Cookie) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Cookies = toStateCookies(cookies)
	return f.writeLocked()
}

func (f *File) writeLocked() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("创建认证状态目录: %w", err)
	}
	data, err := json.MarshalIndent(f.state, "", "  ")
	if err != nil {
		return fmt.Errorf("编码认证状态: %w", err)
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(f.path), ".auth-state-*.json")
	if err != nil {
		return fmt.Errorf("创建认证状态临时文件: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("设置认证状态权限: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("写入认证状态: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("同步认证状态: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭认证状态: %w", err)
	}
	if err := replaceFile(tempPath, f.path); err != nil {
		return fmt.Errorf("替换认证状态 %s: %w", f.path, err)
	}
	return nil
}

func validateOAuthMaterial(material *OAuthMaterial) error {
	if material == nil {
		return nil
	}
	if strings.TrimSpace(material.GaiaID) == "" || strings.TrimSpace(material.RefreshToken) == "" {
		return fmt.Errorf("OAuth 续签材料不完整")
	}
	return nil
}

// ToGeminiCookies 将 StateCookie 转换为协议客户端 Cookie
func ToGeminiCookies(cookies []StateCookie) []gemini.Cookie {
	return toGeminiCookies(cookies)
}

func toGeminiCookies(cookies []StateCookie) []gemini.Cookie {
	result := make([]gemini.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		result = append(result, gemini.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     path,
			Expires:  int64(cookie.Expires),
			HTTPOnly: cookie.HTTPOnly,
			Secure:   cookie.Secure,
			SameSite: cookie.SameSite,
		})
	}
	return result
}

func toStateCookies(cookies []gemini.Cookie) []StateCookie {
	result := make([]StateCookie, 0, len(cookies))
	for _, cookie := range cookies {
		result = append(result, StateCookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			Expires:  float64(cookie.Expires),
			HTTPOnly: cookie.HTTPOnly,
			Secure:   cookie.Secure,
			SameSite: cookie.SameSite,
		})
	}
	return result
}
