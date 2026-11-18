//go:build desktop

package desktop

import (
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/Mag1cFall/Gemini-Web2API/internal/i18n"
)

// localePrefix 为页面报告界面语言的消息前缀，菜单随之换成该语言
const localePrefix = "desktop:locale:"

// themePrefix 为页面报告界面配色（dark 或 light）的消息前缀
const themePrefix = "desktop:theme:"

// uiLocale 是托盘菜单与对话框所用的界面语言：页面加载前为系统界面语言，之后跟随页面
var uiLocale atomic.Value

// locale 返回托盘菜单与对话框当前所用的界面语言
func locale() string {
	value, _ := uiLocale.Load().(string)
	return value
}

// tray 是托盘图标与它的菜单，dark 为页面报告的界面配色是否为深色
type tray struct {
	app   *application.App
	shell *shell
	item  *application.SystemTray
	dark  atomic.Pointer[bool]
}

// trayEntry 是托盘菜单的一项：label 为空时为分隔线，checkable 为真时按 checked 显示勾选
type trayEntry struct {
	label              string
	run                func()
	checkable, checked bool
}

// addTray 创建托盘图标并按平台惯例设置单击、双击与右键（见 attach），菜单文字按界面语言
func addTray(s *shell) *tray {
	uiLocale.Store(i18n.SystemLocale())
	item := s.app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		item.SetTemplateIcon(trayTemplate)
	} else {
		item.SetIcon(icon)
	}
	item.SetTooltip(s.options.Title)
	current := &tray{app: s.app, shell: s, item: item}
	current.refresh()
	if runtime.GOOS == "darwin" {
		s.app.Menu.Set(appMenu(menuTexts[locale()]))
	}
	current.attach(s.show)
	return current
}

// entries 返回托盘菜单：显示窗口（有 WebView2 时）、在浏览器中打开、复制 API 地址、开机启动与退出，开机启动的勾选取当前登记状态
func (t *tray) entries() []trayEntry {
	text := menuTexts[locale()]
	app := t.app
	var entries []trayEntry
	if t.shell.webview {
		entries = append(entries, trayEntry{label: text.ShowWindow, run: t.shell.show})
	}
	autostart := text.Autostart
	if runtime.GOOS == "darwin" {
		autostart = text.OpenAtLogin
	}
	enabled := autostartEnabled(app)
	return append(entries,
		trayEntry{label: text.OpenInBrowser, run: func() { openBrowser(app, origin(t.shell.page)) }},
		trayEntry{label: text.CopyAddress, run: func() { app.Clipboard.SetText(t.shell.page.Scheme + "://" + t.shell.page.Host) }},
		trayEntry{},
		trayEntry{label: autostart, checkable: true, checked: enabled, run: func() {
			if err := setAutostart(app, !enabled); err != nil {
				showError(app, err)
			}
			t.refresh()
		}},
		trayEntry{},
		trayEntry{label: text.Quit, run: app.Quit},
	)
}

// handle 处理页面报告的界面语言与配色：语言变化时按新语言重建菜单（macOS 同时重建应用菜单），配色决定之后弹出的菜单深浅；不是这两种消息时返回 false
func (t *tray) handle(message string) bool {
	if value, ok := strings.CutPrefix(message, localePrefix); ok {
		if _, known := menuTexts[value]; known && value != locale() {
			uiLocale.Store(value)
			t.refresh()
			if runtime.GOOS == "darwin" {
				t.app.Menu.Set(appMenu(menuTexts[value]))
			}
		}
		return true
	}
	if value, ok := strings.CutPrefix(message, themePrefix); ok {
		dark := value == "dark"
		t.dark.Store(&dark)
		return true
	}
	return false
}
