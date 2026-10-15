import { CircleAlert, LoaderCircle } from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'

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
        <p className="text-sm break-words text-foreground-muted">
          {error instanceof Error ? error.message : String(error)}
        </p>
      </div>
      {onRetry && <Button onClick={onRetry}>{retryLabel}</Button>}
    </div>
  )
}
