import { createContext, useCallback, useContext } from 'react'
export type Locale = 'zh-CN' | 'en'
export type TranslationParams = Record<string, string | number>
let dictionaries: Record<string, string> | undefined
let englishLoading: Promise<void> | undefined
export function isLocaleLoaded(locale: Locale) {return locale === 'zh-CN' || !!dictionaries}
export function loadLocale(locale: Locale): Promise<void> {
  if (isLocaleLoaded(locale)) return Promise.resolve()
  return englishLoading ??= import('./englishDictionary').then(module => {dictionaries = module.default}).catch(error => {englishLoading = undefined; throw error})
}
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
  const template = locale === 'en' ? dictionaries?.[key] ?? key : key
  return template.replace(/\{(\w+)\}/g, (match, name: string) => params[name] === undefined ? match : String(params[name]))
}
export function setActiveLocale(locale: Locale) { activeLocale = locale }
export function getActiveLocale() { return activeLocale }
export const getLocale = getActiveLocale
interface LanguageContext {locale: Locale; setLocale: (locale: Locale) => void; localeSaved?: boolean; requestedLocale?: Locale}
export const LocaleContext = createContext<LanguageContext>({locale: activeLocale, setLocale: () => {}})
export function useI18n() {
  const {locale, setLocale, localeSaved = true, requestedLocale = locale} = useContext(LocaleContext)
  const t = useCallback((key: string, params?: TranslationParams) => translate(key, params, locale), [locale])
  return {locale, requestedLocale, setLocale, localeSaved, t,
    formatDate: (date: string | Date, options?: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(locale, options ?? {dateStyle: 'medium', timeStyle: 'short'}).format(new Date(date)),
    formatNumber: (value: number, options?: Intl.NumberFormatOptions) => new Intl.NumberFormat(locale, options).format(value),
    formatCurrency: (value: number, currency = 'USD') => new Intl.NumberFormat(locale, {style: 'currency', currency}).format(value)}
}
