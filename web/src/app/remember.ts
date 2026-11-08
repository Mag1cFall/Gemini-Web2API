import { useEffect } from 'react'
import { type Location, useLocation } from 'react-router'

// visited 是本次打开管理端后各路径最后一次的查询参数，最近访问的路径排在最后
const visited = new Map<string, string>()

// useRememberLocation 记下当前路径的查询参数
export function useRememberLocation() {
  const { pathname, search } = useLocation()
  useEffect(() => {
    visited.delete(pathname)
    visited.set(pathname, search)
  }, [pathname, search])
}

// lastLocation 返回导航项 to 的目标：to 及其子路径（end 时只有 to）中最近访问的路径与查询参数，当前位置在其中时为当前位置，没有访问过时为 to
export function lastLocation(to: string, end: boolean, current: Location) {
  const inside = (pathname: string) => pathname === to || (!end && pathname.startsWith(to + '/'))
  if (inside(current.pathname)) return current.pathname + current.search
  let target = to
  for (const [pathname, search] of visited) {
    if (inside(pathname)) target = pathname + search
  }
  return target
}
