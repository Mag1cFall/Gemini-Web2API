import { CircleHelp } from 'lucide-react'
import type { ReactNode } from 'react'

import { Tooltip } from '@/components/ui/tooltip'

// HelpTip 是名称旁的问号按钮，悬停或键盘聚焦时显示说明
export function HelpTip({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Tooltip label={children} side="top">
      <button
        type="button"
        aria-label={label}
        className="inline-flex size-5 shrink-0 cursor-help items-center justify-center rounded-full align-middle text-foreground-subtle hover:text-foreground-muted focus-visible:text-foreground-muted"
      >
        <CircleHelp size={14} aria-hidden />
      </button>
    </Tooltip>
  )
}
