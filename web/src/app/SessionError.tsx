import { appMessages } from '@/app/messages'
import { ErrorState } from '@/components/ui/states'
import { useMessages } from '@/lib/i18n'

// SessionError 是读取管理会话失败时的整页说明
export function SessionError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const m = useMessages(appMessages)
  return (
    <div className="flex min-h-dvh items-center justify-center bg-background safe-x">
      <ErrorState title={m.sessionError} error={error} retryLabel={m.reload} onRetry={onRetry} />
    </div>
  )
}
