import type { ComponentProps } from 'react'
import { twMerge } from 'tailwind-merge'

// inputClass 是单行与多行输入框共用的外观
const inputClass =
  'w-full min-w-0 rounded-sm border border-input-edge border-b-input-underline bg-surface px-3 text-sm text-foreground placeholder:text-placeholder hover:border-primary-border focus:border-b-primary focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-40 aria-invalid:border-destructive-border'

// Input 是单行输入框，保留浏览器的自动填充与原生选择行为
export function Input({ className, ...props }: ComponentProps<'input'>) {
  return (
    <input
      className={twMerge(inputClass, 'h-control-sm pointer-coarse:h-touch', className)}
      {...props}
    />
  )
}

// Textarea 是多行输入框
export function Textarea({ className, ...props }: ComponentProps<'textarea'>) {
  return <textarea className={twMerge(inputClass, 'py-2', className)} {...props} />
}
