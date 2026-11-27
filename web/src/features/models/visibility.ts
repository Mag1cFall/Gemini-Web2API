import type { Model } from '@/features/models/types'

// predefinedGemsSelector 在 HIDDEN_MODELS 中表示全部 Google 预置 Gem（CNgdBe 类型 3）
export const predefinedGemsSelector = 'predefined-gems'

// toggleHiddenModels 根据目标模型与新开放状态计算更新后的 HIDDEN_MODELS 字符串
export function toggleHiddenModels(
  current: string,
  target: Model,
  allModels: Model[],
  open: boolean,
): string {
  const entries = new Set(
    current
      .split(',')
      .map((item) => item.trim().toLowerCase())
      .filter(Boolean),
  )
  const id = target.id.toLowerCase()
  const predefinedIDs = allModels
    .filter((item) => item.predefined_gem && !item.alias_of)
    .map((item) => item.id.toLowerCase())

  if (open) {
    if (target.predefined_gem && entries.has(predefinedGemsSelector)) {
      entries.delete(predefinedGemsSelector)
      for (const gemID of predefinedIDs) {
        if (gemID !== id) entries.add(gemID)
      }
    }
    entries.delete(id)
  } else {
    entries.add(id)
    if (predefinedIDs.length > 0 && predefinedIDs.every((gemID) => entries.has(gemID))) {
      for (const gemID of predefinedIDs) entries.delete(gemID)
      entries.add(predefinedGemsSelector)
    }
  }
  return [...entries].sort().join(',')
}
