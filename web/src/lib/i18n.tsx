import { createContext, type ReactNode, use, useMemo, useState } from 'react'

import { Translator } from '@/lib/translator'

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

// matchLanguage 返回与语言标签最接近的界面语言：台湾、香港、澳门与繁体中文为繁体中文，其余中文为简体中文，其他按语言主标签选择，与 internal/i18n 的 Match 相同
function matchLanguage(tag: string): Language | undefined {
  const lower = tag.toLowerCase()
  if (['zh-tw', 'zh-hk', 'zh-mo'].includes(lower) || lower.startsWith('zh-hant')) return 'zh-TW'
  if (lower === 'zh' || lower.startsWith('zh-')) return 'zh-CN'
  const primary = lower.split('-')[0]
  return languages.find((item) => item.code === primary)?.code
}

// initialLanguage 取已保存的语言，否则按浏览器首选语言依次匹配，都不支持时为英文
function initialLanguage(): Language {
  try {
    const saved = localStorage.getItem(languageKey)
    if (saved && languages.some((item) => item.code === saved)) return saved as Language
  } catch {
    // 本地存储不可用时按浏览器语言选择
  }
  const preferred = navigator.languages.length ? navigator.languages : [navigator.language]
  for (const tag of preferred) {
    const match = matchLanguage(tag)
    if (match) return match
  }
  return 'en'
}

// serverModules 按需加载各语言的服务文案翻译表，服务文案以简体中文写成，简体中文没有翻译表
const serverModules: Record<
  Exclude<Language, 'zh-CN'>,
  () => Promise<{ default: Record<string, string> }>
> = {
  'zh-TW': () => import('@server-messages/zh-TW.json'),
  en: () => import('@server-messages/en.json'),
  ja: () => import('@server-messages/ja.json'),
  ko: () => import('@server-messages/ko.json'),
  fr: () => import('@server-messages/fr.json'),
  de: () => import('@server-messages/de.json'),
}

// translators 是已加载语言的服务文案翻译器
const translators: Partial<Record<Language, Translator>> = {}

// loadServerMessages 加载一种语言的服务文案翻译表，已加载或为简体中文时直接返回
async function loadServerMessages(language: Language) {
  if (language === 'zh-CN' || translators[language]) return
  translators[language] = new Translator((await serverModules[language]()).default, language)
}

// current 是当前界面语言，供组件树之外的服务文案翻译使用
let current = initialLanguage()

// loadInitialLanguage 把启动时的界面语言写入 html lang 并加载其服务文案翻译表，渲染应用前调用
export function loadInitialLanguage() {
  document.documentElement.lang = current
  return loadServerMessages(current)
}

// serverText 把服务生成的简体中文日志、错误与状态说明换成当前界面语言，上游原文、账户、模型名与数值保持原样
export function serverText(text: string) {
  return translators[current]?.translate(text) ?? text
}

interface I18nValue {
  language: Language
  locale: Language
  setLanguage: (language: Language) => Promise<void>
}

const I18nContext = createContext<I18nValue | null>(null)

// I18nProvider 提供当前界面语言；切换时先加载该语言的服务文案翻译表，再保存并同步到 html lang
export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, setLanguageState] = useState(current)
  const value = useMemo<I18nValue>(
    () => ({
      language,
      locale: language,
      setLanguage: async (next) => {
        await loadServerMessages(next)
        current = next
        document.documentElement.lang = next
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
