import type { ReactNode } from 'react'
import { twMerge } from 'tailwind-merge'

// Collapse 是可折叠区域：行高在 0fr 与 1fr 之间过渡并同时淡入淡出，收起后内容不可聚焦
export function Collapse({
  open,
  id,
  className,
  children,
}: {
  open: boolean
  id?: string
  className?: string
  children: ReactNode
}) {
  return (
    <div
      id={id}
      inert={!open}
      className={twMerge(
        'grid transition-[grid-template-rows,opacity] duration-(--duration-base) ' +
          (open ? 'grid-rows-[1fr] opacity-100' : 'grid-rows-[0fr] opacity-0'),
        className,
      )}
    >
      <div className="min-h-0 overflow-hidden">{children}</div>
    </div>
  )
}
