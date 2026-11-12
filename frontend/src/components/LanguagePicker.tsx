import { useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { getActiveLocale, setActiveLocale, isLocaleLoaded, loadLocale, LocaleContext, useI18n, resolveLocale, type Locale } from '../i18n'
export function I18nProvider({children, storageKey = 'studio.locale'}: {children: ReactNode; storageKey?: string}) {
  const parentLocale = useContext(LocaleContext).locale
  const parentLocaleRef = useRef(parentLocale)
  useEffect(() => {parentLocaleRef.current = parentLocale}, [parentLocale])
  const [locale, updateLocale] = useState<Locale>(() => {
    if (storageKey === 'studio.locale') return getActiveLocale()
    try { return resolveLocale(localStorage.getItem(storageKey) ?? parentLocale, navigator.languages) } catch { return parentLocale }
  })
  const [requestedLocale, requestLocale] = useState(locale)
  const [ready, setReady] = useState(() => isLocaleLoaded(locale))
  const [loading, setLoading] = useState(!ready)
  const [failed, setFailed] = useState(false)
  const [revision, setRevision] = useState(0)
  const [localeSaved, setLocaleSaved] = useState(true)
  // Synchronous helpers must see the committed locale before children render.
  setActiveLocale(locale)
  useEffect(() => {
    let cancelled = false
    void loadLocale(requestedLocale).then(() => {
      if (cancelled) return
      setActiveLocale(requestedLocale); updateLocale(requestedLocale); setReady(true); setLoading(false)
      try {localStorage.setItem(storageKey, requestedLocale); setLocaleSaved(true)} catch {setLocaleSaved(false)}
    }).catch(() => {if (!cancelled) {setLoading(false); setFailed(true)}})
    return () => {cancelled = true}
  }, [requestedLocale, storageKey, revision])
  useEffect(() => {
    document.documentElement.lang = locale
  }, [locale])
  // Restore the enclosing provider only when this provider leaves the tree.
  // Cleanup on every locale change would overwrite the newly committed locale
  // used by synchronous status/date helpers after children have rendered.
  useEffect(() => () => {
    setActiveLocale(parentLocaleRef.current)
    document.documentElement.lang = parentLocaleRef.current
  }, [])
  function setLocale(value: Locale) {setLoading(!isLocaleLoaded(value)); setFailed(false); requestLocale(value); setRevision(previous => previous + 1)}
  return <LocaleContext.Provider value={{locale, requestedLocale, setLocale, localeSaved}}>
    {ready && children}
    {(loading || failed) && <div className="locale-feedback" lang="en" role={failed ? 'alert' : 'status'}>
      <span>{failed ? ready ? 'English could not be loaded. Your current page is preserved. Save your work, then reload to try English again.' : 'English could not be loaded. Please retry.' : 'Loading English…'}</span>
      {/* Some browsers retain a failed module graph. A fresh document retries
          it reliably; only offer this before any editable children mount. */}
      {failed && !ready && <button type="button" onClick={() => window.location.reload()}>Retry English</button>}
      <button type="button" lang="zh-CN" onClick={() => setLocale('zh-CN')}>简体中文</button>
    </div>}
  </LocaleContext.Provider>
}

export function LanguagePicker() {
  const {requestedLocale, setLocale, t} = useI18n()
  return <label className="language-picker">{t('语言')}<select aria-label={t('界面语言')} value={requestedLocale} onChange={event => setLocale(event.target.value as Locale)}><option value="zh-CN">简体中文</option><option value="en">English</option></select></label>
}

export function LanguageButtons() {
  const {requestedLocale, setLocale, t} = useI18n()
  return <div className="language-buttons" role="group" aria-label={t('界面语言')}><button type="button" lang="zh-CN" aria-label="简体中文" aria-pressed={requestedLocale === 'zh-CN'} onClick={() => setLocale('zh-CN')}>{'中'}</button><span aria-hidden="true">/</span><button type="button" lang="en" aria-label="English" aria-pressed={requestedLocale === 'en'} onClick={() => setLocale('en')}>en</button></div>
}
