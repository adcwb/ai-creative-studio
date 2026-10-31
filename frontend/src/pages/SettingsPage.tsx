import { useParams } from 'react-router-dom'
import { useAppWorkspace } from '../app/workspace'
import { useI18n } from '../i18n'
import { OrganizationPanel } from '../components/OrganizationPanel'
import { ModelManagementPanel } from '../components/ModelManagementPanel'
import { ModelUsagePanel } from '../components/ModelUsagePanel'
import { NotFoundPage } from './NotFoundPage'

const descriptions: Record<string, [string, string]> = {
  organization: ['组织与成员', '管理工作空间与成员的访问权限。'],
  models: ['模型管理', '管理 AI 网关、网关模型以及每个创作功能的默认绑定。'],
  billing: ['模型使用统计', '查看网关与模型的调用次数、成功率、耗时和使用量。'],
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
      : space ? section === 'models' ? <ModelManagementPanel space={space} onDirtyChange={setDirty} /> : <ModelUsagePanel space={space} onDirtyChange={setDirty} /> : null}
  </div>
}
