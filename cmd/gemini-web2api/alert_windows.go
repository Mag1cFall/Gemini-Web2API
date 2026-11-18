//go:build desktop

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"

	"github.com/Mag1cFall/Gemini-Web2API/internal/i18n"
)

// alert 把启动失败的原因写到附加的控制台；双击启动的窗口版没有控制台，改用系统消息框按系统界面语言显示
func alert(err error) {
	if handle := os.Stderr.Fd(); handle != 0 && handle != uintptr(windows.InvalidHandle) {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	text, _ := windows.UTF16PtrFromString(i18n.Translate(i18n.SystemLocale(), err.Error()))
	title, _ := windows.UTF16PtrFromString("Gemini-Web2API")
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
}
