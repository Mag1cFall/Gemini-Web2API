import { type ComponentProps, type ReactNode, useLayoutEffect, useRef } from 'react'
import { twMerge } from 'tailwind-merge'

// Table 是放在卡片内的数据表，表头 40px、行高 44px，过宽时只在表格内横向滚动；容器窄于 480px 时每行排成卡片，单元格前显示列名
export function Table({ label, children }: { label: string; children: ReactNode }) {
  const table = useRef<HTMLTableElement>(null)
  useLayoutEffect(() => {
    const node = table.current
    if (!node) return
    const heads = [...(node.tHead?.rows[0]?.cells ?? [])].map((cell) => cell.textContent)
    for (const body of node.tBodies) {
      for (const row of body.rows) {
        for (const [index, cell] of [...row.cells].entries())
          cell.dataset.label = heads[index] ?? ''
      }
    }
  })
  return (
    <div className="relative max-w-full overflow-x-auto [container:data-table/inline-size]">
      <table ref={table} aria-label={label} className="data-table w-full border-collapse text-base">
        {children}
      </table>
    </div>
  )
}

// Th 是表头单元格
export function Th({ className, ...props }: ComponentProps<'th'>) {
  return (
    <th
      scope="col"
      className={twMerge(
        'h-control-lg px-3 text-left text-xs font-normal whitespace-nowrap text-foreground-muted first:pl-5 last:pr-5',
        className,
      )}
      {...props}
    />
  )
}

// Td 是数据单元格，数字列用等宽数字
export function Td({ className, ...props }: ComponentProps<'td'>) {
  return (
    <td
      className={twMerge(
        'h-row border-t border-border px-3 py-2 align-middle tabular-nums first:pl-5 last:pr-5',
        className,
      )}
      {...props}
    />
  )
}
