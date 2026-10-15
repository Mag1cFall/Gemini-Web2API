import { cva } from 'class-variance-authority'

// fillVariants 按语气给进度填充上色
const fillVariants = cva('h-full rounded-full', {
  variants: {
    tone: { info: 'bg-primary', warning: 'bg-warning', error: 'bg-error' },
  },
})

// Meter 是 8px 高的比例条，value 为 0 到 1
export function Meter({
  value,
  tone,
  label,
}: {
  value: number
  tone: 'info' | 'warning' | 'error'
  label: string
}) {
  const clamped = Math.min(1, Math.max(0, value))
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(clamped * 100)}
      className="h-2 w-full overflow-hidden rounded-full bg-hover"
    >
      <div className={fillVariants({ tone })} style={{ width: `${clamped * 100}%` }} />
    </div>
  )
}
