import {useEffect, useRef, useState} from 'react'
import {baseURL, errorMessage, type Task} from '../api'
import {useI18n} from '../i18n'
import {resultImageAlt} from '../providerPresentation'
import {Icon} from './Icon'

export function DownloadImage({task}: {task: Task}) {
  const {t} = useI18n()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  async function download() {
    if (pending.current || !task.result_url) return
    const controller = new AbortController(); pending.current = controller
    setBusy(true); setError('')
    try {
      const url = new URL(`${baseURL}${task.result_url}`, window.location.origin)
      url.searchParams.set('download', '1')
      const response = await fetch(url, {credentials: 'include', signal: AbortSignal.any([controller.signal, AbortSignal.timeout(60000)])})
      if (!response.ok) throw new Error(t('下载失败，请重试。'))
      const blob = await response.blob()
      if (controller.signal.aborted) return
      const objectURL = URL.createObjectURL(blob)
      const link = document.createElement('a')
      const extension = ({'image/png':'png','image/jpeg':'jpg','image/webp':'webp','image/svg+xml':'svg'} as Record<string,string>)[task.result_mime ?? blob.type] ?? 'bin'
      link.href = objectURL; link.download = `creation-${task.id}.${extension}`
      document.body.append(link); link.click(); link.remove()
      setTimeout(() => URL.revokeObjectURL(objectURL), 1000)
    } catch (failure) {if (!controller.signal.aborted) setError(errorMessage(failure, t('下载失败，请重试。')))}
    finally {pending.current = null; if (!controller.signal.aborted) setBusy(false)}
  }
  return <div className="image-download"><button type="button" disabled={busy} onClick={() => {void download()}}><Icon name={busy ? 'refresh' : 'download'} className={busy ? 'spinning' : ''}/>{t(busy ? '下载中…' : '下载原图')}</button>{error && <p className="error" role="alert">{error}</p>}</div>
}

export function ImageViewer({task, onClose}: {task: Task; onClose: () => void}) {
  const {t} = useI18n()
  const dialog = useRef<HTMLDialogElement>(null)
  const viewport = useRef<HTMLDivElement>(null)
  const [zoom, setZoom] = useState<number | null>(null)
  const [dimensions, setDimensions] = useState({width: task.result_width || 1024, height: task.result_height || 1024})
  const [bounds, setBounds] = useState({width: 1024, height: 768})
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    const element = dialog.current
    const previous = document.activeElement as HTMLElement | null
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'; element?.showModal()
    const observer = new ResizeObserver(entries => {const box = entries[0].contentRect; setBounds({width: box.width, height: box.height})})
    if (viewport.current) observer.observe(viewport.current)
    return () => {observer.disconnect(); element?.close(); document.body.style.overflow = overflow; previous?.focus()}
  }, [])
  const fit = Math.min(1, bounds.width / dimensions.width, bounds.height / dimensions.height)
  const scale = zoom ?? fit
  function changeZoom(factor: number) {setZoom(Math.max(.1, Math.min(4, scale * factor)))}
  return <dialog ref={dialog} className="image-viewer" aria-labelledby="image-viewer-title" onCancel={event => {event.preventDefault(); event.stopPropagation(); onClose()}} onKeyDown={event => {event.stopPropagation(); if (event.key === '+' || event.key === '=') changeZoom(1.25); if (event.key === '-') changeZoom(.8)}}>
    <div className="image-viewer-toolbar"><h2 id="image-viewer-title">{t('查看原图')}</h2><div className="image-viewer-controls"><button type="button" aria-label={t('缩小')} disabled={scale <= .1} onClick={() => changeZoom(.8)}>−</button><output>{Math.round(scale * 100)}%</output><button type="button" aria-label={t('放大')} disabled={scale >= 4} onClick={() => changeZoom(1.25)}>+</button><button type="button" aria-pressed={zoom === null} onClick={() => setZoom(null)}>{t('适应窗口')}</button><button type="button" aria-pressed={zoom === 1} onClick={() => setZoom(1)}>{t('原始尺寸')}</button></div><DownloadImage task={task}/><button type="button" className="icon-button" autoFocus aria-label={t('关闭大图')} onClick={onClose}><Icon name="close"/></button></div>
    <div className="image-viewer-viewport" ref={viewport}>{failed ? <p role="alert">{t('图片加载失败，请关闭后重试。')}</p> : <img src={`${baseURL}${task.result_url}`} alt={resultImageAlt(task)} style={{width: dimensions.width * scale, height: dimensions.height * scale}} onLoad={event => setDimensions({width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight})} onError={() => setFailed(true)}/>}</div>
  </dialog>
}
