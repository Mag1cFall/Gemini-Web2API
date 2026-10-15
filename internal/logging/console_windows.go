package logging

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVirtualTerminal 为 Windows 控制台开启 ANSI 转义序列处理
func enableVirtualTerminal(file *os.File) bool {
	handle := windows.Handle(file.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}

// disableQuickEdit 关闭标准输入控制台的快速编辑模式，鼠标点击控制台窗口时输出照常写入
func disableQuickEdit() {
	handle := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(handle, mode&^windows.ENABLE_QUICK_EDIT_MODE|windows.ENABLE_EXTENDED_FLAGS)
}
