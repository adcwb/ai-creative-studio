import { Outlet, useLocation } from 'react-router-dom'
import { useAppWorkspace } from '../../app/workspace'
import { Icon } from '../../components/Icon'
import { TaskDetail } from '../../components/TaskDetail'
import { useI18n } from '../../i18n'
import { useImageController } from './useImageWorkspace'

export function ImageWorkspace() {
  const workspace = useAppWorkspace()
  const location = useLocation()
  const image = useImageController()
  const {t} = useI18n()
  const {studio, detailId, space, savedIds, editTask, setDetailId} = image
  const active = location.pathname === '/studio' || location.pathname === '/assets'
  return <>
    {active && studio.connectionError && <div role="alert" className="error">{t('无法同步服务：')}{studio.connectionError} <button onClick={studio.reconnect}>{t('重新连接')}</button></div>}
    {active && studio.actionError && !detailId && <div role="alert" className="error">{studio.actionError}</div>}
    {active && studio.notice && !detailId && <p role="status" className="notice"><Icon name="check" />{studio.notice}</p>}
    <Outlet context={{...workspace, image}} />
    {active && detailId && <TaskDetail key={detailId} id={detailId} space={space} catalog={studio.catalog} saved={savedIds.has(detailId)} saving={studio.saving} busy={studio.busy} actionError={studio.actionError} notice={studio.notice} onClose={() => setDetailId(null)} onUpdate={studio.updateTask} onSave={task => {void studio.save(task)}} onEdit={editTask} onResume={studio.resume} />}
  </>
}
