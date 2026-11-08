import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Info, Languages, LogOut, Menu, Minus, Moon, RefreshCw, Square, Sun, X } from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import type { AccountsUsage } from '@/features/accounts/types'
import { sessionKey, useSession, useStatus } from '@/features/service/queries'
import { api, appURL } from '@/lib/api'
import { isDesktop, maximisedEvent, nativeTitleBar, windowControl } from '@/lib/desktop'
import { fill, type Language, languages, useI18n, useMessages } from '@/lib/i18n'
import { setTheme, useTheme } from '@/lib/theme'

// BrandMark 是产品标志，Gemini Advanced 渐变四角星
function BrandMark() {
  return <img src={appURL('/favicon.svg')} alt="" aria-hidden className="size-6 shrink-0" />
}

// ServiceChip 在服务无法连接或没有可用账号时提示，正常运行时不显示
function ServiceChip() {
  const m = useMessages(appMessages)
  const status = useStatus()
  if (status.isPending || status.data?.ready === true) return null
  return (
    <span role="status" className="hidden items-center gap-2 text-sm text-foreground md:flex">
      <span
        aria-hidden
        className={'size-2 rounded-full ' + (status.isError ? 'bg-error' : 'bg-warning')}
      />
      <span>{status.isError ? m.offline : m.notReady}</span>
    </span>
  )
}

// ThemeToggle 在深浅色之间切换并记住选择，提示与图标随当前配色更新
function ThemeToggle() {
  const m = useMessages(appMessages)
  const dark = useTheme() === 'dark'
  return (
    <HeaderAction
      label={dark ? m.lightTheme : m.darkTheme}
      onClick={() => {
        setTheme(dark ? 'light' : 'dark')
      }}
    >
      {dark ? <Sun size={18} aria-hidden /> : <Moon size={18} aria-hidden />}
    </HeaderAction>
  )
}

// HeaderAction 是标题栏图标按钮，悬停为浅色圆角底
function HeaderAction({
  label,
  onClick,
  className = '',
  children,
}: {
  label: string
  onClick?: () => void
  className?: string
  children: ReactNode
}) {
  return (
    <Tooltip label={label}>
      <Button
        variant="ghost"
        size="icon"
        aria-label={label}
        onClick={onClick}
        className={
          'text-foreground no-drag enabled:hover:bg-header-hover enabled:active:bg-muted ' +
          className
        }
      >
        {children}
      </Button>
    </Tooltip>
  )
}

// WindowControls 是桌面无边框窗口右上角的最小化、最大化与关闭，Windows 上作为非客户区按钮参与系统命中测试
function WindowControls() {
  const m = useMessages(appMessages)
  const [maximised, setMaximised] = useState(false)
  useEffect(() => {
    const onChange = (event: Event) => {
      setMaximised((event as CustomEvent<boolean>).detail)
    }
    window.addEventListener(maximisedEvent, onChange)
    return () => {
      window.removeEventListener(maximisedEvent, onChange)
    }
  }, [])
  return (
    <div className="flex items-center gap-2 border-l border-border pl-2">
      <HeaderAction
        label={m.minimise}
        className="[--wails-non-client-region:minimize]"
        onClick={() => {
          windowControl('minimise')
        }}
      >
        <Minus size={18} aria-hidden />
      </HeaderAction>
      <HeaderAction
        label={maximised ? m.restore : m.maximise}
        className="[--wails-non-client-region:maximize]"
        onClick={() => {
          windowControl('toggle-maximise')
        }}
      >
        <Square size={16} aria-hidden />
      </HeaderAction>
      <HeaderAction
        label={m.close}
        className="[--wails-non-client-region:close]"
        onClick={() => {
          windowControl('close')
        }}
      >
        <X size={18} aria-hidden />
      </HeaderAction>
    </div>
  )
}

// Header 是 48px 标题栏：左侧产品名，右侧异常提示与图标操作，桌面窗口中整条可拖动
export function Header({ minimal = false, onMenu }: { minimal?: boolean; onMenu?: () => void }) {
  const m = useMessages(appMessages)
  const { language, setLanguage } = useI18n()
  const client = useQueryClient()
  const navigate = useNavigate()
  const session = useSession()
  const refresh = useMutation({
    mutationFn: () => api<AccountsUsage>('/api/accounts/usage?refresh=true'),
    onSuccess: (usage) => {
      void client.invalidateQueries()
      const failures = usage.accounts.filter((account) => account.error)
      toast(
        failures.length === 0
          ? { tone: 'success', title: m.refreshDone }
          : {
              tone: 'error',
              title: fill(m.refreshFailed, {
                message: failures
                  .map((account) => account.account_id + ': ' + String(account.error))
                  .join('; '),
              }),
            },
      )
    },
    onError: (error) => {
      toast({ tone: 'error', title: fill(m.refreshFailed, { message: error.message }) })
    },
  })
  const logout = useMutation({
    mutationFn: () => api('/api/auth/logout', { method: 'POST' }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: sessionKey })
      void navigate('/login')
    },
  })
  const canLogout = session.data?.enabled === true && session.data.authenticated
  return (
    <header
      className={
        'flex h-header shrink-0 items-center gap-4 px-4 select-none ' +
        (isDesktop ? 'drag-region ' : '') +
        (nativeTitleBar ? 'pl-[84px]' : '')
      }
    >
      {onMenu && (
        <Button
          variant="ghost"
          size="icon"
          aria-label={m.navLabel}
          onClick={onMenu}
          className="-mr-2 text-foreground no-drag enabled:hover:bg-header-hover enabled:active:bg-muted lg:hidden"
        >
          <Menu size={20} aria-hidden />
        </Button>
      )}
      <Link
        to="/"
        className="flex min-w-0 items-center gap-2 rounded-md text-md font-semibold no-drag"
      >
        <BrandMark />
        <span className="truncate" title={m.product}>
          {m.product}
        </span>
      </Link>
      <div className="flex-1" />
      {!minimal && <ServiceChip />}
      <div className="flex items-center gap-2">
        <ThemeToggle />
        <DropdownMenu>
          <Tooltip label={m.language}>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                aria-label={m.language}
                className="text-foreground no-drag enabled:hover:bg-header-hover enabled:active:bg-muted"
              >
                <Languages size={18} aria-hidden />
              </Button>
            </DropdownMenuTrigger>
          </Tooltip>
          <DropdownMenuContent>
            <DropdownMenuRadioGroup
              value={language}
              onValueChange={(value) => {
                setLanguage(value as Language)
              }}
            >
              {languages.map((item) => (
                <DropdownMenuRadioItem key={item.code} value={item.code}>
                  {item.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
        {!minimal && (
          <HeaderAction
            label={m.refresh}
            onClick={() => {
              if (!refresh.isPending) refresh.mutate()
            }}
          >
            <RefreshCw size={18} aria-hidden className={refresh.isPending ? 'animate-spin' : ''} />
          </HeaderAction>
        )}
        {!minimal && (
          <HeaderAction
            label={m.about}
            onClick={() => {
              void navigate('/about')
            }}
          >
            <Info size={18} aria-hidden />
          </HeaderAction>
        )}
        {canLogout && (
          <HeaderAction
            label={m.logout}
            onClick={() => {
              logout.mutate()
            }}
          >
            <LogOut size={18} aria-hidden />
          </HeaderAction>
        )}
      </div>
      {isDesktop && !nativeTitleBar && <WindowControls />}
    </header>
  )
}
