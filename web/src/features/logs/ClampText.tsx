import { type ReactNode, useLayoutEffect, useRef, useState } from 'react'

import { logsMessages } from '@/features/logs/messages'
import { useMessages } from '@/lib/i18n'

// ClampText 默认只显示前三行的高度，超出时在下方给出展开与收起
export function ClampText({ className, children }: { className?: string; children: ReactNode }) {
  const m = useMessages(logsMessages)
  const text = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [overflow, setOverflow] = useState(false)
  useLayoutEffect(() => {
    const node = text.current
    if (node && !open) setOverflow(node.scrollHeight > node.clientHeight + 1)
  }, [children, open])
  return (
    <div className="min-w-0">
      <div ref={text} className={(open ? '' : 'max-h-[3lh] overflow-hidden ') + (className ?? '')}>
        {children}
      </div>
      {(overflow || open) && (
        <button
          type="button"
          aria-expanded={open}
          className="font-sans text-xs text-primary hover:text-primary-hover"
          onClick={() => {
            setOpen(!open)
          }}
        >
          {open ? m.collapse : m.expand}
        </button>
      )}
    </div>
  )
}
