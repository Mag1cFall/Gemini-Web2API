import type { ReactNode } from 'react'

// Stat 是指标条中的一项
export interface Stat {
  label: string
  value: ReactNode
  detail?: ReactNode
}

// StatStrip 是浅灰底的指标条，各项以细线分隔，窄屏换行
export function StatStrip({ items }: { items: Stat[] }) {
  return (
    <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-lg bg-border md:grid-cols-4">
      {items.map((item) => (
        <div
          key={item.label}
          className="flex min-w-0 flex-col gap-1 bg-muted px-6 py-4 max-sm:px-4"
        >
          <dt className="text-sm text-foreground-muted">{item.label}</dt>
          <dd className="text-2xl font-semibold break-words tabular-nums">{item.value}</dd>
          {item.detail && <dd className="text-xs text-foreground-muted">{item.detail}</dd>}
        </div>
      ))}
    </dl>
  )
}
