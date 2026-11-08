//go:build desktop

// Package desktop 用 Wails 窗口与托盘承载管理端，macOS 保留系统红绿灯按钮，其他系统为无边框窗口
package desktop

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Options 为桌面应用的运行参数
type Options struct {
	// Title 是应用名、窗口标题与托盘提示
	Title string
	// DataDir 是 WebView 数据目录
	DataDir string
	// Hidden 为 true 时启动后只显示托盘
	Hidden bool
	// Serve 在取得单实例锁后运行服务，直到 ctx 取消；开始监听后以管理端地址调用 ready
	Serve func(ctx context.Context, ready func(url string)) error
}

// HiddenFlag 是开机启动附带的启动参数名，cmd 解析到它时设置 Options.Hidden
const HiddenFlag = "hidden"

// uniqueID 是单实例锁与开机启动项的标识
const uniqueID = "com.mag1cfall.gemini-web2api"

// shellQuery 是管理端识别桌面宿主的查询参数
const shellQuery = "shell"

// openPrefix 为页面请求用系统浏览器打开链接的消息前缀
const openPrefix = "desktop:open:"

// windowPrefix 为页面顶栏窗口按钮的消息前缀
const windowPrefix = "desktop:window:"

// localePrefix 为页面报告界面语言的消息前缀，菜单随之换成该语言
const localePrefix = "desktop:locale:"

// maximisedEvent 是通知页面窗口最大化状态的事件名
const maximisedEvent = "desktop:maximised"

// closeToTray 表示关闭窗口时隐藏到托盘；Linux 的托盘依赖桌面环境扩展，关闭窗口即退出
var closeToTray = runtime.GOOS != "linux"

// windowActions 为页面顶栏窗口按钮对应的窗口操作
var windowActions = map[string]func(*application.WebviewWindow){
	"minimise":        func(window *application.WebviewWindow) { window.Minimise() },
	"toggle-maximise": (*application.WebviewWindow).ToggleMaximise,
	"close":           (*application.WebviewWindow).Close,
}

// icon 为托盘与应用图标
//
//go:embed icon.png
var icon []byte

// trayTemplate 为 macOS 菜单栏的单色模板图标，系统按菜单栏深浅色着色
//
//go:embed tray-template.png
var trayTemplate []byte

// pageScript 在页面中转交外链、拖放与界面语言，Windows 上报告标题栏与窗口按钮的非客户区，macOS 与 Linux 上实现标题栏拖动，Linux 另有边缘缩放
//
//go:embed page.js
var pageScript string

// loadedEvents 为各平台页面加载完成的窗口事件
var loadedEvents = []events.WindowEventType{
	events.Windows.WebViewNavigationCompleted,
	events.Mac.WebViewDidFinishNavigation,
	events.Linux.WindowLoadFinished,
}

