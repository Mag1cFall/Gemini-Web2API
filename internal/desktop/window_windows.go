//go:build desktop && windows

package desktop

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// procAllowSetForegroundWindow 授予独立窗口子进程前台聚焦权限，确保托盘唤回与第二实例能把窗口带到前台
var procAllowSetForegroundWindow = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

// windowInit 是主进程写入窗口子进程标准输入首行的启动配置
type windowInit struct {
	Title   string           `json:"title"`
	URL     string           `json:"url"`
	DataDir string           `json:"dataDir"`
	Bounds  application.Rect `json:"bounds"`
	Maxed   bool             `json:"maxed"`
}

// windowCommand 是主进程发往窗口子进程的控制指令
type windowCommand struct {
	Cmd string `json:"cmd"`
}

// windowEvent 是窗口子进程经标准输出上报给主进程的事件
type windowEvent struct {
	Event     string           `json:"event"`
	Value     string           `json:"value,omitempty"`
	Bounds    application.Rect `json:"bounds,omitempty"`
	Maxed     bool             `json:"maxed,omitempty"`
	Minimised bool             `json:"minimised,omitempty"`
}

// windowHost 管理 Windows 上的独立窗口子进程与关联的作业对象
type windowHost struct {
	child   *windowProcess
	jobOnce sync.Once
	job     windows.Handle
}

// windowProcess 是运行中的独立窗口子进程及其标准输入控制通道
type windowProcess struct {
	cmd      *exec.Cmd
	pid      int
	showCh   chan struct{}
	quitCh   chan struct{}
	quitOnce sync.Once
	done     chan struct{}
	closing  atomic.Bool
	quitting atomic.Bool
}

// requestShow 非阻塞请求窗口子进程显示并聚焦
func (p *windowProcess) requestShow() {
	select {
	case p.showCh <- struct{}{}:
	default:
	}
}

// requestQuit 通知窗口子进程退出，多次调用幂等
func (p *windowProcess) requestQuit() {
	p.quitting.Store(true)
	p.quitOnce.Do(func() { close(p.quitCh) })
}

// openWindow 在服务开始监听后启动首个独立窗口子进程
func (s *shell) openWindow() {
	s.showWindow()
}

// showWindow 在窗口子进程不存在时启动它，存在时授予前台权限并通知其显示与聚焦；旧子进程正在关闭退出时等其结束再启动新进程
func (s *shell) showWindow() {
	s.opening.Lock()
	defer s.opening.Unlock()
	if s.shuttingDown.Load() {
		return
	}
	s.mu.Lock()
	child := s.windows.child
	s.mu.Unlock()
	if child != nil && child.closing.Load() {
		select {
		case <-child.done:
		case <-time.After(5 * time.Second):
		}
		s.mu.Lock()
		child = s.windows.child
		s.mu.Unlock()
	}
	if child == nil {
		s.startWindowLocked()
		return
	}
	allowSetForegroundWindow(child.pid)
	child.requestShow()
}

// startWindowLocked 启动新的独立窗口子进程并接管其标准输入、输出与错误管道；调用方须持有 s.opening
func (s *shell) startWindowLocked() {
	if s.shuttingDown.Load() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		logging.Error(logging.SourceService, "启动桌面窗口失败", "错误", err)
		return
	}
	s.mu.Lock()
	initCfg := windowInit{
		Title:   s.options.Title,
		URL:     pageAt(s.page, s.route),
		DataDir: s.dataDir,
		Bounds:  s.bounds,
		Maxed:   s.maxed,
	}
	s.mu.Unlock()
	initLine, err := json.Marshal(initCfg)
	if err != nil {
		logging.Error(logging.SourceService, "编码桌面窗口配置失败", "错误", err)
		return
	}

	cmd := exec.Command(exe, "--"+WindowFlag)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		logging.Error(logging.SourceService, "创建桌面窗口输入管道失败", "错误", err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		logging.Error(logging.SourceService, "创建桌面窗口输出管道失败", "错误", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		logging.Error(logging.SourceService, "创建桌面窗口错误管道失败", "错误", err)
		return
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		logging.Error(logging.SourceService, "启动桌面窗口进程失败", "错误", err)
		return
	}
	s.windows.assignJob(cmd.Process.Pid)
	if _, err := stdin.Write(append(initLine, '\n')); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logging.Error(logging.SourceService, "发送桌面窗口配置失败", "错误", err)
		return
	}
	allowSetForegroundWindow(cmd.Process.Pid)

	child := &windowProcess{
		cmd:    cmd,
		pid:    cmd.Process.Pid,
		showCh: make(chan struct{}, 1),
		quitCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	s.mu.Lock()
	s.windows.child = child
	s.mu.Unlock()

	stopWrite := make(chan struct{})
	go writeWindowCommands(stdin, child.showCh, child.quitCh, stopWrite)

	var loggedErr atomic.Bool
	var pipes sync.WaitGroup
	pipes.Add(2)
	go func() {
		defer pipes.Done()
		s.readWindowEvents(stdout, child, &loggedErr)
	}()
	go func() {
		defer pipes.Done()
		readWindowStderr(stderr, &loggedErr)
	}()
	go func() {
		pipes.Wait()
		waitErr := cmd.Wait()
		close(stopWrite)
		s.mu.Lock()
		if s.windows.child == child {
			s.windows.child = nil
		}
		s.mu.Unlock()
		if waitErr != nil && !child.quitting.Load() && !loggedErr.Load() {
			logging.Error(logging.SourceService, "桌面窗口异常退出", "错误", waitErr)
		}
		close(child.done)
	}()
}

