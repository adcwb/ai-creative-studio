import { createContext, useContext, useState, type ReactNode } from 'react'
import { I18nProvider } from './LanguagePicker'
import { preferenceKey, readTheme, defaultTheme, type Theme } from '../preferences'
import {StudioTheme} from '../ui/StudioTheme'
export interface NavigationPreferences {collapsed: boolean; groups: Record<string, boolean>}
// Workbench side panels, per account. true means collapsed.
export interface PanelPreferences {outline: boolean; inspector: boolean}
const defaultPanels: PanelPreferences = {outline: false, inspector: false}
function readPanels(key: string): PanelPreferences {try {const value = JSON.parse(localStorage.getItem(key) ?? 'null'); return {outline: value?.outline === true, inspector: value?.inspector === true}} catch {return defaultPanels}}
const defaultNavigation: NavigationPreferences = {collapsed: false, groups: {}}
function readNavigation(key: string): NavigationPreferences {try {const value = JSON.parse(localStorage.getItem(key) ?? 'null'); return {collapsed: value?.collapsed === true, groups: Object.fromEntries(Object.entries(value?.groups ?? {}).filter(([key,value]) => ['creative','avatars','resources','organization','system'].includes(key) && typeof value === 'boolean')) as Record<string,boolean>}} catch {return defaultNavigation}}
const Preferences = createContext<{theme: Theme; setTheme: (theme: Theme) => void; saved: boolean; navigation: NavigationPreferences; setNavigation: (value: NavigationPreferences) => void; panels: PanelPreferences; setPanels: (value: PanelPreferences) => void}>({navigation: defaultNavigation, setNavigation: () => {}, theme: defaultTheme, setTheme: () => {}, saved: true, panels: defaultPanels, setPanels: () => {}})
// A keyed boundary is remounted on identity changes. New accounts may use the
// anonymous language choice, never another account’s preferences. No auth is stored.
export function UserPreferences({userID, children}: {userID: string; children: ReactNode}) {
  const key = preferenceKey(userID)
  const [theme, update] = useState(() => readTheme(key))
  const [navigation, updateNavigation] = useState(() => readNavigation(`${key}:navigation`))
  function setNavigation(value: NavigationPreferences) {updateNavigation(value); try {localStorage.setItem(`${key}:navigation`, JSON.stringify(value)); setSaved(true)} catch {setSaved(false)}}
  const [saved, setSaved] = useState(true)
  const [panels, updatePanels] = useState(() => readPanels(`${key}:panels`))
  function setPanels(value: PanelPreferences) {updatePanels(value); try {localStorage.setItem(`${key}:panels`, JSON.stringify(value)); setSaved(true)} catch {setSaved(false)}}
  function setTheme(value: Theme) {
    update(value)
    try {localStorage.setItem(key, value); setSaved(true)} catch {setSaved(false)}
  }
  return <I18nProvider storageKey={userID === 'demo' ? 'studio.locale' : `${key}:locale`}><Preferences.Provider value={{theme, setTheme, saved, navigation, setNavigation, panels, setPanels}}><div className="user-workspace" data-theme={theme}><StudioTheme appearance={theme}>{children}</StudioTheme></div></Preferences.Provider></I18nProvider>
}
// eslint-disable-next-line react-refresh/only-export-components
export function usePreferences() {return useContext(Preferences)}
