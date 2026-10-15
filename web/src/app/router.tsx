import { createBrowserRouter, useLocation } from 'react-router'

import { Layout } from '@/app/Layout'
import { NotFound, RouteError } from '@/app/RouteError'

// CatchAll 渲染未匹配路径的空状态
function CatchAll() {
  return <NotFound path={useLocation().pathname} />
}

// router 定义全部页面，页面代码按路由拆分加载
export const router = createBrowserRouter([
  {
    path: '/login',
    lazy: async () => ({ Component: (await import('@/features/login/LoginPage')).LoginPage }),
    errorElement: <RouteError />,
  },
  {
    element: <Layout />,
    errorElement: <RouteError />,
    children: [
      {
        errorElement: <RouteError />,
        children: [
          {
            index: true,
            lazy: async () => ({
              Component: (await import('@/features/dashboard/DashboardPage')).DashboardPage,
            }),
          },
          {
            path: 'logs',
            lazy: async () => ({ Component: (await import('@/features/logs/LogsPage')).LogsPage }),
          },
          {
            path: 'requests',
            lazy: async () => ({
              Component: (await import('@/features/requests/RequestsPage')).RequestsPage,
            }),
          },
          {
            path: 'usage',
            lazy: async () => ({
              Component: (await import('@/features/usage/UsagePage')).UsagePage,
            }),
          },
          {
            path: 'accounts',
            lazy: async () => ({
              Component: (await import('@/features/accounts/AccountsPage')).AccountsPage,
            }),
          },
          {
            path: 'accounts/import',
            lazy: async () => ({
              Component: (await import('@/features/accounts/ImportPage')).ImportPage,
            }),
          },
          {
            path: 'accounts/:id',
            lazy: async () => ({
              Component: (await import('@/features/accounts/AccountPage')).AccountPage,
            }),
          },
          {
            path: 'models',
            lazy: async () => ({
              Component: (await import('@/features/models/ModelsPage')).ModelsPage,
            }),
          },
          {
            path: 'playground',
            lazy: async () => ({
              Component: (await import('@/features/playground/PlaygroundPage')).PlaygroundPage,
            }),
          },
          {
            path: 'settings/:section?',
            lazy: async () => ({
              Component: (await import('@/features/settings/SettingsPage')).SettingsPage,
            }),
          },
          {
            path: 'about',
            lazy: async () => ({
              Component: (await import('@/features/about/AboutPage')).AboutPage,
            }),
          },
          { path: '*', Component: CatchAll },
        ],
      },
    ],
  },
])
