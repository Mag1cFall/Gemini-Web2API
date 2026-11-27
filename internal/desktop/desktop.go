//go:build desktop

// Package desktop 用 Wails 窗口与托盘承载管理端，macOS 保留系统红绿灯按钮，其他系统为无边框窗口
package desktop

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/Mag1cFall/Gemini-Web2API/internal/i18n"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
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

// WindowFlag 是 Windows 独立窗口子进程的启动参数名，cmd 与探针解析到它时直接调用 RunWindow
const WindowFlag = "desktop-window"

// uniqueID 是单实例锁与开机启动项的标识
const uniqueID = "com.mag1cfall.gemini-web2api"

// shellQuery 是管理端识别桌面宿主的查询参数
const shellQuery = "shell"

// openPrefix 为页面请求用系统浏览器打开链接的消息前缀
const openPrefix = "desktop:open:"

// windowPrefix 为页面顶栏窗口按钮的消息前缀
const windowPrefix = "desktop:window:"

// routePrefix 为页面报告当前路径与查询的消息前缀，重建窗口时打开该页面
const routePrefix = "desktop:route:"

// busyPrefix 为页面报告是否有进行中的生成或未保存的修改的消息前缀，取值为 1 或 0
const busyPrefix = "desktop:busy:"

// maximisedEvent 是通知页面窗口最大化状态的事件名
const maximisedEvent = "desktop:maximised"

// closeToTray 表示关闭窗口后应用留在托盘，窗口与 WebView 一并销毁，再次显示时重建；Linux 的托盘依赖桌面环境扩展，关闭窗口即退出
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

// pageScript 在页面中转交外链、拖放、界面语言、配色与当前页面，Windows 上报告标题栏与窗口按钮的非客户区，macOS 与 Linux 上实现标题栏拖动，Linux 另有边缘缩放
//
//go:embed page.js
var pageScript string

// loadedEvents 为各平台页面加载完成的窗口事件
var loadedEvents = []events.WindowEventType{
	events.Windows.WebViewNavigationCompleted,
	events.Mac.WebViewDidFinishNavigation,
	events.Linux.WindowLoadFinished,
}

// activeShell 保存当前运行中的桌面壳，供在线更新重启前先关闭独立窗口子进程
var activeShell atomic.Pointer[shell]

// shell 是桌面应用与它的主窗口：主窗口关闭到托盘后与 WebView 一并销毁，再次显示时按关闭前的位置、大小、最大化状态与页面重建；Windows 上管理窗口运行在同可执行文件启动的子进程中
type shell struct {
	app     *application.App
	options Options
	dataDir string
	// page 是带桌面宿主参数的管理端首页地址，服务开始监听时写入
	page *url.URL
	// webview 为 false 时系统没有 WebView2 运行时，管理端在默认浏览器中打开
	webview bool
	tray    *tray
	// opening 串行化主窗口的创建与关闭
	opening      sync.Mutex
	mu           sync.Mutex
	bounds       application.Rect
	maxed        bool
	route        string
	pendingShow  bool
	shuttingDown atomic.Bool
	windows      windowHost
}

