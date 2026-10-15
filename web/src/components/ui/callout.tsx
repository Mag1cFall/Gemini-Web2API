import { cva } from 'class-variance-authority'
import type { ReactNode } from 'react'

// calloutVariants 是面板内提示块的语气
const calloutVariants = cva('flex flex-col items-start gap-3 rounded-md p-4 text-sm', {
  variants: {
    tone: {
      muted: 'bg-muted text-foreground-muted',
      warning: 'bg-warning-soft text-warning-text',
      error: 'bg-error-soft text-error-text',
    },
  },
})

// Callout 是面板内的说明或警告块，用于不需要弹层的就地提示
export function Callout({
  tone,
  role,
  children,
}: {
  tone: 'muted' | 'warning' | 'error'
  role?: 'alert' | 'status'
  children: ReactNode
}) {
  return (
    <div role={role} className={calloutVariants({ tone })}>
      {children}
    </div>
  )
}
