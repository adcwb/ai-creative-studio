import { createContext, useContext, useState, type ReactNode } from 'react'
import { I18nProvider } from './LanguagePicker'
import { preferenceKey, readTheme, type Theme } from '../preferences'
export interface NavigationPreferences {collapsed: boolean; groups: Record<string, boolean>}
const defaultNavigation: NavigationPreferences = {collapsed: false, groups: {}}
function readNavigation(key: string): NavigationPreferences {try {const value = JSON.parse(localStorage.getItem(key) ?? 'null'); return {collapsed: value?.collapsed === true, groups: Object.fromEntries(Object.entries(value?.groups ?? {}).filter(([key,value]) => ['creative','avatars','resources','organization'].includes(key) && typeof value === 'boolean')) as Record<string,boolean>}} catch {return defaultNavigation}}
const Preferences = createContext<{theme: Theme; setTheme: (theme: Theme) => void; saved: boolean; navigation: NavigationPreferences; setNavigation: (value: NavigationPreferences) => void}>({navigation: defaultNavigation, setNavigation: () => {}, theme: 'forest', setTheme: () => {}, saved: true})
// A keyed boundary is remounted on identity changes. New accounts may use the
// anonymous language choice, never another account’s preferences. No auth is stored.
export function UserPreferences({userID, children}: {userID: string; children: ReactNode}) {
  const key = preferenceKey(userID)
  const [theme, update] = useState(() => readTheme(key))
  const [navigation, updateNavigation] = useState(() => readNavigation(`${key}:navigation`))
  function setNavigation(value: NavigationPreferences) {updateNavigation(value); try {localStorage.setItem(`${key}:navigation`, JSON.stringify(value)); setSaved(true)} catch {setSaved(false)}}
  const [saved, setSaved] = useState(true)
  function setTheme(value: Theme) {
    update(value)
    try {localStorage.setItem(key, value); setSaved(true)} catch {setSaved(false)}
  }
  return <I18nProvider storageKey={userID === 'demo' ? 'studio.locale' : `${key}:locale`}><Preferences.Provider value={{theme, setTheme, saved, navigation, setNavigation}}><div className="user-workspace" data-theme={theme}>{children}</div></Preferences.Provider></I18nProvider>
}
// eslint-disable-next-line react-refresh/only-export-components
export function usePreferences() {return useContext(Preferences)}
