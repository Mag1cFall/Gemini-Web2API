import { Check } from 'lucide-react'
import { RadioGroup as Primitive } from 'radix-ui'
import type { ReactNode } from 'react'

// RadioOption 是单选组的一项，description 显示在名称下方
export interface RadioOption<T extends string> {
  value: T
  label: ReactNode
  description?: ReactNode
}

// RadioGroup 是带说明的单选列表，方向键在选项间移动
export function RadioGroup<T extends string>({
  value,
  onValueChange,
  options,
  label,
}: {
  value: T
  onValueChange: (value: T) => void
  options: RadioOption<T>[]
  label: string
}) {
  return (
    <Primitive.Root
      value={value}
      onValueChange={(next) => {
        onValueChange(next as T)
      }}
      aria-label={label}
      className="flex flex-col gap-3"
    >
      {options.map((option) => (
        <label key={option.value} className="flex cursor-pointer items-start gap-3">
          <span className="flex h-icon shrink-0 items-center">
            <Primitive.Item
              value={option.value}
              className="flex size-4 shrink-0 items-center justify-center rounded-full border border-border-strong bg-surface hover:border-primary data-[state=checked]:border-primary data-[state=checked]:bg-primary"
            >
              <Primitive.Indicator>
                <Check size={12} strokeWidth={3} className="text-white" aria-hidden />
              </Primitive.Indicator>
            </Primitive.Item>
          </span>
          <span className="min-w-0">
            <span className="block text-base font-medium">{option.label}</span>
            {option.description && (
              <span className="mt-1 block text-sm text-foreground-muted">{option.description}</span>
            )}
          </span>
        </label>
      ))}
    </Primitive.Root>
  )
}
