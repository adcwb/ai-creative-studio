import { translate as t, useI18n } from "../i18n"
import type { ReactNode } from 'react'
import type { Capabilities, Space } from '../api'
import { capabilityLabel } from '../providerPresentation'
import { LanguagePicker } from './LanguagePicker'
import type { Session } from './AuthGate'
import { Icon } from './Icon'

export function StudioShell({spaces, space, workbench, onWorkbenchChange, capability, capabilityError, onSpaceChange, children, session, onLogout}: {session: Session; onLogout: () => void; spaces: Space[]; space: string; workbench: 'image' | 'drama'; onWorkbenchChange: (workbench: 'image' | 'drama') => void; capability: Capabilities | null; capabilityError: string; onSpaceChange: (space: string) => void; children: ReactNode}) {
  useI18n()

  return <div className="shell">
    <a className="skip-link" href="#workspace">{t("跳到创作工作区")}</a>
    <aside className="sidebar">
      <div className="sidebar-inner">
      <a className="brand" href="/" aria-label={t("映创工作室首页")} onClick={event => {event.preventDefault(); onWorkbenchChange('image')}}><img src="/brand/lockup-dark.svg" alt={t("映创 Creative Studio")} width="158" height="48" /></a>
      <div className="space-picker"><label htmlFor="space">{t("工作空间")}</label><select id="space" aria-label={session.mode === 'demo' ? t("选择演示空间") : t("选择组织")} value={space} disabled={!spaces.length} onChange={event => onSpaceChange(event.target.value)}>{spaces.length ? spaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">{t("等待空间数据")}</option>}</select></div>
      <LanguagePicker /><div className="sidebar-current"><Icon name={workbench === 'image' ? 'image' : 'script'} /><span>{workbench === 'image' ? t("视觉工作室") : t("短剧工作台")}</span><span className="current-dot" /></div>
      <div className="sidebar-note">{t("让创意，")}<br />{t("从一张图开始。")}</div>
      <div className="roadmap"><span className="eyebrow">WORDS INTO STORIES</span><strong>{t("短剧工作台已开放")}</strong><p>{t("简报 · 分镜 · 本地成片")}</p></div>
      <div className="demo-id"><span className="avatar">{t("映")}</span><div>{session.mode === 'demo' ? <>{t("演示身份 · demo-user")}<small>{t("无生产认证 · 空间不构成权限隔离")}</small></> : <>{session.user?.email}<small>{t("组织角色：")}{spaces.find(item => item.id === space)?.role ?? t("未选择")}</small><button onClick={onLogout}>{t("退出登录")}</button></>}</div></div>
      </div>
    </aside>
    <main id="workspace"><header className="topbar"><nav className="workbench-nav" aria-label={t("选择工作台")}><button type="button" aria-current={workbench === 'image' ? 'page' : undefined} onClick={() => onWorkbenchChange('image')}>{t("图像工作室")}</button><span aria-hidden="true">/</span><button type="button" aria-current={workbench === 'drama' ? 'page' : undefined} onClick={() => onWorkbenchChange('drama')}>{t("短剧工作台")}</button></nav>{session.mode === 'secure' && <button className="mobile-signout" onClick={onLogout}>{t('退出登录')}</button>}<span className={`provider-badge ${workbench === 'image' && !capability?.ready ? 'provider-unavailable' : ''}`}><span />{workbench === 'drama' ? t("短剧创作 · 本地成片") : capabilityLabel(capability, capabilityError)}</span></header>{children}<footer><span>{t("映创 · 为每一次灵感留白")}</span><span>{t("创作记录 / 持久保存")}</span></footer></main>
  </div>
}
