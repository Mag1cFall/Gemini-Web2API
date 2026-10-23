import {
  Boxes,
  ChartColumn,
  ChevronDown,
  LayoutDashboard,
  LoaderCircle,
  type LucideIcon,
  MessageSquare,
  ScrollText,
  Server,
  Settings,
  SquareActivity,
  UserPlus,
  Users,
  Wrench,
} from 'lucide-react'
import { Dialog as DialogPrimitive } from 'radix-ui'
import { useState } from 'react'
import { Navigate, NavLink, Outlet, useLocation } from 'react-router'

import { type EventsConnection, useAdminEvents } from '@/app/events'
import { Header } from '@/app/Header'
import { appMessages } from '@/app/messages'
import { Collapse } from '@/components/ui/collapse'
import { Loading } from '@/components/ui/states'
import { useAccounts } from '@/features/accounts/queries'
import type { Account } from '@/features/accounts/types'
import { useSession } from '@/features/service/queries'
import { fill, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// accountTone 是账号状态圆点的颜色类
function accountTone(state: Account['state']) {
  switch (state) {
    case 'ready':
      return 'bg-success'
    case 'cooldown':
      return 'bg-warning'
    case 'auth_required':
    case 'unavailable':
      return 'bg-error'
    case 'initializing':
      return 'bg-info'
    default:
      return 'bg-foreground-subtle'
  }
}

// NavItem 是一个导航目的地
interface NavItem {
  to: string
  label: string
  icon?: LucideIcon
  dot?: string
  badge?: string
  end?: boolean
}

// navClass 是导航项的外观：悬停与选中为浅灰圆角底，选中项左缘为 4×16 品牌蓝竖条
const navClass =
  'relative flex h-control items-center gap-3 rounded-sm pr-2 pl-[42px] text-base text-foreground hover:bg-nav-active active:bg-nav-pressed aria-[current=page]:bg-nav-active aria-[current=page]:before:absolute aria-[current=page]:before:top-1/2 aria-[current=page]:before:left-0 aria-[current=page]:before:h-4 aria-[current=page]:before:w-1 aria-[current=page]:before:-translate-y-1/2 aria-[current=page]:before:rounded-full aria-[current=page]:before:bg-primary'

// NavRow 渲染一个导航项，文字截断时悬停显示全文
function NavRow({ item, className = navClass }: { item: NavItem; className?: string }) {
  return (
    <NavLink to={item.to} end={item.end ?? false} className={className}>
      {item.icon && <item.icon size={20} aria-hidden className="shrink-0" />}
      {item.dot && (
        <span aria-hidden className="flex size-icon shrink-0 items-center justify-center">
          <span className={'size-2 rounded-full ' + item.dot} />
        </span>
      )}
      <span className="min-w-0 flex-1 truncate" title={item.label}>
        {item.label}
      </span>
      {item.badge && (
        <span className="shrink-0 rounded-sm bg-info-soft px-[6px] text-xs leading-[20px] text-primary">
          {item.badge}
        </span>
      )}
    </NavLink>
  )
}

// collapsedKey 是本地存储中保存已收起分组的键
const collapsedKey = 'gemini-web2api.nav.collapsed'

// readCollapsed 读取已收起的分组，本地存储不可用时全部展开
function readCollapsed(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(collapsedKey) ?? '[]')
    return Array.isArray(value) ? value.filter((item) => typeof item === 'string') : []
  } catch {
    return []
  }
}

// NavGroup 是可折叠的一组导航项，分组行左侧为图标，右侧为折叠箭头
function NavGroup({
  id,
  title,
  icon: Icon,
  items,
  collapsed,
  onToggle,
}: {
  id: string
  title: string
  icon: LucideIcon
  items: NavItem[]
  collapsed: boolean
  onToggle: (id: string) => void
}) {
  return (
    <li className="flex flex-col">
      <button
        type="button"
        aria-expanded={!collapsed}
        aria-controls={'nav-' + id}
        onClick={() => {
          onToggle(id)
        }}
        className="flex h-control items-center gap-2 rounded-sm pr-1 pl-4 text-base text-foreground hover:bg-nav-active active:bg-nav-pressed"
      >
        <Icon size={20} aria-hidden className="shrink-0" />
        <span className="min-w-0 flex-1 truncate text-left">{title}</span>
        <span aria-hidden className="flex size-[28px] items-center justify-center">
          <ChevronDown
            size={16}
            className={
              'text-foreground-muted transition-transform duration-(--duration-base) ' +
              (collapsed ? '-rotate-90' : '')
            }
          />
        </span>
      </button>
      <Collapse open={!collapsed} id={'nav-' + id}>
        <ul className="flex flex-col gap-1 pt-1">
          {items.map((item) => (
            <li key={item.to}>
              <NavRow item={item} />
            </li>
          ))}
        </ul>
      </Collapse>
    </li>
  )
}

// OfflineCard 是事件流断开时侧栏顶部的离线卡片，倒计时结束自动重连
function OfflineCard({ connection }: { connection: EventsConnection }) {
  const m = useMessages(appMessages)
  const now = useNow(1000)
  const seconds = Math.max(0, Math.ceil((connection.retryAt - now.getTime()) / 1000))
  const retrying = connection.phase === 'retrying'
  return (
    <div
      role="status"
      className="mx-1 flex flex-col gap-2 rounded-md border border-offline-border bg-offline p-3 text-white"
    >
      <div className="flex flex-col">
        <span className="text-base font-semibold">{m.offlineTitle}</span>
        <span className="text-xs">{m.offlineReason}</span>
      </div>
      <button
        type="button"
        disabled={retrying}
        onClick={connection.reconnect}
        className="flex h-[28px] items-center justify-center gap-1 self-start rounded-sm border border-white px-3 text-xs hover:bg-white/15 disabled:cursor-default"
      >
        {retrying ? (
          <>
            <LoaderCircle size={14} aria-hidden className="animate-spin" />
            {m.reconnecting}
          </>
        ) : (
          fill(m.reconnectIn, { seconds })
        )}
      </button>
    </div>
  )
}

