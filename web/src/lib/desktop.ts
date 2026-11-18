import { useEffect, useId } from 'react'

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

// isDesktop 表示页面运行在桌面壳的窗口中
export const isDesktop = readShell() === 'desktop'

// nativeTitleBar 表示桌面窗口使用 macOS 的红绿灯按钮，页面不画窗口按钮并为其让出左侧位置
export const nativeTitleBar = isDesktop && navigator.userAgent.includes('Mac OS X')

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

// keepers 是关闭窗口时需要保留页面的原因：进行中的生成与未保存的修改
const keepers = new Set<string>()

// reportBusy 把是否需要保留页面报告给桌面壳，页面空闲时关闭窗口即释放窗口与 WebView
function reportBusy() {
  if (isDesktop) window._wails?.invoke('desktop:busy:' + (keepers.size > 0 ? '1' : '0'))
}

reportBusy()

// useKeepPage 在 keep 为真期间让桌面窗口关闭时只隐藏，页面与进行中的请求保留到空闲
export function useKeepPage(keep: boolean) {
  const id = useId()
  useEffect(() => {
    if (!keep) return
    keepers.add(id)
    reportBusy()
    return () => {
      keepers.delete(id)
      reportBusy()
    }
  }, [id, keep])
}
