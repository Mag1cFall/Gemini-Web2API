import { Tabs as Primitive } from 'radix-ui'
import type { ComponentProps } from 'react'
import { twMerge } from 'tailwind-merge'

import { useSlidingIndicator } from '@/components/ui/sliding-indicator'

export const Tabs = Primitive.Root
export const TabsContent = Primitive.Content

// TabsList 是标签页的按钮行，选中项下方的 16×3 品牌蓝指示条平移到选中项
export function TabsList({ className, children, ...props }: ComponentProps<typeof Primitive.List>) {
  const { container, indicator } = useSlidingIndicator<HTMLDivElement, HTMLSpanElement>(
    '[data-state=active]',
    false,
  )
  return (
    <Primitive.List
      ref={container}
      className={twMerge('relative flex max-w-full gap-8 overflow-x-auto', className)}
      {...props}
    >
      {children}
      <span
        ref={indicator}
        aria-hidden
        className="absolute bottom-0 left-0 h-[3px] w-4 rounded-full bg-primary opacity-0 data-ready:transition-transform data-ready:duration-(--duration-base)"
      />
    </Primitive.List>
  )
}

// TabsTrigger 是单个标签，选中项为正文色
export function TabsTrigger({ className, ...props }: ComponentProps<typeof Primitive.Trigger>) {
  return (
    <Primitive.Trigger
      className={twMerge(
        'relative inline-flex h-control shrink-0 items-center gap-1 text-base whitespace-nowrap text-foreground-muted hover:text-foreground data-[state=active]:text-foreground pointer-coarse:h-touch',
        className,
      )}
      {...props}
    />
  )
}
