import { useEffect, useRef, useState } from 'react'

// ChartSeries 是时间图的一条序列：柱与面积各自堆叠，折线单独绘制，null 表示该桶没有取值，right 为真时用右侧坐标轴
export interface ChartSeries {
  name: string
  color: string
  kind: 'bar' | 'area' | 'line'
  values: (number | null)[]
  right?: boolean
}

// ChartReference 是一条带标签的水平参考线
export interface ChartReference {
  value: number
  label: string
  color: string
}

// plotTop 与 plotBottom 是绘图区上方留白与下方横轴标签的高度
const plotTop = 8
const plotBottom = 24

// textWidth 估算 12px 坐标轴文字的宽度，中日韩字符按全角计
function textWidth(text: string) {
  let width = 0
  for (const char of text) width += char.charCodeAt(0) > 0x2e80 ? 12 : 7
  return width
}

// niceStep 返回不小于 raw 的 1、2、5 乘以 10 的幂
function niceStep(raw: number) {
  const power = 10 ** Math.floor(Math.log10(raw))
  const unit = raw / power
  return (unit <= 1 ? 1 : unit <= 2 ? 2 : unit <= 5 ? 5 : 10) * power
}

// axisScale 返回坐标轴上限与刻度间隔，steps 给出候选间隔时从中选取
function axisScale(peak: number, integer: boolean, steps?: number[]) {
  if (peak <= 0) return { max: 1, step: 1 }
  let step = steps?.find((item) => peak / item <= 4) ?? niceStep(peak / 4)
  if (integer) step = Math.max(1, step)
  return { max: Math.ceil(peak / step) * step, step }
}

// stackPeak 返回一组堆叠序列逐桶合计的最大值
function stackPeak(items: ChartSeries[], count: number) {
  let peak = 0
  for (let index = 0; index < count; index++) {
    peak = Math.max(
      peak,
      items.reduce((sum, item) => sum + (item.values[index] ?? 0), 0),
    )
  }
  return peak
}

// useWidth 返回元素的实时宽度
function useWidth() {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  useEffect(() => {
    const node = ref.current
    if (!node) return
    const observer = new ResizeObserver(([entry]) => {
      if (entry) setWidth(entry.contentRect.width)
    })
    observer.observe(node)
    return () => {
      observer.disconnect()
    }
  }, [])
  return [ref, width] as const
}

// useLegend 保存一张图的图例开关与悬停突出的序列
export function useLegend() {
  const [hidden, setHidden] = useState<string[]>([])
  const [focus, setFocus] = useState<string | null>(null)
  return {
    hidden,
    focus,
    setFocus,
    toggle: (name: string) => {
      setHidden((current) =>
        current.includes(name) ? current.filter((item) => item !== name) : [...current, name],
      )
    },
  }
}

// Legend 是 useLegend 返回的图例状态
export type Legend = ReturnType<typeof useLegend>

// ChartLegend 是可开关序列的图例，悬停时突出对应序列
export function ChartLegend({
  items,
  legend,
}: {
  items: { name: string; color: string }[]
  legend: Legend
}) {
  return (
    <ul className="flex flex-wrap gap-x-3 gap-y-1 text-xs">
      {items.map((item) => {
        const off = legend.hidden.includes(item.name)
        return (
          <li key={item.name} className="max-w-full min-w-0">
            <button
              type="button"
              aria-pressed={!off}
              onClick={() => {
                legend.toggle(item.name)
              }}
              onPointerEnter={() => {
                legend.setFocus(item.name)
              }}
              onPointerLeave={() => {
                legend.setFocus(null)
              }}
              className={
                'flex max-w-full items-center gap-1 rounded-sm py-px ' +
                (off ? 'text-foreground-subtle' : 'text-foreground-muted hover:text-foreground')
              }
            >
              <span
                aria-hidden
                className="size-[10px] shrink-0 rounded-sm"
                style={{ backgroundColor: off ? 'var(--color-border-strong)' : item.color }}
              />
              <span className="truncate">{item.name}</span>
            </button>
          </li>
        )
      })}
    </ul>
  )
}

