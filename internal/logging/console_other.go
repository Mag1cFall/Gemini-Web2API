//go:build !windows

package logging

import "os"

// enableVirtualTerminal 在非 Windows 终端直接可用 ANSI 转义序列
func enableVirtualTerminal(*os.File) bool {
	return true
}

// disableQuickEdit 在非 Windows 终端为空操作
func disableQuickEdit() {}
