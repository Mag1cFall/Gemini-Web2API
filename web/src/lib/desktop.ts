// shellKey 是会话存储中记录宿主的键，刷新页面后仍保持桌面模式
const shellKey = 'gemini-web2api.shell'

// readShell 读取 ?shell= 参数并在会话内记住
function readShell(): string | null {
  const shell = new URLSearchParams(window.location.search).get('shell')
  try {
    if (shell) sessionStorage.setItem(shellKey, shell)
    return shell ?? sessionStorage.getItem(shellKey)
  } catch {
    return shell
  }
}

// isDesktop 表示页面运行在桌面壳的无边框窗口中
export const isDesktop = readShell() === 'desktop'

// WindowAction 是标题栏窗口按钮发给桌面壳的动作
export type WindowAction = 'minimise' | 'toggle-maximise' | 'close'

declare global {
  interface Window {
    _wails?: { invoke: (message: string) => void }
  }
}

// windowControl 把窗口按钮动作转交桌面壳
export function windowControl(action: WindowAction) {
  window._wails?.invoke('desktop:window:' + action)
}

// maximisedEvent 是桌面壳通知窗口最大化状态变化的页面事件
export const maximisedEvent = 'desktop:maximised'
