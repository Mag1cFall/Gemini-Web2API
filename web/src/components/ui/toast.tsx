import { CircleAlert, CircleCheck, X } from 'lucide-react'
import { Toast as Primitive } from 'radix-ui'
import { useSyncExternalStore } from 'react'

// ToastItem 是一条轻提示，action 用于撤销等后续操作
export interface ToastItem {
  id: number
  title: string
  tone: 'success' | 'error'
  action?: { label: string; onClick: () => void }
}

let items: ToastItem[] = []
let nextId = 1
const listeners = new Set<() => void>()

// emit 通知订阅者提示列表已变化
function emit() {
  for (const listener of listeners) listener()
}

// successDuration 是成功提示自动消失前的显示时长
const successDuration = 2500

// toast 显示一条提示：成功提示 2.5 秒后自动消失，失败提示保留到用户关闭
export function toast(item: Omit<ToastItem, 'id'>) {
  const id = nextId++
  items = [...items, { ...item, id }]
  emit()
  if (item.tone === 'success') {
    setTimeout(() => {
      dismiss(id)
    }, successDuration)
  }
}

// dismiss 移除指定提示
function dismiss(id: number) {
  items = items.filter((item) => item.id !== id)
  emit()
}

// subscribe 订阅提示列表变化
function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

// Toaster 在页面顶部居中渲染提示
export function Toaster({ closeLabel }: { closeLabel: string }) {
  const current = useSyncExternalStore(subscribe, () => items)
  return (
    <Primitive.Provider duration={Infinity} swipeDirection="up">
      {current.map((item) => (
        <Primitive.Root
          key={item.id}
          onOpenChange={(open) => {
            if (!open) dismiss(item.id)
          }}
          className="flex w-full items-center gap-3 rounded-sm bg-surface px-4 py-3 shadow-popover data-[state=closed]:animate-fade-out data-[state=open]:animate-pop-in"
        >
          {item.tone === 'success' ? (
            <CircleCheck aria-hidden size={20} className="shrink-0 text-primary" />
          ) : (
            <CircleAlert aria-hidden size={20} className="shrink-0 text-error-text" />
          )}
          <Primitive.Title className="min-w-0 flex-1 text-base break-words">
            {item.title}
          </Primitive.Title>
          {item.action && (
            <Primitive.Action
              altText={item.action.label}
              onClick={item.action.onClick}
              className="shrink-0 rounded-sm px-2 text-sm font-semibold text-primary hover:text-primary-hover"
            >
              {item.action.label}
            </Primitive.Action>
          )}
          <Primitive.Close
            aria-label={closeLabel}
            className="flex size-6 shrink-0 items-center justify-center rounded-sm text-foreground-muted hover:bg-hover"
          >
            <X size={16} aria-hidden />
          </Primitive.Close>
        </Primitive.Root>
      ))}
      <Primitive.Viewport className="fixed inset-x-4 top-16 z-toast mx-auto flex max-w-dialog flex-col gap-2 outline-none" />
    </Primitive.Provider>
  )
}
