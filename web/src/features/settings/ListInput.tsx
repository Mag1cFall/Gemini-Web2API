import { Plus, X } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

// ListInput 把以逗号分隔的配置值编辑为每行一项的列表，可增删行
export function ListInput({
  id,
  value,
  itemLabel,
  addLabel,
  removeLabel,
  placeholder = '',
  onChange,
}: {
  id: string
  value: string
  itemLabel: string
  addLabel: string
  removeLabel: string
  placeholder?: string
  onChange: (next: string) => void
}) {
  const items = value.split(',')
  const update = (next: string[]) => {
    onChange(next.join(','))
  }
  return (
    <div className="flex w-full flex-col gap-2">
      {items.map((item, index) => (
        <div key={index} className="relative flex items-center">
          <Input
            id={index === 0 ? id : undefined}
            value={item}
            placeholder={placeholder}
            aria-label={itemLabel + ' ' + String(index + 1)}
            aria-describedby={id + '-hint'}
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
      ))}
      <Button
        variant="ghost"
        size="sm"
        className="-ml-3 self-start"
        onClick={() => {
          update([...items, ''])
        }}
      >
        <Plus size={16} aria-hidden />
        {addLabel}
      </Button>
    </div>
  )
}
