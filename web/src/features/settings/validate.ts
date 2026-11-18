import type { ServiceConfig } from '@/features/settings/types'

// proxySchemes 是出口代理接受的协议，与 chromeauth.ValidateProxy 相同
const proxySchemes = ['http:', 'https:', 'socks4:', 'socks4a:', 'socks5:', 'socks5h:']

// SettingErrors 是各设置项的格式错误类型，键为配置字段
export type SettingErrors = Partial<
  Record<
    keyof ServiceConfig | 'admin_password',
    'listen' | 'proxy' | 'authStates' | 'password' | 'incomplete' | 'duplicate'
  >
>

// validListen 判断监听地址是否为 主机:端口
function validListen(value: string) {
  const match = /^(\[[^\]]*\]|[^:]*):(\d+)$/.exec(value.trim())
  return match !== null && Number(match[2]) <= 65535
}

// validProxy 判断代理是否为带主机名的受支持协议 URL
function validProxy(value: string) {
  try {
    const url = new URL(value.trim())
    return proxySchemes.includes(url.protocol) && url.hostname !== ''
  } catch {
    return false
  }
}

// pairProblem 返回“名称:值”成对列表的问题：有一侧为空为 incomplete，左侧重复为 duplicate
export function pairProblem(rows: [string, string][]): 'incomplete' | 'duplicate' | undefined {
  if (rows.some(([left, right]) => !left.trim() || !right.trim())) return 'incomplete'
  const names = rows.map(([left]) => left.trim())
  return new Set(names).size === names.length ? undefined : 'duplicate'
}

// splitPairs 把逗号分隔的“名称:值”拆成行，以最后一个冒号分开
export function splitPairs(value: string): [string, string][] {
  return value
    .split(',')
    .filter((item) => item !== '')
    .map((item) => {
      const index = item.lastIndexOf(':')
      return index < 0 ? [item, ''] : [item.slice(0, index), item.slice(index + 1)]
    })
}

// joinPairs 把行写回逗号分隔的“名称:值”，保留未填完的行以便继续编辑
export function joinPairs(rows: [string, string][]) {
  return rows.map(([left, right]) => left + ':' + right).join(',')
}

// validateSettings 按服务端相同的规则检查各设置项的格式，返回有错误的字段
export function validateSettings(
  value: ServiceConfig,
  password: string,
  passwordSet: boolean,
): SettingErrors {
  const errors: SettingErrors = {}
  if (!validListen(value.listen_addr)) errors.listen_addr = 'listen'
  if (
    value.proxy
      .split(',')
      .map((item) => item.trim())
      .some((item) => item !== '' && !validProxy(item))
  ) {
    errors.proxy = 'proxy'
  }
  if (value.auth_states.split(',').every((item) => item.trim() === '')) {
    errors.auth_states = 'authStates'
  }
  if (value.admin_auth_enabled && !passwordSet && password === '') {
    errors.admin_password = 'password'
  }
  const mapping = pairProblem(splitPairs(value.model_mapping))
  if (mapping) errors.model_mapping = mapping
  const accounts = pairProblem(splitPairs(value.model_accounts))
  if (accounts) errors.model_accounts = accounts
  return errors
}
