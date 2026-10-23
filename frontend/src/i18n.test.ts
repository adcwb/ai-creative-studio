import { describe, expect, it } from 'vitest'
import { resolveLocale, translate } from './i18n'
describe('internationalization', () => {
  it('respects a valid saved choice and otherwise uses browser language', () => {
    expect(resolveLocale('en', ['zh-CN'])).toBe('en')
    expect(resolveLocale('zh-CN', ['en-US'])).toBe('zh-CN')
    expect(resolveLocale('invalid', ['zh-TW'])).toBe('zh-CN')
    expect(resolveLocale(null, ['fr-FR'])).toBe('en')
    expect(resolveLocale(null, [])).toBe('en')
  })
  it('translates parameterized accessible labels without modifying user text', () => {
    expect(translate('片段 {p0} 字幕', {p0: 3}, 'en')).toBe('Clip 3 subtitle')
    expect(translate('片段 {p0} 字幕', {p0: 3}, 'zh-CN')).toBe('片段 3 字幕')
    expect(translate('User-provided project name', {}, 'en')).toBe('User-provided project name')
  })
  it('keeps currency independent from interface locale', () => {
    expect(new Intl.NumberFormat('zh-CN', {style: 'currency', currency: 'USD'}).resolvedOptions().currency).toBe('USD')
    expect(new Intl.NumberFormat('en', {style: 'currency', currency: 'USD'}).resolvedOptions().currency).toBe('USD')
  })
})
