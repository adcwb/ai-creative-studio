import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { setActiveLocale } from '../i18n'
import { SubtitleAlignment } from './SubtitleAlignment'

describe('text-only transcription', () => {
  it('shows text in both locales without offering timestamp application', () => {
    for (const locale of ['zh-CN', 'en'] as const) {
      setActiveLocale(locale)
      const html = renderToStaticMarkup(<SubtitleAlignment index={0} clip={{id: 'clip', duration: 2, subtitle: '', audio_asset_id: 'audio'}} tasks={[{id: 'task', status: 'succeeded', mode: 'transcription', provider: 'openai-asr', model: 'gpt-transcribe', source_version: 1, clip_id: 'clip', audio_asset_id: 'audio', duration: 2, cues: [], warnings: [], transcript: '<synthetic text>'}]} capabilities={{transcription: {provider: 'openai-asr', model: 'gpt-transcribe', ready: true, mode: 'real', message: ''}}} locked={false} dirty={false} version={1} onChange={() => {}} onCreate={() => {}} onCancel={() => {}} onApply={() => {}} />)
      expect(html).toContain('&lt;synthetic text&gt;')
      expect(html).toContain(locale === 'en' ? 'Text-only transcription' : '纯文本转写')
      expect(html).not.toContain(locale === 'en' ? 'Apply to clip' : '应用至片段')
    }
    setActiveLocale('zh-CN')
  })
})
