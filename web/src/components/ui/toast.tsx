import { CircleAlert, CircleCheck, X } from 'lucide-react'
import { Toast as Primitive } from 'radix-ui'
import { useLayoutEffect, useRef, useState, useSyncExternalStore } from 'react'

import { ClampList, ClampText } from '@/components/ui/clamp'
import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { Tooltip } from '@/components/ui/tooltip'

// ToastFailure 是详情中的一个失败项与原因
export interface ToastFailure {
  label: string
  message: string
}

// ToastItem 是一条轻提示：title 只写结论与数量，detail 是“详情”对话框中的原因或失败项，action 用于撤销等后续操作，onClose 在提示不经 action 关闭时调用
export interface ToastItem {
  id: number
  title: string
  tone: 'success' | 'error'
  detail?: string | ToastFailure[]
  action?: { label: string; onClick: () => void }
  onClose?: () => void
}

let items: ToastItem[] = []
let nextId = 1
const listeners = new Set<() => void>()

// emit 通知订阅者提示列表已变化
function emit() {
  for (const listener of listeners) listener()
}

// toastDuration 是提示自动消失前的显示时长，单位毫秒，指针停在提示上与窗口失去焦点时暂停计时
const toastDuration = 5000

// toastLimit 是同时显示的提示条数，超出时移除最早的一条
const toastLimit = 3

// toast 显示一条提示，5 秒后自动消失，最多同时显示 3 条
export function toast(item: Omit<ToastItem, 'id'>) {
  const next = [...items, { ...item, id: nextId++ }]
  const dropped = next.slice(0, -toastLimit)
  items = next.slice(-toastLimit)
  emit()
  for (const entry of dropped) entry.onClose?.()
}

// dismiss 移除指定提示，acted 为假时调用其 onClose
function dismiss(id: number, acted: boolean) {
  const item = items.find((entry) => entry.id === id)
  if (!item) return
  items = items.filter((entry) => entry !== item)
  emit()
  if (!acted) item.onClose?.()
}

// subscribe 订阅提示列表变化
function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

// detailText 是详情的纯文本，用于复制
function detailText(detail: string | ToastFailure[]) {
  return typeof detail === 'string'
    ? detail
    : detail.map((failure) => failure.label + ': ' + failure.message).join('\n')
}

// ToastTitle 是最多两行的提示标题，被截断时悬停显示全文
function ToastTitle({ title }: { title: string }) {
  const node = useRef<HTMLDivElement>(null)
  const [clamped, setClamped] = useState(false)
  useLayoutEffect(() => {
    const element = node.current
    if (element) setClamped(element.scrollHeight > element.clientHeight + 1)
  }, [title])
  const element = (
    <Primitive.Title ref={node} className="line-clamp-2 min-w-0 flex-1 text-sm break-words">
      {title}
    </Primitive.Title>
  )
  return clamped ? <Tooltip label={title}>{element}</Tooltip> : element
}

// ToastLabels 是提示与详情对话框的按钮文案
export interface ToastLabels {
  dismiss: string
  details: string
  close: string
  copy: string
  copied: string
}

// Toaster 在页面顶部居中渲染 360px 宽的提示，带详情的提示可打开“详情”对话框查看并复制原因与失败项
export function Toaster({ labels }: { labels: ToastLabels }) {
  const current = useSyncExternalStore(subscribe, () => items)
  const [shown, setShown] = useState<ToastItem>()
  const [open, setOpen] = useState(false)
  return (
    <Primitive.Provider duration={toastDuration} swipeDirection="up">
      {current.map((item) => (
        <Primitive.Root
          key={item.id}
          onOpenChange={(next) => {
            if (!next) dismiss(item.id, false)
          }}
          className="flex w-full items-center gap-3 rounded-sm bg-surface px-4 py-3 shadow-popover data-[state=closed]:animate-fade-out data-[state=open]:animate-pop-in"
        >
          {item.tone === 'success' ? (
            <CircleCheck aria-hidden size={20} className="shrink-0 text-primary" />
          ) : (
            <CircleAlert aria-hidden size={20} className="shrink-0 text-error-text" />
          )}
          <ToastTitle title={item.title} />
          {item.detail !== undefined && (
            <Primitive.Action
              altText={labels.details}
              onClick={() => {
                setShown(item)
                setOpen(true)
              }}
              className="shrink-0 rounded-sm px-2 text-sm font-semibold text-primary hover:text-primary-hover"
            >
              {labels.details}
            </Primitive.Action>
          )}
          {item.action && (
            <Primitive.Action
              altText={item.action.label}
              onClick={() => {
                dismiss(item.id, true)
                item.action?.onClick()
              }}
              className="shrink-0 rounded-sm px-2 text-sm font-semibold text-primary hover:text-primary-hover"
            >
              {item.action.label}
            </Primitive.Action>
          )}
          <Primitive.Close
            aria-label={labels.dismiss}
            className="flex size-6 shrink-0 items-center justify-center rounded-sm text-foreground-muted hover:bg-hover"
          >
            <X size={16} aria-hidden />
          </Primitive.Close>
        </Primitive.Root>
      ))}
      <Primitive.Viewport className="fixed inset-x-4 top-16 z-toast mx-auto flex max-w-field flex-col gap-2 outline-none" />
      <Dialog open={open} onOpenChange={setOpen}>
        {shown?.detail !== undefined && (
          <DialogContent title={shown.title} closeLabel={labels.close}>
            <div className="relative">
              <div className="absolute -top-1 right-0">
                <CopyButton
                  value={detailText(shown.detail)}
                  label={labels.copy}
                  copiedLabel={labels.copied}
                />
              </div>
              {typeof shown.detail === 'string' ? (
                <div className="pr-10">
                  <ClampText className="text-sm break-words whitespace-pre-wrap select-text">
                    {shown.detail}
                  </ClampText>
                </div>
              ) : (
                <ClampList className="flex flex-col gap-3 pr-10">
                  {shown.detail.map((failure, index) => (
                    <li key={index} className="flex min-w-0 flex-col gap-1">
                      <span className="text-sm font-medium wrap-anywhere">{failure.label}</span>
                      <ClampText className="text-sm break-words whitespace-pre-wrap text-foreground-muted select-text">
                        {failure.message}
                      </ClampText>
                    </li>
                  ))}
                </ClampList>
              )}
            </div>
          </DialogContent>
        )}
      </Dialog>
    </Primitive.Provider>
  )
}
