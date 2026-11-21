package proxypool

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// containerLoopbackHint 是容器内环回代理不可用时的排障说明
const containerLoopbackHint = "容器内环回地址指向容器自身，访问宿主机代理请改用 host.docker.internal（Linux 加 --add-host=host.docker.internal:host-gateway）、宿主机局域网地址或 --network host"

// InContainer 报告当前进程是否运行在容器中
var InContainer = detectContainer

// detectContainer 按容器运行时写入的标识文件判断当前是否处于容器环境
func detectContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

// isLoopbackProxy 报告代理地址的主机是否为 127.0.0.1、localhost 或 ::1 等环回地址
func isLoopbackProxy(proxyURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(proxyURL))
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(parsed.Hostname(), ".")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LoopbackHint 在容器中运行且 proxyURL 指向环回地址时返回排障说明，否则返回空
func LoopbackHint(proxyURL string) string {
	if !InContainer() || !isLoopbackProxy(proxyURL) {
		return ""
	}
	return containerLoopbackHint
}

// WithLoopbackHint 在容器中运行且 proxyURL 指向环回地址时把排障说明附在 err 末尾，否则原样返回
func WithLoopbackHint(proxyURL string, err error) error {
	if err == nil {
		return nil
	}
	hint := LoopbackHint(proxyURL)
	if hint == "" || strings.Contains(err.Error(), hint) {
		return err
	}
	return fmt.Errorf("%w；%s", err, hint)
}
