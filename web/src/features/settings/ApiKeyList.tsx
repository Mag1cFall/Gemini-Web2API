import { Plus, X } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { settingsMessages } from '@/features/settings/messages'
import { useMessages } from '@/lib/i18n'

// ApiKeyList 把以逗号分隔的 PROXY_API_KEY 编辑为每行一个 key 的列表，可增删行
export function ApiKeyList({
  id,
  value,
  onChange,
}: {
  id: string
  value: string
  onChange: (next: string) => void
}) {
  const m = useMessages(settingsMessages)
  const keys = value.split(',')
  const update = (next: string[]) => {
    onChange(next.join(','))
  }
  return (
    <div className="flex w-full flex-col gap-2">
      {keys.map((key, index) => (
        <div key={index} className="relative flex items-center">
          <Input
            id={index === 0 ? id : undefined}
            value={key}
            aria-label={m.apiKey + ' ' + String(index + 1)}
            aria-describedby={id + '-hint'}
            className="pr-8"
            onChange={(event) => {
              update(keys.map((item, at) => (at === index ? event.target.value : item)))
            }}
          />
          <Button
            variant="ghost"
            size="icon"
            aria-label={m.apiKeyRemove}
            className="absolute top-1/2 right-1 size-6 -translate-y-1/2"
            disabled={keys.length === 1 && key === ''}
            onClick={() => {
              update(keys.filter((_, at) => at !== index))
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
          update([...keys, ''])
        }}
      >
        <Plus size={16} aria-hidden />
        {m.apiKeyAdd}
      </Button>
    </div>
  )
}
