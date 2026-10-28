import { Composer } from '../components/Composer'
import { TaskList } from '../components/TaskList'
import { TemplateGallery } from '../components/TemplateGallery'
import { ProviderBanner } from '../components/ProviderBanner'
import { RuntimeModelPicker } from '../components/RuntimeModelPicker'
import { useImageWorkspace } from '../features/images/useImageWorkspace'
import { useI18n } from '../i18n'

export function ImagePage() {
  const {t} = useI18n()
  const {space, studio, capability, capabilityError, refreshCapability, preview, runtimeChoice, setRuntimeChoice, effectiveCapability, draft, setDraft, editing, promptRef, savedIds, running, chooseTemplate, submitDraft, cancelEdit, startNew, openDetail, editTask} = useImageWorkspace()
  return <div className="image-page"><header className="page-heading"><div><h1>{t('图像创作')}</h1><p>{t('描述画面，调整风格，开始下一次创作。')}</p></div><div className="page-stat"><strong>{studio.tasks.length}</strong><span>{t('次创作探索')}{running > 0 && <small>{running} {t('个任务进行中')}</small>}</span></div></header>
    <ProviderBanner capability={capability} error={capabilityError} onRefresh={refreshCapability} />
    <TemplateGallery catalog={studio.catalog} error={studio.catalogError} disabled={studio.busy} onChoose={chooseTemplate} onRetry={studio.reconnect} />
    <div className="creation-grid"><div className="creation-controls"><RuntimeModelPicker space={space} capability="image" preview={preview} value={runtimeChoice} onChange={setRuntimeChoice} disabled={studio.busy} /><Composer catalog={studio.catalogError ? null : studio.catalog} capability={effectiveCapability} draft={draft} busy={studio.busy} retryTask={editing?.task ?? null} promptRef={promptRef} onChange={setDraft} onSubmit={() => {void submitDraft()}} onCancelEdit={cancelEdit} onNew={startNew} /></div><TaskList tasks={studio.tasks} catalog={studio.catalog} loading={studio.loading} error={studio.connectionError} savedIds={savedIds} saving={studio.saving} busy={studio.busy} onSave={task => {void studio.save(task)}} onDetail={task => openDetail(task.id)} onEdit={editTask} onResume={studio.resume} /></div>
  </div>
}
