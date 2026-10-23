import { translate as t, useI18n } from "../i18n"
import { useEffect, useState, type ReactNode } from 'react'
import { LanguagePicker } from './LanguagePicker'
import { errorMessage, request, setCSRFToken, type Space } from '../api'

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
  useEffect(() => {
    const controller = new AbortController()
    void request<Session>('/auth/session', {signal: controller.signal}).then(value => {if (!controller.signal.aborted) {setCSRFToken(value.csrf_token ?? ''); setSession(value); setError('')}}).catch(reason => {if (!controller.signal.aborted) setError(errorMessage(reason, t("无法读取登录状态")))})
    function expired() {setCSRFToken(''); setSession(previous => previous ? {...previous, authenticated: false, user: undefined} : null); setError(t("登录已过期，请重新登录。未提交的草稿不会自动保存。"))}
    window.addEventListener('studio-session-expired', expired)
    return () => {controller.abort(); window.removeEventListener('studio-session-expired', expired)}
  }, [revision])
  async function authenticate() {
    if (busy) return
    setBusy(true); setError('')
    try {const value = await request<Session>(register ? '/auth/register' : '/auth/login', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({email, password})}); setCSRFToken(value.csrf_token ?? ''); setSession(value); setPassword('')} catch (reason) {setError(errorMessage(reason, t("登录失败")))} finally {setBusy(false)}
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
  return <main className="auth-screen"><section className="panel auth-panel"><img src="/brand/lockup-dark.svg" width="158" height="48" alt={t("映创 Creative Studio")} /><LanguagePicker /><h1>{register ? t("创建映创账户") : t("登录映创")}</h1><p>{t("登录后访问你的组织与创作记录。新账户不会继承演示空间的数据。")}</p>{error && <p role="alert" className="error">{error}</p>}{!session ? <button onClick={() => setRevision(value => value + 1)}>{t("重新读取登录状态")}</button> : <form onSubmit={event => {event.preventDefault(); void authenticate()}}><fieldset disabled={busy}><label htmlFor="auth-email">{t("邮箱")}</label><input id="auth-email" type="email" autoComplete="username" required maxLength={254} value={email} onChange={event => setEmail(event.target.value)} /><label htmlFor="auth-password">{t("密码")}</label><input id="auth-password" type="password" autoComplete={register ? 'new-password' : 'current-password'} required minLength={register ? 12 : undefined} maxLength={128} value={password} onChange={event => setPassword(event.target.value)} />{register && <p className="drama-help">{t("密码至少12字节，最多128字节。请使用独立的长密码。")}</p>}<button className="primary" type="submit">{busy ? t("处理中…") : register ? t("注册并登录") : t("登录")}</button>{session.registration_enabled && <button type="button" onClick={() => {setRegister(value => !value); setError(''); setPassword('')}}>{register ? t("已有账户，前往登录") : t("创建新账户")}</button>}{session.sso_available && <button type="button" onClick={() => void startSSO()}>{t('企业SSO登录')}</button>}</fieldset></form>}</section></main>
}
