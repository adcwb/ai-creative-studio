import { useEffect, useState } from 'react'
import { errorMessage, request } from '../api'
import { generationLabels, type ScriptGeneration } from '../drama'

export function GenerationHistory({space, projectId, revision}: {space: string; projectId: string; revision: number}) {
  const [open, setOpen] = useState(false)
  const [items, setItems] = useState<ScriptGeneration[] | null>(null)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    void request<ScriptGeneration[]>(`/projects/${encodeURIComponent(projectId)}/generations?${new URLSearchParams({space_id: space})}`, {signal: controller.signal})
      .then(result => {if (!controller.signal.aborted) {setItems(result); setError('')}})
      .catch(failure => {if (!controller.signal.aborted) setError(errorMessage(failure, '生成记录读取失败'))})
    return () => controller.abort()
  }, [space, projectId, revision, open, retry])
  return <section className="drama-history"><button type="button" aria-expanded={open} onClick={() => setOpen(value => !value)}>{open ? '收起生成记录' : '查看生成记录'}<span aria-hidden="true">{open ? '−' : '+'}</span></button>
    {open && <div className="drama-history-content"><p>真实生成尝试记录；不提供旧稿查看或回滚。</p>{error ? <div className="error" role="alert">{error}<button type="button" onClick={() => setRetry(value => value + 1)}>重试读取生成记录</button></div> : items ? items.length ? <ol>{items.map(item => <li key={item.id}><div><span className={`status ${item.status}`}>{generationLabels[item.status]}</span><time>{new Date(item.created_at).toLocaleString('zh-CN')}</time></div><p>{item.source_title} · 基于版本 {item.base_version} · {item.provider}</p>{item.error && <p className="drama-history-error">{item.error} {item.error_code && <code>({item.error_code})</code>}</p>}<code>{item.id}</code></li>)}</ol> : <p>还没有生成记录。</p> : <p role="status">正在读取生成记录…</p>}</div>}
  </section>
}
