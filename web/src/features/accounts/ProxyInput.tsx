import { useMutation } from '@tanstack/react-query'
import { AlertCircle, AlertTriangle, CheckCircle2, Radio } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { importMessages } from '@/features/accounts/messages'
import type { ProxyDiagnostic } from '@/features/settings/types'
import { api } from '@/lib/api'
import { useMessages } from '@/lib/i18n'

// ProxyInput 提供代理输入框与连接测试按钮，即时回显连接状态、延迟与风控诊断
export function ProxyInput({
  id,
  value,
  placeholder = 'http://127.0.0.1:7890',
  testLabel,
  disabled = false,
  onChange,
}: {
  id: string
  value: string
  placeholder?: string
  testLabel?: string
  disabled?: boolean
  onChange: (value: string) => void
}) {
  const m = useMessages(importMessages)
  const [diagnostic, setDiagnostic] = useState<ProxyDiagnostic | null>(null)

  const test = useMutation({
    mutationFn: (target: string) =>
      api<ProxyDiagnostic>('/api/proxies/test', {
        method: 'POST',
        json: { proxy: target },
      }),
    onSuccess: (data, target) => {
      // 避免慢请求返回后覆盖用户已修改的新输入
      if (target === value.trim()) {
        setDiagnostic(data)
      }
    },
    onError: (err, target) => {
      if (target === value.trim()) {
        setDiagnostic({
          ok: false,
          status: 'network_error',
          latency_ms: 0,
          message: err.message,
        })
      }
    },
  })

  return (
    <div className="flex w-full flex-col gap-2">
      <div className="flex w-full items-center gap-2">
        <Input
          id={id}
          value={value}
          disabled={disabled}
          placeholder={placeholder}
          className="flex-1"
          onChange={(event) => {
            if (diagnostic) setDiagnostic(null)
            onChange(event.target.value)
          }}
        />
        <Button
          type="button"
          variant="secondary"
          disabled={disabled}
          loading={test.isPending}
          onClick={() => {
            test.mutate(value.trim())
          }}
        >
          <Radio className="size-4 shrink-0" aria-hidden />
          <span>{testLabel ?? m.testProxy}</span>
        </Button>
      </div>

      {diagnostic && (
        <div
          className={
            'flex items-start gap-2 rounded-md p-2.5 text-xs ' +
            (diagnostic.ok
              ? 'bg-success-soft text-success-text'
              : diagnostic.status === 'captcha_blocked'
                ? 'bg-warning-soft text-warning-text'
                : 'bg-error-soft text-error-text')
          }
        >
          {diagnostic.ok ? (
            <CheckCircle2 className="size-4 shrink-0 text-success" aria-hidden />
          ) : diagnostic.status === 'captcha_blocked' ? (
            <AlertTriangle className="size-4 shrink-0 text-warning" aria-hidden />
          ) : (
            <AlertCircle className="size-4 shrink-0 text-error" aria-hidden />
          )}
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="font-medium">{diagnostic.message}</span>
            {diagnostic.status === 'captcha_blocked' && (
              <span className="opacity-90">{m.captchaDiagnosticHint}</span>
            )}
            {diagnostic.error && diagnostic.status !== 'captcha_blocked' && (
              <span className="font-mono text-[11px] opacity-80">{diagnostic.error}</span>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
