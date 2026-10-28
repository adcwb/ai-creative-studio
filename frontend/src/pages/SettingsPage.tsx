import { useParams } from 'react-router-dom'
import { useAppWorkspace } from '../app/workspace'
import { useI18n } from '../i18n'
import { OrganizationPanel } from '../components/OrganizationPanel'
import { ModelConfigPanel } from '../components/ModelConfigPanel'
import { GovernancePanel } from '../components/GovernancePanel'
import { NotFoundPage } from './NotFoundPage'

const descriptions: Record<string, [string, string]> = {
  organization: ['组织与成员', '管理工作空间与成员的访问权限。'],
  models: ['模型接入管理', '配置组织可用的模型与凭据，创作时选择使用。'],
  billing: ['预算与费用记录', '查看各币种预算、费用流水与变更记录。'],
}
export function SettingsPage() {
  const {section = ''} = useParams()
  const {session, space, refreshSpaces, setDirty} = useAppWorkspace()
  const {t} = useI18n()
  const description = descriptions[section]
  if (!description) return <NotFoundPage />
  const canManage = space?.role === 'owner' || space?.role === 'admin'
  const denied = session.mode !== 'secure' || (section === 'models' && !canManage)
  return <div className="settings-page">
    <header className="page-heading"><div><h1>{t(description[0])}</h1><p>{t(description[1])}</p></div></header>
    {denied ? <section className="panel page-empty"><h2>{t('当前账户无法访问此页面')}</h2><p>{t(session.mode === 'demo' ? '演示模式不提供组织管理。请使用安全模式登录。' : '模型接入需要组织所有者或管理员权限。')}</p></section>
      : section === 'organization' ? <OrganizationPanel userID={session.user?.id} space={space} onChange={refreshSpaces} onDirtyChange={setDirty} />
      : space ? section === 'models' ? <ModelConfigPanel space={space} onDirtyChange={setDirty} /> : <GovernancePanel space={space} onDirtyChange={setDirty} /> : null}
  </div>
}