// Layout 是登录后的外壳：标题栏、左列分组导航与内容面板
export function Layout() {
  const m = useMessages(appMessages)
  const location = useLocation()
  const [drawer, setDrawer] = useState<string | null>(null)
  const session = useSession()
  const authenticated = session.data?.authenticated === true
  const connection = useAdminEvents(authenticated)
  const accounts = useAccounts()
  const [collapsed, setCollapsed] = useState(readCollapsed)
  if (session.isPending) return <Loading label={m.loading} />
  if (session.data && !authenticated) {
    return (
      <Navigate
        to={'/login?next=' + encodeURIComponent(location.pathname + location.search)}
        replace
      />
    )
  }
  const toggle = (id: string) => {
    const next = collapsed.includes(id)
      ? collapsed.filter((item) => item !== id)
      : [...collapsed, id]
    setCollapsed(next)
    try {
      localStorage.setItem(collapsedKey, JSON.stringify(next))
    } catch {
      // 本地存储不可用时只在本次会话生效
    }
  }
  const groups: { id: string; title: string; icon: LucideIcon; items: NavItem[] }[] = [
    {
      id: 'service',
      title: m.groupService,
      icon: Server,
      items: [
        { to: '/', label: m.overview, icon: LayoutDashboard, end: true },
        { to: '/logs', label: m.logs, icon: ScrollText },
        { to: '/requests', label: m.requests, icon: SquareActivity },
        { to: '/usage', label: m.usage, icon: ChartColumn },
      ],
    },
    {
      id: 'accounts',
      title: m.groupAccounts,
      icon: Users,
      items: [
        ...(accounts.data?.accounts ?? []).map((account) => ({
          to: '/accounts/' + encodeURIComponent(account.account_id),
          label: account.email ?? account.account_id,
          dot: accountTone(account.state),
          ...(account.tier ? { badge: account.tier } : {}),
        })),
        { to: '/accounts', label: m.accounts, icon: Users, end: true },
        { to: '/accounts/import', label: m.importAccount, icon: UserPlus },
      ],
    },
    {
      id: 'tools',
      title: m.groupTools,
      icon: Wrench,
      items: [
        { to: '/models', label: m.models, icon: Boxes },
        { to: '/playground', label: m.playground, icon: MessageSquare },
      ],
    },
  ]
  const offline = connection.phase === 'waiting' || connection.phase === 'retrying'
  const sidebar = (
    <>
      {offline && <OfflineCard connection={connection} />}
      <nav
        aria-label={m.navLabel}
        className="flex min-h-0 flex-1 flex-col overflow-y-auto px-1 pt-2 pb-2"
      >
        <ul className="flex flex-col gap-1">
          {groups.map((group) => (
            <NavGroup
              key={group.id}
              {...group}
              collapsed={collapsed.includes(group.id)}
              onToggle={toggle}
            />
          ))}
        </ul>
      </nav>
      <div className="border-t-2 border-nav-active px-1 py-2">
        <NavRow
          item={{ to: '/settings', label: m.settings, icon: Settings }}
          className={navClass.replace('pl-[42px]', 'pl-4').replace('gap-3', 'gap-2')}
        />
      </div>
    </>
  )
  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-background safe-x">
      <a
        href="#main"
        className="sr-only z-toast rounded-md bg-surface px-4 py-2 focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        {m.skipToContent}
      </a>
      <Header
        onMenu={() => {
          setDrawer(location.pathname)
        }}
      />
      <DialogPrimitive.Root
        open={drawer === location.pathname}
        onOpenChange={(open) => {
          setDrawer(open ? location.pathname : null)
        }}
      >
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-overlay bg-overlay data-[state=closed]:animate-fade-out data-[state=open]:animate-fade-in lg:hidden" />
          <DialogPrimitive.Content className="fixed inset-y-0 left-0 z-modal flex w-sidebar max-w-[85vw] flex-col bg-background pt-2 shadow-modal focus:outline-none data-[state=closed]:animate-drawer-out data-[state=open]:animate-drawer-in lg:hidden">
            <DialogPrimitive.Title className="sr-only">{m.navLabel}</DialogPrimitive.Title>
            <DialogPrimitive.Description className="sr-only">
              {m.navLabel}
            </DialogPrimitive.Description>
            {sidebar}
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
      <div className="flex min-h-0 flex-1">
        <aside className="hidden w-sidebar shrink-0 flex-col lg:flex">{sidebar}</aside>
        <main
          id="main"
          tabIndex={-1}
          className="relative min-w-0 flex-1 [scrollbar-gutter:stable] overflow-y-auto rounded-tl-panel border-t border-l border-panel-edge bg-panel focus:outline-none"
        >
          {offline && (
            <p
              role="status"
              className="sticky top-0 z-sticky bg-warning-soft px-6 py-2 text-sm text-warning-text lg:hidden"
            >
              {m.offlineReason}
            </p>
          )}
          <div
            key={location.pathname}
            className="flex min-h-full max-w-page animate-page-in flex-col gap-3 px-10 pt-8 pb-8 max-lg:px-4 max-lg:pt-4 [&>header]:mb-3"
          >
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
