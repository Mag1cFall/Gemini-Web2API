import type { BadgeTone } from '@/components/ui/badge'
import type { AccountState } from '@/features/accounts/types'

// stateTone 是账户状态标签的语气
export function stateTone(state: AccountState): BadgeTone {
  switch (state) {
    case 'ready':
      return 'success'
    case 'cooldown':
      return 'warning'
    case 'auth_required':
    case 'unavailable':
      return 'error'
    case 'initializing':
      return 'info'
    default:
      return 'neutral'
  }
}

// windowTone 按已用比例给出比例条语气
export function windowTone(fraction: number): 'info' | 'warning' | 'error' {
  if (fraction >= 0.9) return 'error'
  if (fraction >= 0.7) return 'warning'
  return 'info'
}
