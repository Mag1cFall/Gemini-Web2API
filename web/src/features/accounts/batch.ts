// BatchFailure 是批量操作中失败的账号与错误信息
export interface BatchFailure {
  id: string
  message: string
}

// runBatch 以最多 limit 个并发对每个账号执行 task，全部结束后按 ids 顺序返回失败项
export async function runBatch(
  ids: string[],
  limit: number,
  task: (id: string) => Promise<unknown>,
): Promise<BatchFailure[]> {
  const errors = new Map<string, string>()
  let next = 0
  const worker = async () => {
    for (;;) {
      const id = ids[next++]
      if (id === undefined) return
      try {
        await task(id)
      } catch (error) {
        errors.set(id, error instanceof Error ? error.message : String(error))
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, ids.length) }, worker))
  return ids.flatMap((id) => {
    const message = errors.get(id)
    return message === undefined ? [] : [{ id, message }]
  })
}
