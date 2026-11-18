//go:build desktop && !linux

package desktop

// trayHosted 在 Windows 与 macOS 上恒为真，系统自带托盘与菜单栏
func trayHosted() bool {
	return true
}
