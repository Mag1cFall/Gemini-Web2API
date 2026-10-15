import type { ReactNode } from 'react'

// FormRow 是设置列表的一行：左侧名称与说明，右侧控件，行间以细线分隔
export function FormRow({
  id,
  label,
  hint,
  tag,
  error,
  children,
}: {
  id: string
  label: ReactNode
  hint?: ReactNode
  tag?: ReactNode
  error?: string | undefined
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-3 border-t border-border py-4 first:border-t-0 md:flex-row md:items-start md:justify-between md:gap-8">
      <div className="flex min-w-0 flex-col gap-1 md:max-w-prose">
        <div className="flex flex-wrap items-center gap-2">
          <label htmlFor={id} className="text-base font-medium">
            {label}
          </label>
          {tag}
        </div>
        {hint && (
          <p id={id + '-hint'} className="text-sm text-foreground-muted">
            {hint}
          </p>
        )}
      </div>
      <div className="flex w-full shrink-0 flex-col gap-2 md:w-field">
        {children}
        {error && (
          <p id={id + '-error'} role="alert" className="text-sm text-error-text">
            {error}
          </p>
        )}
      </div>
    </div>
  )
}
