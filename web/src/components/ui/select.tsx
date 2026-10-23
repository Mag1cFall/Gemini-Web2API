import { Check, ChevronDown } from 'lucide-react'
import { Select as Primitive } from 'radix-ui'
import { twMerge } from 'tailwind-merge'

import { ignoreSecondaryRelease } from '@/lib/pointer'

// SelectOption 是下拉框的一个选项
export interface SelectOption<T extends string> {
  value: T
  label: string
}

// Select 是单选下拉框，保留键盘选择与读屏语义
export function Select<T extends string>({
  value,
  onValueChange,
  options,
  label,
  size = 'md',
  className,
  id,
}: {
  value: T
  onValueChange: (value: T) => void
  options: SelectOption<T>[]
  label: string
  size?: 'sm' | 'md'
  className?: string
  id?: string
}) {
  return (
    <Primitive.Root
      value={value}
      onValueChange={(next) => {
        onValueChange(next as T)
      }}
    >
      <Primitive.Trigger
        id={id}
        aria-label={label}
        className={twMerge(
          'inline-flex min-w-0 items-center justify-between gap-2 rounded-md border border-border bg-surface px-3 text-sm text-foreground hover:border-primary-border data-[state=open]:border-primary-border pointer-coarse:h-touch',
          size === 'sm' ? 'h-control-sm' : 'h-control',
          className,
        )}
      >
        <span className="truncate" title={options.find((option) => option.value === value)?.label}>
          <Primitive.Value />
        </span>
        <Primitive.Icon className="shrink-0 text-foreground-muted">
          <ChevronDown size={16} aria-hidden />
        </Primitive.Icon>
      </Primitive.Trigger>
      <Primitive.Portal>
        <Primitive.Content
          position="popper"
          sideOffset={4}
          collisionPadding={8}
          className="z-popover max-h-(--radix-select-content-available-height) min-w-(--radix-select-trigger-width) overflow-hidden rounded-sm bg-surface py-1 shadow-popover data-[state=closed]:animate-fade-out data-[state=open]:animate-fade-in"
        >
          <Primitive.Viewport>
            {options.map((option) => (
              <Primitive.Item
                key={option.value}
                value={option.value}
                onPointerUp={ignoreSecondaryRelease}
                className="flex h-control-sm cursor-pointer items-center justify-between gap-4 px-4 text-sm text-foreground outline-none select-none data-highlighted:bg-hover data-[state=checked]:text-primary pointer-coarse:h-touch"
              >
                <Primitive.ItemText>{option.label}</Primitive.ItemText>
                <Primitive.ItemIndicator>
                  <Check size={16} aria-hidden />
                </Primitive.ItemIndicator>
              </Primitive.Item>
            ))}
          </Primitive.Viewport>
        </Primitive.Content>
      </Primitive.Portal>
    </Primitive.Root>
  )
}
