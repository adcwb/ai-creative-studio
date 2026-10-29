import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import type { Capabilities, Space } from '../api'
import { useI18n } from '../i18n'
import { capabilityLabel } from '../providerPresentation'
import { LanguagePicker } from './LanguagePicker'
import type { Session } from './AuthGate'
import { Icon } from './Icon'

export function StudioShell({spaces, space, capability, capabilityError, onSpaceChange, children, session, onLogout}: {
  session: Session; onLogout: () => void; spaces: Space[]; space: string
  capability: Capabilities | null; capabilityError: string; onSpaceChange: (space: string) => void; children: ReactNode
}) {
  const {t} = useI18n()
  const location = useLocation()
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLButtonElement>(null)
  const sidebarRef = useRef<HTMLElement>(null)
  const activeSpace = spaces.find(item => item.id === space)
  const canManage = activeSpace?.role === 'owner' || activeSpace?.role === 'admin'
  const roles: Record<string, string> = {owner: '所有者', admin: '管理员', editor: '编辑者', viewer: '只读成员'}
  const title = location.pathname.startsWith('/settings') ? '工作空间设置' : location.pathname.startsWith('/projects') ? '短剧工作台' : '视觉工作室'
  useEffect(() => {
    if (!open) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    sidebarRef.current?.querySelector<HTMLElement>('a, button')?.focus()
    function keydown(event: KeyboardEvent) {
      if (event.key === 'Escape') {setOpen(false); menuRef.current?.focus()}
      if (event.key !== 'Tab') return
      const items = sidebarRef.current?.querySelectorAll<HTMLElement>('a[href], button, select')
      if (!items?.length) return
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) {event.preventDefault(); last.focus()}
      else if (!event.shiftKey && document.activeElement === last) {event.preventDefault(); first.focus()}
    }
    window.addEventListener('keydown', keydown)
    return () => {document.body.style.overflow = previousOverflow; window.removeEventListener('keydown', keydown)}
  }, [open])
  const navItem = (to: string, label: string, icon: Parameters<typeof Icon>[0]['name']) => <NavLink to={to} onClick={() => setOpen(false)}><Icon name={icon} /><span>{t(label)}</span></NavLink>
  return <div className="shell app-shell">
    <a className="skip-link" href="#page-content">{t('跳到创作工作区')}</a>
    {open && <button type="button" className="sidebar-backdrop" aria-label={t('关闭导航')} onClick={() => {setOpen(false); menuRef.current?.focus()}} />}
    <aside ref={sidebarRef} id="app-navigation" className={`sidebar ${open ? 'is-open' : ''}`} aria-label={t('主导航')} role={open ? 'dialog' : undefined} aria-modal={open ? true : undefined}>
      <div className="sidebar-inner">
        <div className="sidebar-brand"><Link className="brand" to="/studio" onClick={() => setOpen(false)} aria-label={t('映创工作室首页')}><img src="/brand/lockup-dark.svg" alt={t('映创 Creative Studio')} width="158" height="48" /></Link><button type="button" className="sidebar-close" aria-label={t('关闭导航')} onClick={() => {setOpen(false); menuRef.current?.focus()}}><Icon name="close" /></button></div>
        <div className="space-picker"><label htmlFor="space">{t('工作空间')}</label><select id="space" aria-label={session.mode === 'demo' ? t('选择演示空间') : t('选择组织')} value={space} disabled={!spaces.length} onChange={event => {setOpen(false); onSpaceChange(event.target.value)}}>{spaces.length ? spaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">{t('等待空间数据')}</option>}</select></div>
        <nav className="app-nav" aria-label={t('创作导航')}><span className="nav-group-label">{t('创作空间')}</span>{navItem('/studio', '图像创作', 'image')}{navItem('/projects', '短剧创作', 'script')}<span className="nav-parent">{t('数字人')}</span><div className="nav-subgroup">{navItem('/digital-humans/video', '视频型数字人', 'users')}{navItem('/digital-humans/interactive', '交互型数字人', 'users')}{navItem('/digital-humans/3d', '3D 数字人', 'users')}</div><span className="nav-parent">{t('资源中心')}</span><div className="nav-subgroup">{navItem('/assets', '素材文件', 'grid')}{navItem('/library', '创作资产', 'users')}</div>{navItem('/publish', '发布中心', 'script')}</nav>
        {session.mode === 'secure' && <nav className="app-nav settings-nav" aria-label={t('管理导航')}><span className="nav-group-label">{t('组织与设置')}</span>{navItem('/settings/organization', '组织与成员', 'users')}{canManage && navItem('/settings/models', '模型接入', 'settings')}{navItem('/settings/billing', '预算与费用', 'wallet')}</nav>}
        <div className="sidebar-account"><LanguagePicker /><div className="account-profile"><span className="avatar">{session.user?.email?.slice(0, 1).toUpperCase() ?? t('映')}</span><div><strong title={session.user?.email}>{session.mode === 'demo' ? t('演示工作空间') : session.user?.email}</strong><small>{session.mode === 'demo' ? t('模拟模式') : t(roles[activeSpace?.role ?? ''] ?? '未选择')}</small></div></div>{session.mode === 'secure' ? <button type="button" className="account-signout" onClick={onLogout}>{t('退出登录')}</button> : <p className="demo-description">{t('无生产认证 · 空间不构成权限隔离')}</p>}</div>
      </div>
    </aside>
    <main id="workspace"><header className="topbar"><div className="topbar-location"><button ref={menuRef} type="button" className="menu-toggle" aria-label={t('打开导航')} aria-controls="app-navigation" aria-expanded={open} onClick={() => setOpen(true)}><Icon name="menu" /></button><span>{t(title)}</span><span className="topbar-space">/ {activeSpace?.name ?? t('工作空间')}</span></div><span className={`provider-badge ${!capability?.ready ? 'provider-unavailable' : ''}`}><span />{capabilityLabel(capability, capabilityError)}</span></header><div id="page-content" tabIndex={-1} className="page-content">{children}</div></main>
  </div>
}
