import { useQuery } from '@tanstack/react-query'
import { ArrowUp, Folder } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { ClampText } from '@/components/ui/clamp'
import { Dialog, DialogClose, DialogContent } from '@/components/ui/dialog'
import { settingsMessages } from '@/features/settings/messages'
import { api } from '@/lib/api'
import { fill, useMessages } from '@/lib/i18n'

// Directories 对应 GET /api/directories 的响应，parent 为空表示已到根目录
interface Directories {
  path: string
  parent: string
  directories: string[]
}

// separator 返回路径使用的分隔符
function separator(path: string) {
  return path.includes('\\') ? '\\' : '/'
}

// DirectoryDialog 浏览服务所在机器的目录并选择一个，start 为起始路径，相对路径相对数据目录
export function DirectoryDialog({
  start,
  onChoose,
  onClose,
}: {
  start: string
  onChoose: (path: string) => void
  onClose: () => void
}) {
  const m = useMessages(settingsMessages)
  const [path, setPath] = useState(start)
  const listing = useQuery({
    queryKey: ['directories', path],
    queryFn: () => api<Directories>('/api/directories?path=' + encodeURIComponent(path)),
  })
  const current = listing.data
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        title={m.browseTitle}
        description={current && <code className="font-mono break-all">{current.path}</code>}
        closeLabel={m.cancel}
        footer={
          <>
            <DialogClose asChild>
              <Button variant="secondary">{m.cancel}</Button>
            </DialogClose>
            <Button
              variant="primary"
              disabled={!current}
              onClick={() => {
                if (current) onChoose(current.path)
              }}
            >
              {m.browseChoose}
            </Button>
          </>
        }
      >
        {listing.isError && (
          <Callout tone="error" role="alert">
            <ClampText className="wrap-anywhere">
              {fill(m.browseFailed, { message: listing.error.message })}
            </ClampText>
          </Callout>
        )}
        <ul className="flex flex-col">
          {current?.parent && (
            <li>
              <button
                type="button"
                className="flex w-full items-center gap-2 rounded-sm px-2 py-2 text-left text-sm hover:bg-hover"
                onClick={() => {
                  setPath(current.parent)
                }}
              >
                <ArrowUp size={16} aria-hidden className="text-foreground-muted" />
                {m.browseUp}
              </button>
            </li>
          )}
          {current?.directories.map((name) => (
            <li key={name}>
              <button
                type="button"
                className="flex w-full items-center gap-2 rounded-sm px-2 py-2 text-left text-sm hover:bg-hover"
                onClick={() => {
                  setPath(current.path.replace(/[\\/]$/, '') + separator(current.path) + name)
                }}
              >
                <Folder size={16} aria-hidden className="text-foreground-muted" />
                <span className="min-w-0 truncate">{name}</span>
              </button>
            </li>
          ))}
          {current?.directories.length === 0 && (
            <li className="px-2 py-2 text-sm text-foreground-muted">{m.browseEmpty}</li>
          )}
        </ul>
      </DialogContent>
    </Dialog>
  )
}
