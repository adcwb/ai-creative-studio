export const themes = ['forest', 'paper', 'screening'] as const
export type Theme = typeof themes[number]
export function preferenceKey(userID: string) { return `studio.preferences.v1:${encodeURIComponent(userID)}` }
export function readTheme(key: string): Theme {
  try { const saved = localStorage.getItem(key); return themes.includes(saved as Theme) ? saved as Theme : 'forest' } catch { return 'forest' }
}
