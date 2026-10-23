import { describe, expect, it } from 'vitest'
import type { Capabilities, Provider, Task } from './api'
import { capabilityLabel, resultImageAlt, resultOriginLabel, resultProviderLabel, retryCostNotice, taskStatusLabel } from './providerPresentation'
const real = { provider: 'openai-images', mode: 'real', ready: true, model: 'gpt-image-2-2026-04-21', message: 'local config only' } satisfies Capabilities
const mock = { provider: 'mock-svg', mode: 'mock', ready: true, message: 'synthetic SVG' } satisfies Capabilities
const task = { id: 'synthetic', space_id: 'personal-demo', prompt: 'test fixture', kind: 'general', ratio: '1:1', style: 'natural', status: 'failed', progress: 100, provider: 'openai-images', created_at: '', error_code: 'execution_interrupted' } satisfies Task
describe('historical provider presentation and cost uncertainty', () => {
  it('labels historical SVG and PNG by each result provider', () => {
    expect(resultOriginLabel(mock)).toContain('非 AI 生成')
    expect(resultImageAlt(mock)).toContain('模拟')
    expect(resultProviderLabel(mock)).toContain('mock-svg')
    expect(resultOriginLabel(task)).toContain('OpenAI')
    expect(resultProviderLabel({provider: 'gemini-interactions-images'})).toBe('Gemini Interactions')
    expect(resultProviderLabel({provider: 'gemini-images'})).toBe('Gemini generateContent')
    expect(resultImageAlt(task)).not.toContain('模拟')
    expect(taskStatusLabel({ provider: 'mock-svg', status: 'succeeded' })).toBe('模拟完成')
    expect(taskStatusLabel({ provider: 'openai-images', status: 'succeeded' })).toBe('生成完成')
  })
  it('preserves provenance and charge uncertainty for every real provider', () => {
    for (const provider of ['gemini-images', 'gemini-interactions-images', 'ark-images', 'jimeng-images', 'minimax-images'] satisfies Provider[]) {
      const historical = {...task, provider}
      expect(resultOriginLabel(historical)).not.toContain('OpenAI')
      expect(resultImageAlt(historical)).not.toContain('模拟')
      expect(retryCostNotice(historical, mock)).toContain('可能已经计费')
      expect(retryCostNotice(historical, {...real, provider})).toContain('新的费用')
    }
  })
  it('does not equate local readiness with remote credential verification', () => {
    expect(capabilityLabel(real, '')).toContain('配置就绪')
    expect(capabilityLabel(null, '')).toContain('检查')
    expect(capabilityLabel(real, 'network failure')).toContain('未知')
    expect(capabilityLabel({ ...real, ready: false }, '')).toContain('不可用')
  })
  it('retains possible prior charges and explains the current retry provider', () => {
    expect(retryCostNotice(task, real)).toContain('可能已经计费')
    expect(retryCostNotice(task, real)).toContain('新的费用')
    expect(retryCostNotice(task, mock)).toContain('模拟 SVG')
    expect(retryCostNotice(task, mock)).toContain('可能已经计费')
    expect(retryCostNotice({ ...task, provider: 'mock-svg' }, real)).toContain('新的费用')
    expect(retryCostNotice({ ...task, provider: 'mock-svg' }, mock)).toBe('')
  })
})
