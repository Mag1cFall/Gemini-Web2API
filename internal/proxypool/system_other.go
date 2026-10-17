//go:build !windows

package proxypool

// osProxyServer 在 Windows 以外的系统返回空，系统代理只取环境变量
func osProxyServer() string {
	return ""
}
