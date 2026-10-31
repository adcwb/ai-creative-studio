import {translate as t} from './i18n'
import type { Capabilities, Provider, ResultSource, Task } from './api'

const names: Record<Provider, string> = {'gateway-images': 'AI 网关', 'mock-svg': 'mock-svg', 'openai-images': 'OpenAI', 'gemini-images': 'Gemini generateContent', 'gemini-interactions-images': 'Gemini Interactions', 'ark-images': '火山方舟', 'jimeng-images': '即梦', 'minimax-images': 'MiniMax'}

export function resultOriginLabel(source: ResultSource) {
  return source.provider === 'mock-svg' ? t('模拟图像 · 非 AI 生成') : t('{provider} 图像服务返回', {provider: t(names[source.provider] ?? source.provider)})
}

export function resultImageAlt(source: ResultSource) {
  return source.provider === 'mock-svg' ? t('程序绘制的模拟图像，非真实 AI 生成') : t('{provider} 图像服务返回的图像', {provider: t(names[source.provider] ?? source.provider)})
}

export function resultProviderLabel(source: ResultSource) {
  return source.provider === 'mock-svg' ? t('mock-svg · 程序绘图') : source.provider === 'openai-images' ? 'OpenAI Images' : t(names[source.provider] ?? source.provider)
}

export function taskStatusLabel(task: Pick<Task, 'status' | 'provider'>) {
  if (task.status === 'queued') return t('等待执行')
  if (task.status === 'failed') return t('执行失败')
  if (task.status === 'running') return task.provider === 'mock-svg' ? t('模拟制作中') : t('图像生成中')
  return task.provider === 'mock-svg' ? t('模拟完成') : t('生成完成')
}

export function capabilityLabel(capability: Capabilities | null, error: string) {
  if (error) return t('图像服务状态未知')
  if (!capability) return t('正在检查图像服务')
  if (!capability.ready || capability.mode === 'unavailable') return t('图像服务不可用')
  return capability.mode === 'mock' ? t('模拟图像服务') : t('真实图像服务 · 配置就绪')
}

export function retryCostNotice(task: Task, capability: Capabilities | null) {
  const previousMayHaveCharged = task.provider !== 'mock-svg' && ['provider_timeout', 'execution_interrupted', 'provider_error', 'invalid_provider_response'].includes(task.error_code ?? '')
  const previous = previousMayHaveCharged ? t('上次真实服务请求的结果未确认，可能已经计费。') : ''
  if (capability?.mode === 'real') return previous + t('本次重试会重新请求真实图像服务，可能产生新的费用。')
  if (task.provider !== 'mock-svg' && capability?.mode === 'mock') return previous + t('当前为模拟模式，本次重试会生成模拟 SVG，原任务记录保留。')
  return previous
}
