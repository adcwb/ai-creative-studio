import {useEffect, useRef, useState} from 'react'
import {ApiError, errorMessage, kindLabels, promptError, request, type Catalog, type TaskInput} from '../../api'
import {useNavigationTask} from '../../app/navigationGuard'
import {Icon} from '../../components/Icon'
import {useI18n} from '../../i18n'
import './prompts.css'
import {ReferenceBindingsEditor} from './ReferenceBindingsEditor'
import {pendingKey, type PendingPrompt, type PromptMetadata, type PromptRecipe, type PromptResult, type PromptVariable} from './types'

export function PromptEditor({space, actor, initial, seed, catalog, recovery, onSaved, onClose}: {space: string; actor: string; initial: PromptResult | null; seed: TaskInput; catalog: Catalog | null; recovery?: PendingPrompt | null; onSaved: (r: PromptResult) => void; onClose: () => void}) {
  const {t} = useI18n()
  const dialog = useRef<HTMLDialogElement>(null)
  const [metadata, setMetadata] = useState<PromptMetadata>(() => recovery?.metadata ?? (initial?.template ? {...initial.template} : {name: '', description: '', category: '', tags: [], visibility: 'private'}))
  const [recipe, setRecipe] = useState<PromptRecipe>(() => recovery?.recipe ?? initial?.version ?? {syntax: 'plain', body: seed.prompt, defaults: {kind: seed.kind, ratio: seed.ratio, style: seed.style}, notes: ''})
  const [tags, setTags] = useState(() => metadata.tags.join(', '))
  const [dirty, setDirty] = useState(Boolean(recovery))
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(Boolean(recovery))
  const [conflict, setConflict] = useState(false)
  const [copyAsNew, setCopyAsNew] = useState(false)
  const [error, setError] = useState('')
  const [shareConfirmed, setShareConfirmed] = useState(false)
  const pending = useRef<PendingPrompt | null>(recovery ?? null)
  const active = useRef(true)
  useNavigationTask(dirty || busy || uncertain)
  useEffect(() => {active.current = true; const previous = document.activeElement as HTMLElement | null; const current = dialog.current; current?.showModal(); return () => {active.current = false; current?.close(); previous?.focus()}}, [])
  function close() {if (busy) return; if ((dirty || uncertain) && !window.confirm(t('放弃本次未保存修改？未确认的保存请求仍可稍后恢复。'))) return; onClose()}
  const changingVisibility = Boolean(initial && metadata.visibility === 'organization' && initial.template.visibility === 'private')
  const invalid = !metadata.name.trim() || [...metadata.name.trim()].length > 80 || promptError(recipe.body) || (changingVisibility && !shareConfirmed)
  function changeMetadata(patch: Partial<PromptMetadata>) {setMetadata(value => ({...value, ...patch})); setDirty(true)}
  function changeRecipe(patch: Partial<PromptRecipe>) {setRecipe(value => ({...value, ...patch})); setDirty(true)}
  function variable(index: number, patch: Partial<PromptVariable>) {changeRecipe({variables: (recipe.variables ?? []).map((v, i) => i === index ? {...v, ...patch} : v)})}
  async function save() {
    if (busy || (!pending.current && invalid)) return
    if (!pending.current) {
      // Select editable fields explicitly: fetched identity/version fields are never sent back as authorizing input.
      const m: PromptMetadata = {name: metadata.name, description: metadata.description, category: metadata.category, tags: tags.split(/[,，]/).map(x => x.trim()).filter(Boolean), visibility: metadata.visibility}
      const v: PromptRecipe = {syntax: recipe.syntax, body: recipe.body, defaults: recipe.defaults, notes: recipe.notes, ...(recipe.reference_bindings?.length ? {reference_bindings: recipe.reference_bindings} : {}), ...(recipe.syntax === 'variables_v1' ? {variables: (recipe.variables ?? []).map(v => ({...v, ...(v.type === 'enum' ? {options: v.options?.filter(value => value !== '')} : {})}))} : {})}
      pending.current = {path: initial && !copyAsNew ? `/prompt-templates/${initial.template.id}/versions` : '/prompt-templates', method: 'POST', key: crypto.randomUUID(), body: JSON.stringify(initial && !copyAsNew ? {expected_head_version_id: initial.template.head_version_id, expected_row_version: initial.template.row_version, metadata: m, version: v} : {...m, version: v}), metadata: m, recipe: v, initial}
    }
    const operation = pending.current
    try {sessionStorage.setItem(pendingKey(space, actor), JSON.stringify(operation))} catch {setError(t('无法保存请求恢复记录，请允许此站点使用会话存储后重试。')); return}
    setBusy(true); setError('')
    try {
      const result = await request<PromptResult>(`${operation.path}?${new URLSearchParams({space_id: space})}`, {method: operation.method, headers: {'Content-Type': 'application/json', 'Idempotency-Key': operation.key}, body: operation.body})
      sessionStorage.removeItem(pendingKey(space, actor)); pending.current = null
      if (active.current) {setDirty(false); setUncertain(false); onSaved(result)}
    } catch (reason) {
      if (!active.current) return
      const definite = reason instanceof ApiError && reason.status >= 400 && reason.status < 500
      if (definite) {sessionStorage.removeItem(pendingKey(space, actor)); pending.current = null; setUncertain(false); setConflict(reason.status === 409)} else {setUncertain(true)}
      setError(errorMessage(reason, t('提示词保存失败，请保留草稿重试。')) + (reason instanceof ApiError && reason.field ? ` (${reason.field})` : ''))
    } finally {if (active.current) setBusy(false)}
  }
  return <dialog ref={dialog} className="detail-dialog prompt-editor" aria-labelledby="prompt-editor-title" onCancel={event => {event.preventDefault(); close()}}>
    <div className="dialog-header"><div><span className="eyebrow">YOUR CREATIVE RECIPE</span><h2 id="prompt-editor-title">{t(initial && !copyAsNew ? '编辑我的提示词' : '保存自己的提示词')}</h2></div><button className="icon-button" aria-label={t('关闭提示词编辑')} onClick={close} disabled={busy}><Icon name="close" /></button></div>
    <p className="composer-help">{t('保存只记录配方，不会生成图像。每次编辑会保留一个新版本。')}</p>
    {error && <p className="error" role="alert">{error}</p>}
    {conflict && <p className="notice">{t('其他人已更新此提示词。你的草稿仍在，请复制文字后关闭并重新打开，核对新版再保存。')} <button type="button" onClick={() => {setCopyAsNew(true); setConflict(false); setError('')}}>{t('另存为新提示词')}</button></p>}
    {uncertain && <p className="notice" role="status">{t('保存结果尚未确认。请重试原请求，避免创建重复版本。')}</p>}
    <form onSubmit={event => {event.preventDefault(); void save()}}>
      <fieldset disabled={busy || uncertain || conflict} className="prompt-fields"><legend className="sr-only">{t('提示词配方')}</legend>
        <label>{t('提示词名称')}<input autoFocus value={metadata.name} maxLength={80} required onChange={event => changeMetadata({name: event.target.value})} /></label>
        <label>{t('简述')}<input value={metadata.description} maxLength={500} onChange={event => changeMetadata({description: event.target.value})} /></label>
        <label className="prompt-wide">{t('配方类型')}<select aria-label={t('配方类型')} value={recipe.syntax} onChange={event => changeRecipe({syntax: event.target.value as PromptRecipe['syntax'], variables: event.target.value === 'plain' ? undefined : recipe.variables ?? []})}><option value="plain">{t('纯文本（花括号保持原样）')}</option><option value="variables_v1">{t('变量配方')}</option></select></label>
        <label className="prompt-wide">{t('提示词正文')}<textarea aria-label={t('提示词正文')} value={recipe.body} rows={6} maxLength={2000} required aria-describedby="recipe-text-hint" onChange={event => changeRecipe({body: event.target.value})} /><small id="recipe-text-hint">{t(recipe.syntax === 'plain' ? '3–1000 个字符；花括号按普通文字保存，不执行变量或脚本。' : '展开后需为3–1000个字符；请先声明正文中使用的每个变量。')}</small></label>
        {recipe.syntax === 'variables_v1' && <div className="prompt-wide prompt-variable-editor"><p className="composer-help">{t('正文用 {{subject}} 引用变量；只支持文字或选项，不执行表达式。')} {t('变量名称以小写字母开头，只使用小写字母、数字和下划线。')}</p>{(recipe.variables ?? []).map((v, index) => <fieldset key={index} className="prompt-variable-row"><legend>{t('变量 {number}', {number: index + 1})}</legend><label>{t('变量名称')}<input aria-label={t('变量 {number} 名称', {number: index + 1})} value={v.name} maxLength={32} onChange={event => variable(index, {name: event.target.value})} /></label><label>{t('显示名称')}<input aria-label={t('变量 {number} 显示名称', {number: index + 1})} value={v.label} maxLength={80} onChange={event => variable(index, {label: event.target.value})} /></label><label>{t('取值类型')}<select aria-label={t('变量 {number} 类型', {number: index + 1})} value={v.type} onChange={event => variable(index, {type: event.target.value as PromptVariable['type'], options: event.target.value === 'enum' ? v.options ?? [] : undefined})}><option value="text">{t('文字')}</option><option value="enum">{t('固定选项')}</option></select></label><label>{t('单项字符长度')}<input aria-label={t('变量 {number} 长度', {number: index + 1})} type="number" min={1} max={1000} value={v.max_length} onChange={event => variable(index, {max_length: Number(event.target.value)})} /></label><label>{t('默认值')}<input aria-label={t('变量 {number} 默认值', {number: index + 1})} value={v.default ?? ''} onChange={event => variable(index, {default: event.target.value || undefined})} /></label><label className="prompt-checkbox"><input aria-label={t('变量 {number} 必填', {number: index + 1})} type="checkbox" checked={v.required} onChange={event => variable(index, {required: event.target.checked})} />{t('必填')}</label>{v.type === 'enum' && <label className="prompt-wide">{t('固定选项（每行一个）')}<textarea aria-label={t('变量 {number} 选项', {number: index + 1})} value={v.options?.join('\n') ?? ''} onChange={event => variable(index, {options: event.target.value.split('\n')})} /></label>}<button type="button" onClick={() => changeRecipe({variables: recipe.variables?.filter((_, i) => i !== index)})}>{t('移除此变量')}</button></fieldset>)}<button type="button" onClick={() => {let n = 1; while (recipe.variables?.some(v => v.name === `item_${n}`)) n += 1; changeRecipe({variables: [...(recipe.variables ?? []), {name: `item_${n}`, label: '', type: 'text', required: true, max_length: 120}]})}}>{t('添加变量')}</button></div>}
        <ReferenceBindingsEditor space={space} value={recipe.reference_bindings ?? []} onChange={reference_bindings => changeRecipe({reference_bindings})} />
        <label>{t('分类')}<input value={metadata.category} maxLength={80} onChange={event => changeMetadata({category: event.target.value})} /></label>
        <label>{t('标签（逗号分隔）')}<input value={tags} onChange={event => {setTags(event.target.value); setDirty(true)}} /></label>
        <label>{t('创作类型')}<select aria-label={t('创作类型')} value={recipe.defaults.kind} onChange={event => changeRecipe({defaults: {...recipe.defaults, kind: event.target.value as TaskInput['kind']}})}>{(['poster', 'portrait', 'general'] as const).map(kind => <option key={kind} value={kind}>{t(kindLabels[kind])}</option>)}</select></label>
        <label>{t('画面比例')}<select aria-label={t('画面比例')} value={recipe.defaults.ratio} onChange={event => changeRecipe({defaults: {...recipe.defaults, ratio: event.target.value as TaskInput['ratio']}})}>{(catalog?.ratios ?? [{value: '1:1', label: '1:1'}, {value: '4:5', label: '4:5'}, {value: '16:9', label: '16:9'}]).map(r => <option key={r.value} value={r.value}>{t(r.label)}</option>)}</select></label>
        <label>{t('视觉风格')}<select aria-label={t('视觉风格')} value={recipe.defaults.style} onChange={event => changeRecipe({defaults: {...recipe.defaults, style: event.target.value as TaskInput['style']}})}>{(catalog?.styles ?? [{value: 'natural', label: '自然'}, {value: 'studio', label: '影棚'}, {value: 'illustration', label: '插画'}]).map(s => <option key={s.value} value={s.value}>{t(s.label)}</option>)}</select></label>
        <label>{t('可见范围')}<select aria-label={t('可见范围')} disabled={Boolean(initial && initial.template.owner_id !== actor)} value={metadata.visibility} onChange={event => {changeMetadata({visibility: event.target.value as PromptMetadata['visibility']}); setShareConfirmed(false)}}><option value="private">{t('仅我可见')}</option><option value="organization">{t('当前组织可见')}</option></select></label>
        <label className="prompt-wide">{t('版本说明')}<input value={recipe.notes} maxLength={500} onChange={event => changeRecipe({notes: event.target.value})} /></label>
        {metadata.visibility === 'organization' && <p className="composer-help prompt-wide">{t('组织成员可以读取所有历史版本，有编辑权的成员可以修改。改回私有不会撤回已经进入任务历史的内容。')}</p>}
        {changingVisibility && <label className="prompt-wide prompt-checkbox"><input type="checkbox" checked={shareConfirmed} onChange={event => setShareConfirmed(event.target.checked)} />{t('我确认向当前组织共享此提示词的所有历史版本')}</label>}
      </fieldset>
      <div className="dialog-actions"><button type="button" onClick={close} disabled={busy}>{t('取消')}</button><button className="primary" type="submit" disabled={busy || conflict || (!uncertain && Boolean(invalid))}>{t(busy ? '保存中…' : uncertain ? '重试原保存请求' : initial && !copyAsNew ? '保存新版本' : '保存提示词')}</button></div>
    </form>
  </dialog>
}
