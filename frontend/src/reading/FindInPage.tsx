import {useEffect, useMemo, useRef, useState} from 'react'
import {useI18n} from '../i18n'
import {Icon} from '../components/Icon'
import './reading.css'

// Search runs over the data, not the DOM, because virtualised rows off screen
// are not mounted. Jumping scrolls the match into view; the caller marks the
// active one so it can be highlighted.
export function FindInPage({texts, onJump, label}: {texts: string[]; onJump: (index: number, query: string) => void; label: string}) {
  const {t} = useI18n()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(0)
  const field = useRef<HTMLInputElement>(null)
  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return []
    return texts.reduce<number[]>((found, text, index) => {
      if (text.toLowerCase().includes(needle)) found.push(index)
      return found
    }, [])
  }, [texts, query])

  useEffect(() => {
    function keydown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f') {event.preventDefault(); setOpen(true)}
      if (event.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [])
  useEffect(() => {if (open) field.current?.focus()}, [open])

  function step(delta: number) {
    if (!matches.length) return
    const next = (cursor + delta + matches.length) % matches.length
    setCursor(next)
    onJump(matches[next], query)
  }
  if (!open) return null
  return <div className="find-in-page" role="search" aria-label={label}>
    <Icon name="search" />
    <input ref={field} value={query} maxLength={100} aria-label={label} placeholder={t('查找正文')} onChange={event => {setQuery(event.target.value); setCursor(0)}} onKeyDown={event => {
      if (event.key !== 'Enter') return
      event.preventDefault()
      if (!matches.length) return
      if (cursor === 0 && query) onJump(matches[0], query)
      else step(event.shiftKey ? -1 : 1)
    }} />
    <span className="find-in-page-count" aria-live="polite">{query ? (matches.length ? `${cursor + 1}/${matches.length}` : t('无匹配')) : ''}</span>
    <button type="button" aria-label={t('上一个匹配')} title={t('上一个匹配')} disabled={!matches.length} onClick={() => step(-1)}><Icon name="chevron" className="find-in-page-prev" /></button>
    <button type="button" aria-label={t('下一个匹配')} title={t('下一个匹配')} disabled={!matches.length} onClick={() => step(1)}><Icon name="chevron" className="find-in-page-next" /></button>
    <button type="button" aria-label={t('关闭查找')} title={t('关闭查找')} onClick={() => setOpen(false)}><Icon name="close" /></button>
  </div>
}