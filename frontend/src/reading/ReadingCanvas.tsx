import {useEffect, useImperativeHandle, useRef, type ReactNode, type RefObject} from 'react'
import {Virtuoso, type VirtuosoHandle} from 'react-virtuoso'
import './reading.css'

// Below the threshold the list renders plainly: measuring and scroll anchoring
// cost more than they save on short content, and plain DOM keeps browser find
// and anchor links working. Above it, react-virtuoso takes over with automatic
// height measurement, which long prose needs.
export const virtualThreshold = 30

export interface ReadingCanvasHandle { scrollToIndex: (index: number) => void }

export function ReadingCanvas<T>({items, renderItem, itemKey, onActiveIndexChange, onAtBottomChange, initialIndex = 0, followOutput = false, className = '', controlRef}: {
  items: T[]
  renderItem: (item: T, index: number) => ReactNode
  itemKey: (item: T, index: number) => string
  onActiveIndexChange?: (index: number) => void
  onAtBottomChange?: (atBottom: boolean) => void
  initialIndex?: number
  followOutput?: boolean
  className?: string
  controlRef?: RefObject<ReadingCanvasHandle | null>
}) {
  const virtual = items.length > virtualThreshold
  const virtuoso = useRef<VirtuosoHandle>(null)
  const plain = useRef<HTMLDivElement>(null)
  // Callbacks are held in a ref so a new identity from the parent does not
  // tear down and rebuild the observer on every render.
  const active = useRef(onActiveIndexChange)
  useEffect(() => {active.current = onActiveIndexChange}, [onActiveIndexChange])

  useImperativeHandle(controlRef, () => ({
    scrollToIndex(index: number) {
      if (virtual) virtuoso.current?.scrollToIndex({index, align: 'start', behavior: 'smooth'})
      else plain.current?.querySelector<HTMLElement>(`[data-reading-index="${index}"]`)?.scrollIntoView({behavior: 'smooth', block: 'start'})
    },
  }), [virtual])

  useEffect(() => {
    if (virtual || !plain.current) return
    const seen = new Map<number, number>()
    const observer = new IntersectionObserver(entries => {
      for (const entry of entries) seen.set(Number((entry.target as HTMLElement).dataset.readingIndex), entry.intersectionRatio)
      let best = -1, ratio = 0
      for (const [index, value] of seen) if (value > ratio) {ratio = value; best = index}
      if (best >= 0) active.current?.(best)
    }, {threshold: [0, 0.25, 0.5, 0.75, 1]})
    for (const node of plain.current.querySelectorAll<HTMLElement>('[data-reading-index]')) observer.observe(node)
    return () => observer.disconnect()
  }, [virtual, items.length])

  useEffect(() => {
    if (virtual || initialIndex <= 0) return
    plain.current?.querySelector<HTMLElement>(`[data-reading-index="${initialIndex}"]`)?.scrollIntoView({block: 'start'})
    // Only on first mount: later position changes come from the outline.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (!virtual) return <div ref={plain} className={`reading-canvas ${className}`}>
    {items.map((item, index) => <div key={itemKey(item, index)} data-reading-index={index}>{renderItem(item, index)}</div>)}
  </div>

  return <Virtuoso
    ref={virtuoso}
    className={`reading-canvas reading-canvas-virtual ${className}`}
    data={items}
    initialTopMostItemIndex={initialIndex}
    computeItemKey={(index, item) => itemKey(item, index)}
    itemContent={(index, item) => <div data-reading-index={index}>{renderItem(item, index)}</div>}
    rangeChanged={range => active.current?.(range.startIndex)}
    atBottomStateChange={atBottom => onAtBottomChange?.(atBottom)}
    followOutput={followOutput ? 'smooth' : false}
    increaseViewportBy={{top: 400, bottom: 600}}
  />
}