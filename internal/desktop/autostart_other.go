//go:build desktop && !windows

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// autostartEnabled 返回是否已登记登录时启动
func autostartEnabled(app *application.App) bool {
	enabled, _ := app.Autostart.IsEnabled()
	return enabled
}

// setAutostart 登记或取消登录时启动
func setAutostart(app *application.App, enabled bool) error {
	if !enabled {
		return app.Autostart.Disable()
	}
	return app.Autostart.EnableWithOptions(autostartOptions())
}
