import { Badge, type BadgeTone } from '@/components/ui/badge'
import { requestStateMessages } from '@/features/requests/messages'
import type { RequestState } from '@/features/requests/types'
import { useMessages } from '@/lib/i18n'

// stateTone 是请求状态标签的语气，工具调用计为成功
export const stateTone: Record<RequestState, BadgeTone> = {
  running: 'info',
  completed: 'success',
  tool_calls: 'success',
  failed: 'error',
  cancelled: 'neutral',
}

// RequestStateBadge 是请求状态标签
export function RequestStateBadge({ state }: { state: RequestState }) {
  const m = useMessages(requestStateMessages)
  return <Badge tone={stateTone[state]}>{m[state]}</Badge>
}
