import type { Capabilities, ResultSource, Task } from './api'

export function resultOriginLabel(source: ResultSource) {
  return source.provider === 'mock-svg' ? '模拟图像 · 非 AI 生成' : 'OpenAI 图像服务返回'
}

export function resultImageAlt(source: ResultSource) {
  return source.provider === 'mock-svg' ? '程序绘制的模拟图像，非真实 AI 生成' : 'OpenAI 图像服务返回的图像'
}

export function resultProviderLabel(source: ResultSource) {
  return source.provider === 'mock-svg' ? 'mock-svg · 程序绘图' : 'OpenAI Images'
}

export function taskStatusLabel(task: Pick<Task, 'status' | 'provider'>) {
  if (task.status === 'queued') return '等待执行'
  if (task.status === 'failed') return '执行失败'
  if (task.status === 'running') return task.provider === 'mock-svg' ? '模拟制作中' : '图像生成中'
  return task.provider === 'mock-svg' ? '模拟完成' : '生成完成'
}

export function capabilityLabel(capability: Capabilities | null, error: string) {
  if (error) return '图像服务状态未知'
  if (!capability) return '正在检查图像服务'
  if (!capability.ready || capability.mode === 'unavailable') return '图像服务不可用'
  return capability.mode === 'mock' ? '模拟图像服务' : '真实图像服务 · 配置就绪'
}

export function retryCostNotice(task: Task, capability: Capabilities | null) {
  const previousMayHaveCharged = task.provider === 'openai-images' && ['provider_timeout', 'execution_interrupted', 'provider_error', 'invalid_provider_response'].includes(task.error_code ?? '')
  const previous = previousMayHaveCharged ? '上次真实服务请求的结果未确认，可能已经计费。' : ''
  if (capability?.mode === 'real') return `${previous}本次重试会重新请求真实图像服务，可能产生新的费用。`
  if (task.provider === 'openai-images' && capability?.mode === 'mock') return `${previous}当前为模拟模式，本次重试会生成模拟 SVG，原任务记录保留。`
  return previous
}
