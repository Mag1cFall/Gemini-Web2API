//go:build desktop

package app

import (
	"os"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

// kernel32 是控制台函数所在的系统库
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

// attachConsoleProc 是 kernel32 的 AttachConsole
var attachConsoleProc = kernel32.NewProc("AttachConsole")

// allocConsoleProc 是 kernel32 的 AllocConsole
var allocConsoleProc = kernel32.NewProc("AllocConsole")

// setConsoleCtrlHandlerProc 是 kernel32 的 SetConsoleCtrlHandler
var setConsoleCtrlHandlerProc = kernel32.NewProc("SetConsoleCtrlHandler")

// attachParentProcess 是 AttachConsole 附加到父进程控制台的参数
const attachParentProcess = ^uintptr(0)

// attachConsole 让 GUI 子系统的桌面版附加到启动它的控制台，没有控制台时新建一个；启动时没有的标准输入输出改用该控制台，interrupt 非空时在第一次 Ctrl+C 或 Ctrl+Break 时调用，之后的按键按系统默认结束进程
func attachConsole(interrupt func()) {
	if attached, _, _ := attachConsoleProc.Call(attachParentProcess); attached == 0 {
		if allocated, _, _ := allocConsoleProc.Call(); allocated == 0 {
			return
		}
	}
	if !inherited(os.Stdin) {
		if input, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
			os.Stdin = input
		}
	}
	if !inherited(os.Stdout) || !inherited(os.Stderr) {
		if output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
			if !inherited(os.Stdout) {
				os.Stdout = output
			}
			if !inherited(os.Stderr) {
				os.Stderr = output
			}
		}
	}
	if interrupt != nil {
		var interrupted atomic.Bool
		setConsoleCtrlHandlerProc.Call(windows.NewCallback(func(event uint32) uintptr {
			if event != windows.CTRL_C_EVENT && event != windows.CTRL_BREAK_EVENT || interrupted.Swap(true) {
				return 0
			}
			interrupt()
			return 1
		}), 1)
	}
}

// inherited 判断启动时是否继承了有效的标准句柄，例如重定向的文件或管道
func inherited(file *os.File) bool {
	handle := file.Fd()
	return handle != 0 && handle != uintptr(windows.InvalidHandle)
}
