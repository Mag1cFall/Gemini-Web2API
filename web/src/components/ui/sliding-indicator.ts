import { useLayoutEffect, useRef } from 'react'

// useSlidingIndicator 返回容器与指示条的 ref，用 transform 把指示条平移到容器内选中项下方；fit 为真时宽度与选中项相同，否则保持自身宽度居中；首次定位不播放过渡
export function useSlidingIndicator<C extends HTMLElement, I extends HTMLElement>(
  selector: string,
  fit: boolean,
) {
  const container = useRef<C>(null)
  const indicator = useRef<I>(null)
  useLayoutEffect(() => {
    const root = container.current
    const bar = indicator.current
    if (!root || !bar) return
    const place = () => {
      const item = root.querySelector<HTMLElement>(selector)
      if (!item) {
        bar.style.opacity = '0'
        return
      }
      if (fit) bar.style.width = `${String(item.offsetWidth)}px`
      const left = fit
        ? item.offsetLeft
        : item.offsetLeft + (item.offsetWidth - bar.offsetWidth) / 2
      bar.style.transform = `translateX(${String(left)}px)`
      bar.style.opacity = '1'
    }
    place()
    const frame = requestAnimationFrame(() => {
      bar.dataset.ready = ''
    })
    const resize = new ResizeObserver(place)
    resize.observe(root)
    const mutation = new MutationObserver(place)
    mutation.observe(root, { subtree: true, attributes: true, attributeFilter: ['data-state'] })
    return () => {
      cancelAnimationFrame(frame)
      resize.disconnect()
      mutation.disconnect()
    }
  }, [selector, fit])
  return { container, indicator }
}
