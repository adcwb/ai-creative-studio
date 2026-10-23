import { useEffect, useState, type ReactNode } from 'react'
import { getActiveLocale, setActiveLocale, LocaleContext, useI18n, type Locale } from '../i18n'
export function I18nProvider({children}: {children: ReactNode}) {
  const [locale, updateLocale] = useState<Locale>(getActiveLocale())
  useEffect(() => {document.documentElement.lang = locale}, [locale])
  function setLocale(value: Locale) {
    setActiveLocale(value); updateLocale(value); document.documentElement.lang = value
    try {localStorage.setItem('studio.locale', value)} catch { /* Selection still works in memory. */ }
  }
  return <LocaleContext.Provider value={{locale, setLocale}}>{children}</LocaleContext.Provider>
}

export function LanguagePicker() {
  const {locale, setLocale, t} = useI18n()
  return <label className="language-picker">{t('语言')}<select aria-label={t('界面语言')} value={locale} onChange={event => setLocale(event.target.value as Locale)}><option value="zh-CN">简体中文</option><option value="en">English</option></select></label>
}
