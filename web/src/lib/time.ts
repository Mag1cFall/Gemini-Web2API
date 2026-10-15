import { useEffect, useState } from 'react'

// useNow 返回按 interval 毫秒刷新的当前时间，用于相对时间与倒计时
export function useNow(interval: number) {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const timer = setInterval(() => {
      setNow(new Date())
    }, interval)
    return () => {
      clearInterval(timer)
    }
  }, [interval])
  return now
}
