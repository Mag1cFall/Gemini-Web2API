import { FileQuestion } from 'lucide-react'
import { isRouteErrorResponse, Link, useLocation, useRouteError } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { EmptyState, ErrorState } from '@/components/ui/states'
import { fill, useMessages } from '@/lib/i18n'

// RouteError 是路由级 ErrorBoundary，渲染失败与不存在的页面都在面板内说明
export function RouteError() {
  const m = useMessages(appMessages)
  const error = useRouteError()
  const location = useLocation()
  if (isRouteErrorResponse(error) && error.status === 404) {
    return <NotFound path={location.pathname} />
  }
  console.error(error)
  return (
    <ErrorState
      title={m.pageError}
      error={error}
      retryLabel={m.reload}
      onRetry={() => {
        window.location.reload()
      }}
    />
  )
}

// NotFound 是不存在路径的空状态
export function NotFound({ path }: { path: string }) {
  const m = useMessages(appMessages)
  return (
    <EmptyState
      icon={<FileQuestion size={20} aria-hidden />}
      title={m.notFound}
      description={fill(m.notFoundDescription, { path })}
      action={
        <Button asChild>
          <Link to="/">{m.backHome}</Link>
        </Button>
      }
    />
  )
}
