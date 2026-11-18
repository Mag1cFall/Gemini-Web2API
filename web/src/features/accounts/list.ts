import type { Account, AccountState } from '@/features/accounts/types'

// pageSize 是账号列表每页的行数
export const pageSize = 20

// SortKey 是账号列表可排序的列
export type SortKey = 'account' | 'state' | 'tier' | 'models' | 'window5h' | 'lastUsed'

// sortKeys 是全部可排序的列
export const sortKeys: SortKey[] = ['account', 'state', 'tier', 'models', 'window5h', 'lastUsed']

// accountStates 是状态筛选的选项顺序
export const accountStates: AccountState[] = [
  'ready',
  'cooldown',
  'initializing',
  'auth_required',
  'unavailable',
  'disabled',
]

// AccountQuery 是保存在地址栏中的搜索词、状态筛选、排序与页码
export interface AccountQuery {
  q: string
  state: AccountState | ''
  sort: SortKey
  desc: boolean
  page: number
}

// parseAccountQuery 从地址栏参数读出账号列表条件，未知取值按默认处理
export function parseAccountQuery(params: URLSearchParams): AccountQuery {
  const state = params.get('state') ?? ''
  const sort = params.get('sort') ?? ''
  const page = Number(params.get('page'))
  return {
    q: params.get('q') ?? '',
    state: accountStates.includes(state as AccountState) ? (state as AccountState) : '',
    sort: sortKeys.includes(sort as SortKey) ? (sort as SortKey) : 'account',
    desc: params.get('dir') === 'desc',
    page: Number.isInteger(page) && page > 1 ? page : 1,
  }
}

// accountQueryParams 把账号列表条件写回地址栏参数，默认值不写入
export function accountQueryParams(query: AccountQuery): URLSearchParams {
  const params = new URLSearchParams()
  if (query.q) params.set('q', query.q)
  if (query.state) params.set('state', query.state)
  if (query.sort !== 'account') params.set('sort', query.sort)
  if (query.desc) params.set('dir', 'desc')
  if (query.page > 1) params.set('page', String(query.page))
  return params
}

// sortValue 返回账号在一列上的排序值，没有值时为 undefined 并排在最后
function sortValue(account: Account, key: SortKey): string | number | undefined {
  switch (key) {
    case 'account':
      return account.email ?? account.account_id
    case 'state':
      return accountStates.indexOf(account.state)
    case 'tier':
      return account.tier
    case 'models':
      return account.models.length
    case 'window5h':
      return account.usage?.current?.used_ratio
    case 'lastUsed':
      return account.last_used ? Date.parse(account.last_used) : undefined
  }
}

// filterAccounts 按邮箱搜索词与状态筛选账号并按列排序，没有值的行总在最后
export function filterAccounts(accounts: Account[], query: AccountQuery): Account[] {
  const needle = query.q.trim().toLowerCase()
  const matched = accounts.filter(
    (account) =>
      (!query.state || account.state === query.state) &&
      (!needle || (account.email ?? account.account_id).toLowerCase().includes(needle)),
  )
  return matched.sort((a, b) => {
    const left = sortValue(a, query.sort)
    const right = sortValue(b, query.sort)
    if (left === undefined || right === undefined) {
      return left === right ? 0 : left === undefined ? 1 : -1
    }
    const order =
      typeof left === 'number' && typeof right === 'number'
        ? left - right
        : String(left).localeCompare(String(right))
    return query.desc ? -order : order
  })
}

// tierLabel 返回订阅层级的显示名：免费层级按界面语言，付费层级为官网的 Plus、Pro、Ultra
export function tierLabel(tier: string, free: string): string {
  if (tier === 'free') return free
  return tier.charAt(0).toUpperCase() + tier.slice(1)
}
