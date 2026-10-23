import { useSyncExternalStore } from 'react'

// Theme 是管理端配色模式
export type Theme = 'light' | 'dark'

// themeKey 是保存配色选择的 localStorage 键
const themeKey = 'user_theme'

// systemQuery 是系统深色模式的媒体查询
const systemQuery = window.matchMedia('(prefers-color-scheme: dark)')

// currentTheme 返回根元素当前的配色模式，首帧前由 public/theme.js 写入
function currentTheme(): Theme {
  return document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light'
}

// subscribeTheme 在根元素的配色模式变化时调用 onChange，返回取消订阅的函数
function subscribeTheme(onChange: () => void) {
  const observer = new MutationObserver(onChange)
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  return () => {
    observer.disconnect()
  }
}

// useTheme 返回根元素当前的配色模式，跟随系统切换与手动切换后都随之更新
export function useTheme(): Theme {
  return useSyncExternalStore(subscribeTheme, currentTheme)
}

// savedTheme 返回保存的配色选择，没有保存时为空
function savedTheme(): Theme | undefined {
  try {
    const saved = window.localStorage.getItem(themeKey)
    return saved === 'light' || saved === 'dark' ? saved : undefined
  } catch {
    return undefined
  }
}

// setTheme 切换配色模式并记住选择
export function setTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme
  try {
    window.localStorage.setItem(themeKey, theme)
  } catch {
    // 无法写入时只在本次打开期间生效
  }
}

// applyTheme 在没有保存选择时随系统配色切换
export function applyTheme() {
  systemQuery.addEventListener('change', (event) => {
    if (!savedTheme()) document.documentElement.dataset.theme = event.matches ? 'dark' : 'light'
  })
}
