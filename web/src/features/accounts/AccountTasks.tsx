import { useMutation, useQueryClient } from '@tanstack/react-query'
import { CircleAlert, CircleCheck, LoaderCircle, X } from 'lucide-react'
import { useEffect, useRef } from 'react'

import { Badge, type BadgeTone } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { importMessages, taskMessages } from '@/features/accounts/messages'
import {
  accountsKey,
  chromeProfilesKey,
  tasksKey,
  useAccountTasks,
} from '@/features/accounts/queries'
import type { AccountTask, AccountTaskItem } from '@/features/accounts/types'
import { api } from '@/lib/api'
import { useMessages } from '@/lib/i18n'

// itemTones 是任务中账号各状态的标签颜色
const itemTones: Record<AccountTaskItem['state'], BadgeTone> = {
  pending: 'neutral',
  running: 'info',
  done: 'success',
  failed: 'error',
}

// TaskIcon 是任务状态的图标
function TaskIcon({ state }: { state: AccountTask['state'] }) {
  if (state === 'running') {
    return <LoaderCircle size={16} aria-hidden className="shrink-0 animate-spin text-primary" />
  }
  if (state === 'done') {
    return <CircleCheck size={16} aria-hidden className="shrink-0 text-success" />
  }
  return <CircleAlert size={16} aria-hidden className="shrink-0 text-error" />
}

// AccountTasks 列出在服务端运行的导入账号任务，任务结束时刷新账号与 Chrome 列表，结束的任务保留到用户关闭
export function AccountTasks() {
  const m = useMessages(taskMessages)
  const im = useMessages(importMessages)
  const client = useQueryClient()
  const tasks = useAccountTasks().data
  const running = useRef<string[]>([])
  useEffect(() => {
    if (!tasks) return
    const current = tasks.filter((task) => task.state === 'running').map((task) => task.id)
    if (running.current.some((id) => !current.includes(id))) {
      void client.invalidateQueries({ queryKey: accountsKey })
      void client.invalidateQueries({ queryKey: chromeProfilesKey })
    }
    running.current = current
  }, [tasks, client])
  const dismiss = useMutation({
    mutationFn: (id: string) => api('/api/accounts/tasks/' + id, { method: 'DELETE' }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: tasksKey })
    },
    onError: (error) => {
      toast({ tone: 'error', title: error.message })
    },
  })
  if (!tasks || tasks.length === 0) return null
  return (
    <ul aria-label={m.tasks} className="mb-3 flex w-full flex-col gap-2">
      {tasks.map((task) => {
        const finished = task.items.filter(
          (item) => item.state === 'done' || item.state === 'failed',
        ).length
        return (
          <li key={task.id} className="rounded-md border border-border bg-surface px-4 py-3">
            <div className="flex items-center gap-2">
              <TaskIcon state={task.state} />
              <span className="min-w-0 flex-1 truncate text-base font-medium">
                {task.kind === 'chrome' ? im.chromeTitle : im.cookieTitle}
              </span>
              <span className="text-xs text-foreground-muted tabular-nums">
                {String(finished) + '/' + String(task.items.length)}
              </span>
              {task.state === 'running' ? (
                <Button
                  size="sm"
                  onClick={() => {
                    dismiss.mutate(task.id)
                  }}
                >
                  {m.cancel}
                </Button>
              ) : (
                <Tooltip label={m.close}>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={m.close}
                    onClick={() => {
                      dismiss.mutate(task.id)
                    }}
                  >
                    <X size={16} aria-hidden />
                  </Button>
                </Tooltip>
              )}
            </div>
            <ul className="mt-2 flex flex-col gap-1 border-t border-border pt-2">
              {task.items.map((item) => (
                <li key={item.label} className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="min-w-0 truncate text-sm" title={item.label}>
                    {item.label}
                  </span>
                  <Badge tone={itemTones[item.state]}>{m[item.state]}</Badge>
                  {item.error && (
                    <p className="w-full text-xs break-words text-error-text">{item.error}</p>
                  )}
                </li>
              ))}
            </ul>
          </li>
        )
      })}
    </ul>
  )
}
