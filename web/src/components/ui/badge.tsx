import { cva, type VariantProps } from 'class-variance-authority'
import type { ReactNode } from 'react'

// badgeVariants 定义状态标签的五种语气
const badgeVariants = cva(
  'inline-flex h-6 shrink-0 items-center gap-1 rounded-sm px-2 text-xs font-medium whitespace-nowrap',
  {
    variants: {
      tone: {
        success: 'bg-success-soft text-success-text',
        warning: 'bg-warning-soft text-warning-text',
        error: 'bg-error-soft text-error-text',
        info: 'bg-info-soft text-primary',
        neutral: 'bg-muted text-foreground-muted',
      },
    },
    defaultVariants: { tone: 'neutral' },
  },
)

// dotVariants 是状态标签前的圆点颜色
const dotVariants = cva('size-2 rounded-full', {
  variants: {
    tone: {
      success: 'bg-success',
      warning: 'bg-warning',
      error: 'bg-error',
      info: 'bg-info',
      neutral: 'bg-foreground-subtle',
    },
  },
})

// BadgeTone 是状态标签的语气
export type BadgeTone = NonNullable<VariantProps<typeof badgeVariants>['tone']>

// Badge 是带圆点的状态标签，颜色之外同时用文字表达状态
export function Badge({ tone, children }: { tone: BadgeTone; children: ReactNode }) {
  return (
    <span className={badgeVariants({ tone })}>
      <span aria-hidden className={dotVariants({ tone })} />
      {children}
    </span>
  )
}

// Tag 是不表达状态的中性小标签，用于能力、方法等枚举值
export function Tag({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex h-6 shrink-0 items-center rounded-sm bg-muted px-2 font-mono text-xs whitespace-nowrap text-foreground-muted">
      {children}
    </span>
  )
}
