import type { ComponentProps, ReactNode } from 'react'
import { twMerge } from 'tailwind-merge'

// Table 是放在卡片内的数据表，表头 40px、行高 44px，过宽时只在表格内横向滚动
export function Table({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="relative max-w-full overflow-x-auto">
      <table aria-label={label} className="w-full border-collapse text-base">
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
