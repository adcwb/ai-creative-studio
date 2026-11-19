export const themes = ['dark', 'light'] as const
export type Theme = typeof themes[number]
export const defaultTheme: Theme = 'dark'
// Retired appearances map onto the two surviving themes. Existing accounts keep
// their light/dark reading conditions; only the palette itself changes.
const retired: Record<string, Theme> = {forest: 'light', paper: 'light', screening: 'dark'}
export function preferenceKey(userID: string) { return `studio.preferences.v1:${encodeURIComponent(userID)}` }
export function resolveTheme(saved: string | null): Theme {
  if (saved && themes.includes(saved as Theme)) return saved as Theme
  if (saved && saved in retired) return retired[saved]
  return defaultTheme
}
export function readTheme(key: string): Theme {
  try { return resolveTheme(localStorage.getItem(key)) } catch { return defaultTheme }
}
// A device-level paint hint, not an account preference: it only lets the shell
// paint in the right appearance before the session resolves. The account key
// above stays authoritative and is never read across accounts.
const appearanceHintKey = 'studio.appearance'
export function readAppearanceHint(): Theme {
  try { return resolveTheme(localStorage.getItem(appearanceHintKey)) } catch { return defaultTheme }
}
export function rememberAppearance(theme: Theme) {
  try { localStorage.setItem(appearanceHintKey, theme) } catch { /* storage may be blocked; painting still works */ }
}