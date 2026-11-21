import { ArrowRight, Plus, X } from 'lucide-react'
import type { ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { joinPairs, splitPairs } from '@/features/settings/validate'

// PairInput 把逗号分隔的“名称:值”配置编辑为成对的行，left 与 right 渲染一行两侧的控件
export function PairInput({
  id,
  value,
  invalid,
  addLabel,
  removeLabel,
  left,
  right,
  onChange,
}: {
  id: string
  value: string
  invalid: boolean
  addLabel: string
  removeLabel: string
  left: (value: string, onChange: (next: string) => void) => ReactNode
  right: (value: string, onChange: (next: string) => void) => ReactNode
  onChange: (next: string) => void
}) {
  const rows = splitPairs(value)
  const update = (index: number, side: 0 | 1, next: string) => {
    onChange(
      joinPairs(
        rows.map((row, at) =>
          at === index ? (side === 0 ? [next, row[1]] : [row[0], next]) : row,
        ),
      ),
    )
  }
  return (
    <div id={id} aria-invalid={invalid || undefined} className="flex w-full flex-col gap-2">
      {rows.map(([name, target], index) => (
        <div key={index} className="flex min-w-0 items-center gap-2">
          <div className="min-w-0 flex-1">
            {left(name, (next) => {
              update(index, 0, next)
            })}
          </div>
          <ArrowRight size={14} aria-hidden className="shrink-0 text-foreground-subtle" />
          <div className="min-w-0 flex-1">
            {right(target, (next) => {
              update(index, 1, next)
            })}
          </div>
          <Button
            variant="ghost"
            size="icon"
            aria-label={removeLabel}
            className="size-6 shrink-0"
            onClick={() => {
              onChange(joinPairs(rows.filter((_, at) => at !== index)))
            }}
          >
            <X size={16} aria-hidden />
          </Button>
        </div>
      ))}
      <Button
        variant="ghost"
        size="sm"
        className="-ml-3 self-start"
        onClick={() => {
          onChange(joinPairs([...rows, ['', '']]))
        }}
      >
        <Plus size={16} aria-hidden />
        {addLabel}
      </Button>
    </div>
  )
}
