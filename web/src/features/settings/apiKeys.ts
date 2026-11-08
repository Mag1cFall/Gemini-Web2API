// firstApiKey 返回以逗号分隔的 PROXY_API_KEY 中的第一个 key，未设置时为空串
export function firstApiKey(value: string): string {
  return (
    value
      .split(',')
      .map((key) => key.trim())
      .find(Boolean) ?? ''
  )
}

// maskKey 返回显示用的 API key：不超过 12 个字符时完整显示，更长时只显示首尾各 4 个字符
export function maskKey(key: string) {
  return key.length <= 12 ? key : key.slice(0, 4) + '…' + key.slice(-4)
}

// parseKeyAccounts 解析 API_KEY_ACCOUNTS 的 key:账号|账号 写法，返回各 key 绑定的账号
export function parseKeyAccounts(value: string) {
  return new Map(
    value
      .split(',')
      .filter(Boolean)
      .map((item) => {
        const index = item.lastIndexOf(':')
        return [item.slice(0, index), item.slice(index + 1).split('|')] as const
      }),
  )
}

// modelsForKey 返回 key 能请求的模型：绑定了账号的 key 只保留这些账号提供的模型，未绑定的 key 保留全部
export function modelsForKey<T extends { accounts: string[] }>(
  models: T[],
  bound: readonly string[] | undefined,
) {
  return bound ? models.filter((model) => model.accounts.some((id) => bound.includes(id))) : models
}
