import type { ReactNode } from 'react'

// Field 把标签、说明与错误绑定到同一个控件，错误显示在控件下方
export function Field({
  id,
  label,
  description,
  error,
  children,
}: {
  id: string
  label: ReactNode
  description?: ReactNode
  error?: string | undefined
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={id} className="text-base font-medium">
        {label}
      </label>
      {children}
      {description && !error && (
        <p id={id + '-description'} className="text-sm text-foreground-muted">
          {description}
        </p>
      )}
      {error && (
        <p id={id + '-error'} role="alert" className="text-sm text-error-text">
          {error}
        </p>
      )}
    </div>
  )
}
