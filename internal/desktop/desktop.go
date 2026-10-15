//go:build desktop

// Package desktop 用 Wails 无边框窗口与托盘承载管理端
package desktop

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Options 为桌面应用的运行参数
type Options struct {
	// URL 是管理端地址，主机部分为服务监听的回环地址与端口
	URL string
	// Title 是应用名、窗口标题与托盘提示
	Title string
	// DataDir 是 WebView 数据目录
	DataDir string
	// Hidden 为 true 时启动后只显示托盘
	Hidden bool
	// Serve 在取得单实例锁后运行服务，直到 ctx 取消
	Serve func(ctx context.Context) error
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

// pageScript 在页面中转交外链与窗口按钮，并实现无边框窗口的拖动与边缘缩放
//
//go:embed page.js
var pageScript string

// loadedEvents 为各平台页面加载完成的窗口事件
var loadedEvents = []events.WindowEventType{
	events.Windows.WebViewNavigationCompleted,
	events.Mac.WebViewDidFinishNavigation,
	events.Linux.WindowLoadFinished,
}

// Run 取得单实例锁后启动服务，服务开始监听时打开窗口与托盘，阻塞到退出；已有实例时唤起它的窗口并退出本进程
func Run(options Options) error {
	page, err := windowURL(options.URL)
	if err != nil {
		return err
	}
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return err
	}

	var app *application.App
	var window *application.WebviewWindow
	show := func() {
		window.Show()
		window.UnMinimise()
		window.Focus()
	}
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
			}
		},
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            options.Title,
		URL:              page.String(),
		Frameless:        true,
		Hidden:           options.Hidden,
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(0xf0, 0xf5, 0xf8),
	})
	if closeToTray {
		window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
			window.Hide()
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

	menu := app.NewMenu()
	menu.Add("显示窗口").OnClick(func(*application.Context) { show() })
	menu.Add("在浏览器中打开").OnClick(func(*application.Context) { openBrowser(app, options.URL) })
	menu.Add("复制 API 地址").OnClick(func(*application.Context) {
		app.Clipboard.SetText(page.Scheme + "://" + page.Host)
	})
	menu.AddSeparator()
	enabled, _ := app.Autostart.IsEnabled()
	menu.AddCheckbox("开机启动", enabled).OnClick(func(click *application.Context) {
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
	menu.Add("退出").OnClick(func(*application.Context) { app.Quit() })
	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip(options.Title)
	tray.SetMenu(menu)
	tray.OnClick(show)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var served error
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		served = options.Serve(ctx)
	}()
	if !waitServing(page.Host, finished) {
		window.Hide()
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
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

// waitServing 等到 address 接受 TCP 连接时返回 true，服务先结束时返回 false
func waitServing(address string, finished <-chan struct{}) bool {
	for {
		if conn, err := net.Dial("tcp", address); err == nil {
			conn.Close()
			return true
		}
		select {
		case <-finished:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
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
