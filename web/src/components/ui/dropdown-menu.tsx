import { DropdownMenu as Primitive } from 'radix-ui'
import type { ComponentProps, ReactNode } from 'react'
import { twMerge } from 'tailwind-merge'

import { ignoreSecondaryRelease } from '@/lib/pointer'

export const DropdownMenu = Primitive.Root
export const DropdownMenuTrigger = Primitive.Trigger

// DropdownMenuContent 是白底浮层菜单
export function DropdownMenuContent({ children }: { children: ReactNode }) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        align="end"
        sideOffset={4}
        collisionPadding={8}
        className="z-popover min-w-menu overflow-hidden rounded-sm bg-surface py-1 shadow-popover data-[state=closed]:animate-fade-out data-[state=open]:animate-fade-in"
      >
        {children}
      </Primitive.Content>
    </Primitive.Portal>
  )
}

// DropdownMenuItem 是菜单项，destructive 用于破坏性操作
export function DropdownMenuItem({
  className,
  destructive = false,
  ...props
}: ComponentProps<typeof Primitive.Item> & { destructive?: boolean }) {
  return (
    <Primitive.Item
      onPointerUp={ignoreSecondaryRelease}
      className={twMerge(
        'flex h-control-sm cursor-pointer items-center gap-2 px-4 text-sm outline-none select-none data-disabled:cursor-not-allowed data-disabled:opacity-40 data-highlighted:bg-hover pointer-coarse:h-touch',
        destructive ? 'text-error-text' : 'text-foreground',
        className,
      )}
      {...props}
    />
  )
}

export const DropdownMenuRadioGroup = Primitive.RadioGroup

// DropdownMenuRadioItem 是菜单中的单选项，选中项为蓝字
export function DropdownMenuRadioItem(props: ComponentProps<typeof Primitive.RadioItem>) {
  return (
    <Primitive.RadioItem
      onPointerUp={ignoreSecondaryRelease}
      className="flex h-control-sm cursor-pointer items-center px-4 text-sm text-foreground outline-none select-none data-highlighted:bg-hover data-[state=checked]:text-primary pointer-coarse:h-touch"
      {...props}
    />
  )
}
