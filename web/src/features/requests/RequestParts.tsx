import type { ReactNode } from 'react'

import { CopyButton } from '@/components/ui/copy-button'
import { bodyMessages } from '@/features/requests/messages'
import { useRequestBody } from '@/features/requests/queries'
import type { Attempt } from '@/features/requests/types'
import { ApiError } from '@/lib/api'
import { formatBytes, formatDuration } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// DetailItem 是详情列表的一项
export interface DetailItem {
  label: string
  value: ReactNode
}

// DetailList 是两列的名称与取值列表，取值过长时截断并以悬停显示全文
export function DetailList({ items }: { items: DetailItem[] }) {
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
      {items.map((item) => (
        <div key={item.label} className="flex min-w-0 justify-between gap-3">
          <dt className="shrink-0 text-foreground-muted">{item.label}</dt>
          <dd
            title={typeof item.value === 'string' ? item.value : undefined}
            className="min-w-0 truncate text-right tabular-nums"
          >
            {item.value}
          </dd>
        </div>
      ))}
    </dl>
  )
}

// DetailSection 是详情面板中带小标题的一节
export function DetailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-sm font-semibold">{title}</h3>
      {children}
    </section>
  )
}

// AttemptList 是失败的上游尝试时间线，empty 为没有失败尝试时的说明
export function AttemptList({ attempts, empty }: { attempts: Attempt[]; empty: string }) {
  const { locale } = useI18n()
  if (attempts.length === 0) return <p className="text-sm text-foreground-muted">{empty}</p>
  return (
    <ol className="flex flex-col gap-2 border-l border-border pl-4">
      {attempts.map((attempt, index) => (
        <li key={index} className="relative text-sm">
          <span
            aria-hidden
            className="absolute top-[7px] -left-[20px] size-2 rounded-full bg-error"
          />
          <div className="flex flex-wrap gap-x-3">
            <span>{attempt.account || '—'}</span>
            <span className="text-foreground-muted tabular-nums">
              {attempt.status > 0 ? 'HTTP ' + String(attempt.status) : '—'}
            </span>
            <span className="text-foreground-muted tabular-nums">
              {formatDuration(attempt.duration_ms, locale)}
            </span>
          </div>
          <p className="font-mono text-xs break-words text-error-text">{attempt.error}</p>
        </li>
      ))}
    </ol>
  )
}

// pretty 缩进 JSON 正文，不是 JSON 时保持原文
function pretty(text: string) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

// byteLength 返回文本的 UTF-8 字节数
function byteLength(text: string) {
  return new TextEncoder().encode(text).length
}

// RequestBodyView 读取并显示一条已结束请求保存的请求与响应正文，没有保存时说明如何开启
export function RequestBodyView({ id }: { id: string }) {
  const m = useMessages(bodyMessages)
  const { locale } = useI18n()
  const body = useRequestBody(id, true)
  if (body.isPending) return <p className="text-sm text-foreground-muted">{m.loading}</p>
  if (body.isError) {
    return body.error instanceof ApiError && body.error.status === 404 ? (
      <p className="text-sm text-foreground-muted">{m.missing}</p>
    ) : (
      <p role="alert" className="text-sm text-error-text">
        {body.error.message}
      </p>
    )
  }
  const parts = [
    { label: m.request, text: body.data.request, size: body.data.request_size },
    { label: m.response, text: body.data.response, size: body.data.response_size },
  ]
  return (
    <div className="flex flex-col gap-3">
      {parts.map((part) => {
        const saved = byteLength(part.text)
        return (
          <div key={part.label} className="flex min-w-0 flex-col gap-1">
            <div className="flex items-center gap-2 text-xs text-foreground-muted">
              <span className="font-medium text-foreground">{part.label}</span>
              <span className="tabular-nums">{formatBytes(part.size, locale)}</span>
              {part.size > saved && (
                <span className="text-warning-text">
                  {fill(m.truncated, {
                    saved: formatBytes(saved, locale),
                    size: formatBytes(part.size, locale),
                  })}
                </span>
              )}
              <span className="ml-auto">
                <CopyButton value={part.text} label={m.copy} copiedLabel={m.copied} />
              </span>
            </div>
            <pre className="max-h-[320px] overflow-auto rounded-sm border border-border bg-panel p-3 font-mono text-xs break-words whitespace-pre-wrap">
              {part.text ? pretty(part.text) : m.empty}
            </pre>
          </div>
        )
      })}
    </div>
  )
}
