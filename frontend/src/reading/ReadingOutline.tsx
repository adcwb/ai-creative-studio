import {useI18n} from '../i18n'
import {ReadingCanvas, type ReadingCanvasHandle} from './ReadingCanvas'
import type {RefObject} from 'react'
import './reading.css'

export interface OutlineEntry {key: string; title: string; summary?: string; status?: 'idle' | 'active' | 'done' | 'failed'}

// The outline is the long, uniform list in a reading page, so it is the part
// that virtualises. Scene prose stays in the document: it is sharded instead,
// which keeps browser find and deep anchors working there.
export function ReadingOutline({entries, activeIndex, onSelect, label, controlRef, initialIndex = 0}: {
  entries: OutlineEntry[]
  activeIndex: number
  onSelect: (index: number) => void
  label: string
  controlRef?: RefObject<ReadingCanvasHandle | null>
  initialIndex?: number
}) {
  const {t} = useI18n()
  return <nav className="reading-outline" aria-label={label}>
    <ReadingCanvas
      items={entries}
      controlRef={controlRef}
      initialIndex={initialIndex}
      itemKey={entry => entry.key}
      renderItem={(entry, index) => <button type="button" aria-current={index === activeIndex ? 'true' : undefined} onClick={() => onSelect(index)}>
        <span className="reading-outline-index">{String(index + 1).padStart(2, '0')}</span>
        <span className="reading-outline-text">
          <strong>{entry.title || t('未命名')}</strong>
          {entry.summary && <small>{entry.summary}</small>}
        </span>
        <span className="reading-outline-dot" data-status={entry.status ?? 'idle'} aria-hidden="true" />
      </button>}
    />
  </nav>
}