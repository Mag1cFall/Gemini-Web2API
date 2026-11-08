import { type ComponentProps, useState } from 'react'

import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'

// unitSeconds 是时长输入可选的单位与对应秒数
const unitSeconds = { s: 1, m: 60, h: 3600 } as const

// DurationUnit 是时长输入的单位
type DurationUnit = keyof typeof unitSeconds

// goUnits 是 Go 时长各单位对应的秒数
const goUnits: Record<string, number> = {
  h: 3600,
  m: 60,
  s: 1,
  ms: 1e-3,
  us: 1e-6,
  µs: 1e-6,
  ns: 1e-9,
}

// durationSeconds 把 Go 时长（如 5m、1h30m、500ms）换算为秒，0 为 0，不是时长时为 NaN
function durationSeconds(value: string) {
  if (value === '0') return 0
  const parts = [...value.matchAll(/(\d+(?:\.\d+)?)(ms|us|µs|ns|h|m|s)/g)]
  if (parts.map((part) => part[0]).join('') !== value || parts.length === 0) return Number.NaN
  return parts.reduce(
    (total, [, amount, unit]) => total + Number(amount) * (goUnits[unit ?? ''] ?? 0),
    0,
  )
}

// initialUnit 是开始编辑时的单位：整小时用小时，整分钟用分钟，否则用秒
function initialUnit(seconds: number): DurationUnit {
  if (seconds > 0 && seconds % 3600 === 0) return 'h'
  if (seconds > 0 && seconds % 60 === 0) return 'm'
  return 's'
}

// amountText 是时长在单位下保留 6 位有效数字的数值文本，不是时长时为空
function amountText(value: string, unit: DurationUnit) {
  const seconds = durationSeconds(value)
  return Number.isNaN(seconds) ? '' : String(Number((seconds / unitSeconds[unit]).toPrecision(6)))
}

// DurationInput 是数值加单位的时长输入，读写 Go 时长文本，数值为 0 时写 0，切换单位只换算显示的数值；units 是秒、分钟、小时的名称，unitLabel 是单位下拉框的读屏名称
export function DurationInput({
  value,
  onChange,
  units,
  unitLabel,
  ...props
}: Omit<ComponentProps<'input'>, 'value' | 'onChange'> & {
  value: string
  onChange: (value: string) => void
  units: Record<DurationUnit, string>
  unitLabel: string
}) {
  const [unit, setUnit] = useState(() => initialUnit(durationSeconds(value)))
  const [state, setState] = useState({ value, draft: amountText(value, unit) })
  if (state.value !== value) setState({ value, draft: amountText(value, unit) })
  const write = (draft: string, next: DurationUnit) => {
    const amount = Number(draft)
    const written =
      draft === '' || Number.isNaN(amount) ? draft : amount === 0 ? '0' : String(amount) + next
    setState({ value: written, draft })
    onChange(written)
  }
  return (
    <div className="flex gap-2">
      <Input
        inputMode="decimal"
        value={state.draft}
        onChange={(event) => {
          write(event.target.value.replace(/[^\d.]/g, ''), unit)
        }}
        className="min-w-0 flex-1 tabular-nums"
        {...props}
      />
      <Select
        label={unitLabel}
        value={unit}
        onValueChange={(next) => {
          setUnit(next)
          setState({ value: state.value, draft: amountText(state.value, next) })
        }}
        options={(Object.keys(unitSeconds) as DurationUnit[]).map((item) => ({
          value: item,
          label: units[item],
        }))}
        className="w-[112px] shrink-0"
      />
    </div>
  )
}