// Run 取得单实例锁后启动服务，服务开始监听时打开窗口与托盘（隐藏启动只创建托盘，显示时创建窗口；Linux 桌面环境没有系统托盘时隐藏启动也打开窗口），阻塞到退出；已有实例时唤起它的窗口并退出本进程；Windows 缺少 WebView2 运行时时只创建托盘，管理端在默认浏览器中打开
func Run(options Options) error {
	log.SetOutput(webviewLog{})
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return err
	}
	s := &shell{options: options, dataDir: dataDir, page: &url.URL{}, webview: true}
	activeShell.Store(s)
	defer activeShell.CompareAndSwap(s, nil)
	s.app = application.New(application.Options{
		Name: options.Title,
		Icon: icon,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               uniqueID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { go s.show() },
		},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: closeToTray, WndProcInterceptor: func(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
			return s.tray.message(hwnd, msg, wParam, lParam)
		}},
		RawMessageHandler: func(_ application.Window, message string, _ *application.OriginInfo) { s.handle(message) },
		ErrorHandler:      func(err error) { logging.Error(logging.SourceService, "桌面窗口错误", "错误", err) },
		WarningHandler:    func(message string) { logging.Warn(logging.SourceService, "桌面窗口警告", "内容", message) },
	})
	s.webview = runtime.GOOS != "windows" || s.app.Env.Info().PlatformInfo["WebView2"] != ""
	hosted := trayHosted()
	s.tray = addTray(s)
	s.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { go s.show() })
	return s.serve(func(address string) error {
		page, err := windowURL(address)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.page = page
		deferred := s.pendingShow
		s.pendingShow = false
		s.mu.Unlock()
		switch {
		case !s.webview:
			logging.Warn(logging.SourceService, "没有找到 WebView2 运行时，管理端在默认浏览器中打开")
			if !options.Hidden || deferred {
				s.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { go s.show() })
			}
		case !options.Hidden || !hosted || deferred:
			if !hosted {
				logging.Info(logging.SourceService, "桌面环境没有系统托盘，关闭窗口即退出")
			}
			s.openWindow()
		default:
			s.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { setDockIcon(false) })
		}
		return nil
	})
}

// webviewLog 是标准库 log 的输出：Wails 的 WebView2 层报告的错误写入服务日志，全部输出照常写到标准错误
type webviewLog struct{}

// Write 把带 [WebView2 Error] 的一行记为桌面窗口错误
func (webviewLog) Write(p []byte) (int, error) {
	if _, text, ok := strings.Cut(string(p), "[WebView2 Error] "); ok {
		logging.Error(logging.SourceService, "桌面窗口错误", "错误", strings.TrimSpace(text))
	}
	return os.Stderr.Write(p)
}

// serve 在后台运行服务，开始监听后以管理端地址调用 ready（先行结束时不调用），再运行应用直到退出；服务先于退出结束时显示其错误并退出应用
func (s *shell) serve(ready func(address string) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var served error
	finished := make(chan struct{})
	listening := make(chan string, 1)
	go func() {
		defer close(finished)
		served = s.options.Serve(ctx, func(address string) { listening <- address })
	}()
	select {
	case address := <-listening:
		if err := ready(address); err != nil {
			return err
		}
	case <-finished:
	}
	s.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			<-finished
			if ctx.Err() != nil {
				return
			}
			s.shuttingDown.Store(true)
			s.closeWindow()
			if served != nil {
				showError(s.app, served)
			}
			s.app.Quit()
		}()
	})
	err := s.app.Run()
	s.shuttingDown.Store(true)
	s.closeWindow()
	cancel()
	<-finished
	return errors.Join(err, served)
}

// quit 先关闭独立窗口子进程再退出桌面应用，供托盘菜单“退出”调用
func (s *shell) quit() {
	s.shuttingDown.Store(true)
	s.closeWindow()
	s.app.Quit()
}

// beginRestart 在在线更新重启前暂时禁止新开窗口并关闭当前独立窗口子进程以释放可执行文件句柄；返回的函数在重启失败时恢复窗口启动能力
func beginRestart() func() {
	s := activeShell.Load()
	if s == nil {
		return func() {}
	}
	s.shuttingDown.Store(true)
	s.closeWindow()
	return func() { s.shuttingDown.Store(false) }
}

// handle 处理页面发来的消息：外链、窗口按钮、当前页面、忙碌状态与托盘所用的界面语言和配色
func (s *shell) handle(message string) {
	if s.tray.handle(message) {
		return
	}
	if target, ok := strings.CutPrefix(message, openPrefix); ok {
		openBrowser(s.app, target)
	} else if action, ok := strings.CutPrefix(message, windowPrefix); ok && windowActions[action] != nil {
		s.handleWindowAction(action)
	} else if route, ok := strings.CutPrefix(message, routePrefix); ok && strings.HasPrefix(route, "/") {
		s.mu.Lock()
		s.route = route
		s.mu.Unlock()
	} else if busy, ok := strings.CutPrefix(message, busyPrefix); ok {
		s.setBusy(busy == "1")
	}
}

