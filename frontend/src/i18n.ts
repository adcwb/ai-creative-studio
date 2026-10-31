import { createContext, useCallback, useContext } from 'react'
import core from './locales/core'
import mediaLegacy from './locales/mediaLegacy'
import drama from './locales/drama'
import governance from './locales/governance'
import runtime from './locales/runtime'
import mediaComposite from './locales/mediaComposite'
import social from './locales/social'
import douyin from './locales/douyin'
import poster from './locales/poster'
import navigation from './locales/navigation'
import digitalHuman from './locales/digitalHuman'
import library from './locales/library'
import production from './locales/production'
import appearance from './locales/appearance'
import models from './locales/models'
export type Locale = 'zh-CN' | 'en'
export type TranslationParams = Record<string, string | number>
const dictionaries: Record<string, string> = {...drama, ...mediaLegacy, ...core, ...governance, ...runtime, ...mediaComposite, ...poster, ...social, ...douyin, ...navigation, ...digitalHuman, ...library, ...production, ...models, ...appearance}
export function resolveLocale(saved: string | null, languages: readonly string[]): Locale {
  if (saved === 'zh-CN' || saved === 'en') return saved
  return languages[0]?.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}
function initialLocale(): Locale {
  if (typeof window === 'undefined') return 'zh-CN'
  let saved: string | null = null
  try {saved = localStorage.getItem('studio.locale')} catch { /* Storage may be disabled. */ }
  return resolveLocale(saved, navigator.languages ?? [navigator.language])
}
let activeLocale: Locale = initialLocale()
export function translate(key: string, params: TranslationParams = {}, locale = activeLocale): string {
  const template = locale === 'en' ? dictionaries[key] ?? key : key
  return template.replace(/\{(\w+)\}/g, (match, name: string) => params[name] === undefined ? match : String(params[name]))
}
export function setActiveLocale(locale: Locale) { activeLocale = locale }
export function getActiveLocale() { return activeLocale }
export const getLocale = getActiveLocale
interface LanguageContext {locale: Locale; setLocale: (locale: Locale) => void; localeSaved?: boolean}
export const LocaleContext = createContext<LanguageContext>({locale: activeLocale, setLocale: () => {}})
export function useI18n() {
  const {locale, setLocale, localeSaved = true} = useContext(LocaleContext)
  const t = useCallback((key: string, params?: TranslationParams) => translate(key, params, locale), [locale])
  return {locale, setLocale, localeSaved, t,
    formatDate: (date: string | Date, options?: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(locale, options ?? {dateStyle: 'medium', timeStyle: 'short'}).format(new Date(date)),
    formatNumber: (value: number, options?: Intl.NumberFormatOptions) => new Intl.NumberFormat(locale, options).format(value),
    formatCurrency: (value: number, currency = 'USD') => new Intl.NumberFormat(locale, {style: 'currency', currency}).format(value)}
}
