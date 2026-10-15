import { Search } from 'lucide-react'
import { type ComponentProps, useRef } from 'react'
import { twMerge } from 'tailwind-merge'

import { Input } from '@/components/ui/input'
import { useFocusShortcut } from '@/lib/hotkeys'

// SearchInput 是带放大镜的搜索框，按 / 聚焦
export function SearchInput({ className, ...props }: ComponentProps<'input'>) {
  const ref = useRef<HTMLInputElement>(null)
  useFocusShortcut(ref)
  return (
    <div className={twMerge('relative min-w-0', className)}>
      <Search
        size={16}
        aria-hidden
        className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-foreground-muted"
      />
      <Input ref={ref} type="search" className="pr-8 pl-8" {...props} />
      <kbd
        aria-hidden
        className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 rounded-sm border border-border px-1 font-mono text-xs text-foreground-subtle pointer-coarse:hidden"
      >
        /
      </kbd>
    </div>
  )
}
