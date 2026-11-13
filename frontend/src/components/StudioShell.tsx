import {hasPermission} from '../permissions'
import {gatewayCreationRequired} from '../creationGateway'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import type { Capabilities, Space } from '../api'
import { useI18n } from '../i18n'
import { capabilityLabel } from '../providerPresentation'
import { usePreferences } from './UserPreferences'
import { workspaceTitle } from '../workspaceTitle'
import type { Session } from './AuthGate'
import { Icon } from './Icon'

export function StudioShell({spaces, space, capability, capabilityError, onSpaceChange, children, session, onLogout}: {
  session: Session; onLogout: () => void; spaces: Space[]; space: string
  capability: Capabilities | null; capabilityError: string; onSpaceChange: (space: string) => void; children: ReactNode
}) {
  const {t} = useI18n()
  const location = useLocation()
  const gatewayPage = gatewayCreationRequired && (location.pathname.startsWith('/studio') || location.pathname.startsWith('/projects') || location.pathname.startsWith('/series/'))
  const showLegacyStatus = !gatewayCreationRequired && session.mode === 'demo'
  const {navigation, setNavigation} = usePreferences()
  const collapsed = navigation.collapsed
  const groupOpen = (key: string) => collapsed || (navigation.groups[key] ?? (key === activeGroup || ['creative','organization','system'].includes(key)))
  const toggleGroup = (key: string) => setNavigation({...navigation, groups: {...navigation.groups, [key]: !groupOpen(key)}})
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLButtonElement>(null)
  const sidebarRef = useRef<HTMLElement>(null)
  const activeSpace = spaces.find(item => item.id === space)
  const canManage = hasPermission(activeSpace, 'models.manage')
  const roles: Record<string, string> = {owner: '所有者', admin: '管理员', editor: '编辑者', viewer: '只读成员'}
  const title = workspaceTitle(location.pathname)
  useEffect(() => {
    if (!open) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    sidebarRef.current?.querySelector<HTMLElement>('a, button')?.focus()
    function keydown(event: KeyboardEvent) {
      if (event.key === 'Escape') {setOpen(false); menuRef.current?.focus()}
      if (event.key !== 'Tab') return
      const items = [...(sidebarRef.current?.querySelectorAll<HTMLElement>('a[href], button, select') ?? [])].filter(item => item.getClientRects().length && !item.hasAttribute('disabled'))
      if (!items?.length) return
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) {event.preventDefault(); last.focus()}
      else if (!event.shiftKey && document.activeElement === last) {event.preventDefault(); first.focus()}
    }
    window.addEventListener('keydown', keydown)
    return () => {document.body.style.overflow = previousOverflow; window.removeEventListener('keydown', keydown)}
  }, [open])
  const navItem = (to: string, label: string, icon: Parameters<typeof Icon>[0]['name']) => <NavLink className={({isActive})=>isActive||(to==='/projects'&&location.pathname.startsWith('/series/'))?'active':undefined} to={to} title={t(label)} aria-label={t(label)} onClick={() => setOpen(false)}><Icon name={icon} /><span>{t(label)}</span></NavLink>
  const activeGroup = location.pathname.startsWith('/digital-humans') ? 'avatars' : ['/assets','/library'].includes(location.pathname) ? 'resources' : ['/settings/preferences','/settings/backend'].includes(location.pathname) ? 'system' : location.pathname.startsWith('/settings') ? 'organization' : 'creative'
  const group = (key: string, label: string, icon: Parameters<typeof Icon>[0]['name'], content: ReactNode) => <section className="navigation-group" data-group={key} data-active={activeGroup === key}><button type="button" className="nav-parent group-toggle" title={t(label)} aria-label={t(label)} aria-expanded={groupOpen(key)} aria-controls={`nav-group-${key}`} onClick={() => toggleGroup(key)}><Icon name={icon} /><span>{t(label)}</span><span className="group-chevron" aria-hidden="true">{!groupOpen(key) ? '›' : '⌄'}</span></button><div id={`nav-group-${key}`} className="nav-subgroup" hidden={!groupOpen(key)}>{content}</div></section>
  return <div className={`shell app-shell ${collapsed ? 'navigation-collapsed' : ''}`}>
    <a className="skip-link" href="#page-content">{t('跳到创作工作区')}</a>
    {open && <button type="button" className="sidebar-backdrop" aria-label={t('关闭导航')} onClick={() => {setOpen(false); menuRef.current?.focus()}} />}
    <aside ref={sidebarRef} id="app-navigation" className={`sidebar ${open ? 'is-open' : ''}`} aria-label={t('主导航')} role={open ? 'dialog' : undefined} aria-modal={open ? true : undefined}>
      <div className="sidebar-inner"><div className="sidebar-header">
        <div className="sidebar-brand"><Link className="brand" to="/studio" onClick={() => setOpen(false)} aria-label={t('映创工作室首页')}><img className="brand-lockup" src="/brand/lockup-dark.svg" alt={t('映创 Creative Studio')} width="158" height="48" /><img className="brand-symbol" src="/brand/mark.svg" width="32" height="32" alt="" /></Link><button type="button" className="sidebar-close" aria-label={t('关闭导航')} onClick={() => {setOpen(false); menuRef.current?.focus()}}><Icon name="close" /></button></div>
        <button type="button" className="sidebar-collapse" aria-label={t(collapsed ? '展开侧栏' : '收起侧栏')} aria-expanded={!collapsed} onClick={() => setNavigation({...navigation, collapsed: !collapsed})}><Icon name="menu" /><span>{t('收起侧栏')}</span></button>
        <div className="space-picker"><label htmlFor="space">{t('工作空间')}</label><select id="space" aria-label={session.mode === 'demo' ? t('选择演示空间') : t('选择组织')} value={space} disabled={!spaces.length} onChange={event => {setOpen(false); onSpaceChange(event.target.value)}}>{spaces.length ? spaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">{t('等待空间数据')}</option>}</select></div>
        </div><div className="sidebar-navigation">
        <nav className="app-nav" aria-label={t('创作导航')}>
          {group('creative', '创作空间', 'clapper', <>{navItem('/studio', '图像创作', 'image')}{navItem('/projects', '短剧创作', 'clapper')}{navItem('/workflows','创作流程','layers')}{navItem('/publish', '发布中心', 'publish')}</>)}
          {group('avatars', '数字人', 'person', <>{navItem('/digital-humans/video', '视频型数字人', 'videoPerson')}{navItem('/digital-humans/interactive', '交互型数字人', 'conversation')}{navItem('/digital-humans/3d', '3D 数字人', 'cube')}</>)}
          {group('resources', '资源中心', 'folder', <>{navItem('/assets', '素材文件', 'grid')}{navItem('/library', '创作资产', 'layers')}</>)}
        </nav>
        {session.mode === 'secure' && <nav className="app-nav settings-nav" aria-label={t('管理导航')}>{group('organization', '空间与协作', 'users', <>{navItem('/settings/spaces', '团队空间', 'folder')}{navItem('/settings/organization', '组织与成员', 'users')}{canManage && navItem('/settings/models', '模型管理', 'settings')}{navItem('/settings/billing', '模型使用统计', 'wallet')}</>)}</nav>}
        <nav className="app-nav personal-nav" aria-label={t('个人设置')}>{group('system', '系统设置', 'settings', <>{navItem('/settings/preferences', '偏好设置', 'settings')}{session.mode === 'secure' && navItem('/settings/backend', '后端配置', 'settings')}</>)}</nav>
        </div><div className="sidebar-account"><Link className="sidebar-settings-shortcut" to="/settings/preferences" title={t('系统设置')} aria-label={t('系统设置')} onClick={() => setOpen(false)}><Icon name="settings" /><span>{t('系统设置')}</span></Link><div className="account-profile"><span className="avatar">{session.user?.email?.slice(0, 1).toUpperCase() ?? t('映')}</span><div><strong title={session.user?.email}>{session.mode === 'demo' ? t('演示工作空间') : session.user?.email}</strong><small>{session.mode === 'demo' ? t('模拟模式') : t(roles[activeSpace?.role ?? ''] ?? '未选择')}</small></div></div>{session.mode === 'secure' ? <button type="button" className="account-signout" title={t('退出登录')} onClick={onLogout}>{t('退出登录')}</button> : <p className="demo-description">{t('无生产认证 · 空间不构成权限隔离')}</p>}</div>
      </div>
    </aside>
    <main id="workspace"><header className="topbar workspace-page-header" aria-label={t('页面信息')}><div className="topbar-location"><button ref={menuRef} type="button" className="menu-toggle" aria-label={t('打开导航')} aria-controls="app-navigation" aria-expanded={open} onClick={() => setOpen(true)}><Icon name="menu" /></button><div className="workspace-page-context"><strong>{t(title)}</strong><span>{activeSpace?.name ?? t('没有可用组织')} · {session.mode === 'demo' ? t('模拟模式') : t(roles[activeSpace?.role ?? ''] ?? '未选择')}</span></div></div><span className={`provider-badge ${showLegacyStatus && !capability?.ready ? 'provider-unavailable' : ''}`}>{showLegacyStatus && <span />}{gatewayPage ? t('网关模型创作') : showLegacyStatus ? capabilityLabel(capability, capabilityError) : t('当前工作空间')}</span></header><div id="page-content" tabIndex={-1} className="page-content">{children}</div></main>
  </div>
}
