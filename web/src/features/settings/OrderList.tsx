import { ArrowDown, ArrowUp, GripVertical } from 'lucide-react'
import { type PointerEvent as ReactPointerEvent, useEffect, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'
import { fill } from '@/lib/i18n'

// OrderList 是按住左侧把手拖动排序的列表，每行另有上移、下移按钮供键盘调整
export function OrderList({
  id,
  items,
  format,
  dragLabel,
  moveUpLabel,
  moveDownLabel,
  onChange,
}: {
  id: string
  items: string[]
  format: (item: string) => string
  dragLabel: string
  moveUpLabel: string
  moveDownLabel: string
  onChange: (next: string[]) => void
}) {
  const [dragging, setDragging] = useState<number>()
  const latest = useRef({ items, onChange })
  useEffect(() => {
    latest.current = { items, onChange }
  }, [items, onChange])
  const move = (from: number, to: number) => {
    const current = latest.current.items
    if (to < 0 || to >= current.length || from === to) return
    const next = [...current]
    const [item] = next.splice(from, 1)
    if (item !== undefined) next.splice(to, 0, item)
    latest.current.onChange(next)
  }
  const startDrag = (index: number) => (event: ReactPointerEvent<HTMLButtonElement>) => {
    event.preventDefault()
    let current = index
    setDragging(index)
    const follow = (moved: PointerEvent) => {
      const row = document
        .elementFromPoint(moved.clientX, moved.clientY)
        ?.closest<HTMLElement>(`[data-order="${id}"]`)
      const target = row ? Number(row.dataset.index) : current
      if (target === current) return
      move(current, target)
      current = target
      setDragging(target)
    }
    const stop = () => {
      setDragging(undefined)
      window.removeEventListener('pointermove', follow)
      window.removeEventListener('pointerup', stop)
    }
    window.addEventListener('pointermove', follow)
    window.addEventListener('pointerup', stop)
  }
  return (
    <ol id={id} className="flex w-full touch-none flex-col gap-1">
      {items.map((item, index) => (
        <li
          key={item}
          data-order={id}
          data-index={index}
          className={
            'flex min-w-0 items-center gap-2 rounded-sm border border-border bg-surface py-1 pr-1 pl-1 text-sm ' +
            (dragging === index ? 'border-primary-border bg-primary-soft' : '')
          }
        >
          <button
            type="button"
            aria-label={dragLabel}
            className="flex size-6 shrink-0 cursor-grab items-center justify-center rounded-sm text-foreground-subtle hover:bg-hover active:cursor-grabbing"
            onPointerDown={startDrag(index)}
          >
            <GripVertical size={16} aria-hidden />
          </button>
          <span className="w-5 shrink-0 text-xs text-foreground-muted tabular-nums">
            {index + 1}
          </span>
          <Tooltip overflow label={format(item)}>
            <span className="min-w-0 flex-1 truncate">{format(item)}</span>
          </Tooltip>
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            aria-label={fill(moveUpLabel, { name: format(item) })}
            disabled={index === 0}
            onClick={() => {
              move(index, index - 1)
            }}
          >
            <ArrowUp size={14} aria-hidden />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            aria-label={fill(moveDownLabel, { name: format(item) })}
            disabled={index === items.length - 1}
            onClick={() => {
              move(index, index + 1)
            }}
          >
            <ArrowDown size={14} aria-hidden />
          </Button>
        </li>
      ))}
    </ol>
  )
}
