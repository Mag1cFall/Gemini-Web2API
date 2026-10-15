import { Popover as Primitive } from 'radix-ui'
import type { ReactNode } from 'react'
import { twMerge } from 'tailwind-merge'

export const Popover = Primitive.Root
export const PopoverTrigger = Primitive.Trigger

// PopoverContent 是白底浮层，用于筛选、范围选择等就地表单
export function PopoverContent({
  align = 'start',
  className,
  children,
}: {
  align?: 'start' | 'end'
  className?: string
  children: ReactNode
}) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        align={align}
        sideOffset={4}
        collisionPadding={8}
        className={twMerge(
          'z-popover max-w-(--radix-popover-content-available-width) rounded-sm bg-surface p-3 shadow-popover focus:outline-none data-[state=closed]:animate-fade-out data-[state=open]:animate-fade-in',
          className,
        )}
      >
        {children}
      </Primitive.Content>
    </Primitive.Portal>
  )
}
