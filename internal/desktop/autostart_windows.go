//go:build desktop

package desktop

import (
	"errors"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows/registry"
)

// approvedKey 为任务管理器与系统设置记录各登录启动项启用状态的注册表键，值名与 Run 中的值名相同
const approvedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`

// autostartEnabled 返回登录时是否启动当前可执行文件：Run 中有指向它的命令，且任务管理器或系统设置没有停用这一项
func autostartEnabled(app *application.App) bool {
	status, err := app.Autostart.Status()
	if err != nil || !status.Enabled {
		return false
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, approvedKey, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer key.Close()
	state, _, err := key.GetBinaryValue(status.Path[strings.LastIndex(status.Path, `\`)+1:])
	return err != nil || len(state) == 0 || state[0]&1 == 0
}

// setAutostart 登记或取消登录时启动，两种情况都清除任务管理器记录的停用状态
func setAutostart(app *application.App, enabled bool) error {
	names := []string{uniqueID}
	if status, err := app.Autostart.Status(); err == nil && status.Enabled {
		names = append(names, status.Path[strings.LastIndex(status.Path, `\`)+1:])
	}
	if key, err := registry.OpenKey(registry.CURRENT_USER, approvedKey, registry.SET_VALUE); err == nil {
		for _, name := range names {
			if err := key.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
				key.Close()
				return err
			}
		}
		key.Close()
	}
	if !enabled {
		return app.Autostart.Disable()
	}
	return app.Autostart.EnableWithOptions(autostartOptions())
}
