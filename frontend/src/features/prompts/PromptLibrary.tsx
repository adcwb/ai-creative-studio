import {useEffect, useRef, useState, type ReactNode} from 'react'
import {errorMessage, request, type Catalog, type TaskInput} from '../../api'
import {Icon} from '../../components/Icon'
import {useI18n} from '../../i18n'
import {UsePromptDialog} from './UsePromptDialog'
import {PromptEditor} from './PromptEditor'
import {readPending, type PendingPrompt, type PromptPage, type PromptResult, type PromptVersion} from './types'
import './prompts.css'

export function PromptLibrary({space, actor, readOnly, catalog, draft, disabled, builtin, onChoose}: {space: string; actor: string; readOnly: boolean; catalog: Catalog | null; draft: TaskInput; disabled: boolean; builtin: ReactNode; onChoose: (value: TaskInput) => void}) {
  const {t, formatDate} = useI18n()
  const [tab, setTab] = useState('builtin')
  const [search, setSearch] = useState('')
  const [category, setCategory] = useState('')
  const [tag, setTag] = useState('')
  const [status, setStatus] = useState('active')
  const [favoriteOnly, setFavoriteOnly] = useState(false)
  const [items, setItems] = useState<PromptResult[]>([])
  const [cursor, setCursor] = useState('')
  const [next, setNext] = useState('')
  const [revision, setRevision] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [editor, setEditor] = useState<{initial: PromptResult | null; seed: TaskInput; recovery?: PendingPrompt | null} | null>(null)
  const [pending, setPending] = useState(() => readPending(space, actor))
  const [using, setUsing] = useState<{item: PromptResult; version: PromptVersion} | null>(null)
  const [history, setHistory] = useState<{item: PromptResult; versions: PromptVersion[]; next: string} | null>(null)
  const historyDialog = useRef<HTMLDialogElement>(null)
  const historyActive = Boolean(history)
  useEffect(() => {const current = historyDialog.current; if (historyActive) current?.showModal(); return () => {current?.close()}}, [historyActive])
  useEffect(() => {function updated() {setCursor(''); setRevision(value => value + 1)}; window.addEventListener('studio-prompts-updated', updated); return () => window.removeEventListener('studio-prompts-updated', updated)}, [])
  const query = new URLSearchParams({space_id: space}).toString()
  useEffect(() => {
    if (tab === 'builtin') return
    const abort = new AbortController()
    const timer = setTimeout(() => {
      setLoading(true); setError('')
      const q = new URLSearchParams({space_id: space, source: tab, q: search, category, tag, status, favorite: String(favoriteOnly), cursor, limit: '12'})
      void request<PromptPage<PromptResult>>(`/prompt-templates?${q}`, {signal: abort.signal}).then(value => {if (!abort.signal.aborted) {setItems(previous => cursor ? [...previous.filter(old => !value.items.some(item => item.template.id === old.template.id)), ...value.items] : value.items); setNext(value.next_cursor)}}).catch(reason => {if (!abort.signal.aborted) setError(errorMessage(reason, t('无法读取我的提示词，请重试。')))}).finally(() => {if (!abort.signal.aborted) setLoading(false)})
    }, 200)
    return () => {clearTimeout(timer); abort.abort()}
  }, [space, tab, search, category, tag, status, favoriteOnly, cursor, revision, t])
  function refresh() {setCursor(''); setRevision(value => value + 1)}
  function filter(action: () => void) {setRevision(value => value + 1); setItems([]); setCursor(''); setNext(''); setError(''); action()}
  function openEditor(initial: PromptResult | null, seed = draft) {setNotice(''); setEditor({initial, seed})}
  async function mutate(item: PromptResult, action: 'favorite' | 'archive' | 'restore' | 'copy', version = item.version) {
    if (busy) return
    if (action === 'archive' && !window.confirm(t('归档后不能再选用，但历史版本与已生成作品会保留。继续归档？'))) return
    if (action === 'copy') {setHistory(null); openEditor(null, {prompt: version.body, ...version.defaults}); return}
    setBusy(true); setError('')
    try {
      const id = item.template.id
      if (action === 'favorite') await request(`/prompt-templates/${id}/favorite?${query}`, {method: 'PUT', headers: {'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID()}, body: JSON.stringify({enabled: !item.favorite})})
      else {
        const m = item.template
        await request(`/prompt-templates/${id}?${query}`, {method: 'PATCH', headers: {'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID()}, body: JSON.stringify({name: m.name, description: m.description, category: m.category, tags: m.tags, visibility: m.visibility, expected_row_version: m.row_version, status: action === 'archive' ? 'archived' : 'active'})})
      }
      refresh(); setNotice(t('提示词已更新，已有作品与历史版本保留。'))
    } catch (reason) {setError(errorMessage(reason, t('提示词操作未确认，请刷新列表核对后重试。')))} finally {setBusy(false)}
  }
  async function showHistory(item: PromptResult, after = '') {
    setError(''); setBusy(true)
    try {const value = await request<PromptPage<PromptVersion>>(`/prompt-templates/${item.template.id}/versions?${query}&cursor=${encodeURIComponent(after)}`); setHistory(previous => ({item, versions: after && previous ? [...previous.versions, ...value.items] : value.items, next: value.next_cursor}))} catch (reason) {setError(errorMessage(reason, t('提示词历史读取失败，请重试。')))} finally {setBusy(false)}
  }
  async function choose(item: PromptResult, version = item.version) {
    if (disabled || busy) return
    if (draft.prompt.trim() && !window.confirm(t('用此提示词替换当前画面描述和参数？取消可保留原草稿。'))) return
    if (version.syntax === 'variables_v1') {setHistory(null); setUsing({item, version}); return}
    setError(''); setBusy(true)
    try {
      const preview = await request<{prompt: string; defaults: TaskInput}>(`/prompt-templates/${item.template.id}/preview?${query}`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({version_id: version.id})})
      onChoose({prompt: preview.prompt, kind: preview.defaults.kind, ratio: preview.defaults.ratio, style: preview.defaults.style, prompt_source: {template_id: item.template.id, version_id: version.id, edited: true}})
      setHistory(null); setNotice(t('已填入固定版本，可继续修改。点击创建图像后才会调用模型。'))
    } catch (reason) {setError(errorMessage(reason, t('提示词选用失败，请重试。')))} finally {setBusy(false)}
  }
  return <>
    <div className="prompt-tabs" role="group" aria-label={t('提示词来源')}>{[['builtin', '内置灵感'], ['mine', '我的提示词'], ['organization', '组织共享']].map(([value, label]) => <button type="button" key={value} aria-pressed={tab === value} onClick={() => filter(() => setTab(value))}>{t(label)}</button>)}<button type="button" disabled={disabled || readOnly || Boolean(pending)} onClick={() => openEditor(null)}><Icon name="spark" />{t('保存当前描述')}</button></div>
    {pending && <p className="notice" role="status">{t('有一条保存请求尚未确认。')} <button onClick={() => setEditor({initial: pending.initial, seed: {prompt: pending.recipe.body, ...pending.recipe.defaults}, recovery: pending})}>{t('恢复并核对保存')}</button></p>}
    {notice && <p className="notice" role="status">{notice}</p>}
    {error && <p className="error" role="alert">{error} <button onClick={refresh}>{t('重新加载')}</button></p>}
    {tab === 'builtin' ? builtin : <>
      <div className="prompt-toolbar"><label>{t('搜索提示词')}<input type="search" value={search} maxLength={100} onChange={event => filter(() => setSearch(event.target.value))} /></label><label>{t('分类')}<input value={category} maxLength={80} onChange={event => filter(() => setCategory(event.target.value))} /></label><label>{t('标签')}<input value={tag} maxLength={40} onChange={event => filter(() => setTag(event.target.value))} /></label><label>{t('显示状态')}<select aria-label={t('显示状态')} value={status} onChange={event => filter(() => setStatus(event.target.value))}><option value="active">{t('使用中')}</option><option value="archived">{t('已归档')}</option></select></label><label className="prompt-checkbox"><input type="checkbox" checked={favoriteOnly} onChange={event => filter(() => setFavoriteOnly(event.target.checked))} />{t('只看收藏')}</label></div>
      {loading && <p role="status">{t('正在读取提示词…')}</p>}
      {!loading && !error && items.length === 0 && <div className="empty"><Icon name="spark" /><p>{t('这里还没有提示词。保存当前描述，积累自己的创作灵感。')}</p></div>}
      <div className="template-grid">{items.map(item => <article className="prompt-library-card" key={item.template.id}><h3>{item.template.name}</h3><p>{item.template.description || item.version.body}</p><span className="prompt-card-meta">{item.template.category} {item.template.tags.map(x => `#${x}`).join(' ')} · {t('版本 {number}', {number: item.version.number})}</span><div className="prompt-card-actions"><button className="primary" disabled={disabled || busy || item.template.status !== 'active'} onClick={() => {void choose(item)}}>{t('选用提示词')}</button><button disabled={busy} onClick={() => {void showHistory(item)}}>{t('版本记录')}</button><button aria-pressed={item.favorite} disabled={busy} onClick={() => {void mutate(item, 'favorite')}}>{t(item.favorite ? '取消收藏' : '收藏')}</button>{!readOnly && <><button disabled={busy || item.template.status !== 'active' || Boolean(pending)} onClick={() => openEditor(item)}>{t('编辑')}</button><button disabled={busy || Boolean(pending)} onClick={() => {void mutate(item, 'copy')}}>{t('复制为我的提示词')}</button><button disabled={busy} onClick={() => {void mutate(item, item.template.status === 'active' ? 'archive' : 'restore')}}>{t(item.template.status === 'active' ? '归档' : '恢复使用')}</button></>}</div></article>)}</div>
      {next && <button disabled={loading} onClick={() => setCursor(next)}>{t('加载更多提示词')}</button>}
    </>}
    {using && <UsePromptDialog space={space} item={using.item} version={using.version} onClose={() => setUsing(null)} onApply={value => {onChoose(value); setUsing(null); setNotice(t('已填入固定版本，可继续修改。点击创建图像后才会调用模型。'))}} />}
    {editor && <PromptEditor space={space} actor={actor} initial={editor.initial} seed={editor.seed} catalog={catalog} recovery={editor.recovery} onSaved={() => {setEditor(null); setPending(null); setTab('mine'); refresh(); setNotice(t('提示词已保存，没有发起模型调用。'))}} onClose={() => {setEditor(null); setPending(readPending(space, actor))}} />}
    {history && <dialog ref={historyDialog} className="detail-dialog" aria-labelledby="prompt-history-title" onCancel={() => setHistory(null)}><div className="dialog-header"><h2 id="prompt-history-title">{t('版本记录')} · {history.item.template.name}</h2><button className="icon-button" aria-label={t('关闭版本记录')} onClick={() => setHistory(null)}><Icon name="close" /></button></div><p className="composer-help">{t('历史正文保持不变。选用只预填；另存副本不会覆盖当前版本。')}</p><div className="prompt-history">{history.versions.map(version => <article key={version.id}><strong>{t('版本 {number}', {number: version.number})} · {formatDate(version.created_at)}</strong><p>{version.body}</p><small>{version.notes}</small><div className="prompt-card-actions"><button disabled={disabled || busy || history.item.template.status !== 'active'} onClick={() => {void choose(history.item, version)}}>{t('选用此版本')}</button>{!readOnly && <><button disabled={busy || Boolean(pending) || history.item.template.status !== 'active'} onClick={() => {const initial = {...history.item, version}; setHistory(null); openEditor(initial)}}>{t('以此版本继续编辑')}</button><button disabled={busy || Boolean(pending)} onClick={() => {void mutate(history.item, 'copy', version)}}>{t('另存为新提示词')}</button></>}</div></article>)}</div>{history.next && <button disabled={busy} onClick={() => {void showHistory(history.item, history.next)}}>{t('加载更多版本')}</button>}</dialog>}
  </>
}
