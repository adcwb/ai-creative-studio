import { translate as t, useI18n } from "../i18n"
import { useEffect, useState, type ReactNode } from 'react'
import { LanguagePicker } from './LanguagePicker'
import { errorMessage, request, setCSRFToken, type Space } from '../api'
import { AuthBrand } from './AuthBrand'
import './auth.css'

export interface Session { mode: 'demo' | 'secure'; authenticated: boolean; registration_enabled: boolean; sso_available: boolean; user?: {id: string; email: string}; csrf_token?: string; spaces?: Space[] }
export function AuthGate({children}: {children: (session: Session, logout: () => void) => ReactNode}) {
  useI18n()

  const [session, setSession] = useState<Session | null>(null)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [register, setRegister] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [showPassword, setShowPassword] = useState(false)
  const [loadingSession, setLoadingSession] = useState(true)
  useEffect(() => {
    const controller = new AbortController()
    void request<Session>('/auth/session', {signal: controller.signal}).then(value => {if (!controller.signal.aborted) {setCSRFToken(value.csrf_token ?? ''); setSession(value); setError('')}}).catch(reason => {if (!controller.signal.aborted) setError(errorMessage(reason, t("无法读取登录状态")))}).finally(() => {if (!controller.signal.aborted) setLoadingSession(false)})
    function expired() {setCSRFToken(''); setSession(previous => previous ? {...previous, authenticated: false, user: undefined} : null); setError(t("登录已过期，请重新登录。未提交的草稿不会自动保存。"))}
    window.addEventListener('studio-session-expired', expired)
    return () => {controller.abort(); window.removeEventListener('studio-session-expired', expired)}
  }, [revision])
  async function authenticate() {
    if (busy) return
    setBusy(true); setError('')
    try {const value = await request<Session>(register ? '/auth/register' : '/auth/login', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({email, password})}); setCSRFToken(value.csrf_token ?? ''); setSession(value); setPassword(''); setShowPassword(false)} catch (reason) {setError(errorMessage(reason, register ? t('注册失败，请重试') : t("登录失败")))} finally {setBusy(false)}
  }
  async function startSSO() {
    if (busy) return
    setBusy(true); setError('')
    try {const result = await request<{authorization_url: string}>('/auth/sso/start', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: '{}'}); window.location.assign(result.authorization_url)} catch (reason) {setError(errorMessage(reason, t('企业登录暂不可用'))); setBusy(false)}
  }
  async function logout() {
    try {await request('/auth/logout', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: '{}'}); setCSRFToken(''); setSession(previous => previous ? {...previous, authenticated: false, user: undefined} : null)} catch (reason) {setError(errorMessage(reason, t("退出失败，请重试")))}
  }
  if (session?.mode === 'demo' || session?.authenticated) return <>{error && <p role="alert" className="error">{error}</p>}{children(session, () => {void logout()})}</>
  return <main className="auth-screen">
    <AuthBrand />
    <section className="auth-content" aria-labelledby="auth-title">
      <header className="auth-toolbar"><LanguagePicker /></header>
      <div className="auth-panel">
        <div className="auth-heading">
          <span className="auth-heading-mark" aria-hidden="true"><img src="/brand/mark.svg" width="40" height="40" alt="" /></span>
          <h1 id="auth-title">{register ? t('创建映创账户') : t('登录映创')}</h1>
          <p>{register ? t('开启你的创作空间，让第一个想法在这里生长。') : t('欢迎回来，继续你的下一帧创作。')}</p>
        </div>
        {error && <p role="alert" className="auth-error">{error}</p>}
        {!session ? <div className="auth-session">
          {loadingSession ? <p role="status"><span className="auth-spinner" aria-hidden="true" />{t('正在准备你的工作室…')}</p> : <button className="auth-submit" onClick={() => {setLoadingSession(true); setError(''); setRevision(value => value + 1)}}>{t('重新读取登录状态')}</button>}
        </div> : <form onSubmit={event => {event.preventDefault(); void authenticate()}} aria-busy={busy}>
          <fieldset disabled={busy}>
            <div className="auth-field">
              <label htmlFor="auth-email">{t('邮箱')}</label>
              <input id="auth-email" name="email" type="email" autoComplete="username" placeholder={t('输入你的邮箱地址')} required maxLength={254} autoCapitalize="none" spellCheck={false} value={email} onChange={event => setEmail(event.target.value)} />
            </div>
            <div className="auth-field">
              <label htmlFor="auth-password">{t('密码')}</label>
              <div className="auth-password">
                <input id="auth-password" name="password" type={showPassword ? 'text' : 'password'} autoComplete={register ? 'new-password' : 'current-password'} placeholder={register ? t('设置你的登录密码') : t('输入你的密码')} required minLength={register ? 12 : undefined} maxLength={128} aria-describedby={register ? 'auth-password-help' : undefined} value={password} onChange={event => setPassword(event.target.value)} />
                <button className="auth-password-toggle" type="button" aria-label={t('显示密码')} aria-pressed={showPassword} aria-controls="auth-password" onClick={() => setShowPassword(value => !value)}>
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z" /><circle cx="12" cy="12" r="3" />{showPassword && <path d="m3 3 18 18" />}</svg>
                </button>
              </div>
              {register && <p id="auth-password-help" className="auth-help">{t('密码至少12字节，最多128字节。请使用独立的长密码。')}</p>}
            </div>
            <button className="auth-submit" type="submit" aria-live="polite">{busy && <span className="auth-spinner" aria-hidden="true" />}<span>{busy ? t('处理中…') : register ? t('注册并登录') : t('登录')}</span></button>
            {session.sso_available && <div className="auth-sso"><div className="auth-divider"><span>{t('或使用企业账户')}</span></div><button type="button" onClick={() => void startSSO()}>{t('企业SSO登录')}</button></div>}
            {session.registration_enabled && <p className="auth-switch">{!register && <span>{t('第一次来到映创？')}</span>}<button type="button" onClick={() => {setRegister(value => !value); setError(''); setPassword(''); setShowPassword(false)}}>{register ? t('已有账户，前往登录') : t('创建新账户')}</button></p>}
          </fieldset>
        </form>}
        <p className="auth-footnote">{register ? t('新账户将拥有独立工作空间，不包含演示数据。') : t('你的灵感、作品与团队，在这里相聚。')}</p>
      </div>
      <footer className="auth-footer">{t('映创 · AI 创作工作室')}</footer>
    </section>
  </main>
}
