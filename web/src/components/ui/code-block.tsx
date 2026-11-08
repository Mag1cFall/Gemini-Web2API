import type { ReactNode } from 'react'

import { CopyButton } from '@/components/ui/copy-button'

// CodeBlock 是带文件名与复制按钮的代码片段，长行横向滚动
export function CodeBlock({
  code,
  title,
  copyLabel,
  copiedLabel,
}: {
  code: string
  title: ReactNode
  copyLabel: string
  copiedLabel: string
}) {
  return (
    <figure className="flex min-w-0 flex-col overflow-hidden rounded-md border border-border">
      <figcaption className="flex h-control items-center justify-between gap-3 border-b border-border bg-muted pr-1 pl-4 text-xs text-foreground-muted">
        <span className="min-w-0 truncate font-mono">{title}</span>
        <CopyButton value={code} label={copyLabel} copiedLabel={copiedLabel} />
      </figcaption>
      <pre className="overflow-x-auto bg-surface px-4 py-3 font-mono text-xs leading-[20px] whitespace-pre select-text">
        {code}
      </pre>
    </figure>
  )
}
