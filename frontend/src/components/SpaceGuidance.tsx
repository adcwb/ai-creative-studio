import {useState} from 'react'
import {Link} from 'react-router-dom'
import {useAppWorkspace} from '../app/workspace'
import {useI18n} from '../i18n'

export function SpaceGuidance() {
  const {space, spaces, switchSpace, session} = useAppWorkspace()
  const {t} = useI18n()
  const [selected, setSelected] = useState('')
  const teams = spaces.filter(item => item.type === 'team' && item.id !== space?.id)
  const target = teams.find(item => item.id === selected) ?? teams[0]
  if (session.mode !== 'secure' || space?.type !== 'personal' || !target) return null
  return <section className="space-guidance" aria-label={t('工作空间提示')}>
    <div><h2>{t('团队作品在另一个空间？')}</h2><p>{t('这里是你的个人空间。已有团队的作品和模型配置仍保留在团队空间，切换即可继续使用。')}</p></div>
    <div className="space-guidance-actions"><label>{t('已有团队空间')}<select value={target.id} onChange={event => setSelected(event.target.value)}>{teams.map(team => <option key={team.id} value={team.id}>{team.name}</option>)}</select></label><button type="button" onClick={() => switchSpace(target.id)}>{t('进入团队空间')}</button><Link to="/settings/spaces">{t('查看全部空间')}</Link></div>
  </section>
}
