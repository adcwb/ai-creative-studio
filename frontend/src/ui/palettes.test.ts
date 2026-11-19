import {expect, test} from 'vitest'
import {themes, type Theme} from '../preferences'
import {colorSchemes, palettes, themeVariables, type Palette} from './palettes'

// sRGB relative luminance, the method documented in docs/brand.md.
function luminance(hex: string) {
  const value = hex.replace('#', '')
  const channels = [0, 2, 4].map(offset => parseInt(value.slice(offset, offset + 2), 16) / 255)
    .map(channel => channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4)
  return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722
}
function contrast(a: string, b: string) {
  const left = luminance(a), right = luminance(b)
  return (Math.max(left, right) + 0.05) / (Math.min(left, right) + 0.05)
}

type Pair = [label: string, foreground: keyof Palette, background: keyof Palette, floor: number]

const textPairs: Pair[] = [
  ['正文/画布', 'ink', 'canvas', 4.5],
  ['正文/面板', 'ink', 'surface', 4.5],
  ['正文/次级块', 'ink', 'surfaceSoft', 4.5],
  ['次级字/画布', 'inkMuted', 'canvas', 4.5],
  ['次级字/面板', 'inkMuted', 'surface', 4.5],
  ['次级字/次级块', 'inkMuted', 'surfaceSoft', 4.5],
  ['占位字/输入底', 'inkPlaceholder', 'surfaceInput', 4.5],
  ['主色上的字', 'onPrimary', 'primary', 4.5],
  ['主色悬停上的字', 'onPrimary', 'primaryHover', 4.5],
  ['主色按下上的字', 'onPrimary', 'primaryActive', 4.5],
  ['链接/面板', 'link', 'surface', 4.5],
  ['链接/画布', 'link', 'canvas', 4.5],
  ['侧栏正文', 'onForest', 'forest', 4.5],
  ['侧栏次级字', 'onForestMuted', 'forest', 4.5],
  ['侧栏选中字', 'accent', 'forestRaised', 4.5],
  ['成功字/底', 'successText', 'successBg', 4.5],
  ['提示字/底', 'warningText', 'warningBg', 4.5],
  ['错误字/底', 'errorText', 'errorBg', 4.5],
]

const graphicPairs: Pair[] = [
  ['控件边界/面板', 'controlBorder', 'surface', 3],
  ['控件边界/输入底', 'controlBorder', 'surfaceInput', 3],
  ['控件边界/次级块', 'controlBorder', 'surfaceSoft', 3],
  ['控件边界/画布', 'controlBorder', 'canvas', 3],
  ['主色块/面板', 'primary', 'surface', 3],
  ['主色块/画布', 'primary', 'canvas', 3],
  ['焦点环/面板', 'focus', 'surface', 3],
  ['焦点环/画布', 'focus', 'canvas', 3],
  ['焦点环/侧栏', 'focus', 'forest', 3],
  ['光轨/面板', 'rail', 'surface', 3],
  ['光轨/画布', 'rail', 'canvas', 3],
  ['光轨/次级块', 'rail', 'surfaceSoft', 3],
  ['选中边框/面板', 'greenSoft', 'surface', 3],
  ['侧栏控件边界', 'forestControlBorder', 'forest', 3],
  ['侧栏控件边界/抬起', 'forestControlBorder', 'forestRaised', 3],
  ['海报图标/底', 'link', 'templatePoster', 3],
  ['肖像图标/底', 'clay', 'templatePortrait', 3],
  ['通用图标/底', 'slate', 'templateGeneral', 3],
]

for (const theme of themes) {
  test(`${theme} 主题的文字对比度不低于 4.5:1`, () => {
    for (const [label, foreground, background, floor] of textPairs) {
      expect(contrast(palettes[theme][foreground], palettes[theme][background]), label).toBeGreaterThanOrEqual(floor)
    }
  })
  test(`${theme} 主题的控件边界与图标对比度不低于 3:1`, () => {
    for (const [label, foreground, background, floor] of graphicPairs) {
      expect(contrast(palettes[theme][foreground], palettes[theme][background]), label).toBeGreaterThanOrEqual(floor)
    }
  })
}

test('每套色板都覆盖全部令牌，且映射到既有 CSS 变量名', () => {
  const keys = Object.keys(palettes.dark).sort()
  for (const theme of themes) expect(Object.keys(palettes[theme]).sort()).toEqual(keys)
  const variables = themeVariables('dark' as Theme)
  expect(variables['--surface-soft']).toBe(palettes.dark.surfaceSoft)
  expect(variables['--on-forest-muted']).toBe(palettes.dark.onForestMuted)
  expect(variables['--forest-control-border']).toBe(palettes.dark.forestControlBorder)
  expect(variables['--template-portrait']).toBe(palettes.dark.templatePortrait)
  expect(Object.keys(variables)).toHaveLength(keys.length)
  expect(colorSchemes.dark).toBe('dark')
  expect(colorSchemes.light).toBe('light')
})

test('暗房主色悬停加深而不是提亮，以保住白字对比度', () => {
  expect(luminance(palettes.dark.primaryHover)).toBeLessThan(luminance(palettes.dark.primary))
  expect(contrast(palettes.dark.onPrimary, palettes.dark.primaryHover)).toBeGreaterThanOrEqual(4.5)
})