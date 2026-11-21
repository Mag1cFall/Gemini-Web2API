import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router'

import { Header } from '@/app/Header'
import { appMessages } from '@/app/messages'
import { SessionError } from '@/app/SessionError'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Loading } from '@/components/ui/states'
import { sessionKey, useSession } from '@/features/service/queries'
import type { Session } from '@/features/service/types'
import { api } from '@/lib/api'
import { defineMessages, useMessages } from '@/lib/i18n'

// loginMessages 是管理登录页的文案
const loginMessages = defineMessages({
  'zh-CN': {
    title: '登录管理端',
    description: '从其他电脑访问需要管理用户名与密码。',
    username: '用户名',
    password: '密码',
    submit: '登录',
    required: '不能为空。',
    disabled: '远程登录未开启。在服务所在电脑上打开管理端，于“设置 › 管理登录”中开启。',
  },
  'zh-TW': {
    title: '登入管理介面',
    description: '從其他電腦存取需要管理員的使用者名稱與密碼。',
    username: '使用者名稱',
    password: '密碼',
    submit: '登入',
    required: '不能為空。',
    disabled: '遠端登入未開啟。請在執行服務的電腦上開啟管理介面，於「設定 › 管理登入」中開啟。',
  },
  en: {
    title: 'Sign in to the console',
    description: 'Opening the console from another computer needs the admin username and password.',
    username: 'Username',
    password: 'Password',
    submit: 'Sign in',
    required: 'Required.',
    disabled:
      'Remote sign-in is off. Open the console on the computer running the service and turn it on under Settings › Admin sign-in.',
  },
  ja: {
    title: '管理画面にログイン',
    description: '別のコンピューターから開くには管理者のユーザー名とパスワードが必要です。',
    username: 'ユーザー名',
    password: 'パスワード',
    submit: 'ログイン',
    required: '入力してください。',
    disabled:
      'リモートログインはオフです。サービスを実行しているコンピューターで管理画面を開き、「設定 › 管理ログイン」でオンにしてください。',
  },
  ko: {
    title: '관리 콘솔 로그인',
    description: '다른 컴퓨터에서 열려면 관리자 사용자 이름과 비밀번호가 필요합니다.',
    username: '사용자 이름',
    password: '비밀번호',
    submit: '로그인',
    required: '필수 항목입니다.',
    disabled:
      '원격 로그인이 꺼져 있습니다. 서비스를 실행 중인 컴퓨터에서 관리 콘솔을 열고 “설정 › 관리 로그인”에서 켜세요.',
  },
  fr: {
    title: 'Connexion à la console',
    description:
      'Pour ouvrir la console depuis un autre ordinateur, il faut le nom d’utilisateur et le mot de passe d’administration.',
    username: 'Nom d’utilisateur',
    password: 'Mot de passe',
    submit: 'Se connecter',
    required: 'Champ obligatoire.',
    disabled:
      'La connexion à distance est désactivée. Ouvrez la console sur l’ordinateur qui exécute le service et activez-la dans Paramètres › Connexion d’administration.',
  },
  de: {
    title: 'Bei der Konsole anmelden',
    description:
      'Für den Zugriff von einem anderen Computer sind Admin-Benutzername und Passwort nötig.',
    username: 'Benutzername',
    password: 'Passwort',
    submit: 'Anmelden',
    required: 'Pflichtfeld.',
    disabled:
      'Die Fernanmeldung ist aus. Öffne die Konsole auf dem Computer, auf dem der Dienst läuft, und aktiviere sie unter Einstellungen › Admin-Anmeldung.',
  },
})

// LoginPage 是远程访问时的管理登录
export function LoginPage() {
  const m = useMessages(loginMessages)
  const app = useMessages(appMessages)
  const client = useQueryClient()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const session = useSession()
  const next = params.get('next') ?? '/'
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState<{
    username?: string | undefined
    password?: string | undefined
    root?: string
  }>({})
  const login = useMutation({
    mutationFn: (credentials: { username: string; password: string }) =>
      api<Session>('/api/auth/login', { method: 'POST', json: credentials }),
    onSuccess: async (result) => {
      client.setQueryData(sessionKey, result)
      await navigate(next.startsWith('/') ? next : '/', { replace: true })
    },
    onError: (error) => {
      setErrors({ root: error.message })
    },
  })
  if (session.isPending) return <Loading label={app.loading} />
  if (session.isError && !session.data) {
    return <SessionError error={session.error} onRetry={() => void session.refetch()} />
  }
  if (session.data.authenticated) return <Navigate to={next} replace />
  return (
    <div className="flex min-h-dvh flex-col safe-x">
      <Header minimal />
      <main className="flex flex-1 items-start justify-center px-4 pt-16 pb-16">
        <div className="flex w-full max-w-login flex-col gap-6 rounded-lg bg-surface p-8 shadow-raised max-sm:p-6">
          <div>
            <h1 className="text-2xl font-semibold">{m.title}</h1>
            <p className="mt-1 text-sm text-foreground-muted">{m.description}</p>
          </div>
          {!session.data.enabled ? (
            <Callout tone="muted" role="status">
              {m.disabled}
            </Callout>
          ) : (
            <form
              noValidate
              className="flex flex-col gap-4"
              onSubmit={(event) => {
                event.preventDefault()
                const trimmed = username.trim()
                const nextErrors: { username?: string; password?: string } = {}
                if (!trimmed) nextErrors.username = m.required
                if (!password) nextErrors.password = m.required
                setErrors(nextErrors)
                if (nextErrors.username || nextErrors.password) return
                login.mutate({ username: trimmed, password })
              }}
            >
              <Field id="username" label={m.username} error={errors.username}>
                <Input
                  id="username"
                  autoComplete="username"
                  autoCapitalize="none"
                  spellCheck={false}
                  aria-invalid={errors.username ? true : undefined}
                  aria-describedby={errors.username ? 'username-error' : undefined}
                  value={username}
                  onChange={(event) => {
                    setUsername(event.target.value)
                    if (errors.username)
                      setErrors({
                        ...errors,
                        username: event.target.value.trim() ? undefined : m.required,
                      })
                  }}
                />
              </Field>
              <Field id="password" label={m.password} error={errors.password}>
                <Input
                  id="password"
                  type="password"
                  autoComplete="current-password"
                  aria-invalid={errors.password ? true : undefined}
                  aria-describedby={errors.password ? 'password-error' : undefined}
                  value={password}
                  onChange={(event) => {
                    setPassword(event.target.value)
                    if (errors.password)
                      setErrors({
                        ...errors,
                        password: event.target.value ? undefined : m.required,
                      })
                  }}
                />
              </Field>
              {errors.root && (
                <p role="alert" className="text-sm text-error-text">
                  {errors.root}
                </p>
              )}
              <Button type="submit" variant="primary" size="lg" loading={login.isPending}>
                {m.submit}
              </Button>
            </form>
          )}
        </div>
      </main>
    </div>
  )
}
