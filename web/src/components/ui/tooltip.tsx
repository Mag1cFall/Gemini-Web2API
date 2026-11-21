import { Tooltip as Primitive } from 'radix-ui'
import { type ReactNode, useRef, useState } from 'react'

// TooltipProvider 统一提示的出现延迟
export function TooltipProvider({ children }: { children: ReactNode }) {
  return (
    <Primitive.Provider delayDuration={400} skipDelayDuration={200}>
      {children}
    </Primitive.Provider>
  )
}

// Tooltip 在悬停与键盘聚焦时显示说明；overflow 为真时只在子元素的文字被截断时显示
export function Tooltip({
  label,
  side = 'bottom',
  overflow = false,
  children,
}: {
  label: ReactNode
  side?: 'top' | 'bottom' | 'left' | 'right'
  overflow?: boolean
  children: ReactNode
}) {
  const trigger = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  return (
    <Primitive.Root
      open={open}
      onOpenChange={(next) => {
        const node = trigger.current
        setOpen(next && (!overflow || (node !== null && node.scrollWidth > node.clientWidth)))
      }}
    >
      <Primitive.Trigger ref={trigger} asChild>
        {children}
      </Primitive.Trigger>
      <Primitive.Portal>
        <Primitive.Content
          side={side}
          sideOffset={8}
          collisionPadding={8}
          className="z-popover flex max-w-prose items-center gap-2 rounded-sm bg-surface px-3 py-2 text-xs text-foreground shadow-popover data-[state=closed]:animate-fade-out data-[state=delayed-open]:animate-fade-in"
        >
          <span className="break-words">{label}</span>
          <Primitive.Arrow className="fill-surface" width={10} height={5} />
        </Primitive.Content>
      </Primitive.Portal>
    </Primitive.Root>
  )
}

// DisabledHint 在 reason 非空时把子元素包在可悬停的外层中，悬停显示不可用的原因；不可用的按钮自身不响应悬停
export function DisabledHint({ reason, children }: { reason: string; children: ReactNode }) {
  if (!reason) return children
  return (
    <Tooltip label={reason}>
      <span className="inline-flex">{children}</span>
    </Tooltip>
  )
}
