import { useI18n } from '../i18n'
import { useEffect, useState } from 'react'
import { errorMessage, request } from '../api'
import { generationLabels, type ScriptGeneration } from '../drama'

export function GenerationHistory({space, projectId, revision}: {space: string; projectId: string; revision: number}) {
  const { t, formatDate } = useI18n()
  const [open, setOpen] = useState(false)
  const [items, setItems] = useState<ScriptGeneration[] | null>(null)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    void request<ScriptGeneration[]>(`/projects/${encodeURIComponent(projectId)}/generations?${new URLSearchParams({space_id: space})}`, {signal: controller.signal})
      .then(result => {if (!controller.signal.aborted) {setItems(result); setError('')}})
      .catch(failure => {if (!controller.signal.aborted) setError(errorMessage(failure, "生成记录读取失败"))})
    return () => controller.abort()
  }, [space, projectId, revision, open, retry])
  return <section className="drama-history"><button type="button" aria-expanded={open} onClick={() => setOpen(value => !value)}>{open ? t("收起生成记录") : t("查看生成记录")}<span aria-hidden="true">{open ? '−' : '+'}</span></button>
    {open && <div className="drama-history-content"><p>{t("服务端生成尝试记录；旧稿可在版本历史中查看和恢复。")}</p>{error ? <div className="error" role="alert">{t(error)}<button type="button" onClick={() => setRetry(value => value + 1)}>{t("重试读取生成记录")}</button></div> : items ? items.length ? <ol>{items.map(item => <li key={item.id}><div><span className={`status ${item.status}`}>{t(generationLabels[item.status])}</span><time>{formatDate(item.created_at)}</time></div><p>{item.source_title}· {t("基于版本 {version}",{version:item.base_version})} · {item.provider}</p>{item.error && <p className="drama-history-error">{t(item.error)} {item.error_code && <code>({item.error_code})</code>}</p>}<code>{item.id}</code></li>)}</ol> : <p>{t("还没有生成记录。")}</p> : <p role="status">{t("正在读取生成记录…")}</p>}</div>}
  </section>
}
