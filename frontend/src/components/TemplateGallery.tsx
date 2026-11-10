import { useI18n } from '../i18n'
import type { Catalog, Template, TaskInput } from '../api'
import { Icon } from './Icon'
import {PromptLibrary} from '../features/prompts/PromptLibrary'

export function TemplateGallery({catalog, error, disabled, onChoose, onRetry, space, actor, readOnly, draft, onChooseSaved}: {space: string; actor: string; readOnly: boolean; draft: TaskInput; onChooseSaved: (value: TaskInput) => void; catalog: Catalog | null; error: string; disabled: boolean; onChoose: (template: Template) => void; onRetry: () => void}) {
  const { t } = useI18n()
  const builtin = <>

    {error ? <div className="error" role="alert">{t("无法加载创作模板：")}{t(error)} <button onClick={onRetry}>{t("重新加载模板")}</button></div> : !catalog ? <div className="loading-block" role="status">{t("正在读取创作模板…")}</div> : <div className="template-grid">{catalog.templates.map(template => <button key={template.id} type="button" className={`template-card template-${template.kind}`} onClick={() => onChoose({...template, prompt: t(template.prompt)})} disabled={disabled}>
      <span className="template-art" aria-hidden="true"><Icon name={template.kind} /><span className="art-orbit" /></span>
      <span className="template-copy"><strong>{t(template.name)}</strong><small>{t(template.description)}</small><span>{template.ratio} <span aria-hidden="true">·</span> {t(catalog.styles.find(style => style.value === template.style)?.label ?? template.style)}</span></span><Icon name="arrow" />
    </button>)}</div>}
  </>
 return <section className="templates" aria-labelledby="templates-title"><div className="section-heading"><h2 id="templates-title">{t("从一个灵感开始")}</h2><span>{t("选用模板，继续写下你的想法")}</span></div><p className="template-explanation">{t('模板只填入提示词与参数，不代表已经生成的作品。')}</p><PromptLibrary key={`${space}:${actor}`} space={space} actor={actor} readOnly={readOnly} catalog={catalog} draft={draft} disabled={disabled} builtin={builtin} onChoose={onChooseSaved} /></section>
}