// show 显示主窗口，没有时创建；没有 WebView2 时在默认浏览器中打开管理端
func (s *shell) show() {
	if s.shuttingDown.Load() {
		return
	}
	s.mu.Lock()
	page := s.page
	if page == nil || page.Scheme == "" {
		s.pendingShow = true
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if !s.webview {
		openBrowser(s.app, origin(page))
		return
	}
	s.showWindow()
}

// recordWindowState 记下窗口关闭或隐藏前的位置、大小与最大化状态，最小化或最大化时不覆盖正常尺寸
func (s *shell) recordWindowState(bounds application.Rect, maxed, minimised bool) {
	s.mu.Lock()
	s.maxed = maxed
	if !maxed && !minimised && bounds.Width > 0 && bounds.Height > 0 {
		s.bounds = bounds
	}
	s.mu.Unlock()
}

// currentPage 返回管理端首页地址的快照，供托盘动作在任意 goroutine 安全读取
func (s *shell) currentPage() *url.URL {
	s.mu.Lock()
	page := s.page
	s.mu.Unlock()
	return page
}

// windowWindowsOptions 返回承载 WebView2 的窗口进程的 Windows 配置
func windowWindowsOptions(dataDir string) application.WindowsOptions {
	return application.WindowsOptions{WebviewUserDataPath: dataDir}
}

// newWebviewWindow 按给定标题、地址、几何与最大化状态创建管理端窗口并注册页面脚本与最大化通知
func newWebviewWindow(app *application.App, title, targetURL string, bounds application.Rect, maxed bool) *application.WebviewWindow {
	options := application.WebviewWindowOptions{
		Title:            title,
		URL:              targetURL,
		Frameless:        runtime.GOOS != "darwin",
		Mac:              application.MacWindow{TitleBar: application.MacTitleBarHiddenInset, InvisibleTitleBarHeight: 48},
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(0xf0, 0xf5, 0xf8),
		Linux:            application.LinuxWindow{WebviewGpuPolicy: application.WebviewGpuPolicyNever},
		// WebView2CompositionHosting 让页面报告的标题栏与窗口按钮参与 Windows 命中测试，悬停最大化按钮时系统显示贴靠布局
		Windows: application.WindowsWindow{WebView2CompositionHosting: true},
	}
	if bounds.Width > 0 {
		options.InitialPosition = application.WindowXY
		options.X, options.Y, options.Width, options.Height = bounds.X, bounds.Y, bounds.Width, bounds.Height
	}
	window := app.Window.NewWithOptions(options)
	if maxed {
		window.Maximise()
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
	return window
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

// origin 返回管理端首页地址，不带桌面宿主参数
func origin(page *url.URL) string {
	return page.Scheme + "://" + page.Host + "/"
}

// pageAt 返回管理端在 route（路径与查询）上的地址，查询带桌面宿主参数；route 为空或无法解析时为首页
func pageAt(page *url.URL, route string) string {
	address, err := url.Parse(route)
	if route == "" || err != nil {
		return page.String()
	}
	query := address.Query()
	query.Set(shellQuery, "desktop")
	target := *page
	target.Path, target.RawQuery = address.Path, query.Encode()
	return target.String()
}

// autostartOptions 返回开机启动的登记参数；能隐藏到托盘的平台启动后只显示托盘
func autostartOptions() application.AutostartOptions {
	options := application.AutostartOptions{Identifier: uniqueID}
	if closeToTray {
		options.Arguments = []string{"--" + HiddenFlag}
	}
	return options
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

// showError 用原生对话框按界面语言显示错误，关闭对话框后返回
func showError(app *application.App, err error) {
	app.Dialog.Error().SetTitle(app.Config().Name).SetMessage(i18n.Translate(locale(), err.Error())).Show()
}
