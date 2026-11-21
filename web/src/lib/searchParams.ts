import type { SetURLSearchParams } from 'react-router'

// patchSearchParams 把非空键值合并到 URL 查询参数，空串或 undefined 删除对应键
export function patchSearchParams(
  setParams: SetURLSearchParams,
  changes: Record<string, string | undefined>,
) {
  setParams(
    (current) => {
      const next = new URLSearchParams(current)
      for (const [key, value] of Object.entries(changes)) {
        if (value) next.set(key, value)
        else next.delete(key)
      }
      return next
    },
    { replace: true },
  )
}
