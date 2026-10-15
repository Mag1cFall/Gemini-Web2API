import { Minus, Plus } from 'lucide-react'
import type { ComponentProps } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

// NumberInput 是带加减按钮的整数输入框，替代浏览器自带的上下箭头
export function NumberInput({
  value,
  onChange,
  min,
  max,
  decreaseLabel,
  increaseLabel,
  ...props
}: Omit<ComponentProps<'input'>, 'value' | 'onChange' | 'min' | 'max'> & {
  value: number
  onChange: (value: number) => void
  min: number
  max: number
  decreaseLabel: string
  increaseLabel: string
}) {
  const clamp = (next: number) => Math.min(max, Math.max(min, next))
  return (
    <div className="flex items-center gap-1">
      <Button
        size="icon"
        aria-label={decreaseLabel}
        disabled={value <= min}
        onClick={() => {
          onChange(clamp(value - 1))
        }}
      >
        <Minus size={16} aria-hidden />
      </Button>
      <Input
        inputMode="numeric"
        value={Number.isNaN(value) ? '' : String(value)}
        onChange={(event) => {
          const digits = event.target.value.replace(/\D/g, '')
          onChange(digits === '' ? Number.NaN : Number(digits))
        }}
        className="w-16 text-center tabular-nums"
        {...props}
      />
      <Button
        size="icon"
        aria-label={increaseLabel}
        disabled={value >= max}
        onClick={() => {
          onChange(clamp(value + 1))
        }}
      >
        <Plus size={16} aria-hidden />
      </Button>
    </div>
  )
}
