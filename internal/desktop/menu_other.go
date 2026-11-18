//go:build desktop && !windows

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// attach 让单击菜单栏或托盘图标在新的 goroutine 中显示窗口，右键由系统弹出 refresh 设置的菜单
func (t *tray) attach(show func()) {
	t.item.OnClick(func() { go show() })
}

// refresh 按当前界面语言与开机启动状态重建系统菜单并设置到托盘图标
func (t *tray) refresh() {
	menu := t.app.NewMenu()
	for _, entry := range t.entries() {
		switch {
		case entry.label == "":
			menu.AddSeparator()
		case entry.checkable:
			menu.AddCheckbox(entry.label, entry.checked).OnClick(func(*application.Context) { go entry.run() })
		default:
			menu.Add(entry.label).OnClick(func(*application.Context) { go entry.run() })
		}
	}
	t.item.SetMenu(menu)
}

// message 只在 Windows 上拦截托盘消息
func (t *tray) message(uintptr, uint32, uintptr, uintptr) (uintptr, bool) {
	return 0, false
}
