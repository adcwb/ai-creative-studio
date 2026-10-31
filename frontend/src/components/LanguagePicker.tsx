import { useContext, useEffect, useState, type ReactNode } from 'react'
import { getActiveLocale, setActiveLocale, LocaleContext, useI18n, resolveLocale, type Locale } from '../i18n'
export function I18nProvider({children, storageKey = 'studio.locale'}: {children: ReactNode; storageKey?: string}) {
  const parentLocale = useContext(LocaleContext).locale
  const [locale, updateLocale] = useState<Locale>(() => {
    if (storageKey === 'studio.locale') return getActiveLocale()
    try { return resolveLocale(localStorage.getItem(storageKey) ?? parentLocale, navigator.languages) } catch { return parentLocale }
  })
  const [localeSaved, setLocaleSaved] = useState(true)
  // Legacy non-hook formatters share this locale. Set it before children render
  // when an account's saved locale is first mounted, not one effect later.
  setActiveLocale(locale)
  useEffect(() => {
    setActiveLocale(locale); document.documentElement.lang = locale
    return () => {setActiveLocale(parentLocale); document.documentElement.lang = parentLocale}
  }, [locale, parentLocale])
  function setLocale(value: Locale) {
    setActiveLocale(value); updateLocale(value); document.documentElement.lang = value
    try {localStorage.setItem(storageKey, value); setLocaleSaved(true)} catch {setLocaleSaved(false)}
  }
  return <LocaleContext.Provider value={{locale, setLocale, localeSaved}}>{children}</LocaleContext.Provider>
}

export function LanguagePicker() {
  const {locale, setLocale, t} = useI18n()
  return <label className="language-picker">{t('语言')}<select aria-label={t('界面语言')} value={locale} onChange={event => setLocale(event.target.value as Locale)}><option value="zh-CN">简体中文</option><option value="en">English</option></select></label>
}

export function LanguageButtons() {
  const {locale, setLocale, t} = useI18n()
  return <div className="language-buttons" role="group" aria-label={t('界面语言')}><button type="button" lang="zh-CN" aria-label="简体中文" aria-pressed={locale === 'zh-CN'} onClick={() => setLocale('zh-CN')}>{'中'}</button><span aria-hidden="true">/</span><button type="button" lang="en" aria-label="English" aria-pressed={locale === 'en'} onClick={() => setLocale('en')}>en</button></div>
}
