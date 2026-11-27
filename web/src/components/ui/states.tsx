import { CircleAlert, LoaderCircle } from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { ClampText } from '@/components/ui/clamp'
import { PageHeader } from '@/components/ui/page'

// useDelayedFlag 让加载指示延迟 200ms 出现，出现后至少保持 300ms
export function useDelayedFlag(active: boolean) {
  const [visible, setVisible] = useState(false)
  const [shownAt, setShownAt] = useState(0)
  useEffect(() => {
    if (active && !visible) {
      const timer = setTimeout(() => {
        setVisible(true)
        setShownAt(Date.now())
      }, 200)
      return () => {
        clearTimeout(timer)
      }
    }
    if (!active && visible) {
      const timer = setTimeout(
        () => {
          setVisible(false)
        },
        Math.max(0, 300 - (Date.now() - shownAt)),
      )
      return () => {
        clearTimeout(timer)
      }
    }
    return undefined
  }, [active, visible, shownAt])
  return visible
}

// Loading 是数据区域的加载状态
export function Loading({ label }: { label: string }) {
  const visible = useDelayedFlag(true)
  return (
    <div
      role="status"
      className="flex items-center justify-center gap-2 py-12 text-sm text-foreground-muted"
    >
      {visible && (
        <>
          <LoaderCircle aria-hidden size={16} className="animate-spin" />
          {label}
        </>
      )}
    </div>
  )
}

// placeholderRows 是页面占位骨架的行数，与一张表格卡片的常见高度相近
const placeholderRows = 6

// PagePlaceholder 是页面数据到达前的占位：标题立即显示，内容位置 200 ms 内保持空白，之后显示同高的骨架卡片
export function PagePlaceholder({
  title,
  description,
  label,
}: {
  title: ReactNode
  description?: ReactNode
  label: string
}) {
  const visible = useDelayedFlag(true)
  return (
    <>
      <PageHeader title={title} description={description} />
      <section
        role="status"
        aria-label={label}
        className={
          'flex min-h-[316px] flex-col rounded-md border ' +
          (visible ? 'border-border bg-surface' : 'border-transparent')
        }
      >
        {visible && (
          <>
            <div className="h-[57px] border-b border-border px-5 py-5">
              <div className="h-4 w-[160px] animate-pulse rounded-sm bg-muted" />
            </div>
            {Array.from({ length: placeholderRows }, (_, index) => (
              <div
                key={index}
                className="flex h-row items-center gap-6 border-b border-border px-5 last:border-b-0"
              >
                <div className="h-3 w-1/4 animate-pulse rounded-sm bg-muted" />
                <div className="h-3 w-1/3 animate-pulse rounded-sm bg-muted" />
                <div className="h-3 w-1/6 animate-pulse rounded-sm bg-muted" />
              </div>
            ))}
          </>
        )}
      </section>
    </>
  )
}

// EmptyState 是数据区域的空状态，action 给出下一步操作
export function EmptyState({
  icon,
  title,
  description,
  action,
}: {
  icon: ReactNode
  title: string
  description?: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-4 py-12 text-center">
      <span className="flex size-12 items-center justify-center rounded-lg bg-muted text-foreground-muted">
        {icon}
      </span>
      <div className="flex max-w-prose flex-col gap-1">
        <p className="text-md font-semibold">{title}</p>
        {description && <p className="text-sm text-foreground-muted">{description}</p>}
      </div>
      {action}
    </div>
  )
}

// ErrorState 是数据区域的错误状态，说明原因并提供重试
export function ErrorState({
  title,
  error,
  retryLabel,
  onRetry,
}: {
  title: string
  error: unknown
  retryLabel: string
  onRetry?: () => void
}) {
  return (
    <div
      role="alert"
      className="flex flex-col items-center justify-center gap-3 px-4 py-12 text-center"
    >
      <span className="flex size-12 items-center justify-center rounded-lg bg-error-soft text-error-text">
        <CircleAlert size={20} aria-hidden />
      </span>
      <div className="flex max-w-prose flex-col gap-1">
        <p className="text-md font-semibold">{title}</p>
        <ClampText className="text-sm wrap-anywhere text-foreground-muted">
          {error instanceof Error ? error.message : String(error)}
        </ClampText>
      </div>
      {onRetry && <Button onClick={onRetry}>{retryLabel}</Button>}
    </div>
  )
}
