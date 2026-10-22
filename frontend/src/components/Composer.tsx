import type { RefObject } from 'react'
import { kindLabels, promptError, type Capabilities, type Catalog, type Task, type TaskInput } from '../api'
import { retryCostNotice } from '../providerPresentation'
import { Icon } from './Icon'

interface Props {
  catalog: Catalog | null; capability: Capabilities | null; draft: TaskInput; busy: boolean; retryTask: Task | null
  promptRef: RefObject<HTMLTextAreaElement | null>; onChange: (draft: TaskInput) => void
  onSubmit: () => void; onCancelEdit: () => void; onNew: () => void
}

export function Composer({catalog, capability, draft, busy, retryTask, promptRef, onChange, onSubmit, onCancelEdit, onNew}: Props) {
  const invalid = promptError(draft.prompt)
  const ready = !!capability?.ready && capability.mode !== 'unavailable'
  const retryNotice = retryTask ? retryCostNotice(retryTask, capability) : ''
  const kindOptions = catalog ? [...new Set(catalog.templates.map(template => template.kind))] : []
  return <section className="panel composer" aria-labelledby="composer-title">
    <div className="section-heading"><div><span className="eyebrow">MAKE IT YOURS</span><h2 id="composer-title">创作设定</h2></div><span className="step-number">01</span></div>
    {retryTask && <div className="editing-note"><strong>正在编辑失败任务</strong><p>原描述和参数已保留。调整后提交，将关联创建新的重试任务。</p>{retryNotice && <p className="retry-cost-notice">{retryNotice}</p>}<code>{retryTask.id}</code><button type="button" onClick={onCancelEdit} disabled={busy}>取消编辑</button></div>}
    <form onSubmit={event => {event.preventDefault(); if (!invalid && catalog && !busy && ready) onSubmit()}}>
      <fieldset disabled={busy || !catalog}>
        <legend className="sr-only">图像创作参数</legend>
        <label htmlFor="kind">创作类型</label>
        <select id="kind" value={draft.kind} onChange={event => onChange({...draft, kind: event.target.value as TaskInput['kind']})}>{kindOptions.map(kind => <option key={kind} value={kind}>{kindLabels[kind]}</option>)}</select>
        <div className="prompt-label"><label htmlFor="prompt">描述你的画面</label><span>必填</span></div>
        <textarea ref={promptRef} id="prompt" value={draft.prompt} onChange={event => onChange({...draft, prompt: event.target.value})} placeholder="描述主体、环境、光线与构图，让灵感更具体一些……" rows={5} maxLength={2000} aria-describedby="prompt-hint prompt-count" aria-invalid={draft.prompt.length > 0 && !!invalid} />
        <div className="field-footer"><span id="prompt-hint">{draft.prompt.length > 0 && invalid ? invalid : '3–1000 个字符 · 不输入敏感信息'}</span><span id="prompt-count">{[...draft.prompt.trim()].length} / 1000</span></div>
        <div className="parameter-grid"><div><label htmlFor="ratio">画面比例</label><select id="ratio" value={draft.ratio} onChange={event => onChange({...draft, ratio: event.target.value as TaskInput['ratio']})}>{catalog?.ratios.map(ratio => <option key={ratio.value} value={ratio.value}>{ratio.label}</option>)}</select></div><div><label htmlFor="style">视觉风格</label><select id="style" value={draft.style} onChange={event => onChange({...draft, style: event.target.value as TaskInput['style']})}>{catalog?.styles.map(style => <option key={style.value} value={style.value}>{style.label}</option>)}</select></div></div>
        <p className="composer-help">{capability?.mode === 'mock' ? '比例会改变画布尺寸；风格会改变模拟图形。' : '比例与风格作为创作设定提交，实际尺寸见任务详情。'}<br />肖像仅为概念分类，不上传或编辑照片。</p>
        <button className="primary" type="submit" disabled={busy || !catalog || !!invalid || !ready}><Icon name={busy ? 'refresh' : 'spark'} className={busy ? 'spinning' : ''} />{busy ? '正在提交…' : retryTask ? '创建重试任务' : capability?.mode === 'mock' ? '创建模拟图像' : '创建图像'}{!busy && <Icon name="arrow" />}</button>
      </fieldset>
    </form>
    <div className="composer-bottom"><span>{capability?.mode === 'mock' ? '不消耗模型额度 · 模拟 SVG' : capability?.mode === 'real' ? '真实服务请求 · 可能计费' : '服务就绪后可提交'}</span><button type="button" className="text-button" onClick={onNew} disabled={busy}>开始新创作</button></div>
  </section>
}
