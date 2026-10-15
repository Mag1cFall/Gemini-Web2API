// firstApiKey 返回以逗号分隔的 PROXY_API_KEY 中的第一个 key，未设置时为空串
export function firstApiKey(value: string): string {
  return (
    value
      .split(',')
      .map((key) => key.trim())
      .find(Boolean) ?? ''
  )
}
