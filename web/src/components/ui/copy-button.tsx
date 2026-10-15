import { Check, Copy } from 'lucide-react'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'

// clipboardAvailable 表示页面处于可使用剪贴板 API 的安全上下文
export const clipboardAvailable = window.isSecureContext

// CopyButton 把文本写入剪贴板，成功后图标短暂变为对勾并调用 onCopied，非安全上下文中不显示
export function CopyButton({
  value,
  label,
  copiedLabel,
  onCopied,
}: {
  value: string
  label: string
  copiedLabel: string
  onCopied?: () => void
}) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => {
      setCopied(false)
    }, 1500)
    return () => {
      clearTimeout(timer)
    }
  }, [copied])
  if (!clipboardAvailable) return null
  return (
    <Tooltip label={copied ? copiedLabel : label}>
      <Button
        variant="ghost"
        size="icon"
        aria-label={label}
        className="size-control-sm"
        onClick={() => {
          void navigator.clipboard.writeText(value).then(() => {
            setCopied(true)
            onCopied?.()
          })
        }}
      >
        {copied ? (
          <Check size={16} aria-hidden className="text-success-text" />
        ) : (
          <Copy size={16} aria-hidden />
        )}
      </Button>
    </Tooltip>
  )
}
