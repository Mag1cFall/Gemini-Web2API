import { ToggleGroup } from 'radix-ui'

import { useSlidingIndicator } from '@/components/ui/sliding-indicator'

// Segmented 是胶囊分段选择，用于筛选与范围切换；选中背景是平移到选中项的白色滑块
export function Segmented<T extends string>({
  value,
  onValueChange,
  options,
  label,
}: {
  value: T
  onValueChange: (value: T) => void
  options: { value: T; label: string }[]
  label: string
}) {
  const { container, indicator } = useSlidingIndicator<HTMLDivElement, HTMLSpanElement>(
    '[data-state=on]',
    true,
  )
  return (
    <ToggleGroup.Root
      ref={container}
      type="single"
      value={value}
      onValueChange={(next) => {
        if (next) onValueChange(next as T)
      }}
      aria-label={label}
      className="relative inline-flex w-fit max-w-full shrink-0 gap-1 overflow-x-auto rounded-lg bg-muted p-1"
    >
      <span
        ref={indicator}
        aria-hidden
        className="absolute top-1 bottom-1 left-0 rounded-md bg-surface opacity-0 shadow-raised data-ready:transition-transform data-ready:duration-(--duration-base)"
      />
      {options.map((option) => (
        <ToggleGroup.Item
          key={option.value}
          value={option.value}
          className="relative h-control-sm shrink-0 rounded-md px-3 text-sm whitespace-nowrap text-foreground data-[state=off]:hover:bg-hover pointer-coarse:h-touch"
        >
          {option.label}
        </ToggleGroup.Item>
      ))}
    </ToggleGroup.Root>
  )
}
