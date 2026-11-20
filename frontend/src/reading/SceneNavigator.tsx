import {useEffect} from 'react'
import {useI18n} from '../i18n'
import {Icon} from '../components/Icon'
import './reading.css'

// Always tells the reader where they are, and moves one scene at a time. The
// bracket shortcuts yield to text fields so they never steal typing.
export function SceneNavigator({index, total, onSelect, unit}: {index: number; total: number; onSelect: (index: number) => void; unit: string}) {
  const {t} = useI18n()
  useEffect(() => {
    function keydown(event: KeyboardEvent) {
      if (event.key !== '[' && event.key !== ']') return
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, select, [contenteditable="true"]')) return
      event.preventDefault()
      onSelect(event.key === '[' ? Math.max(index - 1, 0) : Math.min(index + 1, total - 1))
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [index, total, onSelect])
  if (!total) return null
  return <div className="scene-navigator" role="group" aria-label={t('当前位置')}>
    <button type="button" aria-label={t('上一{unit}', {unit})} title={t('上一{unit}', {unit})} disabled={index <= 0} onClick={() => onSelect(index - 1)}><Icon name="chevron" className="scene-navigator-prev" /></button>
    <span aria-live="polite">{t('第 {p0} {unit} / 共 {p1}', {p0: index + 1, p1: total, unit})}</span>
    <button type="button" aria-label={t('下一{unit}', {unit})} title={t('下一{unit}', {unit})} disabled={index >= total - 1} onClick={() => onSelect(index + 1)}><Icon name="chevron" className="scene-navigator-next" /></button>
  </div>
}