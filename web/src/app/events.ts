import { type QueryClient, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useState } from 'react'

import { accountsKey } from '@/features/accounts/queries'
import type { Account } from '@/features/accounts/types'
import type { LogEntry } from '@/features/logs/types'
import { cooldownsKey, modelsKey } from '@/features/models/queries'
import type { Cooldown, Model } from '@/features/models/types'
import type { RequestInfo } from '@/features/requests/types'
import { statusKey } from '@/features/service/queries'
import type { ServiceStatus } from '@/features/service/types'

// logsKey 保存事件流推送的服务日志，最多 2000 条
export const logsKey = ['logs'] as const

// runningKey 保存事件流推送的进行中请求
export const runningKey = ['requests-running'] as const

// recentKey 保存事件流推送的最近结束请求，最新结束的在前
export const recentKey = ['requests-recent'] as const

// retryDelay 是事件流断开后自动重连前的等待毫秒
const retryDelay = 5000

// EventsPhase 是事件流的连接阶段：首次连接、已连通、断开后等待重连、正在重连
export type EventsPhase = 'connecting' | 'open' | 'waiting' | 'retrying'

// EventsConnection 是事件流的连接状态，retryAt 为下次自动重连的时间戳
export interface EventsConnection {
  phase: EventsPhase
  retryAt: number
  reconnect: () => void
}

// maxLogs 是页面保留的日志条数
const maxLogs = 2000

type AdminEvent =
  | { type: 'status'; data: ServiceStatus }
  | { type: 'accounts'; data: Account[] }
  | { type: 'models'; data: Model[] }
  | { type: 'cooldowns'; data: Cooldown[] }
  | { type: 'requests'; data: RequestInfo[] }
  | { type: 'recent'; data: RequestInfo[] }
  | { type: 'logs'; data: LogEntry[] }
  | { type: 'log'; data: LogEntry }

// upsertRunning 按请求状态把一条请求记录并入或移出进行中列表
function upsertRunning(running: RequestInfo[], record: RequestInfo): RequestInfo[] {
  const others = running.filter((item) => item.id !== record.id)
  return record.state === 'running' ? [...others, record] : others
}

// apply 把一条管理事件写入对应的查询缓存
function apply(client: QueryClient, event: AdminEvent) {
  switch (event.type) {
    case 'status':
      client.setQueryData(statusKey, event.data)
      break
    case 'accounts':
      client.setQueryData(accountsKey, { accounts: event.data })
      break
    case 'models':
      client.setQueryData(modelsKey, { models: event.data })
      break
    case 'cooldowns':
      client.setQueryData(cooldownsKey, { cooldowns: event.data })
      break
    case 'requests':
      client.setQueryData(runningKey, event.data)
      break
    case 'recent':
      client.setQueryData(recentKey, event.data)
      void client.invalidateQueries({ queryKey: ['usage'] })
      break
    case 'logs':
      client.setQueryData(logsKey, event.data)
      break
    case 'log': {
      const entry = event.data
      client.setQueryData<LogEntry[]>(logsKey, (logs = []) =>
        logs.length >= maxLogs
          ? [...logs.slice(logs.length - maxLogs + 1), entry]
          : [...logs, entry],
      )
      if (entry.request) {
        const record = entry.request
        client.setQueryData<RequestInfo[]>(runningKey, (running = []) =>
          upsertRunning(running, record),
        )
      }
      break
    }
  }
}

// useAdminEvents 订阅管理事件流，断开后等待 retryDelay 自动重连，重连后重新收到初始快照
export function useAdminEvents(enabled: boolean): EventsConnection {
  const client = useQueryClient()
  const [phase, setPhase] = useState<EventsPhase>('connecting')
  const [retryAt, setRetryAt] = useState(0)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    if (!enabled) return
    const source = new EventSource('/api/events')
    let timer: ReturnType<typeof setTimeout> | undefined
    source.onopen = () => {
      client.setQueryData(logsKey, [])
      client.setQueryData(runningKey, [])
      setPhase('open')
    }
    source.onerror = () => {
      source.close()
      setRetryAt(Date.now() + retryDelay)
      setPhase('waiting')
      timer = setTimeout(() => {
        setPhase('retrying')
        setAttempt((count) => count + 1)
      }, retryDelay)
    }
    source.onmessage = (message: MessageEvent<string>) => {
      apply(client, JSON.parse(message.data) as AdminEvent)
    }
    return () => {
      source.close()
      clearTimeout(timer)
    }
  }, [client, enabled, attempt])
  const reconnect = useCallback(() => {
    setPhase('retrying')
    setAttempt((count) => count + 1)
  }, [])
  return { phase, retryAt, reconnect }
}
