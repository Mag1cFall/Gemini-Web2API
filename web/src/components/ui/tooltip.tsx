import { Tooltip as Primitive } from 'radix-ui'
import type { ReactNode } from 'react'

// TooltipProvider 统一提示的出现延迟
export function TooltipProvider({ children }: { children: ReactNode }) {
  return (
    <Primitive.Provider delayDuration={400} skipDelayDuration={200}>
      {children}
    </Primitive.Provider>
  )
}

// Tooltip 在悬停与键盘聚焦时显示说明，shortcut 显示对应快捷键
export function Tooltip({
  label,
  shortcut,
  side = 'bottom',
  children,
}: {
  label: ReactNode
  shortcut?: string
  side?: 'top' | 'bottom' | 'left' | 'right'
  children: ReactNode
}) {
  return (
    <Primitive.Root>
      <Primitive.Trigger asChild>{children}</Primitive.Trigger>
      <Primitive.Portal>
        <Primitive.Content
          side={side}
          sideOffset={8}
          collisionPadding={8}
          className="z-popover flex max-w-prose items-center gap-2 rounded-sm bg-surface px-3 py-2 text-xs text-foreground shadow-popover data-[state=closed]:animate-fade-out data-[state=delayed-open]:animate-fade-in"
        >
          <span className="break-words">{label}</span>
          {shortcut && (
            <kbd className="rounded-sm border border-border px-1 font-mono text-xs text-foreground-muted">
              {shortcut}
            </kbd>
          )}
          <Primitive.Arrow className="fill-surface" width={10} height={5} />
        </Primitive.Content>
      </Primitive.Portal>
    </Primitive.Root>
  )
}
