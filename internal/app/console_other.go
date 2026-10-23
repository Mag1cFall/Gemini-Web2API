//go:build !desktop || !windows

package app

// attachConsole 在命令行版与 Windows 以外的桌面版上不需要处理，程序直接使用启动它的终端与信号
func attachConsole(func()) {}
