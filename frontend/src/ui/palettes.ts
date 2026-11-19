import type {Theme} from '../preferences'

// The single source of colour for the whole product. antd tokens and the CSS
// custom properties are both derived from this file, so the two can no longer
// drift apart. Every pair below is checked against WCAG AA (4.5:1 for text,
// 3:1 for control boundaries and icons) using sRGB relative luminance, the same
// method documented in docs/brand.md.
export interface Palette {
  canvas: string; surface: string; surfaceSoft: string; surfaceInput: string
  ink: string; inkMuted: string; inkPlaceholder: string
  border: string; borderStrong: string; controlBorder: string
  forest: string; forestRaised: string; forestLine: string; forestControlBorder: string
  onForest: string; onForestMuted: string
  primary: string; primaryHover: string; primaryActive: string; onPrimary: string
  link: string; greenSoft: string; accent: string; rail: string
  clay: string; slate: string
  templatePoster: string; templatePortrait: string; templateGeneral: string
  successBg: string; successText: string
  warningBg: string; warningBorder: string; warningText: string
  errorBg: string; errorBorder: string; errorText: string
  focus: string
  scrim: string
  shadowSubtle: string; shadowCard: string; shadowDialog: string
}

export const palettes: Record<Theme, Palette> = {
  // 暗房 — dark-first working canvas. The brand green becomes the primary
  // action colour instead of a sidebar-only colour.
  dark: {
    canvas: '#0f1512', surface: '#171f1b', surfaceSoft: '#1e2823', surfaceInput: '#131a17',
    ink: '#e9f0ea', inkMuted: '#a3b5aa', inkPlaceholder: '#8c9e93',
    border: '#2c3a34', borderStrong: '#4a5c54', controlBorder: '#66796f',
    forest: '#0a100d', forestRaised: '#1b2620', forestLine: '#2a3831', forestControlBorder: '#66796f',
    onForest: '#e9f0ea', onForestMuted: '#a3b5aa',
    primary: '#2f7d5b', primaryHover: '#2a7152', primaryActive: '#246347', onPrimary: '#ffffff',
    link: '#7fc9a3', greenSoft: '#7fc9a3', accent: '#cfe3a8', rail: '#cfe3a8',
    clay: '#d9a97f', slate: '#9fb8c4',
    templatePoster: '#1f2a1c', templatePortrait: '#2c231c', templateGeneral: '#1c262b',
    successBg: '#1b3327', successText: '#9fd8b4',
    warningBg: '#332c18', warningBorder: '#7a6636', warningText: '#e8ce93',
    errorBg: '#3a2422', errorBorder: '#8a5a50', errorText: '#f2b3a7',
    focus: '#e3b457',
    scrim: 'rgb(0 0 0 / 60%)',
    shadowSubtle: '0 2px 5px rgb(0 0 0 / 30%)', shadowCard: '0 5px 15px rgb(0 0 0 / 40%)',
    shadowDialog: '0 24px 90px rgb(0 0 0 / 60%)',
  },
  // 工作室 — the original warm-white studio, kept for daylight reading.
  light: {
    canvas: '#f7f7f2', surface: '#ffffff', surfaceSoft: '#f0f3eb', surfaceInput: '#fbfcf8',
    ink: '#263e32', inkMuted: '#63705e', inkPlaceholder: '#63705e',
    border: '#e0e5d9', borderStrong: '#c5d0b8', controlBorder: '#7f8d78',
    forest: '#193b2e', forestRaised: '#254637', forestLine: '#3b5846', forestControlBorder: '#8ca18b',
    onForest: '#f5f5ea', onForestMuted: '#b2c2aa',
    primary: '#244b3b', primaryHover: '#35604a', primaryActive: '#1c3b2e', onPrimary: '#ffffff',
    link: '#3f6149', greenSoft: '#617b50', accent: '#d9e4b5', rail: '#617b50',
    clay: '#a2795d', slate: '#667b88',
    templatePoster: '#e8edda', templatePortrait: '#f1e7da', templateGeneral: '#e5eced',
    successBg: '#edf3e5', successText: '#4d6940',
    warningBg: '#f6f2e7', warningBorder: '#e8dfc7', warningText: '#7c6940',
    errorBg: '#fff1ec', errorBorder: '#f0d9cf', errorText: '#a54837',
    focus: '#a97f2a',
    scrim: 'rgb(8 24 17 / 40%)',
    shadowSubtle: '0 2px 5px rgb(24 48 31 / 2%)', shadowCard: '0 5px 15px rgb(24 48 31 / 5%)',
    shadowDialog: '0 24px 90px rgb(12 31 20 / 20%)',
  },
}

export const colorSchemes: Record<Theme, 'dark' | 'light'> = {dark: 'dark', light: 'light'}

// camelCase palette keys map one-to-one onto the existing custom property names,
// so no hand-maintained lookup table can fall out of sync.
function variableName(key: string) { return `--${key.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`)}` }

export function themeVariables(theme: Theme): Record<string, string> {
  return Object.fromEntries(Object.entries(palettes[theme]).map(([key, value]) => [variableName(key), value]))
}

// antd-mobile is themed through its own custom properties. We deliberately do
// not import its global stylesheet: that file restyles html/body/a globally and
// would override the brand type scale and link colour.
export function mobileVariables(theme: Theme): Record<string, string> {
  const color = palettes[theme]
  return {
    '--adm-color-primary': color.primary,
    '--adm-color-text': color.ink,
    '--adm-color-text-secondary': color.inkMuted,
    '--adm-color-weak': color.inkPlaceholder,
    '--adm-color-light': color.border,
    '--adm-color-border': color.border,
    '--adm-color-background': color.surface,
    '--adm-color-box': color.surfaceSoft,
    '--adm-color-fill-content': color.surfaceSoft,
    '--adm-color-success': color.successText,
    '--adm-color-warning': color.warningText,
    '--adm-color-danger': color.errorText,
    '--adm-color-text-light-solid': color.onPrimary,
    '--adm-font-family': '"PingFang SC", "Microsoft YaHei", system-ui, sans-serif',
    '--adm-font-size-main': '14px',
    '--adm-radius-s': '8px', '--adm-radius-m': '10px', '--adm-radius-l': '14px',
  }
}

// Applies a theme and returns a restore function. Nested providers (boot shell
// plus account shell) therefore unwind correctly when the inner one unmounts.
export function applyTheme(theme: Theme, element: HTMLElement) {
  const variables = {...themeVariables(theme), ...mobileVariables(theme)}
  const names = Object.keys(variables)
  const previous = names.map(name => [name, element.style.getPropertyValue(name)] as const)
  const previousScheme = element.style.getPropertyValue('color-scheme')
  for (const [name, value] of Object.entries(variables)) element.style.setProperty(name, value)
  element.style.setProperty('color-scheme', colorSchemes[theme])
  const meta = element === document.documentElement ? document.querySelector<HTMLMetaElement>('meta[name="theme-color"]') : null
  const previousMeta = meta?.content ?? ''
  if (meta) meta.content = palettes[theme].canvas
  return () => {
    for (const [name, value] of previous) {
      if (value) element.style.setProperty(name, value)
      else element.style.removeProperty(name)
    }
    if (previousScheme) element.style.setProperty('color-scheme', previousScheme)
    else element.style.removeProperty('color-scheme')
    if (meta) meta.content = previousMeta
  }
}