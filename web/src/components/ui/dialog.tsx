import { cva } from 'class-variance-authority'
import { X } from 'lucide-react'
import { Dialog as Primitive } from 'radix-ui'
import { type ReactNode, useRef } from 'react'

export const Dialog = Primitive.Root
export const DialogTrigger = Primitive.Trigger
export const DialogClose = Primitive.Close

// contentVariants 区分居中对话框与右侧详情面板
const contentVariants = cva(
  'fixed z-modal flex flex-col bg-surface text-foreground shadow-modal focus:outline-none',
  {
    variants: {
      layout: {
        center:
          'inset-x-4 top-1/2 mx-auto max-h-dialog -translate-y-1/2 rounded-md data-[state=closed]:animate-pop-out data-[state=open]:animate-pop-in',
        sheet:
          'inset-y-0 right-0 w-full max-w-sheet data-[state=closed]:animate-sheet-out data-[state=open]:animate-sheet-in',
      },
      width: { normal: 'max-w-dialog', wide: 'max-w-dialog-wide' },
    },
  },
)

// DialogContent 是带遮罩、标题栏与关闭按钮的弹层，焦点锁定在弹层内；详情面板打开时焦点落在面板本身
export function DialogContent({
  title,
  description,
  layout = 'center',
  wide = false,
  closeLabel,
  footer,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  layout?: 'center' | 'sheet'
  wide?: boolean
  closeLabel: string
  footer?: ReactNode
  children: ReactNode
}) {
  const content = useRef<HTMLDivElement>(null)
  return (
    <Primitive.Portal>
      <Primitive.Overlay className="fixed inset-0 z-overlay bg-overlay data-[state=closed]:animate-fade-out data-[state=open]:animate-fade-in" />
      <Primitive.Content
        ref={content}
        {...(layout === 'sheet'
          ? {
              onOpenAutoFocus: (event: Event) => {
                event.preventDefault()
                content.current?.focus()
              },
            }
          : {})}
        className={contentVariants({
          layout,
          width: layout === 'center' ? (wide ? 'wide' : 'normal') : undefined,
        })}
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
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">{children}</div>
        {footer && (
          <footer className="flex shrink-0 flex-wrap justify-end gap-3 px-6 pt-2 pb-6">
            {footer}
          </footer>
        )}
      </Primitive.Content>
    </Primitive.Portal>
  )
}
