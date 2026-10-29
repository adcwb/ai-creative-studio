import { useEffect, useRef, useState } from 'react'
import { baseURL } from '../api'
import { audioPosition, type AudioRegion } from '../previewAudio'
import { useI18n } from '../i18n'

export function TimelineAudio({regions, assets, playing, time}: {regions: AudioRegion[]; assets: {id: string; file_url: string; kind: string}[]; playing: boolean; time: number}) {
  const {t} = useI18n()
  const elements = useRef(new Map<string, HTMLAudioElement>())
  const denied = useRef(new Set<string>())
  const starting = useRef(new Set<string>())
  const [error, setError] = useState(false)
  const specs = JSON.stringify(regions.map(r => ({...r, file: assets.find(a => a.id === r.asset && a.kind !== 'image')?.file_url})).filter(r => r.file?.startsWith('/')))
  useEffect(() => {
    const nodes = new Map<string, HTMLAudioElement>()
    for (const r of JSON.parse(specs) as (AudioRegion & {file: string})[]) {
      const audio = new Audio(); audio.crossOrigin = 'use-credentials'; audio.preload = 'auto'; audio.src = `${baseURL}${r.file}`
      audio.onerror = () => setError(true)
      nodes.set(r.id, audio)
    }
    elements.current = nodes; denied.current.clear(); starting.current.clear()
    return () => {for (const audio of nodes.values()) {audio.onerror = null; audio.pause(); audio.removeAttribute('src'); audio.load()} elements.current = new Map()}
  }, [specs])
  useEffect(() => {
    for (const region of regions) {
      const audio = elements.current.get(region.id)
      if (!audio) continue
      const position = audioPosition(region, time, Number.isFinite(audio.duration) ? audio.duration : 0)
      audio.volume = position.volume
      if (audio.readyState > 0 && Math.abs(audio.currentTime - position.offset) > (playing ? .15 : .02)) audio.currentTime = position.offset
      if (playing && position.active) {
        if (audio.paused && !denied.current.has(region.id) && !starting.current.has(region.id)) {
          starting.current.add(region.id)
          void audio.play().catch(() => {denied.current.add(region.id); if (elements.current.get(region.id) === audio) setError(true)}).finally(() => starting.current.delete(region.id))
        }
      } else {audio.pause(); denied.current.delete(region.id)}
    }
  }, [playing, time, regions, specs])
  return error ? <p role="status" className="editor-audio-status">{t('部分声音无法播放，请检查素材或重新点击播放。')}</p> : null
}