// Run 取得单实例锁后启动服务，服务开始监听时按实际地址打开窗口与托盘，阻塞到退出；已有实例时唤起它的窗口并退出本进程
func Run(options Options) error {
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return err
	}

	var app *application.App
	var window *application.WebviewWindow
	show := func() {
		setDockIcon(true)
		window.Show()
		window.UnMinimise()
		window.Focus()
	}
	var localize func(locale string)
	app = application.New(application.Options{
		Name: options.Title,
		Icon: icon,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               uniqueID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { show() },
		},
		Windows: application.WindowsOptions{WebviewUserDataPath: dataDir},
		RawMessageHandler: func(_ application.Window, message string, _ *application.OriginInfo) {
			if target, ok := strings.CutPrefix(message, openPrefix); ok {
				openBrowser(app, target)
			} else if action, ok := strings.CutPrefix(message, windowPrefix); ok && windowActions[action] != nil {
				windowActions[action](window)
			} else if locale, ok := strings.CutPrefix(message, localePrefix); ok {
				localize(locale)
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var served error
	finished := make(chan struct{})
	listening := make(chan string, 1)
	go func() {
		defer close(finished)
		served = options.Serve(ctx, func(url string) { listening <- url })
	}()
	page := &url.URL{}
	select {
	case address := <-listening:
		if page, err = windowURL(address); err != nil {
			return err
		}
	case <-finished:
	}

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            options.Title,
		URL:              page.String(),
		Frameless:        runtime.GOOS != "darwin",
		Mac:              application.MacWindow{TitleBar: application.MacTitleBarHiddenInset, InvisibleTitleBarHeight: 48},
		Hidden:           options.Hidden || page.Host == "",
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(0xf0, 0xf5, 0xf8),
		Linux:            application.LinuxWindow{WebviewGpuPolicy: application.WebviewGpuPolicyNever},
		// WebView2CompositionHosting 让页面报告的标题栏与窗口按钮参与 Windows 命中测试，悬停最大化按钮时系统显示贴靠布局
		Windows: application.WindowsWindow{WebView2CompositionHosting: true},
	})
	if closeToTray {
		window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
			window.Hide()
			setDockIcon(false)
			event.Cancel()
		})
	}
	notifyMaximised := func(*application.WindowEvent) {
		window.ExecJS(fmt.Sprintf("window.dispatchEvent(new CustomEvent(%q,{detail:%t}))", maximisedEvent, window.IsMaximised()))
	}
	window.OnWindowEvent(events.Common.WindowDidResize, notifyMaximised)
	for _, loaded := range loadedEvents {
		window.OnWindowEvent(loaded, func(event *application.WindowEvent) {
			markReady(window)
			window.ExecJS(pageScript)
			notifyMaximised(event)
		})
	}
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { show() })

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplate)
	} else {
		tray.SetIcon(icon)
	}
	tray.SetTooltip(options.Title)
	current := ""
	localize = func(locale string) {
		text, ok := menuTexts[locale]
		if !ok || locale == current {
			return
		}
		current = locale
		tray.SetMenu(trayMenu(app, text, page, show))
		if runtime.GOOS == "darwin" {
			app.Menu.Set(appMenu(text))
		}
	}
	localize(defaultLocale)
	tray.OnClick(show)

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if options.Hidden {
			setDockIcon(false)
		}
		go func() {
			<-finished
			if ctx.Err() != nil {
				return
			}
			if served != nil {
				showError(app, served)
			}
			app.Quit()
		}()
	})
	err = app.Run()
	cancel()
	<-finished
	return errors.Join(err, served)
}

// trayMenu 返回托盘菜单：显示窗口、在浏览器中打开、复制 API 地址、开机启动与退出
func trayMenu(app *application.App, text menuText, page *url.URL, show func()) *application.Menu {
	menu := app.NewMenu()
	menu.Add(text.ShowWindow).OnClick(func(*application.Context) { show() })
	menu.Add(text.OpenInBrowser).OnClick(func(*application.Context) { openBrowser(app, page.Scheme+"://"+page.Host+"/") })
	menu.Add(text.CopyAddress).OnClick(func(*application.Context) {
		app.Clipboard.SetText(page.Scheme + "://" + page.Host)
	})
	menu.AddSeparator()
	autostart := text.Autostart
	if runtime.GOOS == "darwin" {
		autostart = text.OpenAtLogin
	}
	enabled, _ := app.Autostart.IsEnabled()
	menu.AddCheckbox(autostart, enabled).OnClick(func(click *application.Context) {
		toggle := app.Autostart.Disable
		if click.IsChecked() {
			toggle = func() error { return enableAutostart(app) }
		}
		if err := toggle(); err != nil {
			click.ClickedMenuItem().SetChecked(!click.IsChecked())
			showError(app, err)
		}
	})
	menu.AddSeparator()
	menu.Add(text.Quit).OnClick(func(*application.Context) { app.Quit() })
	return menu
}

// windowURL 返回带桌面宿主查询参数的窗口地址
func windowURL(raw string) (*url.URL, error) {
	page, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	query := page.Query()
	query.Set(shellQuery, "desktop")
	page.RawQuery = query.Encode()
	return page, nil
}

// enableAutostart 登记开机启动；能隐藏到托盘的平台启动后只显示托盘
func enableAutostart(app *application.App) error {
	options := application.AutostartOptions{Identifier: uniqueID}
	if closeToTray {
		options.Arguments = []string{"--" + HiddenFlag}
	}
	return app.Autostart.EnableWithOptions(options)
}

// markReady 让窗口立即执行 ExecJS，外部页面不会自行发送 wails:runtime:ready
func markReady(window *application.WebviewWindow) {
	window.HandleMessage("wails:runtime:ready")
}

// openBrowser 用系统浏览器打开 http 与 https 链接
func openBrowser(app *application.App, target string) {
	link, err := url.Parse(target)
	if err != nil || (link.Scheme != "http" && link.Scheme != "https") {
		return
	}
	if err := app.Browser.OpenURL(link.String()); err != nil {
		showError(app, err)
	}
}

// showError 用原生对话框显示错误，关闭对话框后返回
func showError(app *application.App, err error) {
	app.Dialog.Error().SetTitle(app.Config().Name).SetMessage(err.Error()).Show()
}