// writeWindowCommands 串行把控制指令写入窗口子进程的标准输入并在退出时关闭写端
func writeWindowCommands(stdin io.WriteCloser, showCh <-chan struct{}, quitCh, stopCh <-chan struct{}) {
	defer stdin.Close()
	showLine := []byte(`{"cmd":"show"}` + "\n")
	quitLine := []byte(`{"cmd":"quit"}` + "\n")
	for {
		select {
		case <-quitCh:
			_, _ = stdin.Write(quitLine)
			return
		case <-stopCh:
			return
		case <-showCh:
			if _, err := stdin.Write(showLine); err != nil {
				return
			}
		}
	}
}

// readWindowEvents 持续读取窗口子进程标准输出上的 JSON 事件并同步到主进程
func (s *shell) readWindowEvents(stdout io.Reader, child *windowProcess, loggedErr *atomic.Bool) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ev windowEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		switch ev.Event {
		case "message":
			s.handle(ev.Value)
		case "state":
			s.recordWindowState(ev.Bounds, ev.Maxed, ev.Minimised)
		case "closing":
			s.recordWindowState(ev.Bounds, ev.Maxed, ev.Minimised)
			child.closing.Store(true)
		case "error":
			loggedErr.Store(true)
			logging.Error(logging.SourceService, "桌面窗口错误", "错误", ev.Value)
		case "warn":
			logging.Warn(logging.SourceService, "桌面窗口警告", "内容", ev.Value)
		}
	}
}

// readWindowStderr 把窗口子进程的标准错误转写到父进程标准错误，并提取 [WebView2 Error] 记入服务日志
func readWindowStderr(stderr io.Reader, loggedErr *atomic.Bool) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(os.Stderr, line)
		if _, text, ok := strings.Cut(line, "[WebView2 Error] "); ok {
			loggedErr.Store(true)
			logging.Error(logging.SourceService, "桌面窗口错误", "错误", strings.TrimSpace(text))
		}
	}
}

// closeWindow 通知当前窗口子进程退出并等待其结束，超时后强制终止；主进程退出与在线更新重启前调用
func (s *shell) closeWindow() {
	s.opening.Lock()
	defer s.opening.Unlock()
	s.mu.Lock()
	child := s.windows.child
	s.mu.Unlock()
	if child == nil {
		return
	}
	child.requestQuit()
	select {
	case <-child.done:
	case <-time.After(3 * time.Second):
		if child.cmd.Process != nil {
			_ = child.cmd.Process.Kill()
		}
		<-child.done
	}
}

// handleWindowAction 在 Windows 上由窗口子进程本地执行，主进程不直接持有窗口
func (s *shell) handleWindowAction(string) {}

// setBusy 在 Windows 上由窗口子进程本地维护，主进程不直接持有窗口
func (s *shell) setBusy(bool) {}

