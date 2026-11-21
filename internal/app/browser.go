package app

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/Mag1cFall/Gemini-Web2API/internal/admin"
)

// AdminURL 返回监听地址对应的本机管理页地址，通配地址换成 127.0.0.1
func AdminURL(address string) string {
	return admin.BaseURL(address) + "/"
}

// openBrowser 用当前系统的默认浏览器打开地址
func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动浏览器: %w", err)
	}
	return command.Process.Release()
}