// TimeChart 是按时间分桶的柱、面积与折线组合图，悬停显示该桶各序列的取值
export function TimeChart({
  label,
  labels,
  series,
  height,
  format,
  legend,
  rightFormat,
  rightMax = 100,
  integer = false,
  steps,
  reference,
  total,
  sorted = false,
}: {
  label: string
  labels: string[]
  series: ChartSeries[]
  height: number
  format: (value: number) => string
  legend?: Legend
  rightFormat?: (value: number) => string
  rightMax?: number
  integer?: boolean
  steps?: number[]
  reference?: ChartReference | undefined
  total?: string
  sorted?: boolean
}) {
  const [ref, width] = useWidth()
  const [hover, setHover] = useState<{ index: number; x: number } | null>(null)
  const hidden = legend?.hidden ?? []
  const focus = legend?.focus ?? null
  const count = labels.length
  const visible = series.filter((item) => !hidden.includes(item.name))
  const bars = visible.filter((item) => item.kind === 'bar')
  const areas = visible.filter((item) => item.kind === 'area')
  const lines = visible.filter((item) => item.kind === 'line')
  const leftLines = lines.filter((item) => item.right !== true)
  const peak = Math.max(
    stackPeak(bars, count),
    stackPeak(areas, count),
    ...leftLines.flatMap((item) => item.values.map((value) => value ?? 0)),
    reference?.value ?? 0,
  )
  const { max, step } = axisScale(peak, integer, steps)
  const ticks: number[] = []
  for (let value = 0; value <= max + step / 2; value += step) ticks.push(value)
  const hasRight = rightFormat !== undefined && series.some((item) => item.right === true)
  const leftWidth = Math.max(...ticks.map((value) => textWidth(format(value)))) + 8
  const rightWidth =
    (hasRight ? textWidth(rightFormat(rightMax)) + 8 : 8) +
    (reference ? textWidth(reference.label) + 8 : 0)
  const plot = Math.max(0, width - leftWidth - rightWidth)
  const band = count > 0 ? plot / count : 0
  const barWidth = Math.max(1, Math.min(22, band * 0.7))
  const inner = height - plotTop - plotBottom
  const y = (value: number) => plotTop + (1 - value / max) * inner
  const yRight = (value: number) => plotTop + (1 - value / rightMax) * inner
  const cx = (index: number) => leftWidth + band * (index + 0.5)
  const stride = Math.max(
    1,
    Math.ceil((Math.max(0, ...labels.map(textWidth)) + 12) / Math.max(band, 1)),
  )
  const opacity = (name: string) => (focus !== null && focus !== name ? 0.25 : 1)

  const areaPaths = areas.reduce<
    { item: ChartSeries; upper: number[]; fill: string; line: string }[]
  >((list, item) => {
    const base = list.at(-1)?.upper ?? Array.from({ length: count }, () => 0)
    const upper = base.map((value, index) => value + (item.values[index] ?? 0))
    const top = upper.map((value, index) => `${cx(index).toFixed(1)},${y(value).toFixed(1)}`)
    const down = base
      .map((value, index) => `${cx(index).toFixed(1)},${y(value).toFixed(1)}`)
      .reverse()
    return [
      ...list,
      { item, upper, fill: `M${top.join('L')}L${down.join('L')}Z`, line: `M${top.join('L')}` },
    ]
  }, [])

  // linePath 生成折线路径，null 处断开
  const linePath = (item: ChartSeries) => {
    const map = item.right === true ? yRight : y
    let path = ''
    let open = false
    item.values.forEach((value, index) => {
      if (value === null) {
        open = false
        return
      }
      path += `${open ? 'L' : 'M'}${cx(index).toFixed(1)},${map(value).toFixed(1)}`
      open = true
    })
    return path
  }

  const rows =
    hover === null
      ? []
      : visible
          .map((item) => ({ item, value: item.values[hover.index] ?? null }))
          .filter((row): row is { item: ChartSeries; value: number } => row.value !== null)
  if (sorted) rows.sort((a, b) => b.value - a.value)
  const stacked =
    hover === null
      ? 0
      : [...bars, ...areas].reduce((sum, item) => sum + (item.values[hover.index] ?? 0), 0)

  return (
    <div ref={ref} className="relative w-full" style={{ height }}>
      {width > 0 && (
        <svg
          role="img"
          aria-label={label}
          width={width}
          height={height}
          className="block text-xs select-none"
          onPointerMove={(event) => {
            const x = event.clientX - event.currentTarget.getBoundingClientRect().left
            const index = Math.floor((x - leftWidth) / band)
            setHover(band > 0 && index >= 0 && index < count ? { index, x } : null)
          }}
          onPointerLeave={() => {
            setHover(null)
          }}
        >
          {ticks.map((value) => (
            <g key={value}>
              <line
                x1={leftWidth}
                x2={leftWidth + plot}
                y1={y(value)}
                y2={y(value)}
                className="stroke-border"
              />
              <text
                x={leftWidth - 6}
                y={y(value)}
                dy="0.32em"
                textAnchor="end"
                className="fill-foreground-subtle"
              >
                {format(value)}
              </text>
            </g>
          ))}
          {hasRight &&
            [0, rightMax / 2, rightMax].map((value) => (
              <text
                key={value}
                x={leftWidth + plot + 6}
                y={yRight(value)}
                dy="0.32em"
                className="fill-foreground-subtle"
              >
                {rightFormat(value)}
              </text>
            ))}
          {hover !== null && (
            <rect
              x={leftWidth + band * hover.index}
              y={plotTop}
              width={band}
              height={inner}
              className="fill-hover"
            />
          )}
          {labels.map((_, index) => {
            let sum = 0
            return bars.map((item) => {
              const value = item.values[index] ?? 0
              if (value <= 0) return null
              const from = y(sum)
              sum += value
              const to = y(sum)
              return (
                <rect
                  key={item.name + String(index)}
                  x={cx(index) - barWidth / 2}
                  y={to}
                  width={barWidth}
                  height={Math.max(0, from - to)}
                  fill={item.color}
                  opacity={opacity(item.name)}
                />
              )
            })
          })}
          {areaPaths.map(({ item, fill, line }) => (
            <g key={item.name} opacity={opacity(item.name)}>
              <path d={fill} fill={item.color} fillOpacity={0.3} />
              <path d={line} fill="none" stroke={item.color} strokeWidth={1} />
            </g>
          ))}
          {lines.map((item) => (
            <g key={item.name} opacity={opacity(item.name)}>
              <path d={linePath(item)} fill="none" stroke={item.color} strokeWidth={1.5} />
              {item.values.map((value, index) =>
                value !== null &&
                (item.values[index - 1] ?? null) === null &&
                (item.values[index + 1] ?? null) === null ? (
                  <circle
                    key={index}
                    cx={cx(index)}
                    cy={(item.right === true ? yRight : y)(value)}
                    r={2}
                    fill={item.color}
                  />
                ) : null,
              )}
            </g>
          ))}
          {reference && (
            <g>
              <line
                x1={leftWidth}
                x2={leftWidth + plot}
                y1={y(reference.value)}
                y2={y(reference.value)}
                stroke={reference.color}
                strokeDasharray="4 3"
                opacity={0.7}
              />
              <text
                x={leftWidth + plot + 6}
                y={y(reference.value)}
                dy="0.32em"
                className="fill-foreground-muted"
              >
                {reference.label}
              </text>
            </g>
          )}
          {labels.map((item, index) =>
            index % stride === 0 ? (
              <text
                key={index}
                x={cx(index)}
                y={height - 6}
                textAnchor="middle"
                className="fill-foreground-subtle"
              >
                {item}
              </text>
            ) : null,
          )}
          <line
            x1={leftWidth}
            x2={leftWidth + plot}
            y1={plotTop + inner}
            y2={plotTop + inner}
            className="stroke-border-strong"
          />
        </svg>
      )}
      {hover !== null && (
        <div
          role="presentation"
          className="pointer-events-none absolute top-2 z-popover flex min-w-[180px] flex-col gap-1 rounded-sm bg-surface px-3 py-2 text-xs shadow-popover"
          style={{
            left: hover.x,
            transform: hover.x > width / 2 ? 'translateX(calc(-100% - 12px))' : 'translateX(12px)',
          }}
        >
          <span className="text-foreground-muted">{labels[hover.index]}</span>
          {rows.map(({ item, value }) => (
            <span key={item.name} className="flex items-center justify-between gap-3">
              <span className="flex min-w-0 items-center gap-2">
                <span
                  aria-hidden
                  className="size-2 shrink-0 rounded-sm"
                  style={{ backgroundColor: item.color }}
                />
                <span className="truncate">{item.name}</span>
              </span>
              <span className="tabular-nums">
                {item.right === true && rightFormat ? rightFormat(value) : format(value)}
              </span>
            </span>
          ))}
          {total !== undefined && rows.length > 0 && (
            <span className="flex justify-between gap-3 border-t border-border pt-1">
              <span>{total}</span>
              <span className="tabular-nums">{format(stacked)}</span>
            </span>
          )}
        </div>
      )}
    </div>
  )
}
