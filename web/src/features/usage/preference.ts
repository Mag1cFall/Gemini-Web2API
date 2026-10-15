import { useState } from 'react'

// readPreference 读取本地保存的界面偏好，存储不可用或取值非法时返回默认值
function readPreference<T>(key: string, fallback: T, valid: (value: unknown) => value is T): T {
  try {
    const raw = localStorage.getItem(key)
    if (raw === null) return fallback
    const value: unknown = JSON.parse(raw)
    return valid(value) ? value : fallback
  } catch {
    return fallback
  }
}

// usePreference 返回保存在本地存储中的用量页偏好与修改函数，存储不可用时只在本次会话生效
export function usePreference<T>(name: string, fallback: T, valid: (value: unknown) => value is T) {
  const key = 'gemini-web2api.usage.' + name
  const [value, setValue] = useState(() => readPreference(key, fallback, valid))
  const update = (next: T) => {
    setValue(next)
    try {
      localStorage.setItem(key, JSON.stringify(next))
    } catch {
      // 存储不可用时只在本次会话生效
    }
  }
  return [value, update] as const
}
