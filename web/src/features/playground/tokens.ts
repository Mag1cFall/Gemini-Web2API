import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'

import type { Attachment } from '@/features/playground/protocols'
import { authHeaders } from '@/features/playground/protocols'
import { appURL } from '@/lib/api'

// countDelay 是停止输入后发起 token 计数前等待的毫秒数
const countDelay = 500

// useSettled 返回 value 停止变化 delay 毫秒后的值
function useSettled<T>(value: T, delay: number) {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => {
      setSettled(value)
    }, delay)
    return () => {
      clearTimeout(timer)
    }
  }, [value, delay])
  return settled
}

// useInputTokens 在提示词、系统指令或附件停止变化 500 ms 后以 Gemini countTokens 计算输入 token 数，没有模型或提示词时不计数
export function useInputTokens({
  model,
  prompt,
  system,
  attachments,
  apiKey,
  enabled,
}: {
  model: string
  prompt: string
  system: string
  attachments: Attachment[]
  apiKey: string
  enabled: boolean
}) {
  const settledPrompt = useSettled(prompt, countDelay)
  const settledSystem = useSettled(system, countDelay)
  const settledAttachments = useSettled(attachments, countDelay)
  return useQuery({
    queryKey: [
      'playground-tokens',
      model,
      settledPrompt,
      settledSystem,
      settledAttachments.map((item) => item.name + ':' + String(item.data.length)),
      apiKey,
    ],
    queryFn: async ({ signal }) => {
      const response = await fetch(
        appURL(`/v1beta/models/${encodeURIComponent(model)}:countTokens`),
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', ...authHeaders('gemini', apiKey) },
          body: JSON.stringify({
            contents: [
              {
                role: 'user',
                parts: [
                  ...settledAttachments.map((item) => ({
                    inlineData: { mimeType: item.type, data: item.data },
                  })),
                  { text: settledPrompt },
                ],
              },
            ],
            ...(settledSystem ? { systemInstruction: { parts: [{ text: settledSystem }] } } : {}),
          }),
          signal,
        },
      )
      if (!response.ok) throw new Error(String(response.status))
      return ((await response.json()) as { totalTokens?: number }).totalTokens ?? 0
    },
    enabled: enabled && model !== '' && settledPrompt.trim() !== '',
    placeholderData: keepPreviousData,
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
  })
}
