//go:build desktop && !windows

package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// windowHost 在 macOS 与 Linux 上保存主进程内的主窗口与忙碌状态
type windowHost struct {
	window        *application.WebviewWindow
	busy, closing bool
}

// RunWindow 仅在 Windows 上用于独立窗口子进程，其他平台不启用
func RunWindow() error { return nil }

// openWindow 在服务开始监听后创建主窗口
func (s *shell) openWindow() {
	s.opening.Lock()
	defer s.opening.Unlock()
	if s.currentWindow() == nil {
		s.openInProcess()
	}
}

// showWindow 显示主窗口，没有时创建
func (s *shell) showWindow() {
	s.opening.Lock()
	window := s.currentWindow()
	if window == nil {
		window = s.openInProcess()
	}
	s.opening.Unlock()
	s.mu.Lock()
	s.windows.closing = false
	s.mu.Unlock()
	setDockIcon(true)
	window.Show()
	window.UnMinimise()
	window.Focus()
}

// closeWindow 在非 Windows 平台上无需额外回收子进程
func (s *shell) closeWindow() {}

// currentWindow 返回当前主窗口，关闭到托盘后为 nil
func (s *shell) currentWindow() *application.WebviewWindow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.windows.window
}

// openInProcess 按上次关闭前的位置、大小、最大化状态与页面在当前进程内创建主窗口
func (s *shell) openInProcess() *application.WebviewWindow {
	s.mu.Lock()
	bounds, maxed, route, page := s.bounds, s.maxed, s.route, s.page
	s.mu.Unlock()
	window := newWebviewWindow(s.app, s.options.Title, pageAt(page, route), bounds, maxed)
	s.mu.Lock()
	s.windows.window = window
	s.mu.Unlock()
	if closeToTray {
		window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) { s.closed(window, event) })
	}
	return window
}

// handleWindowAction 执行页面顶栏按钮触发的窗口操作
func (s *shell) handleWindowAction(action string) {
	if window := s.currentWindow(); window != nil {
		windowActions[action](window)
	}
}

// closed 在关闭窗口时记下位置、大小与最大化状态并隐藏 Dock 图标；页面忙碌时只隐藏窗口，空闲后再销毁
func (s *shell) closed(window *application.WebviewWindow, event *application.WindowEvent) {
	maxed, minimised, bounds := window.IsMaximised(), window.IsMinimised(), window.Bounds()
	s.mu.Lock()
	keep := s.windows.window == window && s.windows.busy
	if s.windows.window == window {
		s.maxed = maxed
		if !maxed && !minimised {
			s.bounds = bounds
		}
		if keep {
			s.windows.closing = true
		} else {
			s.windows.window, s.windows.busy, s.windows.closing = nil, false, false
		}
	}
	s.mu.Unlock()
	setDockIcon(false)
	if keep {
		event.Cancel()
		window.Hide()
	}
}

// setBusy 记录页面是否有进行中的生成或未保存的修改，窗口已关闭隐藏且页面空闲时销毁窗口与 WebView
func (s *shell) setBusy(busy bool) {
	s.mu.Lock()
	s.windows.busy = busy
	window, release := s.windows.window, !busy && s.windows.closing
	s.mu.Unlock()
	if release && window != nil {
		window.Close()
	}
}
