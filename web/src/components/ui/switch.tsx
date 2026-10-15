import { Switch as Primitive } from 'radix-ui'
import type { ComponentProps } from 'react'

// Switch 是 42×22 的开关：开为品牌蓝底与白色圆钮，关为浅灰底、灰边与深灰圆钮
export function Switch(props: ComponentProps<typeof Primitive.Root>) {
  return (
    <Primitive.Root
      className="inline-flex h-[22px] w-[42px] shrink-0 items-center rounded-full border border-switch-border bg-switch-off px-[4px] disabled:cursor-not-allowed disabled:opacity-40 data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:enabled:hover:border-primary-hover data-[state=checked]:enabled:hover:bg-primary-hover"
      {...props}
    >
      <Primitive.Thumb className="block size-[12px] rounded-full bg-switch-knob transition-transform duration-(--duration-base) data-[state=checked]:translate-x-[20px] data-[state=checked]:bg-white" />
    </Primitive.Root>
  )
}