// assignJob 把窗口子进程加入带 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 的作业对象，确保主进程异常终止时不残留孤儿窗口进程
func (h *windowHost) assignJob(pid int) {
	h.jobOnce.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			_ = windows.CloseHandle(job)
			return
		}
		h.job = job
	})
	if h.job == 0 {
		return
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	_ = windows.AssignProcessToJobObject(h.job, proc)
	_ = windows.CloseHandle(proc)
}

// allowSetForegroundWindow 授予目标进程把窗口置顶到前台的权限
func allowSetForegroundWindow(pid int) {
	_, _, _ = procAllowSetForegroundWindow.Call(uintptr(uint32(pid)))
}

// RunWindow 作为 Windows 独立窗口子进程运行：从标准输入首行读取配置并创建 WebviewWindow，关闭窗口或父进程退出时结束本进程
func RunWindow() error {
	log.SetOutput(os.Stderr)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return nil
	}
	var initCfg windowInit
	if err := json.Unmarshal(scanner.Bytes(), &initCfg); err != nil {
		return nil
	}

	var outMu sync.Mutex
	send := func(ev windowEvent) {
		data, err := json.Marshal(ev)
		if err != nil {
			return
		}
		outMu.Lock()
		_, _ = os.Stdout.Write(append(data, '\n'))
		outMu.Unlock()
	}

	var (
		mu          sync.Mutex
		window      *application.WebviewWindow
		started     bool
		pendingShow bool
		busy        bool
		closing     bool
		forceQuit   bool
		exiting     bool
	)

	app := application.New(application.Options{
		Name:    initCfg.Title,
		Icon:    icon,
		Windows: windowWindowsOptions(initCfg.DataDir),
		RawMessageHandler: func(_ application.Window, message string, _ *application.OriginInfo) {
			if action, ok := strings.CutPrefix(message, windowPrefix); ok && windowActions[action] != nil {
				mu.Lock()
				w := window
				mu.Unlock()
				if w != nil {
					windowActions[action](w)
				}
				return
			}
			if value, ok := strings.CutPrefix(message, busyPrefix); ok {
				mu.Lock()
				busy = value == "1"
				w, release := window, !busy && closing && !exiting
				mu.Unlock()
				if release && w != nil {
					w.Close()
				}
				return
			}
			send(windowEvent{Event: "message", Value: message})
		},
		ErrorHandler:   func(err error) { send(windowEvent{Event: "error", Value: err.Error()}) },
		WarningHandler: func(message string) { send(windowEvent{Event: "warn", Value: message}) },
	})

	w := newWebviewWindow(app, initCfg.Title, initCfg.URL, initCfg.Bounds, initCfg.Maxed)
	mu.Lock()
	window = w
	mu.Unlock()

	w.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		maxed, minimised, bounds := w.IsMaximised(), w.IsMinimised(), w.Bounds()
		mu.Lock()
		keep := !forceQuit && busy
		if keep {
			closing = true
		} else {
			busy, closing, exiting = false, false, true
		}
		mu.Unlock()
		if keep {
			send(windowEvent{Event: "state", Bounds: bounds, Maxed: maxed, Minimised: minimised})
			event.Cancel()
			w.Hide()
			return
		}
		send(windowEvent{Event: "closing", Bounds: bounds, Maxed: maxed, Minimised: minimised})
	})

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		mu.Lock()
		started = true
		showNow := pendingShow && !exiting
		pendingShow = false
		target := window
		mu.Unlock()
		if showNow && target != nil {
			go func() {
				target.Show()
				target.UnMinimise()
				target.Focus()
			}()
		}
	})

	quitApp := func() {
		mu.Lock()
		forceQuit = true
		ready := started
		mu.Unlock()
		if !ready {
			os.Exit(0)
		}
		go func() {
			time.Sleep(2500 * time.Millisecond)
			os.Exit(0)
		}()
		app.Quit()
	}

	go func() {
		for scanner.Scan() {
			var cmd windowCommand
			if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
				continue
			}
			switch cmd.Cmd {
			case "show":
				mu.Lock()
				gone := exiting
				ready := started
				if !gone {
					closing = false
					if !ready {
						pendingShow = true
					}
				}
				target := window
				mu.Unlock()
				if ready && !gone && target != nil {
					target.Show()
					target.UnMinimise()
					target.Focus()
				}
			case "quit":
				quitApp()
				return
			}
		}
		quitApp()
	}()

	return app.Run()
}
