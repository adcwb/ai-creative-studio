import { useI18n } from '../i18n'
import type { Catalog, Template } from '../api'
import { Icon } from './Icon'

export function TemplateGallery({catalog, error, disabled, onChoose, onRetry}: {catalog: Catalog | null; error: string; disabled: boolean; onChoose: (template: Template) => void; onRetry: () => void}) {
  const { t } = useI18n()
  return <section className="templates" aria-labelledby="templates-title">
    <div className="section-heading"><h2 id="templates-title">{t("从一个灵感开始")}</h2><span>{t("选用模板，继续写下你的想法")}</span></div>
    {error ? <div className="error" role="alert">{t("无法加载创作模板：")}{t(error)} <button onClick={onRetry}>{t("重新加载模板")}</button></div> : !catalog ? <div className="loading-block" role="status">{t("正在读取创作模板…")}</div> : <div className="template-grid">{catalog.templates.map(template => <button key={template.id} type="button" className={`template-card template-${template.kind}`} onClick={() => onChoose({...template, prompt: t(template.prompt)})} disabled={disabled}>
      <span className="template-art" aria-hidden="true"><Icon name={template.kind} /><span className="art-orbit" /></span>
      <span className="template-copy"><strong>{t(template.name)}</strong><small>{t(template.description)}</small><span>{template.ratio} <span aria-hidden="true">·</span> {t(catalog.styles.find(style => style.value === template.style)?.label ?? template.style)}</span></span><Icon name="arrow" />
    </button>)}</div>}
  </section>
}
