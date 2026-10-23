import { createContext, type ReactNode, use, useEffect, useMemo, useState } from 'react'

// Language 是界面语言，覆盖 AI Studio 管理端的七种语言
export type Language = 'zh-CN' | 'zh-TW' | 'en' | 'ja' | 'ko' | 'fr' | 'de'

// languages 是语言选项与显示名
export const languages: { code: Language; label: string }[] = [
  { code: 'zh-CN', label: '简体中文' },
  { code: 'zh-TW', label: '繁體中文' },
  { code: 'en', label: 'English' },
  { code: 'ja', label: '日本語' },
  { code: 'ko', label: '한국어' },
  { code: 'fr', label: 'Français' },
  { code: 'de', label: 'Deutsch' },
]

// Messages 是一组七种界面语言的文案
export type Messages<T extends Record<string, string>> = Record<Language, T>

// defineMessages 声明一个功能的七种语言文案，各语言的键与简体中文一致
export function defineMessages<T extends Record<string, string>>(
  messages: { 'zh-CN': T } & Record<Exclude<Language, 'zh-CN'>, Record<keyof T, string>>,
): Messages<T> {
  return messages as Messages<T>
}

// fill 用 vars 替换文案中的 {名称} 占位
export function fill(template: string, vars: Record<string, string | number>) {
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in vars ? String(vars[name]) : match,
  )
}

// plural 按 language 的复数规则为 count 选用单数文案 one 或复数文案 other
export function plural(language: Language, count: number, one: string, other: string) {
  return new Intl.PluralRules(language).select(count) === 'one' ? one : other
}

// languageKey 是本地存储中保存界面语言的键
const languageKey = 'gemini-web2api.language'

// initialLanguage 取已保存的语言，否则按浏览器语言选择
function initialLanguage(): Language {
  try {
    const saved = localStorage.getItem(languageKey)
    if (saved && languages.some((item) => item.code === saved)) return saved as Language
  } catch {
    // 本地存储不可用时按浏览器语言选择
  }
  const browser = navigator.language.toLowerCase()
  if (browser.startsWith('zh'))
    return browser.includes('tw') || browser.includes('hk') ? 'zh-TW' : 'zh-CN'
  const match = languages.find((item) => item.code !== 'zh-TW' && browser.startsWith(item.code))
  return match ? match.code : 'en'
}

interface I18nValue {
  language: Language
  locale: Language
  setLanguage: (language: Language) => void
}

const I18nContext = createContext<I18nValue | null>(null)

// I18nProvider 提供当前界面语言并同步到 html lang
export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, setLanguageState] = useState(initialLanguage)
  useEffect(() => {
    document.documentElement.lang = language
  }, [language])
  const value = useMemo<I18nValue>(
    () => ({
      language,
      locale: language,
      setLanguage: (next) => {
        setLanguageState(next)
        try {
          localStorage.setItem(languageKey, next)
        } catch {
          // 本地存储不可用时只在本次会话生效
        }
      },
    }),
    [language],
  )
  return <I18nContext value={value}>{children}</I18nContext>
}

// useI18n 返回当前语言与区域
export function useI18n() {
  const value = use(I18nContext)
  if (!value) throw new Error('useI18n 必须在 I18nProvider 内使用')
  return value
}

// useMessages 返回当前语言的文案
export function useMessages<T extends Record<string, string>>(messages: Messages<T>): T {
  const { language } = useI18n()
  return messages[language]
}
