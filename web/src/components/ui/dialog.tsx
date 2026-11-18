import { cva } from 'class-variance-authority'
import { X } from 'lucide-react'
import { Dialog as Primitive } from 'radix-ui'
import { type ReactNode, useRef } from 'react'

export const Dialog = Primitive.Root
export const DialogTrigger = Primitive.Trigger
export const DialogClose = Primitive.Close

// contentVariants 是视口居中的对话框，区分普通与加宽两种宽度
const contentVariants = cva(
  'fixed inset-x-4 top-1/2 z-modal mx-auto flex max-h-dialog -translate-y-1/2 flex-col rounded-md bg-surface text-foreground shadow-modal focus:outline-none data-[state=closed]:animate-pop-out data-[state=open]:animate-pop-in',
  { variants: { width: { normal: 'max-w-dialog', wide: 'max-w-dialog-wide' } } },
)

// DialogContent 是视口居中、带遮罩、标题栏与关闭按钮的对话框，焦点锁定在对话框内，内容超出时在对话框内滚动，没有 children 时只有标题、说明与按钮；focusContent 为真时打开后焦点落在对话框本身
export function DialogContent({
  title,
  description,
  wide = false,
  focusContent = false,
  closeLabel,
  footer,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  wide?: boolean
  focusContent?: boolean
  closeLabel: string
  footer?: ReactNode
  children?: ReactNode
}) {
  const content = useRef<HTMLDivElement>(null)
  return (
    <Primitive.Portal>
      <Primitive.Overlay className="fixed inset-0 z-overlay bg-overlay data-[state=closed]:animate-fade-out data-[state=open]:animate-fade-in" />
      <Primitive.Content
        ref={content}
        {...(focusContent
          ? {
              onOpenAutoFocus: (event: Event) => {
                event.preventDefault()
                content.current?.focus()
              },
            }
          : {})}
        className={contentVariants({ width: wide ? 'wide' : 'normal' })}
      >
        <header className="flex min-h-12 shrink-0 items-start gap-4 px-6 pt-4">
          <div className="min-w-0 flex-1 py-1">
            <Primitive.Title className="text-md font-semibold break-words">{title}</Primitive.Title>
            {description ? (
              <Primitive.Description className="mt-1 text-sm text-foreground-muted">
                {description}
              </Primitive.Description>
            ) : (
              <Primitive.Description className="sr-only">{title}</Primitive.Description>
            )}
          </div>
          <Primitive.Close
            aria-label={closeLabel}
            className="-mr-2 flex size-control shrink-0 items-center justify-center rounded-md text-foreground-muted hover:bg-hover active:bg-nav-active pointer-coarse:size-touch"
          >
            <X size={20} aria-hidden />
          </Primitive.Close>
        </header>
        {children && <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">{children}</div>}
        {footer && (
          <footer
            className={`flex shrink-0 flex-wrap justify-end gap-3 px-6 pb-6 ${children ? 'pt-2' : 'pt-6'}`}
          >
            {footer}
          </footer>
        )}
      </Primitive.Content>
    </Primitive.Portal>
  )
}
