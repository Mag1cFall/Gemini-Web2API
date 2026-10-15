import './index.css'

import { QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from 'react-router'

import { appMessages } from '@/app/messages'
import { router } from '@/app/router'
import { Toaster } from '@/components/ui/toast'
import { TooltipProvider } from '@/components/ui/tooltip'
import { sessionKey } from '@/features/service/queries'
import { ApiError } from '@/lib/api'
import { I18nProvider, useMessages } from '@/lib/i18n'
import { applyTheme } from '@/lib/theme'

applyTheme()

// queryClient 管理全部服务端数据，401 时重新读取会话并转到登录页
const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (error, query) => {
      if (error instanceof ApiError && error.status === 401 && query.queryKey[0] !== 'session') {
        void queryClient.invalidateQueries({ queryKey: sessionKey })
      }
    },
  }),
  defaultOptions: {
    queries: {
      retry: (count, error) => !(error instanceof ApiError && error.status < 500) && count < 2,
      refetchOnWindowFocus: false,
    },
  },
})

// AppToaster 以当前语言渲染提示
function AppToaster() {
  return <Toaster closeLabel={useMessages(appMessages).dismiss} />
}

const root = document.getElementById('root')
if (root) {
  createRoot(root).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <I18nProvider>
          <TooltipProvider>
            <RouterProvider router={router} />
            <AppToaster />
          </TooltipProvider>
        </I18nProvider>
      </QueryClientProvider>
    </StrictMode>,
  )
}
