type IconName = 'clapper' | 'person' | 'videoPerson' | 'conversation' | 'cube' | 'folder' | 'layers' | 'publish' | 'spark' | 'grid' | 'image' | 'arrow' | 'close' | 'search' | 'check' | 'refresh' | 'poster' | 'portrait' | 'general' | 'script' | 'users' | 'settings' | 'wallet' | 'menu'

const paths: Record<IconName, string> = {
  clapper: 'M3 9h18v12H3zM3 9 2 4l18-3 1 5-18 3ZM7 3l3 4M14 2l3 4',
  person: 'M16 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0ZM4 21v-2a8 8 0 0 1 16 0v2',
  videoPerson: 'M3 4h18v16H3zM10 14l5-3-5-3z',
  conversation: 'M3 4h18v13H9l-6 4V4ZM7 8h10M7 12h6',
  cube: 'm12 2 9 5v10l-9 5-9-5V7l9-5Zm0 10 9-5M12 12 3 7M12 12v10',
  folder: 'M3 6V3h6l3 3h9v15H3V6Z',
  layers: 'm12 3 10 5-10 5L2 8l10-5ZM2 12l10 5 10-5M2 16l10 5 10-5',
  publish: 'M12 16V3m-5 5 5-5 5 5M4 13v8h16v-8',
  menu: 'M4 6h16M4 12h16M4 18h16',
  users: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M16 3a4 4 0 0 1 0 8M22 21v-2a4 4 0 0 0-3-3.87M13 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z',
  settings: 'M4 7h16M4 17h16M8 4v6M16 14v6',
  wallet: 'M20 8H4V5h14v3M4 8v12h16V8M16 13h4v4h-4z',
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
