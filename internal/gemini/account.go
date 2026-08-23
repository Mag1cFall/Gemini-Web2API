package gemini

// Cookie 表示可无损写回认证状态的 Cookie
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires"`
	HTTPOnly bool   `json:"http_only"`
	Secure   bool   `json:"secure"`
	SameSite string `json:"same_site"`
}

// AccountSource 表示账号固定身份和 Cookie 持久化入口
type AccountSource struct {
	ID          string
	Cookies     []Cookie
	Fingerprint Fingerprint
	Save        func([]Cookie) error
}
