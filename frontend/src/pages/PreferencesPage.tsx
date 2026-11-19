import { useI18n } from '../i18n'
import { usePreferences } from '../components/UserPreferences'
import { LanguageButtons } from '../components/LanguagePicker'
import { useAppWorkspace } from '../app/workspace'
import {hasPermission} from '../permissions'
import {Link} from 'react-router-dom'
import { themes, type Theme } from '../preferences'
import { palettes } from '../ui/palettes'
import type { CSSProperties } from 'react'
const names: Record<Theme, [string, string]> = {dark: ['暗房', '墨绿画布与深绿主色，让画面成为主角。'], light: ['工作室', '暖白与深绿，适合明亮环境下长时间阅读。']}
// Each preview paints in its own palette, not the active one, so both options
// stay legible whichever theme is currently applied. Values come from the same
// single colour source as the rest of the product.
function sampleStyle(value: Theme): CSSProperties {
  const color = palettes[value]
  return {'--sample-canvas': color.canvas, '--sample-sidebar': color.forest, '--sample-rail': color.rail, '--sample-block': color.surfaceSoft, '--sample-border': color.controlBorder} as CSSProperties
}
export function PreferencesPage() {
  const {session, space} = useAppWorkspace()
  const canManage = session.mode === 'secure' && hasPermission(space,'models.manage')
  const {t, localeSaved} = useI18n()
  const {theme, setTheme, saved} = usePreferences()
  return <div className="preferences-page"><header className="page-heading"><div><span className="eyebrow">{t('你的工作室，你的节奏')}</span><h1>{t('系统设置')}</h1><p>{t('调整个人偏好，让创作环境更贴近你的习惯。')}</p></div></header>
    <nav className="settings-shortcuts" aria-label={t('设置入口')}>{canManage && <Link className="button-link" to="/settings/models">{t('管理当前空间的模型')}</Link>}<Link className="button-link" to="/settings/spaces">{t('管理团队空间')}</Link><Link className="button-link" to="/settings/backend">{t('打开后端配置')}</Link></nav>
    <section className="preference-section"><div><span className="preference-number">01</span><h2>{t('界面语言')}</h2><p>{t('语言与币种独立，不会修改项目内容。')}</p></div><LanguageButtons /></section>
    <section className="preference-section appearance-section"><div><span className="preference-number">02</span><h2>{t('工作室外观')}</h2><p>{t('在不同光线与创作状态下，选择适合你的工作台。')}</p></div><div className="theme-options" role="group" aria-label={t('选择页面主题')}>{themes.map(value => <button key={value} className="theme-option" data-appearance={value} type="button" aria-pressed={theme === value} onClick={() => setTheme(value)}><span className="theme-sample" style={sampleStyle(value)} aria-hidden="true"><i /><span><b /><em /><em /><em /></span></span><span className="theme-option-title">{t(names[value][0])}<span aria-hidden="true">{theme === value ? '✓' : '○'}</span></span><span className="theme-description">{t(names[value][1])}</span></button>)}</div></section>
    <section className="preference-section configuration-boundaries"><div><h2>{t('部署与安全边界')}</h2><p>{t('后端全局配置由部署管理员维护，变更后重启相关进程生效。主密钥与管理员权限仍由部署环境管理。')}</p></div><p>{t('优先级：本次明确选择 → 当前组织功能绑定。权限与能力匹配仍然生效。内容审核模式由部署配置决定，创作时会显示当前状态。')}</p></section>
    <p className="drama-help">Frontend revision: <code>{import.meta.env.VITE_BUILD_REVISION || 'unknown'}</code></p>
    <p className="preferences-storage" role="status" aria-label={t('偏好保存状态')}>{t(saved && localeSaved ? '偏好自动保存在当前浏览器，仅应用于当前账户；不会同步到其他设备。' : '浏览器无法保存偏好，本次选择仅在当前页面有效。')}</p>
    {session.mode === 'secure' && !session.deployment_admin && <p className="drama-help">{t('后端全局配置需要部署管理员权限，请在部署环境的 STUDIO_ADMIN_USER_IDS 中配置经核对的账号 ID后重启。')}</p>}
  </div>
}
