import { useI18n } from '../i18n'
import { usePreferences } from '../components/UserPreferences'
import { LanguageButtons } from '../components/LanguagePicker'
import { useState } from 'react'
import { useAppWorkspace } from '../app/workspace'
import { ModelManagementPanel } from '../components/ModelManagementPanel'
import { themes } from '../preferences'
const names = {forest: ['森林工作室', '鼠尾草绿与纸白，让灵感安静生长。'], paper: ['暖纸画廊', '暖灰、陶土与象牙白，像一本展开的作品集。'], screening: ['深色放映室', '石墨与银灰，减少明亮界面对画面的干扰。']}
export function PreferencesPage() {
  const {session, space, setDirty} = useAppWorkspace()
  const [manage, setManage] = useState(false)
  const canManage = session.mode === 'secure' && (space?.role === 'owner' || space?.role === 'admin')
  const {t, localeSaved} = useI18n()
  const {theme, setTheme, saved} = usePreferences()
  return <div className="preferences-page"><header className="page-heading"><div><span className="eyebrow">{t('你的工作室，你的节奏')}</span><h1>{t('系统设置')}</h1><p>{t('调整个人偏好，让创作环境更贴近你的习惯。')}</p></div></header>
    <section className="preference-section"><div><span className="preference-number">01</span><h2>{t('界面语言')}</h2><p>{t('语言与币种独立，不会修改项目内容。')}</p></div><LanguageButtons /></section>
    <section className="preference-section appearance-section"><div><span className="preference-number">02</span><h2>{t('工作室外观')}</h2><p>{t('在不同光线与创作状态下，选择适合你的工作台。')}</p></div><div className="theme-options" role="group" aria-label={t('选择页面主题')}>{themes.map(value => <button key={value} className="theme-option" type="button" aria-pressed={theme === value} onClick={() => setTheme(value)}><span className={`theme-sample sample-${value}`} aria-hidden="true"><i /><span><b /><em /><em /><em /></span></span><span className="theme-option-title">{t(names[value][0])}<span aria-hidden="true">{theme === value ? '✓' : '○'}</span></span><span className="theme-description">{t(names[value][1])}</span></button>)}</div></section>
    <section className="preference-section configuration-boundaries"><div><h2>{t('组织运行配置')}</h2><p>{t('服务地址、模型 ID、超时、输出上限与默认音色由组织管理员管理，保存后用于新任务，无需重启。')}</p></div>{canManage && space ? <div className="organization-settings"><button type="button" aria-expanded={manage} onClick={() => setManage(value => !value)}>{t('管理当前组织的模型网关')}</button><div hidden={!manage}><ModelManagementPanel space={space} onDirtyChange={setDirty} /></div></div> : <p>{t('此区域仅对当前组织所有者和管理员开放。')}</p>}</section>
    <section className="preference-section configuration-boundaries"><div><h2>{t('部署与安全边界')}</h2><p>{t('数据库、加密主密钥、SSO 信任、网络授权和服务监听由部署管理员配置，变更需重启相关进程。这里不读取或显示这些敏感值。')}</p></div><p>{t('优先级：本次明确选择 → 当前组织功能绑定。历史配置仅用于兼容读取，不会静默转换协议或替换供应商。预算与审核限制始终生效。')}</p></section>
    <p className="preferences-storage" role="status">{t(saved && localeSaved ? '偏好自动保存在当前浏览器，仅应用于当前账户；不会同步到其他设备。' : '浏览器无法保存偏好，本次选择仅在当前页面有效。')}</p>
  </div>
}
