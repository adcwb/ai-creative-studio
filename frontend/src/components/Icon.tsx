type IconName = 'spark' | 'grid' | 'image' | 'arrow' | 'close' | 'search' | 'check' | 'refresh' | 'poster' | 'portrait' | 'general' | 'script'

const paths: Record<IconName, string> = {
  spark: 'm12 3 2.4 6.6L21 12l-6.6 2.4L12 21l-2.4-6.6L3 12l6.6-2.4L12 3Z',
  grid: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z',
  image: 'M4 4h16v16H4zM4 16l5-5 5 5 3-3 3 3M14 8h.01',
  arrow: 'M5 12h14M13 6l6 6-6 6',
  close: 'm6 6 12 12M6 18 18 6',
  search: 'M15.5 15.5 21 21M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0Z',
  check: 'm5 12 4 4L19 6',
  refresh: 'M20 8a8 8 0 1 0 0 8M20 3v5h-5',
  poster: 'M5 3h14v18H5zM8 16l4-5 4 5M8 7h8M8 19h5',
  portrait: 'M4 4h16v16H4zM15 9a3 3 0 1 1-6 0 3 3 0 0 1 6 0ZM7 20v-2a5 5 0 0 1 10 0v2',
  general: 'M4 4h16v16H4zM4 15l5-5 5 5 3-3 3 3M14 8h.01',
  script: 'M6 3h12v18H6zM9 7h6M9 11h6M9 15h4',
}

export function Icon({name, className = ''}: {name: IconName; className?: string}) {
  return <svg className={`icon ${className}`} aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d={paths[name]} /></svg>
}
