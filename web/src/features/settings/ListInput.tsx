import { Plus, X } from 'lucide-react'
import type { ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

// ListInput 把以逗号分隔的配置值编辑为每行一项的列表，可增删行；rowAction 渲染每行末尾的额外按钮，actions 渲染添加按钮旁的按钮
export function ListInput({
  id,
  value,
  itemLabel,
  addLabel,
  removeLabel,
  placeholder = '',
  invalid = false,
  rowAction,
  actions,
  onChange,
}: {
  id: string
  value: string
  itemLabel: string
  addLabel: string
  removeLabel: string
  placeholder?: string
  invalid?: boolean
  rowAction?: (index: number, replace: (value: string) => void) => ReactNode
  actions?: (append: (value: string) => void) => ReactNode
  onChange: (next: string) => void
}) {
  const items = value.split(',')
  const update = (next: string[]) => {
    onChange(next.join(','))
  }
  return (
    <div className="flex w-full flex-col gap-2">
      {items.map((item, index) => (
        <div key={index} className="flex items-center gap-2">
          <div className="relative flex min-w-0 flex-1 items-center">
            <Input
              id={index === 0 ? id : undefined}
              value={item}
              placeholder={placeholder}
              aria-label={itemLabel + ' ' + String(index + 1)}
              aria-describedby={id + '-error'}
              aria-invalid={invalid || undefined}
              className="pr-8"
              onChange={(event) => {
                update(items.map((entry, at) => (at === index ? event.target.value : entry)))
              }}
            />
            <Button
              variant="ghost"
              size="icon"
              aria-label={removeLabel}
              className="absolute top-1/2 right-1 size-6 -translate-y-1/2"
              disabled={items.length === 1 && item === ''}
              onClick={() => {
                update(items.filter((_, at) => at !== index))
              }}
            >
              <X size={16} aria-hidden />
            </Button>
          </div>
          {rowAction?.(index, (next) => {
            update(items.map((entry, at) => (at === index ? next : entry)))
          })}
        </div>
      ))}
      <div className="-ml-3 flex flex-wrap items-center gap-1">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            update([...items, ''])
          }}
        >
          <Plus size={16} aria-hidden />
          {addLabel}
        </Button>
        {actions?.((next) => {
          update(items.length === 1 && items[0] === '' ? [next] : [...items, next])
        })}
      </div>
    </div>
  )
}
