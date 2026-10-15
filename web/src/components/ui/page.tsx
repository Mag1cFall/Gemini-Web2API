import type { ReactNode } from 'react'
import { twMerge } from 'tailwind-merge'

// PageHeader 是页面顶部的 24px 标题、说明与本页主操作
export function PageHeader({
  title,
  description,
  actions,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
}) {
  return (
    <header className="flex flex-wrap items-center justify-between gap-4">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold break-words">{title}</h1>
        {description && <p className="mt-1 text-sm text-foreground-muted">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-3">{actions}</div>}
    </header>
  )
}

// Card 是白色分组卡片：1px 边框、8px 圆角、无阴影，卡片内不再嵌套卡片
export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <section
      className={twMerge(
        'min-w-0 overflow-hidden rounded-md border border-border bg-surface',
        className,
      )}
    >
      {children}
    </section>
  )
}

// CardHeader 是卡片标题行：左侧标题与说明，右侧计数、开关或按钮，下方一条分隔线
export function CardHeader({
  title,
  description,
  actions,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-4">
      <div className="min-w-0">
        <h2 className="text-md font-semibold">{title}</h2>
        {description && <p className="mt-1 text-xs text-foreground-muted">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

// CardRow 是卡片中的一行：左侧标题与说明，右侧控件垂直居中，行间以分隔线隔开
export function CardRow({
  title,
  description,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  children?: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-6 gap-y-2 border-b border-border px-5 py-4 last:border-b-0">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="text-base">{title}</div>
        {description && <div className="text-xs text-foreground-muted">{description}</div>}
      </div>
      {children && <div className="flex shrink-0 items-center gap-2">{children}</div>}
    </div>
  )
}

// CardEmpty 是卡片内居中的空状态：线性图标与灰色文字
export function CardEmpty({ icon, children }: { icon?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-5 py-8 text-center text-sm text-foreground-muted">
      {icon}
      <span>{children}</span>
    </div>
  )
}

// CountBadge 是卡片标题行右侧的计数徽标，数字等宽
export function CountBadge({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex h-6 min-w-6 items-center justify-center rounded-full bg-muted px-2 text-xs font-medium text-foreground-muted tabular-nums">
      {children}
    </span>
  )
}

// Section 是带标题行的分组卡片，内容区有内边距
export function Section({
  title,
  description,
  actions,
  children,
}: {
  title: string
  description?: ReactNode
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <Card>
      <CardHeader title={title} description={description} actions={actions} />
      <div className="flex flex-col gap-4 px-5 py-4">{children}</div>
    </Card>
  )
}
