import type { PointerEvent } from 'react'

// ignoreSecondaryRelease 阻止非主键（中键自动滚动、右键）松开时选中浮层中的选项
export function ignoreSecondaryRelease(event: PointerEvent) {
  if (event.button !== 0) event.preventDefault()
}
