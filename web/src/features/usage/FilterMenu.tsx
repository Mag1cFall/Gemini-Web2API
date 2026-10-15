import { ChevronDown, Search } from 'lucide-react'
import { useId, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { usageMessages } from '@/features/usage/messages'
import { useMessages } from '@/lib/i18n'

// FilterMenu 是一个维度的多选筛选，浮层内可搜索取值，已选取值始终列在前面
export function FilterMenu({
  label,
  options,
  selected,
  format,
  onChange,
}: {
  label: string
  options: string[]
  selected: string[]
  format: (value: string) => string
  onChange: (values: string[]) => void
}) {
  const m = useMessages(usageMessages)
  const id = useId()
  const [query, setQuery] = useState('')
  const needle = query.trim().toLowerCase()
  const values = [...new Set([...selected, ...options])]
  const visible = needle
    ? values.filter((value) => format(value).toLowerCase().includes(needle))
    : values
  return (
    <Popover
      onOpenChange={(open) => {
        if (open) setQuery('')
      }}
    >
      <PopoverTrigger asChild>
        <Button
          size="sm"
          className={
            selected.length > 0 ? 'border-primary-border bg-primary-soft text-primary' : ''
          }
        >
          {label}
          {selected.length > 0 && (
            <span className="rounded-sm bg-primary px-1 text-xs text-primary-foreground tabular-nums">
              {selected.length}
            </span>
          )}
          <ChevronDown size={16} aria-hidden className="text-foreground-muted" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-field p-2">
        <div className="relative mb-2">
          <Search
            size={16}
            aria-hidden
            className="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-foreground-muted"
          />
          <Input
            type="search"
            aria-label={m.filterSearch}
            placeholder={m.filterSearch}
            value={query}
            onChange={(event) => {
              setQuery(event.target.value)
            }}
            className="h-control-sm pl-8"
          />
        </div>
        <fieldset className="max-h-[256px] min-w-0 overflow-y-auto">
          <legend className="sr-only">{label}</legend>
          {visible.length === 0 ? (
            <p className="px-2 py-3 text-center text-sm text-foreground-muted">{m.filterNone}</p>
          ) : (
            visible.map((value, index) => (
              <label
                key={value}
                htmlFor={`${id}-${String(index)}`}
                className="flex h-control-sm cursor-pointer items-center gap-2 rounded-sm px-2 text-sm hover:bg-hover"
              >
                <input
                  id={`${id}-${String(index)}`}
                  type="checkbox"
                  checked={selected.includes(value)}
                  onChange={(event) => {
                    onChange(
                      event.target.checked
                        ? [...selected, value]
                        : selected.filter((item) => item !== value),
                    )
                  }}
                  className="size-4 shrink-0 accent-primary"
                />
                <span className="min-w-0 flex-1 truncate" title={format(value)}>
                  {format(value)}
                </span>
              </label>
            ))
          )}
        </fieldset>
        {selected.length > 0 && (
          <div className="mt-2 border-t border-border pt-2">
            <Button
              variant="ghost"
              size="sm"
              className="w-full justify-start"
              onClick={() => {
                onChange([])
              }}
            >
              {m.filtersClear}
            </Button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  )
}
