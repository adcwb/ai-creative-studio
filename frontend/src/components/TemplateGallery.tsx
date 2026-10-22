import type { Catalog, Template } from '../api'
import { Icon } from './Icon'

export function TemplateGallery({catalog, error, disabled, onChoose, onRetry}: {catalog: Catalog | null; error: string; disabled: boolean; onChoose: (template: Template) => void; onRetry: () => void}) {
  return <section className="templates" aria-labelledby="templates-title">
    <div className="section-heading"><h2 id="templates-title">从一个灵感开始</h2><span>选用模板，继续写下你的想法</span></div>
    {error ? <div className="error" role="alert">无法加载创作模板：{error} <button onClick={onRetry}>重新加载模板</button></div> : !catalog ? <div className="loading-block" role="status">正在读取创作模板…</div> : <div className="template-grid">{catalog.templates.map(template => <button key={template.id} type="button" className={`template-card template-${template.kind}`} onClick={() => onChoose(template)} disabled={disabled}>
      <span className="template-art" aria-hidden="true"><Icon name={template.kind} /><span className="art-orbit" /></span>
      <span className="template-copy"><strong>{template.name}</strong><small>{template.description}</small><span>{template.ratio} <span aria-hidden="true">·</span> {catalog.styles.find(style => style.value === template.style)?.label ?? template.style}</span></span><Icon name="arrow" />
    </button>)}</div>}
  </section>
}
