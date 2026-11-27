import { type ReactNode, useLayoutEffect, useRef, useState } from 'react'

import { defineMessages, fill, useMessages } from '@/lib/i18n'

// clampMessages 是折叠文字与列表的展开、收起文案
const clampMessages = defineMessages({
  'zh-CN': { expand: '展开全文', collapse: '收起', more: '显示其余 {count} 项' },
  'zh-TW': { expand: '展開全文', collapse: '收合', more: '顯示其餘 {count} 項' },
  en: { expand: 'Show all', collapse: 'Show less', more: 'Show {count} more' },
  ja: { expand: 'すべて表示', collapse: '折りたたむ', more: '残り {count} 件を表示' },
  ko: { expand: '모두 보기', collapse: '접기', more: '나머지 {count}개 보기' },
  fr: { expand: 'Tout afficher', collapse: 'Réduire', more: 'Afficher {count} de plus' },
  de: { expand: 'Alles anzeigen', collapse: 'Weniger anzeigen', more: '{count} weitere anzeigen' },
})

// clampListLimit 是折叠列表收起时列出的项数
const clampListLimit = 3

// ClampText 默认只显示前三行的高度，超出时在下方给出展开与收起，收起时随宽度变化重新判断是否超出
export function ClampText({ className, children }: { className?: string; children: ReactNode }) {
  const m = useMessages(clampMessages)
  const text = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [overflow, setOverflow] = useState(false)
  useLayoutEffect(() => {
    const node = text.current
    if (!node || open) return
    const measure = () => {
      setOverflow(node.scrollHeight > node.clientHeight + 1)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(node)
    return () => {
      observer.disconnect()
    }
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

// ClampList 默认只列出前三项，其余合并为“+N”，点开后列出全部并给出收起；children 为各项 li
export function ClampList({
  label,
  className,
  children,
}: {
  label?: string
  className?: string
  children: ReactNode[]
}) {
  const m = useMessages(clampMessages)
  const [open, setOpen] = useState(false)
  const hidden = children.length - clampListLimit
  return (
    <ul aria-label={label} className={className}>
      {open || hidden <= 0 ? children : children.slice(0, clampListLimit)}
      {hidden > 0 && (
        <li>
          <button
            type="button"
            aria-expanded={open}
            aria-label={open ? m.collapse : fill(m.more, { count: hidden })}
            className="font-sans text-xs text-primary tabular-nums hover:text-primary-hover"
            onClick={() => {
              setOpen(!open)
            }}
          >
            {open ? m.collapse : '+' + String(hidden)}
          </button>
        </li>
      )}
    </ul>
  )
}
